package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// GoRuntimeConfig is the Go-runtime-specific sidecar config. It lives at
// ~/Library/Application Support/ProjectAtlas/go-runtime-config.json and
// holds settings that are only relevant to the runtime and not part of the
// shared main config.json contract.
type GoRuntimeConfig struct {
	// SwiftBackendURL is a legacy dual-run field retained only for backward
	// compatibility with older installs that may still carry it in the sidecar
	// config. The active runtime no longer depends on a Swift backend.
	SwiftBackendURL string `json:"swiftBackendURL"`

	// BrowserShowWindow controls whether the browser launched by browser control
	// skills opens a visible window. Defaults to false (headless) so the agent
	// browses silently in the background. Set to true to watch what the agent is
	// doing, useful for debugging or demos.
	BrowserShowWindow bool `json:"browserShowWindow"`

	// Locale preferences — inferred from country or set manually.
	UserTemperatureUnit string `json:"userTemperatureUnit"` // "celsius" | "fahrenheit"
	UserCurrency        string `json:"userCurrency"`        // ISO 4217 e.g. "USD", "AED"
	UserUnitSystem      string `json:"userUnitSystem"`      // "metric" | "imperial"

	// Location — resolved via IP geolocation at startup or set manually by the
	// user.  Injected into the system prompt each turn so the model can infer
	// the user's city/timezone without being asked.
	UserCity            string  `json:"userCity"`
	UserCountry         string  `json:"userCountry"`
	UserTimezone        string  `json:"userTimezone"`
	UserLatitude        float64 `json:"userLatitude"`
	UserLongitude       float64 `json:"userLongitude"`
	UserLocationSource  string  `json:"userLocationSource"`  // "ip" | "manual" | ""
	UserLocationUpdated string  `json:"userLocationUpdated"` // RFC3339
}

// GoConfigPath returns the path of the Go-runtime sidecar config file.
func GoConfigPath() string {
	return filepath.Join(SupportDir(), "go-runtime-config.json")
}

// LoadGoConfig reads the Go-runtime sidecar config, returning defaults if
// the file does not exist or is unreadable.
func LoadGoConfig() GoRuntimeConfig {
	data, err := os.ReadFile(GoConfigPath())
	if err != nil {
		return GoRuntimeConfig{}
	}
	var cfg GoRuntimeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return GoRuntimeConfig{}
	}
	return cfg
}

// SaveGoConfig persists the Go-runtime sidecar config atomically.
func SaveGoConfig(cfg GoRuntimeConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := GoConfigPath() + ".tmp." + randomHex(4)
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, GoConfigPath())
}
