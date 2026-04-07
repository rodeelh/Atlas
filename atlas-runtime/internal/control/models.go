package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/engine"
	"atlas-runtime-go/internal/logstore"
)

type ModelsService struct {
	cfgStore  *config.Store
	engineMgr *engine.Manager
	now       func() time.Time
	httpDo    func(*http.Request) (*http.Response, error)
}

var fetchOpenRouterModelsFn = fetchOpenRouterModels

func NewModelsService(cfgStore *config.Store, mgr *engine.Manager) *ModelsService {
	return &ModelsService{
		cfgStore:  cfgStore,
		engineMgr: mgr,
		now:       time.Now,
		httpDo:    (&http.Client{Timeout: 10 * time.Second}).Do,
	}
}

type ModelRecord struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	IsFast      bool   `json:"isFast"`
}

func (s *ModelsService) Selected() map[string]any {
	snap := s.cfgStore.Load()
	return map[string]any{
		"activeAIProvider":             snap.ActiveAIProvider,
		"selectedOpenAIPrimaryModel":   snap.SelectedOpenAIPrimaryModel,
		"selectedOpenAIFastModel":      snap.SelectedOpenAIFastModel,
		"selectedAnthropicModel":       snap.SelectedAnthropicModel,
		"selectedAnthropicFastModel":   snap.SelectedAnthropicFastModel,
		"selectedGeminiModel":          snap.SelectedGeminiModel,
		"selectedGeminiFastModel":      snap.SelectedGeminiFastModel,
		"selectedOpenRouterModel":      snap.SelectedOpenRouterModel,
		"selectedOpenRouterFastModel":  snap.SelectedOpenRouterFastModel,
		"selectedLMStudioModel":        snap.SelectedLMStudioModel,
		"selectedLMStudioModelFast":    snap.SelectedLMStudioModelFast,
		"selectedOllamaModel":          snap.SelectedOllamaModel,
		"selectedOllamaModelFast":      snap.SelectedOllamaModelFast,
		"selectedAtlasEngineModel":     snap.SelectedAtlasEngineModel,
		"selectedAtlasEngineModelFast": snap.SelectedAtlasEngineModelFast,
		"lastRefreshed":                nil,
	}
}

func (s *ModelsService) Available(provider string) map[string]any {
	cfg := s.cfgStore.Load()
	bundle, _ := readRawBundle()
	now := s.now().UTC().Format(time.RFC3339)

	switch provider {
	case "openai":
		primary := cfg.DefaultOpenAIModel
		if cfg.SelectedOpenAIPrimaryModel != "" {
			primary = cfg.SelectedOpenAIPrimaryModel
		}
		if primary == "" {
			primary = "gpt-4.1-mini"
		}
		fast := cfg.SelectedOpenAIFastModel
		if fast == "" {
			fast = "gpt-4.1-mini"
		}
		apiKey, _ := bundle["openAIAPIKey"].(string)
		return map[string]any{
			"primaryModel":    primary,
			"fastModel":       fast,
			"lastRefreshedAt": now,
			"availableModels": fetchOpenAIModels(apiKey),
		}
	case "anthropic":
		primary := cfg.SelectedAnthropicModel
		if primary == "" {
			primary = "claude-haiku-4-5-20251001"
		}
		fast := cfg.SelectedAnthropicFastModel
		if fast == "" {
			fast = "claude-haiku-4-5-20251001"
		}
		apiKey, _ := bundle["anthropicAPIKey"].(string)
		return map[string]any{
			"primaryModel":    primary,
			"fastModel":       fast,
			"lastRefreshedAt": now,
			"availableModels": fetchAnthropicModels(apiKey),
		}
	case "gemini":
		primary := cfg.SelectedGeminiModel
		if primary == "" {
			primary = "gemini-2.5-flash"
		}
		fast := cfg.SelectedGeminiFastModel
		if fast == "" {
			fast = "gemini-2.5-flash"
		}
		apiKey, _ := bundle["geminiAPIKey"].(string)
		return map[string]any{
			"primaryModel":    primary,
			"fastModel":       fast,
			"lastRefreshedAt": now,
			"availableModels": fetchGeminiModels(apiKey),
		}
	case "openrouter":
		apiKey, _ := bundle["openRouterAPIKey"].(string)
		models := s.openRouterModels(false, apiKey, cfg, 25)
		primary := cfg.SelectedOpenRouterModel
		if primary == "" {
			// Default to OpenRouter's free router when no model is explicitly selected.
			primary = "openrouter/auto:free"
		}
		fast := cfg.SelectedOpenRouterFastModel
		if fast == "" {
			fast = primary
		}
		return map[string]any{
			"primaryModel":    primary,
			"fastModel":       fast,
			"lastRefreshedAt": now,
			"availableModels": models,
		}
	case "lm_studio":
		primary := cfg.SelectedLMStudioModel
		if primary == "" {
			primary = "local-model"
		}
		fast := cfg.SelectedLMStudioModelFast
		if fast == "" {
			fast = primary
		}
		apiKey, _ := bundle["lmStudioAPIKey"].(string)
		baseURL := cfg.LMStudioBaseURL
		if baseURL == "" {
			baseURL = "http://localhost:1234"
		}
		return map[string]any{
			"primaryModel":    primary,
			"fastModel":       fast,
			"lastRefreshedAt": now,
			"availableModels": fetchLMStudioModels(baseURL, apiKey),
		}
	case "ollama":
		primary := cfg.SelectedOllamaModel
		if primary == "" {
			primary = "llama3.2"
		}
		fast := cfg.SelectedOllamaModelFast
		if fast == "" {
			fast = primary
		}
		apiKey, _ := bundle["ollamaAPIKey"].(string)
		baseURL := cfg.OllamaBaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return map[string]any{
			"primaryModel":    primary,
			"fastModel":       fast,
			"lastRefreshedAt": now,
			"availableModels": fetchOllamaModels(baseURL, apiKey),
		}
	case "atlas_engine":
		primary := filepath.Base(cfg.SelectedAtlasEngineModel)
		if primary == "" || primary == "." {
			primary = ""
		}
		fast := filepath.Base(cfg.SelectedAtlasEngineModelFast)
		if fast == "" || fast == "." {
			fast = primary
		}
		var models []ModelRecord
		if s.engineMgr != nil {
			if infos, err := s.engineMgr.ListModels(); err == nil {
				for _, m := range infos {
					models = append(models, ModelRecord{ID: m.Name, DisplayName: m.Name, IsFast: false})
				}
			}
		}
		if models == nil {
			models = []ModelRecord{}
		}
		return map[string]any{
			"primaryModel":    primary,
			"fastModel":       fast,
			"lastRefreshedAt": now,
			"availableModels": models,
		}
	default:
		return map[string]any{"availableModels": []ModelRecord{}}
	}
}

func (s *ModelsService) OpenRouterModels(refresh bool, limit int) map[string]any {
	cfg := s.cfgStore.Load()
	bundle, _ := readRawBundle()
	apiKey, _ := bundle["openRouterAPIKey"].(string)
	models := s.openRouterModels(refresh, apiKey, cfg, limit)
	return map[string]any{
		"lastRefreshedAt": s.now().UTC().Format(time.RFC3339),
		"availableModels": models,
	}
}

func (s *ModelsService) OpenRouterModelHealth(model string) map[string]any {
	now := s.now().UTC().Format(time.RFC3339)
	model = strings.TrimSpace(model)
	logResult := func(level, status, message string, extra map[string]string) {
		meta := map[string]string{
			"provider": "openrouter",
			"model":    model,
			"status":   status,
		}
		for k, v := range extra {
			meta[k] = v
		}
		logstore.Write(level, "OpenRouter model health check", meta)
	}
	if model == "" {
		logResult("warn", "unknown", "No model selected.", nil)
		return map[string]any{
			"status":    "unknown",
			"message":   "No model selected.",
			"checkedAt": now,
		}
	}

	bundle, _ := readRawBundle()
	apiKey, _ := bundle["openRouterAPIKey"].(string)
	if strings.TrimSpace(apiKey) == "" {
		logResult("warn", "missing_key", "OpenRouter API key is not configured.", nil)
		return map[string]any{
			"status":    "missing_key",
			"message":   "OpenRouter API key is not configured.",
			"checkedAt": now,
		}
	}

	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens":  1,
		"temperature": 0,
	})
	req, err := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		logResult("error", "unavailable", "Could not construct OpenRouter health request.", nil)
		return map[string]any{
			"status":    "unavailable",
			"message":   "Could not construct OpenRouter health request.",
			"checkedAt": now,
		}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/rodeelh/project-atlas")
	req.Header.Set("X-Title", "Atlas")

	resp, err := s.httpDo(req)
	if err != nil {
		logResult("error", "unavailable", "Unable to reach OpenRouter right now.", nil)
		return map[string]any{
			"status":    "unavailable",
			"message":   "Unable to reach OpenRouter right now.",
			"checkedAt": now,
		}
	}
	defer resp.Body.Close()

	var errBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	msg := strings.TrimSpace(errBody.Error.Message)

	if resp.StatusCode == http.StatusTooManyRequests {
		if msg == "" {
			msg = "This model is currently rate limited on OpenRouter."
		}
		logResult("warn", "rate_limited", msg, map[string]string{"http_status": strconv.Itoa(resp.StatusCode)})
		return map[string]any{
			"status":    "rate_limited",
			"message":   msg,
			"checkedAt": now,
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logResult("info", "ok", "Model is currently available.", map[string]string{"http_status": strconv.Itoa(resp.StatusCode)})
		return map[string]any{
			"status":    "ok",
			"message":   "Model is currently available.",
			"checkedAt": now,
		}
	}
	if msg == "" {
		msg = fmt.Sprintf("OpenRouter returned %d for this model health check.", resp.StatusCode)
	}
	logResult("warn", "warning", msg, map[string]string{"http_status": strconv.Itoa(resp.StatusCode)})
	return map[string]any{
		"status":    "warning",
		"message":   msg,
		"checkedAt": now,
	}
}

func (s *ModelsService) CloudModelHealth(provider, model string) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	now := s.now().UTC().Format(time.RFC3339)

	if provider == "" {
		return map[string]any{
			"status":    "unknown",
			"message":   "No provider selected.",
			"checkedAt": now,
		}
	}
	if model == "" {
		return map[string]any{
			"status":    "unknown",
			"message":   "No model selected.",
			"checkedAt": now,
		}
	}
	if provider == "openrouter" {
		return s.OpenRouterModelHealth(model)
	}

	bundle, _ := readRawBundle()

	type reqInfo struct {
		url     string
		headers map[string]string
		body    map[string]any
	}
	makeReq := func() (reqInfo, string) {
		switch provider {
		case "openai":
			apiKey, _ := bundle["openAIAPIKey"].(string)
			if strings.TrimSpace(apiKey) == "" {
				return reqInfo{}, "missing_key"
			}
			return reqInfo{
				url: "https://api.openai.com/v1/chat/completions",
				headers: map[string]string{
					"Authorization": "Bearer " + apiKey,
					"Content-Type":  "application/json",
				},
				body: map[string]any{
					"model":                 model,
					"messages":              []map[string]string{{"role": "user", "content": "ping"}},
					"max_completion_tokens": 16,
				},
			}, ""
		case "anthropic":
			apiKey, _ := bundle["anthropicAPIKey"].(string)
			if strings.TrimSpace(apiKey) == "" {
				return reqInfo{}, "missing_key"
			}
			return reqInfo{
				url: "https://api.anthropic.com/v1/messages",
				headers: map[string]string{
					"x-api-key":         apiKey,
					"anthropic-version": "2023-06-01",
					"Content-Type":      "application/json",
				},
				body: map[string]any{
					"model":      model,
					"max_tokens": 1,
					"messages": []map[string]string{
						{"role": "user", "content": "ping"},
					},
				},
			}, ""
		case "gemini":
			apiKey, _ := bundle["geminiAPIKey"].(string)
			if strings.TrimSpace(apiKey) == "" {
				return reqInfo{}, "missing_key"
			}
			return reqInfo{
				url: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
				headers: map[string]string{
					"Authorization": "Bearer " + apiKey,
					"Content-Type":  "application/json",
				},
				body: map[string]any{
					"model":       model,
					"messages":    []map[string]string{{"role": "user", "content": "ping"}},
					"max_tokens":  1,
					"temperature": 0,
				},
			}, ""
		default:
			return reqInfo{}, "unknown_provider"
		}
	}

	info, reqErr := makeReq()
	if reqErr == "missing_key" {
		logstore.Write("warn", "Cloud model health check", map[string]string{
			"provider": provider,
			"model":    model,
			"status":   "missing_key",
		})
		return map[string]any{
			"status":    "missing_key",
			"message":   "API key is not configured for this provider.",
			"checkedAt": now,
		}
	}
	if reqErr == "unknown_provider" {
		return map[string]any{
			"status":    "unknown",
			"message":   "Unsupported provider for health check.",
			"checkedAt": now,
		}
	}

	body, _ := json.Marshal(info.body)
	req, err := http.NewRequest("POST", info.url, bytes.NewReader(body))
	if err != nil {
		logstore.Write("error", "Cloud model health check", map[string]string{
			"provider": provider,
			"model":    model,
			"status":   "unavailable",
			"error":    err.Error(),
		})
		return map[string]any{
			"status":    "unavailable",
			"message":   "Could not construct health request.",
			"checkedAt": now,
		}
	}
	for k, v := range info.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.httpDo(req)
	if err != nil {
		logstore.Write("error", "Cloud model health check", map[string]string{
			"provider": provider,
			"model":    model,
			"status":   "unavailable",
			"error":    err.Error(),
		})
		return map[string]any{
			"status":    "unavailable",
			"message":   "Unable to reach provider right now.",
			"checkedAt": now,
		}
	}
	defer resp.Body.Close()

	var errBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	msg := strings.TrimSpace(errBody.Error.Message)
	lowerMsg := strings.ToLower(msg)

	// Some providers/models may return a token-cap probe error even though the
	// model is reachable and authenticated. Treat this as healthy for status UI.
	if strings.Contains(lowerMsg, "max_tokens") && strings.Contains(lowerMsg, "output limit was reached") {
		logstore.Write("info", "Cloud model health check", map[string]string{
			"provider":    provider,
			"model":       model,
			"status":      "ok",
			"http_status": strconv.Itoa(resp.StatusCode),
			"probe_note":  "token_cap_reached",
		})
		return map[string]any{
			"status":    "ok",
			"message":   "Model is reachable (health probe hit output token cap).",
			"checkedAt": now,
		}
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		if msg == "" {
			msg = "This model is currently rate limited."
		}
		logstore.Write("warn", "Cloud model health check", map[string]string{
			"provider":    provider,
			"model":       model,
			"status":      "rate_limited",
			"http_status": strconv.Itoa(resp.StatusCode),
		})
		return map[string]any{
			"status":    "rate_limited",
			"message":   msg,
			"checkedAt": now,
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logstore.Write("info", "Cloud model health check", map[string]string{
			"provider":    provider,
			"model":       model,
			"status":      "ok",
			"http_status": strconv.Itoa(resp.StatusCode),
		})
		return map[string]any{
			"status":    "ok",
			"message":   "Model is currently available.",
			"checkedAt": now,
		}
	}
	if msg == "" {
		displayProvider := provider
		switch provider {
		case "openai":
			displayProvider = "OpenAI"
		case "anthropic":
			displayProvider = "Anthropic"
		case "gemini":
			displayProvider = "Gemini"
		case "openrouter":
			displayProvider = "OpenRouter"
		}
		msg = fmt.Sprintf("%s returned %d for this model health check.", displayProvider, resp.StatusCode)
	}
	logstore.Write("warn", "Cloud model health check", map[string]string{
		"provider":    provider,
		"model":       model,
		"status":      "warning",
		"http_status": strconv.Itoa(resp.StatusCode),
	})
	return map[string]any{
		"status":    "warning",
		"message":   msg,
		"checkedAt": now,
	}
}

func (s *ModelsService) openRouterModels(refresh bool, apiKey string, cfg config.RuntimeConfigSnapshot, limit int) []ModelRecord {
	if limit <= 0 {
		limit = 25
	}
	if !refresh && cfg.OpenRouterModelCache.FetchedAt != "" {
		if fetchedAt, err := time.Parse(time.RFC3339, cfg.OpenRouterModelCache.FetchedAt); err == nil {
			if s.now().Sub(fetchedAt) < 24*time.Hour && len(cfg.OpenRouterModelCache.Models) > 0 {
				models := make([]ModelRecord, 0, len(cfg.OpenRouterModelCache.Models))
				for _, m := range cfg.OpenRouterModelCache.Models {
					models = append(models, ModelRecord{ID: m.ID, DisplayName: m.DisplayName, IsFast: m.IsFast})
				}
				if len(models) > limit {
					return models[:limit]
				}
				return models
			}
		}
	}

	models := fetchOpenRouterModelsFn(apiKey, s.httpDo)
	if len(models) == 0 && len(cfg.OpenRouterModelCache.Models) > 0 {
		out := make([]ModelRecord, 0, len(cfg.OpenRouterModelCache.Models))
		for _, m := range cfg.OpenRouterModelCache.Models {
			out = append(out, ModelRecord{ID: m.ID, DisplayName: m.DisplayName, IsFast: m.IsFast})
		}
		if len(out) > limit {
			return out[:limit]
		}
		return out
	}
	if len(models) == 0 {
		return []ModelRecord{}
	}

	cfg.OpenRouterModelCache = config.OpenRouterModelCache{
		FetchedAt: s.now().UTC().Format(time.RFC3339),
		Models:    make([]config.CachedModelRecord, 0, len(models)),
	}
	for _, m := range models {
		cfg.OpenRouterModelCache.Models = append(cfg.OpenRouterModelCache.Models, config.CachedModelRecord{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			IsFast:      m.IsFast,
		})
	}
	_ = s.cfgStore.Save(cfg)
	if len(models) > limit {
		return models[:limit]
	}
	return models
}

func (s *ModelsService) RefreshActive() map[string]any {
	cfg := s.cfgStore.Load()
	return s.Available(cfg.ActiveAIProvider)
}

func fetchOpenAIModels(apiKey string) []ModelRecord {
	if apiKey == "" {
		return curatedOpenAIModels()
	}
	req, err := http.NewRequest("GET", "https://api.openai.com/v1/models", nil)
	if err != nil {
		return curatedOpenAIModels()
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return curatedOpenAIModels()
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return curatedOpenAIModels()
	}
	skipKeywords := []string{"audio", "realtime", "tts", "whisper", "transcrib", "search", "embed", "instruct"}
	fastKeywords := []string{"mini", "nano", "lite"}
	type entry struct{ id, base string }
	var candidates []entry
	seen := map[string]bool{}
	for _, m := range result.Data {
		id := m.ID
		if seen[id] {
			continue
		}
		lower := strings.ToLower(id)
		if !strings.HasPrefix(lower, "gpt-") && !strings.HasPrefix(lower, "o") {
			continue
		}
		skip := false
		for _, kw := range skipKeywords {
			if strings.Contains(lower, kw) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		seen[id] = true
		candidates = append(candidates, entry{id: id, base: openAIBaseFamily(id)})
	}
	familyBest := map[string]string{}
	for _, c := range candidates {
		prev, ok := familyBest[c.base]
		if !ok || len(c.id) < len(prev) {
			familyBest[c.base] = c.id
		}
	}
	bases := make([]string, 0, len(familyBest))
	for b := range familyBest {
		bases = append(bases, b)
	}
	sort.Slice(bases, func(i, j int) bool { return openAIModelScore(bases[i]) > openAIModelScore(bases[j]) })
	var models []ModelRecord
	for _, base := range bases {
		id := familyBest[base]
		lower := strings.ToLower(id)
		isFast := false
		for _, kw := range fastKeywords {
			if strings.Contains(lower, kw) {
				isFast = true
				break
			}
		}
		models = append(models, ModelRecord{ID: id, DisplayName: openAIDisplayName(id), IsFast: isFast})
	}
	top := topFastAndPrimary(models, 5)
	if len(top) == 0 {
		return curatedOpenAIModels()
	}
	return top
}

func curatedOpenAIModels() []ModelRecord {
	return []ModelRecord{
		{ID: "gpt-4.1", DisplayName: "GPT-4.1", IsFast: false},
		{ID: "gpt-4o", DisplayName: "GPT-4o", IsFast: false},
		{ID: "o3", DisplayName: "O3", IsFast: false},
		{ID: "o4", DisplayName: "O4", IsFast: false},
		{ID: "gpt-4-turbo", DisplayName: "GPT-4 Turbo", IsFast: false},
		{ID: "gpt-4.1-mini", DisplayName: "GPT-4.1 Mini", IsFast: true},
		{ID: "gpt-4o-mini", DisplayName: "GPT-4o Mini", IsFast: true},
		{ID: "o4-mini", DisplayName: "O4 Mini", IsFast: true},
		{ID: "o3-mini", DisplayName: "O3 Mini", IsFast: true},
		{ID: "gpt-4.1-nano", DisplayName: "GPT-4.1 Nano", IsFast: true},
	}
}

func openAIBaseFamily(id string) string {
	re := regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$|-\d{8}$`)
	return re.ReplaceAllString(id, "")
}

func openAIDisplayName(id string) string {
	for _, pair := range [][2]string{
		{"gpt-4.1-mini", "GPT-4.1 Mini"}, {"gpt-4.1-nano", "GPT-4.1 Nano"}, {"gpt-4.1", "GPT-4.1"},
		{"gpt-4o-mini", "GPT-4o Mini"}, {"gpt-4o", "GPT-4o"}, {"gpt-4-turbo", "GPT-4 Turbo"}, {"gpt-4", "GPT-4"},
		{"o4-mini", "O4 Mini"}, {"o3-mini", "O3 Mini"}, {"o3", "O3"},
		{"o1-mini", "O1 Mini"}, {"o1-preview", "O1 Preview"}, {"o1", "O1"},
	} {
		if strings.HasPrefix(id, pair[0]) {
			suffix := strings.TrimPrefix(id, pair[0])
			if suffix == "" {
				return pair[1]
			}
			return pair[1] + " (" + strings.TrimPrefix(suffix, "-") + ")"
		}
	}
	return id
}

func fetchAnthropicModels(apiKey string) []ModelRecord {
	if apiKey == "" {
		return curatedAnthropicModels()
	}
	req, err := http.NewRequest("GET", "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return curatedAnthropicModels()
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return curatedAnthropicModels()
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Data) == 0 {
		return curatedAnthropicModels()
	}
	fastKeywords := []string{"haiku", "flash"}
	var models []ModelRecord
	for _, m := range result.Data {
		name := m.DisplayName
		if name == "" {
			name = anthropicDisplayName(m.ID)
		}
		isFast := false
		lower := strings.ToLower(m.ID)
		for _, kw := range fastKeywords {
			if strings.Contains(lower, kw) {
				isFast = true
				break
			}
		}
		models = append(models, ModelRecord{ID: m.ID, DisplayName: name, IsFast: isFast})
	}
	return topFastAndPrimary(models, 5)
}

func curatedAnthropicModels() []ModelRecord {
	return []ModelRecord{
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", IsFast: false},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6", IsFast: false},
		{ID: "claude-sonnet-4-5-20250929", DisplayName: "Claude Sonnet 4.5", IsFast: false},
		{ID: "claude-opus-4-5-20251101", DisplayName: "Claude Opus 4.5", IsFast: false},
		{ID: "claude-opus-4-1-20250805", DisplayName: "Claude Opus 4.1", IsFast: false},
		{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5", IsFast: true},
		{ID: "claude-haiku-4-6", DisplayName: "Claude Haiku 4.6", IsFast: true},
		{ID: "claude-3-5-haiku-20241022", DisplayName: "Claude 3.5 Haiku", IsFast: true},
		{ID: "claude-3-5-sonnet-20241022", DisplayName: "Claude 3.5 Sonnet", IsFast: false},
		{ID: "claude-3-haiku-20240307", DisplayName: "Claude 3 Haiku", IsFast: true},
	}
}

func anthropicDisplayName(id string) string {
	for _, pair := range [][2]string{
		{"claude-sonnet-4-6", "Claude Sonnet 4.6"}, {"claude-opus-4-6", "Claude Opus 4.6"},
		{"claude-haiku-4-6", "Claude Haiku 4.6"}, {"claude-haiku-4-5", "Claude Haiku 4.5"},
		{"claude-sonnet-4-5", "Claude Sonnet 4.5"}, {"claude-opus-4-5", "Claude Opus 4.5"},
		{"claude-sonnet-4-1", "Claude Sonnet 4.1"}, {"claude-opus-4-1", "Claude Opus 4.1"},
		{"claude-sonnet-4", "Claude Sonnet 4"}, {"claude-opus-4", "Claude Opus 4"},
		{"claude-3-5-sonnet", "Claude 3.5 Sonnet"}, {"claude-3-5-haiku", "Claude 3.5 Haiku"},
		{"claude-3-opus", "Claude 3 Opus"}, {"claude-3-sonnet", "Claude 3 Sonnet"},
		{"claude-3-haiku", "Claude 3 Haiku"},
	} {
		if strings.HasPrefix(id, pair[0]) {
			return pair[1]
		}
	}
	return id
}

func fetchGeminiModels(apiKey string) []ModelRecord {
	if apiKey == "" {
		return curatedGeminiModels()
	}
	req, err := http.NewRequest("GET", "https://generativelanguage.googleapis.com/v1beta/openai/models", nil)
	if err != nil {
		return curatedGeminiModels()
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return curatedGeminiModels()
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Data) == 0 {
		return curatedGeminiModels()
	}
	geminiSkip := []string{"tts", "audio", "embed", "image", "computer", "robotics", "live", "realtime", "native-audio"}
	var candidates []ModelRecord
	for _, m := range result.Data {
		bare := strings.TrimPrefix(m.ID, "models/")
		lower := strings.ToLower(bare)
		skip := false
		for _, kw := range geminiSkip {
			if strings.Contains(lower, kw) {
				skip = true
				break
			}
		}
		if skip || !strings.HasPrefix(lower, "gemini") {
			continue
		}
		candidates = append(candidates, ModelRecord{ID: bare, DisplayName: geminiDisplayName(bare), IsFast: strings.Contains(lower, "flash")})
	}
	sort.Slice(candidates, func(i, j int) bool {
		si, sj := geminiModelScore(candidates[i].ID), geminiModelScore(candidates[j].ID)
		if si != sj {
			return si > sj
		}
		return len(candidates[i].ID) < len(candidates[j].ID)
	})
	seen := map[string]bool{}
	var models []ModelRecord
	for _, m := range candidates {
		base := geminiBaseFamily(m.ID)
		if seen[base] {
			continue
		}
		seen[base] = true
		models = append(models, m)
	}
	top := topFastAndPrimary(models, 5)
	if len(top) == 0 {
		return curatedGeminiModels()
	}
	return top
}

func fetchOpenRouterModels(apiKey string, do func(*http.Request) (*http.Response, error)) []ModelRecord {
	if apiKey == "" {
		return []ModelRecord{}
	}
	req, err := http.NewRequest("GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return []ModelRecord{}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/rodeelh/project-atlas")
	req.Header.Set("X-Title", "Atlas")
	resp, err := do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return []ModelRecord{}
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Architecture  struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			Pricing struct {
				Prompt string `json:"prompt"`
			} `json:"pricing"`
			TopProvider struct {
				MaxCompletionTokens int `json:"max_completion_tokens"`
			} `json:"top_provider"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return []ModelRecord{}
	}
	type scored struct {
		model ModelRecord
		score int
	}
	hasText := func(modalities []string) bool {
		for _, m := range modalities {
			if strings.EqualFold(strings.TrimSpace(m), "text") {
				return true
			}
		}
		return false
	}
	nonTextKeywords := []string{
		"image", "vision", "video", "audio", "speech", "tts", "transcrib", "asr",
		"whisper", "embedding", "embed", "rerank", "moderation", "diffusion",
		"lyria", "music",
	}
	items := make([]scored, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID == "" {
			continue
		}
		display := m.Name
		if strings.TrimSpace(display) == "" {
			display = m.ID
		}
		lower := strings.ToLower(m.ID + " " + display)
		// Keep only text-capable chat models.
		if len(m.Architecture.InputModalities) > 0 && !hasText(m.Architecture.InputModalities) {
			continue
		}
		if len(m.Architecture.OutputModalities) > 0 && !hasText(m.Architecture.OutputModalities) {
			continue
		}
		skip := false
		for _, kw := range nonTextKeywords {
			if strings.Contains(lower, kw) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		isFast := strings.Contains(lower, "mini") || strings.Contains(lower, "flash") || strings.Contains(lower, "haiku")
		// TODO: cost telemetry — pricing is parsed but not yet persisted/used in usage tracking.
		promptCostScore := 0
		if m.Pricing.Prompt != "" {
			if f, err := strconv.ParseFloat(m.Pricing.Prompt, 64); err == nil {
				// Lower price is better; tiny scaling to keep deterministic ordering.
				promptCostScore = int((1.0 / (f + 0.000001)) * 10)
			}
		}
		score := m.ContextLength/1024 + m.TopProvider.MaxCompletionTokens/1024 + promptCostScore
		items = append(items, scored{
			model: ModelRecord{ID: m.ID, DisplayName: display, IsFast: isFast},
			score: score,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		return items[i].model.ID < items[j].model.ID
	})
	out := make([]ModelRecord, 0, len(items))
	for _, it := range items {
		out = append(out, it.model)
	}
	return out
}

func curatedGeminiModels() []ModelRecord {
	return []ModelRecord{
		{ID: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", IsFast: false},
		{ID: "gemini-2.0-pro", DisplayName: "Gemini 2.0 Pro", IsFast: false},
		{ID: "gemini-1.5-pro", DisplayName: "Gemini 1.5 Pro", IsFast: false},
		{ID: "gemini-3-pro", DisplayName: "Gemini 3 Pro", IsFast: false},
		{ID: "gemini-3.1-pro", DisplayName: "Gemini 3.1 Pro", IsFast: false},
		{ID: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", IsFast: true},
		{ID: "gemini-2.5-flash-lite", DisplayName: "Gemini 2.5 Flash Lite", IsFast: true},
		{ID: "gemini-2.0-flash-001", DisplayName: "Gemini 2.0 Flash", IsFast: true},
		{ID: "gemini-2.0-flash-lite", DisplayName: "Gemini 2.0 Flash Lite", IsFast: true},
		{ID: "gemini-3-flash", DisplayName: "Gemini 3 Flash", IsFast: true},
	}
}

func geminiDisplayName(id string) string {
	bare := strings.TrimPrefix(id, "models/")
	for _, pair := range [][2]string{
		{"gemini-3.1-pro", "Gemini 3.1 Pro"}, {"gemini-3.1-flash", "Gemini 3.1 Flash"},
		{"gemini-3-pro", "Gemini 3 Pro"}, {"gemini-3-flash", "Gemini 3 Flash"},
		{"gemini-2.5-pro", "Gemini 2.5 Pro"}, {"gemini-2.5-flash-lite", "Gemini 2.5 Flash Lite"},
		{"gemini-2.5-flash", "Gemini 2.5 Flash"}, {"gemini-2.0-flash-lite", "Gemini 2.0 Flash Lite"},
		{"gemini-2.0-flash", "Gemini 2.0 Flash"}, {"gemini-2.0-pro", "Gemini 2.0 Pro"},
		{"gemini-1.5-flash-8b", "Gemini 1.5 Flash 8B"}, {"gemini-1.5-flash", "Gemini 1.5 Flash"},
		{"gemini-1.5-pro", "Gemini 1.5 Pro"},
	} {
		if strings.HasPrefix(bare, pair[0]) {
			return pair[1]
		}
	}
	return bare
}

func topFastAndPrimary(models []ModelRecord, n int) []ModelRecord {
	var primary, fast []ModelRecord
	for _, m := range models {
		if m.IsFast {
			if len(fast) < n {
				fast = append(fast, m)
			}
		} else if len(primary) < n {
			primary = append(primary, m)
		}
		if len(fast) >= n && len(primary) >= n {
			break
		}
	}
	return append(primary, fast...)
}

func preferredOpenRouterDefault(models []ModelRecord) string {
	// Prefer known free-route candidates first.
	prioritized := []string{
		"qwen/qwen3.6-plus:free",
		"openrouter/auto:free",
		"openrouter/auto",
	}
	for _, id := range prioritized {
		for _, m := range models {
			if m.ID == id {
				return id
			}
		}
	}
	// Fall back to the first free model in the fetched list.
	for _, m := range models {
		if strings.Contains(strings.ToLower(m.ID), ":free") {
			return m.ID
		}
	}
	return ""
}

func openAIModelScore(id string) int {
	lower := strings.ToLower(id)
	isFastVariant := strings.Contains(lower, "mini") || strings.Contains(lower, "nano")
	if len(lower) > 1 && lower[0] == 'o' && lower[1] >= '0' && lower[1] <= '9' {
		rest := lower[1:]
		if idx := strings.IndexByte(rest, '-'); idx > 0 {
			rest = rest[:idx]
		}
		var v int
		fmt.Sscanf(rest, "%d", &v)
		score := (v + 1) * 100
		if isFastVariant {
			score--
		}
		return score
	}
	if strings.HasPrefix(lower, "gpt-") {
		rest := lower[4:]
		var version float64
		if strings.HasPrefix(rest, "4o") {
			version = 4.5
		} else {
			fmt.Sscanf(rest, "%f", &version)
		}
		score := int(version * 100)
		if strings.Contains(lower, "turbo") {
			score += 2
		}
		if isFastVariant {
			score--
		}
		return score
	}
	return 0
}

func geminiModelScore(id string) int {
	lower := strings.ToLower(strings.TrimPrefix(id, "models/"))
	if !strings.HasPrefix(lower, "gemini-") {
		return 0
	}
	rest := lower[7:]
	var version float64
	fmt.Sscanf(rest, "%f", &version)
	score := int(version * 100)
	switch {
	case strings.Contains(lower, "pro"):
		score += 3
	case strings.Contains(lower, "flash-lite"):
		score += 1
	case strings.Contains(lower, "flash"):
		score += 2
	}
	if strings.Contains(lower, "preview") {
		score--
	}
	return score
}

func geminiBaseFamily(id string) string {
	bare := strings.TrimPrefix(id, "models/")
	lower := strings.ToLower(bare)
	for _, variant := range []string{"flash-lite", "flash-8b", "flash", "pro", "nano"} {
		idx := strings.Index(lower, variant)
		if idx >= 0 {
			return bare[:idx+len(variant)]
		}
	}
	return bare
}

func fetchLMStudioModels(baseURL, apiKey string) []ModelRecord {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	req, err := http.NewRequest("GET", base+"/models", nil)
	if err != nil {
		return []ModelRecord{}
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return []ModelRecord{}
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return []ModelRecord{}
	}
	var models []ModelRecord
	for _, m := range result.Data {
		models = append(models, ModelRecord{ID: m.ID, DisplayName: m.ID, IsFast: false})
	}
	return models
}

func fetchOllamaModels(baseURL, apiKey string) []ModelRecord {
	base := strings.TrimRight(baseURL, "/")
	base = strings.TrimSuffix(base, "/v1")
	req, err := http.NewRequest("GET", base+"/api/tags", nil)
	if err != nil {
		return []ModelRecord{}
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return []ModelRecord{}
	}
	defer resp.Body.Close()
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return []ModelRecord{}
	}
	var models []ModelRecord
	for _, m := range result.Models {
		models = append(models, ModelRecord{ID: m.Name, DisplayName: m.Name, IsFast: false})
	}
	return models
}
