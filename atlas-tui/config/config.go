package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds TUI startup configuration.
type Config struct {
	BaseURL string
	Port    int
}

// Load reads ATLAS_PORT from the environment (default 1984).
func Load() Config {
	port := 1984
	if p := os.Getenv("ATLAS_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	return Config{
		BaseURL: fmt.Sprintf("http://localhost:%d", port),
		Port:    port,
	}
}
