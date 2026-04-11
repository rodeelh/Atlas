package config

import (
	"os"
	"path/filepath"
)

// SupportDir returns ~/Library/Application Support/ProjectAtlas — the same
// directory used by the Swift runtime and its macOS app.
func SupportDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "ProjectAtlas")
}

// ConfigPath returns the canonical config file path.
// Matches DefaultPathProvider.configFileURL() in StorageInterfaces.swift.
func ConfigPath() string {
	return filepath.Join(SupportDir(), "config.json")
}

// LegacyConfigPath returns the old config path for migration compatibility.
func LegacyConfigPath() string {
	return filepath.Join(SupportDir(), "atlas-config.json")
}

// DBPath returns the SQLite database path.
// Matches MemoryStore's database path in the Swift runtime.
func DBPath() string {
	return filepath.Join(SupportDir(), "atlas.sqlite3")
}

// AtlasInstallDir returns the directory where the Atlas runtime binary,
// web assets, and the bundled engine (llama-server) are installed.
// Distinct from SupportDir() which holds user data (SQLite, config, MIND.md…).
func AtlasInstallDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Atlas")
}

// ModelsDir returns the directory where Engine LM model files are stored.
// Lives under SupportDir() so models are preserved across uninstalls.
func ModelsDir() string {
	return filepath.Join(SupportDir(), "models")
}

// VoiceModelsDir returns the parent directory for Whisper and Piper models
// (subdirs whisper/ and piper/). Lives under SupportDir() so voice models
// survive runtime reinstalls.
func VoiceModelsDir() string {
	return filepath.Join(SupportDir(), "voice-models")
}

// TLSDir returns the directory where Atlas stores its built-in HTTPS assets
// (certificate and private key) for LAN access.
func TLSDir() string {
	return filepath.Join(SupportDir(), "tls")
}

// TLSCertPath returns the PEM-encoded certificate path for the built-in HTTPS
// listener.
func TLSCertPath() string {
	return filepath.Join(TLSDir(), "atlas-cert.pem")
}

// TLSKeyPath returns the PEM-encoded private key path for the built-in HTTPS
// listener.
func TLSKeyPath() string {
	return filepath.Join(TLSDir(), "atlas-key.pem")
}

// MLXModelsDir returns the directory where MLX-LM model directories are stored.
// Each model is a subdirectory (e.g. "Llama-3.2-3B-Instruct-4bit/") containing
// safetensors shards and a config.json — unlike llama.cpp which uses single .gguf files.
// Lives under SupportDir() so models are preserved across uninstalls.
func MLXModelsDir() string {
	return filepath.Join(SupportDir(), "mlx-models")
}

// MLXVenvDir returns the path to the Python virtual environment Atlas manages
// for the mlx-lm package. Owned entirely by Atlas; never shared with user envs.
func MLXVenvDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".atlas-mlx")
}

// FilesDir returns the default directory where Atlas stores files it generates,
// receives, or sends — unless the user or a skill specifies a different path.
// Created on first access if it does not exist.
func FilesDir() string {
	dir := filepath.Join(SupportDir(), "files")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}
