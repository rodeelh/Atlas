package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type APIKeyStatus struct {
	OpenAIKeySet      bool              `json:"openAIKeySet"`
	OllamaKeySet      bool              `json:"ollamaKeySet"`
	TelegramTokenSet  bool              `json:"telegramTokenSet"`
	DiscordTokenSet   bool              `json:"discordTokenSet"`
	SlackBotTokenSet  bool              `json:"slackBotTokenSet"`
	SlackAppTokenSet  bool              `json:"slackAppTokenSet"`
	BraveSearchKeySet bool              `json:"braveSearchKeySet"`
	AnthropicKeySet   bool              `json:"anthropicKeySet"`
	GeminiKeySet      bool              `json:"geminiKeySet"`
	OpenRouterKeySet  bool              `json:"openRouterKeySet"`
	LMStudioKeySet    bool              `json:"lmStudioKeySet"`
	FinnhubKeySet     bool              `json:"finnhubKeySet"`
	GoogleMapsKeySet  bool              `json:"googleMapsKeySet"`
	ElevenLabsKeySet  bool              `json:"elevenLabsKeySet"`
	CustomKeys        []string          `json:"customKeys"`
	CustomKeyLabels   map[string]string `json:"customKeyLabels"`
}

type CredentialsService struct{}

func NewCredentialsService() *CredentialsService {
	s := &CredentialsService{}
	s.MigrateBundleCustomKeys()
	return s
}

func (s *CredentialsService) Status() APIKeyStatus {
	status := APIKeyStatus{CustomKeys: []string{}, CustomKeyLabels: map[string]string{}}
	out, err := execSecurity("find-generic-password", "-s", "com.projectatlas.credentials", "-a", "bundle", "-w")
	if err != nil {
		return status
	}
	var bundle struct {
		OpenAIAPIKey       string            `json:"openAIAPIKey"`
		TelegramBotToken   string            `json:"telegramBotToken"`
		DiscordBotToken    string            `json:"discordBotToken"`
		SlackBotToken      string            `json:"slackBotToken"`
		SlackAppToken      string            `json:"slackAppToken"`
		BraveSearchAPIKey  string            `json:"braveSearchAPIKey"`
		AnthropicAPIKey    string            `json:"anthropicAPIKey"`
		GeminiAPIKey       string            `json:"geminiAPIKey"`
		OpenRouterAPIKey   string            `json:"openRouterAPIKey"`
		LMStudioAPIKey     string            `json:"lmStudioAPIKey"`
		OllamaAPIKey       string            `json:"ollamaAPIKey"`
		FinnhubAPIKey      string            `json:"finnhubAPIKey"`
		GoogleMapsAPIKey   string            `json:"googleMapsAPIKey"`
		ElevenLabsAPIKey   string            `json:"elevenLabsAPIKey"`
		CustomSecrets      map[string]string `json:"customSecrets"`
		CustomSecretLabels map[string]string `json:"customSecretLabels"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &bundle); err != nil {
		return status
	}
	status.OpenAIKeySet = bundle.OpenAIAPIKey != ""
	status.TelegramTokenSet = bundle.TelegramBotToken != ""
	status.DiscordTokenSet = bundle.DiscordBotToken != ""
	status.SlackBotTokenSet = bundle.SlackBotToken != ""
	status.SlackAppTokenSet = bundle.SlackAppToken != ""
	status.BraveSearchKeySet = bundle.BraveSearchAPIKey != ""
	status.AnthropicKeySet = bundle.AnthropicAPIKey != ""
	status.GeminiKeySet = bundle.GeminiAPIKey != ""
	status.OpenRouterKeySet = bundle.OpenRouterAPIKey != ""
	status.LMStudioKeySet = bundle.LMStudioAPIKey != ""
	status.OllamaKeySet = bundle.OllamaAPIKey != ""
	status.FinnhubKeySet = bundle.FinnhubAPIKey != ""
	status.GoogleMapsKeySet = bundle.GoogleMapsAPIKey != ""
	status.ElevenLabsKeySet = bundle.ElevenLabsAPIKey != ""
	for k := range bundle.CustomSecrets {
		status.CustomKeys = append(status.CustomKeys, k)
		if lbl, ok := bundle.CustomSecretLabels[k]; ok && lbl != "" {
			status.CustomKeyLabels[k] = lbl
		}
	}
	sort.Strings(status.CustomKeys)
	return status
}

func (s *CredentialsService) Store(provider, key, name, label string) error {
	m, ok := readRawBundle()
	if !ok {
		exists, existsErr := keychainItemExists("com.projectatlas.credentials", "bundle")
		if existsErr != nil || exists {
			return fmt.Errorf("credential bundle could not be read from Keychain — open Keychain Access and grant Atlas permission, then try again")
		}
		m = map[string]interface{}{}
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
	case "slack", "slackBot":
		m["slackBotToken"] = key
	case "slackApp":
		m["slackAppToken"] = key
	case "brave", "braveSearch":
		m["braveSearchAPIKey"] = key
	case "finnhub":
		m["finnhubAPIKey"] = key
	case "googlemaps", "googleMaps":
		m["googleMapsAPIKey"] = key
	case "elevenlabs":
		m["elevenLabsAPIKey"] = key
	default:
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
		if label != "" {
			labels, _ := m["customSecretLabels"].(map[string]interface{})
			if labels == nil {
				labels = map[string]interface{}{}
			}
			labels[keyName] = label
			m["customSecretLabels"] = labels
		}
	}
	return writeRawBundle(m)
}

func (s *CredentialsService) Delete(name string) error {
	if name == "" {
		return nil
	}
	m, ok := readRawBundle()
	if !ok {
		return fmt.Errorf("credential bundle could not be read from Keychain — open Keychain Access and grant Atlas permission, then try again")
	}
	customs, _ := m["customSecrets"].(map[string]interface{})
	if customs != nil {
		delete(customs, name)
		m["customSecrets"] = customs
	}
	labels, _ := m["customSecretLabels"].(map[string]interface{})
	if labels != nil {
		delete(labels, name)
		m["customSecretLabels"] = labels
	}
	return writeRawBundle(m)
}

func (s *CredentialsService) MigrateBundleCustomKeys() {
	m, ok := readRawBundle()
	if !ok {
		return
	}
	customs, _ := m["customSecrets"].(map[string]interface{})
	if customs == nil {
		return
	}
	changed := false
	if v, ok := customs["braveSearch"].(string); ok && v != "" {
		if existing, _ := m["braveSearchAPIKey"].(string); existing == "" {
			m["braveSearchAPIKey"] = v
			delete(customs, "braveSearch")
			changed = true
		}
	}
	if v, ok := customs["finnhub"].(string); ok && v != "" {
		if existing, _ := m["finnhubAPIKey"].(string); existing == "" {
			m["finnhubAPIKey"] = v
			delete(customs, "finnhub")
			changed = true
		}
	}
	if v, ok := customs["slackBot"].(string); ok && v != "" {
		if existing, _ := m["slackBotToken"].(string); existing == "" {
			m["slackBotToken"] = v
			delete(customs, "slackBot")
			changed = true
		}
	}
	if changed {
		m["customSecrets"] = customs
		_ = writeRawBundle(m)
	}
}

func readRawBundle() (map[string]interface{}, bool) {
	out, err := execSecurity("find-generic-password", "-s", "com.projectatlas.credentials", "-a", "bundle", "-w")
	if err != nil {
		return map[string]interface{}{}, false
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		return map[string]interface{}{}, false
	}
	return m, true
}

func writeRawBundle(m map[string]interface{}) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}
	_, err = execSecurity("add-generic-password", "-U", "-s", "com.projectatlas.credentials", "-a", "bundle", "-w", string(data))
	return err
}

func keychainItemExists(service, account string) (bool, error) {
	_, err := execSecurity("find-generic-password", "-s", service, "-a", account)
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

func execSecurity(args ...string) (string, error) {
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
