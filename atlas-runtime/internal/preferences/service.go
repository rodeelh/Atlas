// Package preferences holds user locale preferences: temperature unit, currency,
// and unit system (metric/imperial). These are distinct from location but
// auto-inferred from the detected country when not set manually.
package preferences

import (
	"sync"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
)

// Prefs holds the active user preferences.
type Prefs struct {
	TemperatureUnit string // "celsius" | "fahrenheit"
	Currency        string // ISO 4217 e.g. "USD", "EUR", "AED"
	UnitSystem      string // "metric" | "imperial"
}

var (
	mu      sync.RWMutex
	current Prefs
)

// Get returns the current preferences.
func Get() Prefs {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Set stores preferences in memory and persists them.
func Set(p Prefs) {
	mu.Lock()
	current = p
	mu.Unlock()
	persist(p)
}

// TemperatureUnit returns "celsius" or "fahrenheit".
func TemperatureUnit() string {
	mu.RLock()
	defer mu.RUnlock()
	if current.TemperatureUnit == "" {
		return "celsius"
	}
	return current.TemperatureUnit
}

// Currency returns the ISO 4217 currency code.
func Currency() string {
	mu.RLock()
	defer mu.RUnlock()
	if current.Currency == "" {
		return "USD"
	}
	return current.Currency
}

// UnitSystem returns "metric" or "imperial".
func UnitSystem() string {
	mu.RLock()
	defer mu.RUnlock()
	if current.UnitSystem == "" {
		return "metric"
	}
	return current.UnitSystem
}

// LoadFromConfig reads persisted preferences from GoRuntimeConfig into memory.
func LoadFromConfig() {
	cfg := config.LoadGoConfig()
	p := Prefs{
		TemperatureUnit: cfg.UserTemperatureUnit,
		Currency:        cfg.UserCurrency,
		UnitSystem:      cfg.UserUnitSystem,
	}
	mu.Lock()
	current = p
	mu.Unlock()
}

// InferFromCountry sets preferences based on the country name if they are not
// already configured (i.e. zero-value). Call this after IP detection.
func InferFromCountry(country string) {
	mu.Lock()
	p := current
	changed := false

	if p.TemperatureUnit == "" {
		p.TemperatureUnit = tempUnitForCountry(country)
		changed = true
	}
	if p.UnitSystem == "" {
		p.UnitSystem = unitSystemForCountry(country)
		changed = true
	}
	if p.Currency == "" {
		p.Currency = currencyForCountry(country)
		changed = true
	}

	if changed {
		current = p
	}
	mu.Unlock()

	if changed {
		persist(p)
		logstore.Write("info", "Preferences inferred from country: "+country,
			map[string]string{
				"tempUnit":   p.TemperatureUnit,
				"currency":   p.Currency,
				"unitSystem": p.UnitSystem,
			})
	}
}

// ── internals ─────────────────────────────────────────────────────────────────

func persist(p Prefs) {
	cfg := config.LoadGoConfig()
	cfg.UserTemperatureUnit = p.TemperatureUnit
	cfg.UserCurrency = p.Currency
	cfg.UserUnitSystem = p.UnitSystem
	_ = config.SaveGoConfig(cfg)
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
