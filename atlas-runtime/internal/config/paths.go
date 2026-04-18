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

// AtlasInstallDir returns the Atlas install directory. All downloaded and
// built artifacts live here: the daemon app bundle, web assets, engine and
// voice binaries, and all model files. User data (config, SQLite, MIND.md,
// custom skills, etc.) lives separately under SupportDir().
func AtlasInstallDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Atlas")
}

// ModelsDir returns the directory where Engine LM model files (GGUF) are stored.
// Lives under AtlasInstallDir() alongside the engine binary.
func ModelsDir() string {
	return filepath.Join(AtlasInstallDir(), "models")
}

// VoiceModelsDir returns the parent directory for Whisper and Kokoro models
// (subdirs whisper/ and kokoro/). Lives under AtlasInstallDir() alongside the
// voice binary.
func VoiceModelsDir() string {
	return filepath.Join(AtlasInstallDir(), "voice-models")
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
// Each model is a subdirectory containing safetensors shards and config.json.
// Lives under AtlasInstallDir() alongside other model artifacts.
func MLXModelsDir() string {
	return filepath.Join(AtlasInstallDir(), "mlx-models")
}

// MLXVenvDir returns the path to the Python virtual environment Atlas manages
// for the mlx-lm package. Lives under AtlasInstallDir() alongside other
// install artifacts.
func MLXVenvDir() string {
	return filepath.Join(AtlasInstallDir(), "mlx-venv")
}

// FilesDir returns the default directory where Atlas stores files it generates,
// receives, or sends — unless the user or a skill specifies a different path.
// Created on first access if it does not exist.
func FilesDir() string {
	dir := filepath.Join(AtlasInstallDir(), "files")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// GeneratedImagesDir returns the directory where AI-generated images are saved.
// Created on first access if it does not exist.
func GeneratedImagesDir() string {
	dir := filepath.Join(FilesDir(), "images")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// TelegramAttachmentsDir returns the directory where files received over
// Telegram (images, documents, voice notes, stickers) are saved.
// Lives under FilesDir() so users can browse them alongside other Atlas files.
// Created on first access if it does not exist.
func TelegramAttachmentsDir() string {
	dir := filepath.Join(FilesDir(), "Telegram")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// DiscordAttachmentsDir returns the directory where files received over Discord are saved.
func DiscordAttachmentsDir() string {
	dir := filepath.Join(FilesDir(), "Discord")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// WhatsAppAttachmentsDir returns the directory where files received over WhatsApp are saved.
func WhatsAppAttachmentsDir() string {
	dir := filepath.Join(FilesDir(), "WhatsApp")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}
