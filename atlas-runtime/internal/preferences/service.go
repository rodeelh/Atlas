// Package preferences holds user locale preferences: temperature unit, currency,
// and unit system (metric/imperial). These are distinct from location but
// auto-inferred from the detected country when not set manually.
package preferences

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
)

// Prefs holds the active user preferences.
type Prefs struct {
	TemperatureUnit string // "celsius" | "fahrenheit"
	Currency        string // ISO 4217 e.g. "USD", "EUR", "AED"
	UnitSystem      string // "metric" | "imperial"
	Initialized     bool
}

var (
	mu      sync.RWMutex
	current Prefs

	execSecurity = func(args ...string) (string, error) {
		cmd := exec.Command("security", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			return "", fmt.Errorf("security %v failed: %w — %s", args, err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(stdout.String()), nil
	}
)

// Get returns the current preferences.
func Get() Prefs {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Resolved returns the active preferences with a lazy reload from persisted
// config when the in-memory state is unexpectedly empty, plus stable fallback
// defaults for any unresolved fields.
func Resolved() Prefs {
	mu.RLock()
	p := current
	mu.RUnlock()

	if p.TemperatureUnit == "" && p.Currency == "" && p.UnitSystem == "" && !p.Initialized {
		LoadFromConfig()
		mu.RLock()
		p = current
		mu.RUnlock()
	}

	if p.TemperatureUnit == "" {
		p.TemperatureUnit = "celsius"
	}
	if p.Currency == "" {
		p.Currency = "USD"
	}
	if p.UnitSystem == "" {
		p.UnitSystem = "metric"
	}
	if p.TemperatureUnit != "" || p.Currency != "" || p.UnitSystem != "" {
		p.Initialized = p.Initialized || current.Initialized
	}
	return p
}

// Set stores preferences in memory and persists them.
func Set(p Prefs) {
	if p.TemperatureUnit != "" || p.Currency != "" || p.UnitSystem != "" {
		p.Initialized = true
	}
	mu.Lock()
	current = p
	mu.Unlock()
	persist(p)
}

// TemperatureUnit returns "celsius" or "fahrenheit".
func TemperatureUnit() string {
	if p := Resolved(); p.TemperatureUnit != "" {
		return p.TemperatureUnit
	}
	return "celsius"
}

// Currency returns the ISO 4217 currency code.
func Currency() string {
	if p := Resolved(); p.Currency != "" {
		return p.Currency
	}
	return "USD"
}

// UnitSystem returns "metric" or "imperial".
func UnitSystem() string {
	if p := Resolved(); p.UnitSystem != "" {
		return p.UnitSystem
	}
	// Last-resort fallback — LoadFromConfig should have resolved this from
	// persisted config, keychain, or the OS locale, but if not, default to
	// metric (international standard).
	return "metric"
}

// LoadFromConfig reads persisted preferences from GoRuntimeConfig into memory.
// If no persisted preferences are found, it falls back to the macOS system
// locale (AppleMeasurementUnits / AppleTemperatureUnit) so that the correct
// unit system is available immediately, before the async IP-geolocation
// inference has had a chance to run.
func LoadFromConfig() {
	cfg := config.LoadGoConfig()
	p := Prefs{
		TemperatureUnit: cfg.UserTemperatureUnit,
		Currency:        cfg.UserCurrency,
		UnitSystem:      cfg.UserUnitSystem,
		Initialized:     cfg.UserPreferencesInitialized,
	}
	if p.TemperatureUnit != "" || p.Currency != "" || p.UnitSystem != "" {
		p.Initialized = true
	}
	if kp, ok := loadFromKeychain(); ok {
		if p.TemperatureUnit == "" {
			p.TemperatureUnit = kp.TemperatureUnit
		}
		if p.Currency == "" {
			p.Currency = kp.Currency
		}
		if p.UnitSystem == "" {
			p.UnitSystem = kp.UnitSystem
		}
		if kp.Initialized {
			p.Initialized = true
		}
	}
	// If any measurement fields are still unresolved, read the macOS system
	// locale. This gives the correct default from the very first request,
	// without waiting for the async IP-geolocation call. We deliberately do
	// NOT set p.Initialized here so that the subsequent country-based
	// inference (InferFromCountry, called after DetectFromIP) can still
	// refine the values if the OS locale and physical location differ.
	if p.UnitSystem == "" || p.TemperatureUnit == "" {
		osUnit, osTemp := inferFromOSLocale()
		if p.UnitSystem == "" {
			p.UnitSystem = osUnit
		}
		if p.TemperatureUnit == "" {
			p.TemperatureUnit = osTemp
		}
	}
	mu.Lock()
	current = p
	mu.Unlock()
	if p.Initialized {
		persist(p)
	}
}

// InferFromCountry sets preferences based on the country name if they are not
// already configured (i.e. zero-value). Call this after IP detection.
func InferFromCountry(country string) {
	if strings.TrimSpace(country) == "" {
		return
	}
	mu.Lock()
	p := current
	if p.Initialized {
		mu.Unlock()
		return
	}
	p.TemperatureUnit = tempUnitForCountry(country)
	p.UnitSystem = unitSystemForCountry(country)
	p.Currency = currencyForCountry(country)
	p.Initialized = true
	current = p
	mu.Unlock()

	persist(p)
	logstore.Write("info", "Preferences inferred from country: "+country,
		map[string]string{
			"tempUnit":   p.TemperatureUnit,
			"currency":   p.Currency,
			"unitSystem": p.UnitSystem,
		})
}

// ── internals ─────────────────────────────────────────────────────────────────

func persist(p Prefs) {
	cfg := config.LoadGoConfig()
	cfg.UserTemperatureUnit = p.TemperatureUnit
	cfg.UserCurrency = p.Currency
	cfg.UserUnitSystem = p.UnitSystem
	cfg.UserPreferencesInitialized = p.Initialized
	_ = config.SaveGoConfig(cfg)
	_ = saveToKeychain(p)
}

func loadFromKeychain() (Prefs, bool) {
	out, err := execSecurity("find-generic-password", "-s", "com.projectatlas.preferences", "-a", "locale", "-w")
	if err != nil {
		return Prefs{}, false
	}
	var p Prefs
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &p); err != nil {
		return Prefs{}, false
	}
	if p.TemperatureUnit == "" && p.Currency == "" && p.UnitSystem == "" && !p.Initialized {
		return Prefs{}, false
	}
	if p.TemperatureUnit != "" || p.Currency != "" || p.UnitSystem != "" {
		p.Initialized = true
	}
	return p, true
}

func saveToKeychain(p Prefs) error {
	if p.TemperatureUnit == "" && p.Currency == "" && p.UnitSystem == "" && !p.Initialized {
		return nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = execSecurity("add-generic-password", "-U", "-s", "com.projectatlas.preferences", "-a", "locale", "-w", string(data))
	return err
}

// inferFromOSLocale reads macOS system preferences to determine the user's
// preferred unit system and temperature unit. It returns empty strings when
// the values cannot be determined (non-macOS, permission denied, etc.).
//
// Lookup order:
//  1. AppleMeasurementUnits / AppleTemperatureUnit — written only when the
//     user has explicitly overridden their locale default (e.g. a US user
//     switching to metric). Values: "Inches"/"Centimeters", "Fahrenheit"/"Celsius".
//  2. AppleLocale (e.g. "en_US", "ar_SA") — always present; the country code
//     suffix determines the conventional unit system for that locale.
//
// We deliberately do NOT set Initialized=true so that a subsequent
// InferFromCountry call (after IP geolocation) can still refine the values.
func inferFromOSLocale() (unitSystem, tempUnit string) {
	// 1. Explicit overrides (only written when user changes from locale default).
	if out, err := execCommand("defaults", "read", "NSGlobalDomain", "AppleMeasurementUnits"); err == nil {
		switch strings.TrimSpace(out) {
		case "Inches":
			unitSystem = "imperial"
		case "Centimeters":
			unitSystem = "metric"
		}
	}
	if out, err := execCommand("defaults", "read", "NSGlobalDomain", "AppleTemperatureUnit"); err == nil {
		switch strings.TrimSpace(out) {
		case "Fahrenheit":
			tempUnit = "fahrenheit"
		case "Celsius":
			tempUnit = "celsius"
		}
	}

	// 2. Fall back to locale if still unresolved.  AppleLocale is virtually
	//    always present (set during macOS setup).  Format: "language_COUNTRY".
	if unitSystem == "" || tempUnit == "" {
		if locale, err := execCommand("defaults", "read", "NSGlobalDomain", "AppleLocale"); err == nil {
			localeUnit, localeTempUnit := unitSystemFromLocale(strings.TrimSpace(locale))
			if unitSystem == "" {
				unitSystem = localeUnit
			}
			if tempUnit == "" {
				tempUnit = localeTempUnit
			}
		}
	}
	return
}

// unitSystemFromLocale derives the conventional unit system and temperature
// unit from an Apple locale string (e.g. "en_US", "ar_SA", "en_GB").
// The country suffix is the authoritative part; the language prefix is ignored.
func unitSystemFromLocale(locale string) (unitSystem, tempUnit string) {
	// Extract the country code: "en_US" → "US", "ar_SA" → "SA".
	// Also handles "en-US" (hyphen) and bare codes like "US".
	locale = strings.ReplaceAll(locale, "-", "_")
	parts := strings.SplitN(locale, "_", 2)
	country := ""
	if len(parts) == 2 {
		country = strings.ToUpper(parts[1])
	} else {
		country = strings.ToUpper(parts[0])
	}

	// Countries whose locale implies imperial measurements.
	// Only the United States uses the US customary system for everyday measures.
	// Myanmar and Liberia are formal non-metric countries but their Apple locale
	// codes (MY, LR) are included for completeness.
	imperialLocaleCodes := map[string]bool{
		"US": true, // United States
		"LR": true, // Liberia
		"MM": true, // Myanmar
	}
	// Countries whose locale implies Fahrenheit.
	fahrenheitLocaleCodes := map[string]bool{
		"US": true, // United States
		"BZ": true, // Belize
		"KY": true, // Cayman Islands
		"PW": true, // Palau
		"MH": true, // Marshall Islands
		"FM": true, // Micronesia
		"BS": true, // Bahamas
		"TC": true, // Turks and Caicos
		"LR": true, // Liberia
	}

	if imperialLocaleCodes[country] {
		unitSystem = "imperial"
	} else {
		unitSystem = "metric"
	}
	if fahrenheitLocaleCodes[country] {
		tempUnit = "fahrenheit"
	} else {
		tempUnit = "celsius"
	}
	return
}

// execCommand runs a command and returns trimmed stdout, or an error.
func execCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// fahrenheitCountries is the small set of countries that use °F.
var fahrenheitCountries = map[string]bool{
	"United States":    true,
	"Belize":           true,
	"Cayman Islands":   true,
	"Palau":            true,
	"Marshall Islands": true,
	"Micronesia":       true,
	"Bahamas":          true,
	"Turks and Caicos": true,
	"Liberia":          true,
}

// imperialCountries use the imperial system for distances/speeds.
var imperialCountries = map[string]bool{
	"United States": true,
	"Liberia":       true,
	"Myanmar":       true,
}

func tempUnitForCountry(country string) string {
	if fahrenheitCountries[country] {
		return "fahrenheit"
	}
	return "celsius"
}

func unitSystemForCountry(country string) string {
	if imperialCountries[country] {
		return "imperial"
	}
	return "metric"
}

// currencyForCountry returns the primary ISO 4217 currency for a country name.
func currencyForCountry(country string) string {
	m := map[string]string{
		"United States":        "USD",
		"United Kingdom":       "GBP",
		"Canada":               "CAD",
		"Australia":            "AUD",
		"New Zealand":          "NZD",
		"Japan":                "JPY",
		"China":                "CNY",
		"India":                "INR",
		"South Korea":          "KRW",
		"Singapore":            "SGD",
		"Hong Kong":            "HKD",
		"Switzerland":          "CHF",
		"Norway":               "NOK",
		"Sweden":               "SEK",
		"Denmark":              "DKK",
		"Brazil":               "BRL",
		"Mexico":               "MXN",
		"Russia":               "RUB",
		"South Africa":         "ZAR",
		"Saudi Arabia":         "SAR",
		"United Arab Emirates": "AED",
		"Qatar":                "QAR",
		"Kuwait":               "KWD",
		"Bahrain":              "BHD",
		"Oman":                 "OMR",
		"Israel":               "ILS",
		"Turkey":               "TRY",
		"Indonesia":            "IDR",
		"Thailand":             "THB",
		"Malaysia":             "MYR",
		"Philippines":          "PHP",
		"Vietnam":              "VND",
		"Pakistan":             "PKR",
		"Bangladesh":           "BDT",
		"Egypt":                "EGP",
		"Nigeria":              "NGN",
		"Kenya":                "KES",
		"Ghana":                "GHS",
		"Argentina":            "ARS",
		"Colombia":             "COP",
		"Chile":                "CLP",
		"Peru":                 "PEN",
		// Eurozone
		"Germany":     "EUR",
		"France":      "EUR",
		"Italy":       "EUR",
		"Spain":       "EUR",
		"Portugal":    "EUR",
		"Netherlands": "EUR",
		"Belgium":     "EUR",
		"Austria":     "EUR",
		"Greece":      "EUR",
		"Finland":     "EUR",
		"Ireland":     "EUR",
		"Luxembourg":  "EUR",
		"Malta":       "EUR",
		"Cyprus":      "EUR",
		"Slovakia":    "EUR",
		"Slovenia":    "EUR",
		"Estonia":     "EUR",
		"Latvia":      "EUR",
		"Lithuania":   "EUR",
		"Croatia":     "EUR",
	}
	if c, ok := m[country]; ok {
		return c
	}
	return "USD"
}
