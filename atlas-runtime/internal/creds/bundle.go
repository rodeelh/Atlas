// Package creds provides shared credential reading and writing via the macOS Keychain.
package creds

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	keychain "github.com/keybase/go-keychain"
)

// Bundle holds all API credentials from the shared Keychain bundle.
type Bundle struct {
	OpenAIAPIKey       string            `json:"openAIAPIKey"`
	AnthropicAPIKey    string            `json:"anthropicAPIKey"`
	GeminiAPIKey       string            `json:"geminiAPIKey"`
	OpenRouterAPIKey   string            `json:"openRouterAPIKey"`
	LMStudioAPIKey     string            `json:"lmStudioAPIKey"`
	OllamaAPIKey       string            `json:"ollamaAPIKey"`
	BraveSearchAPIKey  string            `json:"braveSearchAPIKey"`
	TwitterBearerToken string            `json:"twitterBearerToken"`
	FinnhubAPIKey      string            `json:"finnhubAPIKey"`
	GoogleMapsAPIKey   string            `json:"googleMapsAPIKey"`
	ElevenLabsAPIKey   string            `json:"elevenLabsAPIKey"`
	TelegramBotToken   string            `json:"telegramBotToken"`
	DiscordBotToken    string            `json:"discordBotToken"`
	SlackBotToken      string            `json:"slackBotToken"`
	SlackAppToken      string            `json:"slackAppToken"`
	CustomSecrets      map[string]string `json:"customSecrets,omitempty"`
	CustomSecretLabels map[string]string `json:"customSecretLabels,omitempty"`
}

// CustomSecret returns a custom key value by name, or "" if not found.
// Skills use this to look up Forge-installed or user-defined API keys.
func (b Bundle) CustomSecret(name string) string {
	return b.CustomSecrets[name]
}

const (
	bundleService = "com.projectatlas.credentials"
	bundleAccount = "bundle"
)

// mu serialises all read-modify-write Keychain operations.
// Reads (Read) do not hold the mutex — only Store does.
var mu sync.Mutex

// Read reads the credential bundle from the macOS Keychain.
// It is a package-level var so tests in other packages can stub it without
// triggering macOS Keychain dialogs.
// Returns an empty Bundle (not an error) if the key is absent.
var Read = defaultRead

func defaultRead() (Bundle, error) {
	out, err := execSecurity("find-generic-password",
		"-s", bundleService,
		"-a", bundleAccount,
		"-w",
	)
	if err != nil {
		// Key not present is not an error at this level.
		return Bundle{}, nil
	}

	var b Bundle
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &b); err != nil {
		return Bundle{}, nil
	}
	return b, nil
}

// Store writes a single credential field into the Keychain bundle.
// provider values match the web UI's providerID strings.
// For custom/forge keys, provider is "custom" and name is the key name.
// This is the ONLY write path for credentials — never write partial structs.
func Store(provider, key, name string) error {
	mu.Lock()
	defer mu.Unlock()

	m, ok := readRaw()
	if !ok {
		// Bundle couldn't be read. Check whether the item exists at all.
		exists, existsErr := itemExists()
		if existsErr != nil || exists {
			return fmt.Errorf("credential bundle could not be read from Keychain — open Keychain Access and grant Atlas permission, then try again")
		}
		m = map[string]interface{}{} // first-time setup: start fresh
	}

	switch provider {
	case "openai":
		m["openAIAPIKey"] = key
	case "anthropic":
		m["anthropicAPIKey"] = key
	case "gemini":
		m["geminiAPIKey"] = key
	case "openrouter":
		m["openRouterAPIKey"] = key
	case "lm_studio":
		m["lmStudioAPIKey"] = key
	case "ollama":
		m["ollamaAPIKey"] = key
	case "telegram":
		m["telegramBotToken"] = key
	case "discord":
		m["discordBotToken"] = key
	case "slack", "slackBot": // web UI sends "slackBot"
		m["slackBotToken"] = key
	case "slackApp":
		m["slackAppToken"] = key
	case "twitter", "x":
		m["twitterBearerToken"] = key
	case "brave", "braveSearch":
		m["braveSearchAPIKey"] = key
	case "finnhub":
		m["finnhubAPIKey"] = key
	case "googlemaps", "googleMaps":
		m["googleMapsAPIKey"] = key
	case "elevenlabs":
		m["elevenLabsAPIKey"] = key
	default:
		// Custom key — stored under customSecrets[name].
		keyName := name
		if keyName == "" {
			keyName = provider
		}
		customs, _ := m["customSecrets"].(map[string]interface{})
		if customs == nil {
			customs = map[string]interface{}{}
		}
		customs[keyName] = key
		m["customSecrets"] = customs
	}

	return writeRaw(m)
}

// ── internal helpers ──────────────────────────────────────────────────────────

// readRaw reads the bundle as a generic map so we can update individual fields
// without losing unrecognised keys. Returns ok=false when the item can't be read.
// Callers must hold mu when using this as part of a read-modify-write.
func readRaw() (map[string]interface{}, bool) {
	out, err := execSecurity("find-generic-password",
		"-s", bundleService,
		"-a", bundleAccount,
		"-w",
	)
	if err != nil {
		return map[string]interface{}{}, false
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		return map[string]interface{}{}, false
	}
	return m, true
}

// writeRaw serialises the map and stores it in the Keychain via the native
// Security.framework API (no subprocess — value never appears in ps args).
// Callers must hold mu.
func writeRaw(m map[string]interface{}) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}
	return writeKeychainItem(bundleService, bundleAccount, data)
}

// writeKeychainItem upserts a generic-password Keychain item using the native
// Security.framework API. The value is passed directly to the kernel — it
// never appears in process args or shell command lines (fixes C-1/C-2).
func writeKeychainItem(service, account string, value []byte) error {
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(service)
	item.SetAccount(account)
	item.SetData(value)
	item.SetSynchronizable(keychain.SynchronizableNo)
	item.SetAccessible(keychain.AccessibleWhenUnlocked)

	// Try to update an existing item first.
	query := keychain.NewItem()
	query.SetSecClass(keychain.SecClassGenericPassword)
	query.SetService(service)
	query.SetAccount(account)
	query.SetMatchLimit(keychain.MatchLimitOne)

	err := keychain.UpdateItem(query, item)
	if err == keychain.ErrorItemNotFound {
		return keychain.AddItem(item)
	}
	return err
}

// itemExists returns true if the Keychain item exists (exit 0),
// false if not found (exit 44), and an error for any other failure.
func itemExists() (bool, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", bundleService, "-a", bundleAccount)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 44 {
			return false, nil
		}
	}
	return false, err
}

// execSecurity runs the macOS `security` CLI with the given arguments and
// returns stdout.
// The error message reports only the subcommand name — never full args, which
// may include Keychain secret values under the -w flag.
func execSecurity(args ...string) (string, error) {
	cmd := exec.Command("security", args...)
	out, err := cmd.Output()
	if err != nil {
		subcmd := ""
		if len(args) > 0 {
			subcmd = args[0]
		}
		return "", fmt.Errorf("security %s: %w", subcmd, err)
	}
	return string(out), nil
}
