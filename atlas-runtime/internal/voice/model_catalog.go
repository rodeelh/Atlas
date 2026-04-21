package voice

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/creds"
)

const (
	defaultOpenAISTTModel     = "gpt-4o-mini-transcribe"
	defaultOpenAITTSModel     = "gpt-4o-mini-tts"
	defaultGeminiSTTModel     = "gemini-2.5-flash"
	defaultGeminiTTSModel     = "gemini-2.5-flash-preview-tts"
	defaultElevenLabsSTTModel = "scribe_v1"
	defaultElevenLabsTTSModel = "eleven_turbo_v2_5"
)

func curatedProviderModels(provider ProviderType) ProviderModelSet {
	switch provider {
	case ProviderOpenAI:
		return ProviderModelSet{
			STT: []ProviderModelOption{
				{ID: "gpt-4o-transcribe", Label: "GPT-4o Transcribe"},
				{ID: defaultOpenAISTTModel, Label: "GPT-4o Mini Transcribe"},
				{ID: "whisper-1", Label: "Whisper (legacy)"},
			},
			TTS: []ProviderModelOption{
				{ID: "gpt-4o-mini-tts", Label: "GPT-4o Mini TTS"},
			},
		}
	case ProviderGemini:
		return ProviderModelSet{
			STT: []ProviderModelOption{
				{ID: defaultGeminiSTTModel, Label: "Gemini 2.5 Flash"},
				{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro"},
			},
			TTS: []ProviderModelOption{
				{ID: defaultGeminiTTSModel, Label: "Gemini 2.5 Flash TTS"},
				{ID: "gemini-2.5-pro-preview-tts", Label: "Gemini 2.5 Pro TTS"},
			},
		}
	case ProviderElevenLabs:
		return ProviderModelSet{
			STT: []ProviderModelOption{
				{ID: defaultElevenLabsSTTModel, Label: "Scribe v1"},
			},
			TTS: []ProviderModelOption{
				{ID: defaultElevenLabsTTSModel, Label: "Turbo v2.5"},
				{ID: "eleven_multilingual_v2", Label: "Multilingual v2"},
				{ID: "eleven_flash_v2_5", Label: "Flash v2.5"},
			},
		}
	default:
		return ProviderModelSet{}
	}
}

func sanitizeProviderConfig(cfg ProviderConfig) ProviderConfig {
	switch cfg.Type {
	case ProviderOpenAI:
		cfg.STTModel = sanitizeAllowedModel(cfg.STTModel, defaultOpenAISTTModel, curatedProviderModels(ProviderOpenAI).STT, func(model string) bool {
			lower := strings.ToLower(model)
			return lower == "whisper-1"
		})
		cfg.TTSModel = sanitizeAllowedModel(cfg.TTSModel, defaultOpenAITTSModel, curatedProviderModels(ProviderOpenAI).TTS, func(model string) bool {
			lower := strings.ToLower(model)
			return lower == "tts-1" || lower == "tts-1-hd"
		})
	case ProviderGemini:
		cfg.STTModel = sanitizeAllowedModel(cfg.STTModel, defaultGeminiSTTModel, curatedProviderModels(ProviderGemini).STT, func(model string) bool {
			lower := strings.ToLower(model)
			if lower == "gemini-2.0-flash" || lower == "gemini-1.5-flash" {
				return false
			}
			if strings.Contains(lower, "tts") || strings.Contains(lower, "image") || strings.Contains(lower, "embed") || strings.Contains(lower, "realtime") || strings.Contains(lower, "live") {
				return false
			}
			return strings.HasPrefix(lower, "gemini-")
		})
		cfg.TTSModel = sanitizeAllowedModel(cfg.TTSModel, defaultGeminiTTSModel, curatedProviderModels(ProviderGemini).TTS, func(model string) bool {
			return strings.HasPrefix(strings.ToLower(model), "gemini-") && strings.Contains(strings.ToLower(model), "tts")
		})
	case ProviderElevenLabs:
		cfg.STTModel = sanitizeAllowedModel(cfg.STTModel, defaultElevenLabsSTTModel, curatedProviderModels(ProviderElevenLabs).STT, nil)
		cfg.TTSModel = sanitizeAllowedModel(cfg.TTSModel, defaultElevenLabsTTSModel, curatedProviderModels(ProviderElevenLabs).TTS, nil)
	}
	return cfg
}

func sanitizeAllowedModel(current, fallback string, allowed []ProviderModelOption, extra func(string) bool) string {
	model := strings.TrimSpace(current)
	if model == "" {
		return fallback
	}
	for _, option := range allowed {
		if option.ID == model {
			return model
		}
	}
	if extra != nil && extra(model) {
		return model
	}
	return fallback
}

const modelCacheTTL = 60 * time.Second

var (
	modelCacheMu sync.Mutex
	modelCache   = map[ProviderType]cachedModelSet{}
)

type cachedModelSet struct {
	set       ProviderModelSet
	fetchedAt time.Time
}

func DiscoverProviderModels(provider ProviderType) ProviderModelSet {
	modelCacheMu.Lock()
	if cached, ok := modelCache[provider]; ok && time.Since(cached.fetchedAt) < modelCacheTTL {
		modelCacheMu.Unlock()
		return cached.set
	}
	modelCacheMu.Unlock()

	bundle, _ := creds.Read()
	apiKey := ""
	switch provider {
	case ProviderOpenAI:
		apiKey = bundle.OpenAIAPIKey
	case ProviderGemini:
		apiKey = bundle.GeminiAPIKey
	case ProviderElevenLabs:
		apiKey = bundle.ElevenLabsAPIKey
	}
	var result ProviderModelSet
	switch provider {
	case ProviderOpenAI:
		result = fetchOpenAIAudioModels(apiKey)
	case ProviderGemini:
		result = fetchGeminiAudioModels(apiKey)
	case ProviderElevenLabs:
		result = curatedProviderModels(provider)
	default:
		return ProviderModelSet{}
	}

	modelCacheMu.Lock()
	modelCache[provider] = cachedModelSet{set: result, fetchedAt: time.Now()}
	modelCacheMu.Unlock()
	return result
}

func fetchOpenAIAudioModels(apiKey string) ProviderModelSet {
	if strings.TrimSpace(apiKey) == "" {
		return curatedProviderModels(ProviderOpenAI)
	}
	req, err := http.NewRequest("GET", "https://api.openai.com/v1/models", nil)
	if err != nil {
		return curatedProviderModels(ProviderOpenAI)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return curatedProviderModels(ProviderOpenAI)
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return curatedProviderModels(ProviderOpenAI)
	}
	var sttIDs, ttsIDs []string
	for _, row := range result.Data {
		id := strings.TrimSpace(row.ID)
		lower := strings.ToLower(id)
		switch {
		case strings.Contains(lower, "transcribe") || lower == "whisper-1":
			sttIDs = append(sttIDs, id)
		case strings.Contains(lower, "tts"):
			ttsIDs = append(ttsIDs, id)
		}
	}
	stt := selectOpenAIAudioOptions(sttIDs, curatedProviderModels(ProviderOpenAI).STT)
	tts := selectOpenAIAudioOptions(ttsIDs, curatedProviderModels(ProviderOpenAI).TTS)
	if len(stt) == 0 {
		stt = curatedProviderModels(ProviderOpenAI).STT
	}
	if len(tts) == 0 {
		tts = curatedProviderModels(ProviderOpenAI).TTS
	}
	return ProviderModelSet{STT: stt, TTS: tts}
}

func fetchGeminiAudioModels(apiKey string) ProviderModelSet {
	if strings.TrimSpace(apiKey) == "" {
		return curatedProviderModels(ProviderGemini)
	}
	req, err := http.NewRequest("GET", "https://generativelanguage.googleapis.com/v1beta/openai/models", nil)
	if err != nil {
		return curatedProviderModels(ProviderGemini)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return curatedProviderModels(ProviderGemini)
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return curatedProviderModels(ProviderGemini)
	}
	var sttIDs, ttsIDs []string
	for _, row := range result.Data {
		id := strings.TrimPrefix(strings.TrimSpace(row.ID), "models/")
		lower := strings.ToLower(id)
		if !strings.HasPrefix(lower, "gemini-") {
			continue
		}
		if strings.Contains(lower, "tts") {
			ttsIDs = append(ttsIDs, id)
			continue
		}
		if strings.Contains(lower, "image") || strings.Contains(lower, "embed") || strings.Contains(lower, "realtime") || strings.Contains(lower, "live") || strings.Contains(lower, "native-audio") || strings.Contains(lower, "robotics") || strings.Contains(lower, "computer") {
			continue
		}
		sttIDs = append(sttIDs, id)
	}
	stt := selectGeminiAudioOptions(sttIDs, curatedProviderModels(ProviderGemini).STT)
	tts := selectGeminiAudioOptions(ttsIDs, curatedProviderModels(ProviderGemini).TTS)
	if len(stt) == 0 {
		stt = curatedProviderModels(ProviderGemini).STT
	}
	if len(tts) == 0 {
		tts = curatedProviderModels(ProviderGemini).TTS
	}
	return ProviderModelSet{STT: dedupeAudioOptions(stt), TTS: dedupeAudioOptions(tts)}
}

func dedupeAudioOptions(items []ProviderModelOption) []ProviderModelOption {
	out := make([]ProviderModelOption, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		out = append(out, item)
	}
	return out
}

func selectOpenAIAudioOptions(discoveredIDs []string, curated []ProviderModelOption) []ProviderModelOption {
	return selectProviderAudioOptions(discoveredIDs, curated, openAIAudioFamily)
}

func selectGeminiAudioOptions(discoveredIDs []string, curated []ProviderModelOption) []ProviderModelOption {
	return selectProviderAudioOptions(discoveredIDs, curated, geminiAudioFamily)
}

func selectProviderAudioOptions(discoveredIDs []string, curated []ProviderModelOption, family func(string) string) []ProviderModelOption {
	if len(discoveredIDs) == 0 {
		return nil
	}
	bestByFamily := map[string]string{}
	for _, id := range discoveredIDs {
		fam := family(id)
		if fam == "" {
			continue
		}
		prev, ok := bestByFamily[fam]
		if !ok || len(id) < len(prev) {
			bestByFamily[fam] = id
		}
	}
	out := make([]ProviderModelOption, 0, len(curated))
	for _, option := range curated {
		if resolved, ok := bestByFamily[family(option.ID)]; ok {
			out = append(out, ProviderModelOption{ID: resolved, Label: option.Label})
		}
	}
	return out
}

func openAIAudioFamily(id string) string {
	lower := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lower, "gpt-4o-transcribe"):
		return "gpt-4o-transcribe"
	case strings.HasPrefix(lower, "gpt-4o-mini-transcribe"):
		return "gpt-4o-mini-transcribe"
	case strings.HasPrefix(lower, "gpt-4o-mini-tts"):
		return "gpt-4o-mini-tts"
	default:
		return ""
	}
}

func geminiAudioFamily(id string) string {
	lower := strings.ToLower(id)
	switch {
	case strings.Contains(lower, "tts") && strings.Contains(lower, "2.5") && strings.Contains(lower, "flash"):
		return defaultGeminiTTSModel
	case strings.Contains(lower, "tts") && strings.Contains(lower, "2.5") && strings.Contains(lower, "pro"):
		return "gemini-2.5-pro-preview-tts"
	case !strings.Contains(lower, "tts") && strings.Contains(lower, "2.5") && strings.Contains(lower, "flash"):
		return defaultGeminiSTTModel
	case !strings.Contains(lower, "tts") && strings.Contains(lower, "2.5") && strings.Contains(lower, "pro"):
		return "gemini-2.5-pro"
	default:
		return ""
	}
}

func openAIAudioLabel(id string) string {
	switch id {
	case "gpt-4o-transcribe":
		return "GPT-4o Transcribe"
	case defaultOpenAISTTModel:
		return "GPT-4o Mini Transcribe"
	case "whisper-1":
		return "Whisper-1"
	case "tts-1":
		return "TTS-1"
	case "tts-1-hd":
		return "TTS-1 HD"
	case defaultOpenAITTSModel:
		return "GPT-4o Mini TTS"
	default:
		return id
	}
}

func geminiAudioLabel(id string) string {
	switch id {
	case defaultGeminiSTTModel:
		return "Gemini 2.5 Flash"
	case "gemini-2.5-pro":
		return "Gemini 2.5 Pro"
	case defaultGeminiTTSModel:
		return "Gemini 2.5 Flash TTS"
	case "gemini-2.5-pro-preview-tts":
		return "Gemini 2.5 Pro TTS"
	default:
		return id
	}
}

func geminiAudioScore(id string) int {
	lower := strings.ToLower(id)
	score := 0
	if strings.Contains(lower, "2.5") {
		score += 50
	}
	if strings.Contains(lower, "flash") {
		score += 20
	}
	if strings.Contains(lower, "pro") {
		score += 10
	}
	if strings.Contains(lower, "tts") {
		score += 5
	}
	if strings.Contains(lower, "preview") {
		score -= 1
	}
	return score
}
