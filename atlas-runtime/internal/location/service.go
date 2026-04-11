// Package location provides Atlas's built-in location service.
//
// On daemon start the service loads any persisted location from GoRuntimeConfig
// and, if no manual override exists or the cached result is stale (>24h),
// fires an IP-geolocation lookup against ip-api.com (free, no key required).
//
// An in-memory singleton keeps the result so the system prompt can read it on
// every turn without hitting disk.  The user can override city/country manually
// via PUT /location; manual overrides are never clobbered by auto-detection.
package location

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/preferences"
)

// Info holds a resolved location.
type Info struct {
	City      string
	Country   string
	Timezone  string
	Latitude  float64
	Longitude float64
	Source    string // "ip" | "manual"
	UpdatedAt time.Time
}

var (
	mu      sync.RWMutex
	current Info
)

// Set stores info in the in-memory cache.
func Set(info Info) {
	mu.Lock()
	current = info
	mu.Unlock()
}

// Get returns the current cached location.
func Get() Info {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// IsKnown returns true when a city has been resolved.
func IsKnown() bool {
	mu.RLock()
	defer mu.RUnlock()
	return current.City != ""
}

// ShouldRefresh returns true if IP detection should run.
// It is false when the user has set a manual override, or when the cached
// IP result is less than 24 hours old.
func ShouldRefresh() bool {
	mu.RLock()
	defer mu.RUnlock()
	if current.Source == "manual" {
		return false
	}
	if current.City == "" {
		return true
	}
	return time.Since(current.UpdatedAt) > 24*time.Hour
}

// LoadFromConfig reads the persisted location out of GoRuntimeConfig into the
// in-memory cache.  Call this once at startup before DetectFromIP.
func LoadFromConfig() {
	cfg := config.LoadGoConfig()
	if cfg.UserCity == "" {
		return
	}
	t, _ := time.Parse(time.RFC3339, cfg.UserLocationUpdated)
	Set(Info{
		City:      cfg.UserCity,
		Country:   cfg.UserCountry,
		Timezone:  cfg.UserTimezone,
		Latitude:  cfg.UserLatitude,
		Longitude: cfg.UserLongitude,
		Source:    cfg.UserLocationSource,
		UpdatedAt: t,
	})
	// Infer preferences from country if not already set (e.g. on first boot
	// after preferences fields were added to GoRuntimeConfig).
	if cfg.UserCountry != "" {
		preferences.InferFromCountry(cfg.UserCountry)
	}
}

// DetectFromIP calls ip-api.com to resolve the public IP's city/country/timezone,
// then updates both the in-memory cache and GoRuntimeConfig.
func DetectFromIP() error {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get("http://ip-api.com/json?fields=status,message,city,country,timezone,lat,lon")
	if err != nil {
		return fmt.Errorf("ip geolocation request: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var result struct {
		Status   string  `json:"status"`
		Message  string  `json:"message"`
		City     string  `json:"city"`
		Country  string  `json:"country"`
		Timezone string  `json:"timezone"`
		Lat      float64 `json:"lat"`
		Lon      float64 `json:"lon"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return fmt.Errorf("ip geolocation parse: %w", err)
	}
	if result.Status != "success" {
		return fmt.Errorf("ip geolocation failed: %s", result.Message)
	}
	if result.City == "" {
		return fmt.Errorf("ip geolocation: empty city in response")
	}

	info := Info{
		City:      result.City,
		Country:   result.Country,
		Timezone:  result.Timezone,
		Latitude:  result.Lat,
		Longitude: result.Lon,
		Source:    "ip",
		UpdatedAt: time.Now(),
	}
	Set(info)
	persist(info)
	preferences.InferFromCountry(info.Country)
	logstore.Write("info",
		fmt.Sprintf("Location detected: %s, %s (%s)", info.City, info.Country, info.Timezone),
		map[string]string{"source": "ip"},
	)
	return nil
}

// DetectFromCoreLocation runs the atlas-location helper binary (installed
// alongside the Atlas daemon) to get a precise WiFi/GPS fix via macOS
// CoreLocation. On success it reverse-geocodes the coordinates via Nominatim,
// updates the in-memory cache and GoRuntimeConfig, and returns nil.
//
// Returns an error if the helper binary is not found, location permission is
// denied, or the request times out — the caller should fall back to DetectFromIP.
func DetectFromCoreLocation() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("corelocation: executable path: %w", err)
	}
	helperPath := filepath.Join(filepath.Dir(execPath), "atlas-location")
	if _, statErr := os.Stat(helperPath); os.IsNotExist(statErr) {
		return fmt.Errorf("corelocation: helper not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 13*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, helperPath).Output()
	if err != nil {
		return fmt.Errorf("corelocation: helper error: %w", err)
	}

	var gps struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Accuracy  float64 `json:"accuracy"`
		Error     string  `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &gps); err != nil {
		return fmt.Errorf("corelocation: parse: %w", err)
	}
	if gps.Error != "" {
		return fmt.Errorf("corelocation: %s", gps.Error)
	}

	// Reverse-geocode coordinates → city/country/timezone via Nominatim.
	reverseURL := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/reverse?lat=%.6f&lon=%.6f&format=json&addressdetails=1",
		gps.Latitude, gps.Longitude,
	)
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", reverseURL, nil)
	if err != nil {
		return fmt.Errorf("corelocation: reverse geocode request: %w", err)
	}
	req.Header.Set("User-Agent", "ProjectAtlas/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("corelocation: reverse geocode: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var geo struct {
		Address struct {
			City       string `json:"city"`
			Town       string `json:"town"`
			Village    string `json:"village"`
			State      string `json:"state"`
			Country    string `json:"country"`
			CountryCode string `json:"country_code"`
		} `json:"address"`
	}
	if err := json.Unmarshal(body, &geo); err != nil || geo.Address.Country == "" {
		return fmt.Errorf("corelocation: reverse geocode parse failed")
	}

	city := geo.Address.City
	if city == "" {
		city = geo.Address.Town
	}
	if city == "" {
		city = geo.Address.Village
	}
	if city == "" {
		city = geo.Address.State
	}

	// Best-effort timezone lookup via Open-Meteo geocoding.
	timezone := ""
	if geoInfo, tzErr := geocodeCity(city, geo.Address.Country); tzErr == nil {
		timezone = geoInfo.timezone
	}

	accuracyNote := ""
	if gps.Accuracy > 0 {
		accuracyNote = fmt.Sprintf(" (±%.0fm)", gps.Accuracy)
	}

	info := Info{
		City:      city,
		Country:   geo.Address.Country,
		Timezone:  timezone,
		Latitude:  gps.Latitude,
		Longitude: gps.Longitude,
		Source:    "gps",
		UpdatedAt: time.Now(),
	}
	Set(info)
	persist(info)
	preferences.InferFromCountry(strings.ToUpper(geo.Address.CountryCode))
	logstore.Write("info",
		fmt.Sprintf("Location detected via CoreLocation: %s, %s (%.6f, %.6f)%s",
			city, geo.Address.Country, gps.Latitude, gps.Longitude, accuracyNote),
		map[string]string{"source": "gps"},
	)
	return nil
}

// SetManual stores a user-supplied city/country as a manual override.
// It also attempts to resolve lat/lon and timezone via Open-Meteo geocoding
// so weather skills have accurate coordinates, but geocoding failure is
// non-fatal — city/country alone is sufficient for the system prompt.
func SetManual(city, country string) error {
	info := Info{
		City:      city,
		Country:   country,
		Source:    "manual",
		UpdatedAt: time.Now(),
	}

	// Best-effort geocode for timezone + coordinates.
	if geo, err := geocodeCity(city, country); err == nil {
		info.Timezone = geo.timezone
		info.Latitude = geo.lat
		info.Longitude = geo.lon
	}

	Set(info)
	persist(info)
	preferences.InferFromCountry(info.Country)
	logstore.Write("info",
		fmt.Sprintf("Location set manually: %s, %s", city, country),
		map[string]string{"source": "manual"},
	)
	return nil
}

// ClearManual removes a manual override and re-enables IP-based detection.
func ClearManual() {
	mu.Lock()
	current.Source = "ip"
	current.UpdatedAt = time.Time{} // force stale so ShouldRefresh returns true
	mu.Unlock()

	cfg := config.LoadGoConfig()
	cfg.UserLocationSource = "ip"
	cfg.UserLocationUpdated = ""
	_ = config.SaveGoConfig(cfg)
}

// ── internals ─────────────────────────────────────────────────────────────────

type geoInfo struct {
	lat, lon float64
	timezone string
}

func geocodeCity(city, country string) (geoInfo, error) {
	q := city
	if country != "" {
		q += ", " + country
	}
	apiURL := "https://geocoding-api.open-meteo.com/v1/search?name=" +
		url.QueryEscape(q) + "&count=1&language=en&format=json"

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return geoInfo{}, err
	}
	defer resp.Body.Close()

	var result struct {
		Results []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Timezone  string  `json:"timezone"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Results) == 0 {
		return geoInfo{}, fmt.Errorf("not found")
	}
	r := result.Results[0]
	return geoInfo{lat: r.Latitude, lon: r.Longitude, timezone: r.Timezone}, nil
}

func persist(info Info) {
	cfg := config.LoadGoConfig()
	cfg.UserCity = info.City
	cfg.UserCountry = info.Country
	cfg.UserTimezone = info.Timezone
	cfg.UserLatitude = info.Latitude
	cfg.UserLongitude = info.Longitude
	cfg.UserLocationSource = info.Source
	cfg.UserLocationUpdated = info.UpdatedAt.UTC().Format(time.RFC3339)
	if err := config.SaveGoConfig(cfg); err != nil {
		logstore.Write("warning", "location: failed to persist: "+err.Error(), nil)
	}
}
