package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"atlas-runtime-go/internal/engine"
)

// TokenUsage holds the input and output token counts from a single AI call.
type TokenUsage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}

// NonAgenticUsageHook is called after every non-agentic LLM call (memory
// extraction, reflection, forge research, classifier, etc.) with the provider
// config and token counts. Set once at startup by the usage-tracking wiring.
// Nil is safe — the field is optional.
var NonAgenticUsageHook func(ctx context.Context, provider ProviderConfig, usage TokenUsage)

type MLXRequestOptions struct {
	Temperature        float64
	TopP               float64
	MinP               float64
	RepetitionPenalty  float64
	ChatTemplateKwargs map[string]any
	Capabilities       *engine.MLXModelCapabilities
}

// streamResult is returned by streamWithToolDetection — the single streaming
// call that replaces the old probe + re-stream pair. One API call per loop
// iteration regardless of whether the turn ends with text or tool calls.
type streamResult struct {
	FinalText    string        // text response (may be non-empty even when ToolCalls is set)
	ToolCalls    []OAIToolCall // non-nil means a tool-call turn
	FinishReason string
	Usage        TokenUsage
	FirstTokenAt time.Duration
	ChunkCount   int
	StreamChars  int
}

// ProviderType identifies which AI backend to use.
type ProviderType string

const (
	ProviderOpenAI      ProviderType = "openai"
	ProviderAnthropic   ProviderType = "anthropic"
	ProviderGemini      ProviderType = "gemini"
	ProviderOpenRouter  ProviderType = "openrouter"
	ProviderLMStudio    ProviderType = "lm_studio"
	ProviderOllama      ProviderType = "ollama"
	ProviderAtlasEngine ProviderType = "atlas_engine"
	ProviderAtlasMLX    ProviderType = "atlas_mlx" // MLX-LM subsystem — Apple Silicon only
)

// providerClass groups providers by their wire-format / deployment shape. One
// entry per class controls dispatching in callAINonStreaming, adapter selection,
// message coalescing, and default base URLs.
type providerClass int

const (
	classCloudResponses  providerClass = iota // OpenAI Responses API
	classCloudAnthropic                       // Anthropic Messages API
	classCloudOAICompat                       // OAI-compatible cloud (Gemini, OpenRouter)
	classLocalJinja                           // local OAI-compatible w/ Jinja templates (Engine, LM Studio, Ollama)
	classLocalMLX                             // MLX-LM (OAI-compatible, accepts tool messages natively)
)

// providerInfo is the single source of truth for provider-wide classification.
// Adding a new provider means adding ONE entry here and the few pieces that
// genuinely differ (credential read + model selection in chat/keychain.go).
type providerInfo struct {
	class          providerClass
	defaultBaseURL string // used by local providers; empty for cloud-native backends
}

var providers = map[ProviderType]providerInfo{
	ProviderOpenAI:      {class: classCloudResponses},
	ProviderAnthropic:   {class: classCloudAnthropic},
	ProviderGemini:      {class: classCloudOAICompat, defaultBaseURL: "https://generativelanguage.googleapis.com/v1beta/openai"},
	ProviderOpenRouter:  {class: classCloudOAICompat, defaultBaseURL: "https://openrouter.ai/api/v1"},
	ProviderLMStudio:    {class: classLocalJinja, defaultBaseURL: "http://localhost:1234"},
	ProviderOllama:      {class: classLocalJinja, defaultBaseURL: "http://localhost:11434"},
	ProviderAtlasEngine: {class: classLocalJinja, defaultBaseURL: "http://localhost:11985"},
	ProviderAtlasMLX:    {class: classLocalMLX, defaultBaseURL: "http://localhost:11990"},
}

func providerClassOf(t ProviderType) providerClass {
	if info, ok := providers[t]; ok {
		return info.class
	}
	return classCloudResponses // default — matches legacy fallthrough to OpenAI
}

// isLocalProvider returns true for providers backed by a local inference server
// that may return 503 while loading a model.
func isLocalProvider(t ProviderType) bool {
	c := providerClassOf(t)
	return c == classLocalJinja || c == classLocalMLX
}

// isCloudProvider returns true for providers that communicate with a remote API
// (as opposed to a locally-managed inference server).
func isCloudProvider(t ProviderType) bool { return !isLocalProvider(t) }

// isCloudOAICompatProvider returns true for cloud providers that use the
// OpenAI-compatible chat-completions API format (Gemini, OpenRouter). These
// require an explicit max_tokens cap; OpenAI uses the Responses API and
// Anthropic has its own wire format.
func isCloudOAICompatProvider(t ProviderType) bool {
	return providerClassOf(t) == classCloudOAICompat
}

// needsMessageCoalescing returns true for local providers whose Jinja chat
// templates enforce strict user/assistant alternation (llama-server, Ollama).
// MLX-LM accepts tool messages natively and does not need coalescing.
func needsMessageCoalescing(t ProviderType) bool {
	return providerClassOf(t) == classLocalJinja
}

func acquireMLXRequestGate(ctx context.Context, p ProviderConfig) (func(), time.Duration, int, error) {
	if p.Type != ProviderAtlasMLX {
		return func() {}, 0, 0, nil
	}
	return engine.AcquireMLXRequest(ctx, oaiCompatBaseURL(p))
}

// ProviderConfig carries everything the agent loop needs to call an AI backend.
type ProviderConfig struct {
	Type         ProviderType
	APIKey       string
	Model        string
	BaseURL      string            // used by local providers and OpenAI-compatible variants
	ExtraHeaders map[string]string // optional provider-specific request headers
	MLX          *MLXRequestOptions
}

func mergedMLXChatTemplateKwargs(p ProviderConfig) map[string]any {
	if p.Type != ProviderAtlasMLX || p.MLX == nil || len(p.MLX.ChatTemplateKwargs) == 0 {
		return nil
	}
	out := make(map[string]any, len(p.MLX.ChatTemplateKwargs))
	for k, v := range p.MLX.ChatTemplateKwargs {
		out[k] = v
	}
	return out
}

func applyMLXRequestOptions(reqBody map[string]any, p ProviderConfig, tools []map[string]any) {
	if p.Type != ProviderAtlasMLX || p.MLX == nil {
		return
	}
	// Only send temperature when explicitly non-zero. Sending temperature=0 in
	// recent mlx-lm triggers a greedy-decoding path that crashes the Python
	// process mid-stream when tools are present.
	if p.MLX.Temperature > 0 {
		reqBody["temperature"] = p.MLX.Temperature
	}
	// Only send top_p when it's a meaningful non-default value.
	if p.MLX.TopP > 0 && p.MLX.TopP < 1 {
		reqBody["top_p"] = p.MLX.TopP
	}
	// min_p and repetition_penalty are optional — only send when non-zero.
	// Sending repetition_penalty=0 causes a validation error in recent mlx-lm
	// (must be > 0 or absent).
	if p.MLX.MinP > 0 {
		reqBody["min_p"] = p.MLX.MinP
	}
	if p.MLX.RepetitionPenalty > 0 {
		reqBody["repetition_penalty"] = p.MLX.RepetitionPenalty
	}
	if kwargs := mergedMLXChatTemplateKwargs(p); len(kwargs) > 0 {
		reqBody["chat_template_kwargs"] = kwargs
	}
}

// ── Non-streaming (Forge + RegenerateMind) ────────────────────────────────────

// CallVision makes a single vision inference call using the configured provider.
// imageB64 is a raw base64-encoded PNG (no data URI prefix). prompt is the
// instruction sent alongside the image. Returns the model's text response.
//
// The image content block format is adapted per-provider (Anthropic vs OpenAI).
func CallVision(ctx context.Context, p ProviderConfig, imageB64, prompt string) (string, error) {
	var imageBlock any
	switch p.Type {
	case ProviderAnthropic:
		imageBlock = map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "image/png",
				"data":       imageB64,
			},
		}
	default: // openai, gemini, lm_studio, ollama, ollama — image_url format
		imageBlock = map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": "data:image/png;base64," + imageB64},
		}
	}

	msg := OAIMessage{
		Role: "user",
		Content: []any{
			imageBlock,
			map[string]any{"type": "text", "text": prompt},
		},
	}

	reply, _, _, err := callAINonStreaming(ctx, p, []OAIMessage{msg}, nil)
	if err != nil {
		return "", err
	}

	// Extract plain text from the reply content (string or content-block slice).
	switch c := reply.Content.(type) {
	case string:
		return strings.TrimSpace(c), nil
	case []any:
		var parts []string
		for _, blk := range c {
			if bmap, ok := blk.(map[string]any); ok {
				if t, ok := bmap["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "")), nil
	}
	return strings.TrimSpace(fmt.Sprintf("%v", reply.Content)), nil
}

// CallAINonStreamingExported allows packages outside the agent package to make
// single-shot non-streaming AI calls (used by Forge and RegenerateMind).
func CallAINonStreamingExported(
	ctx context.Context,
	p ProviderConfig,
	messages []OAIMessage,
	tools []map[string]any,
) (OAIMessage, string, TokenUsage, error) {
	return callAINonStreaming(ctx, p, messages, tools)
}

func callAINonStreaming(
	ctx context.Context,
	p ProviderConfig,
	messages []OAIMessage,
	tools []map[string]any,
) (OAIMessage, string, TokenUsage, error) {
	var msg OAIMessage
	var reason string
	var usage TokenUsage
	var err error
	switch p.Type {
	case ProviderAnthropic:
		msg, reason, usage, err = callAnthropicNonStreaming(ctx, p, messages, tools)
	case ProviderOpenAI:
		msg, reason, usage, err = callOpenAIResponsesNonStreaming(ctx, p, messages, tools)
	default: // openai, gemini, lm_studio, ollama
		msg, reason, usage, err = callOpenAICompatNonStreaming(ctx, p, messages, tools)
	}
	if err == nil && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		AddSessionTokens(usage.InputTokens, usage.OutputTokens)
		if NonAgenticUsageHook != nil {
			NonAgenticUsageHook(ctx, p, usage)
		}
	}
	return msg, reason, usage, err
}


// coalesceForLocalProvider ensures strict user/assistant alternation required
// by Jinja chat templates in llama-server and Ollama. It:
//  1. Keeps the system message as-is (index 0).
//  2. Merges "tool" role messages into the preceding assistant message.
//  3. Merges adjacent same-role messages by concatenating their text content.
//  4. Ensures the sequence after system is user, assistant, user, assistant, ...
//     by dropping messages that would violate alternation.
func coalesceForLocalProvider(messages []OAIMessage) []OAIMessage {
	if len(messages) == 0 {
		return messages
	}

	var out []OAIMessage

	// Preserve system message.
	start := 0
	if messages[0].Role == "system" {
		out = append(out, messages[0])
		start = 1
	}

	// First pass: strip non-user/assistant messages. Tool-role messages are
	// dropped entirely — their verbose JSON results would inflate token count
	// and the model already processed them on the original turn.
	var filtered []OAIMessage
	for i := start; i < len(messages); i++ {
		m := messages[i]
		if m.Role == "user" || m.Role == "assistant" {
			filtered = append(filtered, m)
		}
	}

	// Second pass: coalesce adjacent same-role messages.
	for _, m := range filtered {
		text, _ := m.Content.(string)
		if len(out) > 0 && out[len(out)-1].Role == m.Role {
			prev := &out[len(out)-1]
			prevText, _ := prev.Content.(string)
			if text != "" {
				if prevText != "" {
					prevText += "\n"
				}
				prev.Content = prevText + text
			}
			continue
		}
		out = append(out, OAIMessage{Role: m.Role, Content: text})
	}

	// Third pass: ensure alternation starts with user after system.
	// Drop any leading assistant messages (they'd violate the template).
	final := out[:0]
	for i, m := range out {
		if i == 0 && m.Role == "system" {
			final = append(final, m)
			continue
		}
		expectedRole := "user"
		nonSystemCount := 0
		for _, f := range final {
			if f.Role != "system" {
				nonSystemCount++
			}
		}
		if nonSystemCount%2 == 1 {
			expectedRole = "assistant"
		}
		if m.Role == expectedRole {
			final = append(final, m)
		}
		// Skip messages that don't match expected role — they were already
		// coalesced or are orphaned.
	}

	// Drop a trailing assistant message. llama-server (and Ollama) treat a
	// sequence ending with an assistant turn as a response prefill, which
	// conflicts with enable_thinking when the model's Jinja template enables
	// extended thinking. This happens after tool-call round-trips where the
	// tool-role result was stripped, leaving assistant as the last message.
	for len(final) > 0 && final[len(final)-1].Role == "assistant" {
		final = final[:len(final)-1]
	}

	return final
}

// ── OpenAI-compatible (OpenAI, Gemini, LM Studio) ─────────────────────────────

func oaiCompatBaseURL(p ProviderConfig) string {
	// Cloud-native OAI endpoints (Gemini, OpenRouter) have a fixed, non-/v1 path
	// that must not be re-suffixed.
	if providerClassOf(p.Type) == classCloudOAICompat {
		if info, ok := providers[p.Type]; ok && info.defaultBaseURL != "" {
			return info.defaultBaseURL
		}
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		if info, ok := providers[p.Type]; ok && info.defaultBaseURL != "" {
			base = info.defaultBaseURL
		} else {
			base = "https://api.openai.com/v1"
		}
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base
}

func applyOpenAICompatHeaders(req *http.Request, p ProviderConfig) {
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	for k, v := range p.ExtraHeaders {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
}

var atlasMLXHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DisableKeepAlives: true,
	},
}

var openAIHTTPClient = &http.Client{}

const (
	openAIResponsesStreamTimeout    = 5 * time.Minute
	openAIResponsesNonStreamTimeout = 2 * time.Minute
)

func openAICompatHTTPClient(p ProviderConfig) *http.Client {
	if p.Type == ProviderOpenAI {
		return openAIHTTPClient
	}
	if p.Type == ProviderAtlasMLX {
		return atlasMLXHTTPClient
	}
	return http.DefaultClient
}

func isTransientLocalRequestError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "server closed idle connection")
}

func doOpenAICompatRequest(ctx context.Context, p ProviderConfig, url string, body []byte) (*http.Response, error) {
	client := openAICompatHTTPClient(p)
	attempts := 1
	if isLocalProvider(p.Type) {
		attempts = 2
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		applyOpenAICompatHeaders(req, p)
		if p.Type == ProviderAtlasMLX {
			// mlx_lm.server speaks HTTP/1.0 and closes connections aggressively.
			// Avoid pooled idle sockets and retry a fresh connection on EOF.
			req.Close = true
		}

		resp, err := client.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isTransientLocalRequestError(err) || attempt == attempts-1 {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func withProviderTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline || timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func responsesBaseURL(p ProviderConfig) string {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base
}

func convertChatToolsToResponsesTools(tools []map[string]any) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		converted := map[string]any{
			"type":        "function",
			"name":        name,
			"description": fn["description"],
			"parameters":  fn["parameters"],
		}
		out = append(out, converted)
	}
	return out
}

func convertMessageContentPartsToResponses(content any, textType string) []map[string]any {
	appendText := func(parts []map[string]any, text string) []map[string]any {
		if strings.TrimSpace(text) == "" {
			return parts
		}
		return append(parts, map[string]any{"type": textType, "text": text})
	}

	switch c := content.(type) {
	case nil:
		return nil
	case string:
		return appendText(nil, c)
	case []map[string]any:
		parts := make([]map[string]any, 0, len(c))
		for _, part := range c {
			ptype, _ := part["type"].(string)
			switch ptype {
			case "text":
				if text, _ := part["text"].(string); text != "" {
					parts = append(parts, map[string]any{"type": textType, "text": text})
				}
			case "image_url":
				imageURL, _ := part["image_url"].(map[string]any)
				url, _ := imageURL["url"].(string)
				if url == "" {
					continue
				}
				if strings.HasPrefix(strings.ToLower(url), "data:application/pdf;") {
					parts = append(parts, map[string]any{
						"type":      "input_file",
						"filename":  "attachment.pdf",
						"file_data": url,
					})
				} else {
					parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
				}
			}
		}
		return parts
	case []any:
		parts := make([]map[string]any, 0, len(c))
		for _, raw := range c {
			part, _ := raw.(map[string]any)
			if len(part) == 0 {
				continue
			}
			parts = append(parts, convertMessageContentPartsToResponses([]map[string]any{part}, textType)...)
		}
		return parts
	default:
		return appendText(nil, fmt.Sprintf("%v", c))
	}
}

// extractSystemInstructions pulls system-role messages out of the message list
// and returns their text joined as a single instructions string, plus the
// remaining non-system messages. The Responses API does not accept a "system"
// role in the input array — the system prompt goes in the instructions field.
func extractSystemInstructions(messages []OAIMessage) (instructions string, rest []OAIMessage) {
	var parts []string
	for _, m := range messages {
		if m.Role == "system" {
			if s, ok := m.Content.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, s)
			}
		} else {
			rest = append(rest, m)
		}
	}
	return strings.Join(parts, "\n\n"), rest
}

func convertMessagesToResponsesInput(messages []OAIMessage) []map[string]any {
	input := make([]map[string]any, 0, len(messages)*2)
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			parts := convertMessageContentPartsToResponses(msg.Content, "input_text")
			if len(parts) > 0 {
				input = append(input, map[string]any{
					"role":    "user",
					"content": parts,
				})
			}
		case "assistant":
			parts := convertMessageContentPartsToResponses(msg.Content, "output_text")
			if len(parts) > 0 {
				input = append(input, map[string]any{
					"role":    "assistant",
					"content": parts,
				})
			}
			for _, tc := range msg.ToolCalls {
				callID := strings.TrimSpace(tc.ID)
				if callID == "" {
					callID = fmt.Sprintf("call_%d", time.Now().UnixNano())
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}
		case "tool":
			callID := strings.TrimSpace(msg.ToolCallID)
			if callID == "" {
				continue
			}
			output := ""
			switch c := msg.Content.(type) {
			case string:
				output = c
			case nil:
				output = ""
			default:
				output = fmt.Sprintf("%v", c)
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  output,
			})
		}
	}
	return input
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesOutputItem struct {
	ID        string                   `json:"id"`
	Type      string                   `json:"type"`
	Role      string                   `json:"role"`
	CallID    string                   `json:"call_id"`
	Name      string                   `json:"name"`
	Arguments string                   `json:"arguments"`
	Content   []responsesOutputContent `json:"content"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type responsesResponse struct {
	ID         string                `json:"id"`
	Status     string                `json:"status"`
	Output     []responsesOutputItem `json:"output"`
	OutputText string                `json:"output_text"`
	Usage      responsesUsage        `json:"usage"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
	Incomplete *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

func extractResponsesMessage(resp responsesResponse) (OAIMessage, string, TokenUsage, error) {
	if resp.Error != nil && strings.TrimSpace(resp.Error.Message) != "" {
		return OAIMessage{}, "", TokenUsage{}, errors.New(resp.Error.Message)
	}

	var (
		textParts    []string
		toolCalls    []OAIToolCall
		finishReason = "stop"
	)
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			if item.Role != "" && item.Role != "assistant" {
				continue
			}
			for _, content := range item.Content {
				if content.Text != "" {
					textParts = append(textParts, content.Text)
				}
			}
		case "function_call":
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				callID = item.ID
			}
			toolCalls = append(toolCalls, OAIToolCall{
				ID:   callID,
				Type: "function",
				Function: OAIFunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}
	if len(textParts) == 0 && strings.TrimSpace(resp.OutputText) != "" {
		textParts = append(textParts, resp.OutputText)
	}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if resp.Incomplete != nil && strings.TrimSpace(resp.Incomplete.Reason) != "" {
		finishReason = resp.Incomplete.Reason
	}

	return OAIMessage{
			Role:      "assistant",
			Content:   strings.Join(textParts, ""),
			ToolCalls: toolCalls,
		}, finishReason, TokenUsage{
			InputTokens:       resp.Usage.InputTokens,
			OutputTokens:      resp.Usage.OutputTokens,
			CachedInputTokens: resp.Usage.InputTokensDetails.CachedTokens,
		}, nil
}

func doOpenAIResponsesRequest(ctx context.Context, p ProviderConfig, body []byte, stream bool) (*http.Response, error) {
	client := openAICompatHTTPClient(p)
	url := responsesBaseURL(p) + "/responses"
	maxAttempts := 3
	if stream {
		maxAttempts = 2
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		applyOpenAICompatHeaders(req, p)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt == maxAttempts-1 {
				return nil, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 250 * time.Millisecond):
			}
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusInternalServerError ||
			resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable ||
			resp.StatusCode == http.StatusGatewayTimeout {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("OpenAI Responses error %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
			if attempt == maxAttempts-1 {
				return nil, lastErr
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 400 * time.Millisecond):
			}
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func callOpenAIResponsesNonStreaming(
	ctx context.Context,
	p ProviderConfig,
	messages []OAIMessage,
	tools []map[string]any,
) (OAIMessage, string, TokenUsage, error) {
	ctx, cancel := withProviderTimeout(ctx, openAIResponsesNonStreamTimeout)
	defer cancel()

	instructions, rest := extractSystemInstructions(messages)
	reqBody := map[string]any{
		"model":             p.Model,
		"input":             convertMessagesToResponsesInput(rest),
		"max_output_tokens": 4096,
		"store":             false,
	}
	if instructions != "" {
		reqBody["instructions"] = instructions
	}
	if convertedTools := convertChatToolsToResponsesTools(tools); len(convertedTools) > 0 {
		reqBody["tools"] = convertedTools
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return OAIMessage{}, "", TokenUsage{}, err
	}
	resp, err := doOpenAIResponsesRequest(ctx, p, body, false)
	if err != nil {
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("OpenAI Responses request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("OpenAI Responses error %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	var decoded responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("OpenAI Responses parse failed: %w", err)
	}
	return extractResponsesMessage(decoded)
}


func callOpenAICompatNonStreaming(
	ctx context.Context,
	p ProviderConfig,
	messages []OAIMessage,
	tools []map[string]any,
) (OAIMessage, string, TokenUsage, error) {
	// Local providers (llama-server, Ollama) use Jinja chat templates that
	// enforce strict user/assistant alternation. Coalesce adjacent same-role
	// messages and strip tool-role messages that these backends don't support.
	// atlas_mlx (mlx_lm.server) is a proper OpenAI-compatible server that
	// accepts tool-call + tool-result messages natively — skip coalescing so
	// the model receives tool results and can generate a follow-up response.
	if needsMessageCoalescing(p.Type) {
		messages = coalesceForLocalProvider(messages)
	}

	reqBody := map[string]any{
		"model":    p.Model,
		"messages": messages,
		"stream":   false,
	}
	// Cap output tokens for cloud OAI-compat providers (Gemini, OpenRouter).
	// Local providers manage their own context window limits.
	if isCloudOAICompatProvider(p.Type) {
		reqBody["max_tokens"] = 4096
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}
	applyMLXRequestOptions(reqBody, p, tools)

	body, err := json.Marshal(reqBody)
	if err != nil {
		return OAIMessage{}, "", TokenUsage{}, err
	}
	url := oaiCompatBaseURL(p) + "/chat/completions"

	releaseGate, _, _, err := acquireMLXRequestGate(ctx, p)
	if err != nil {
		return OAIMessage{}, "", TokenUsage{}, err
	}
	defer releaseGate()

	resp, err := doOpenAICompatRequest(ctx, p, url, body)
	if err != nil {
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("AI non-streaming request failed (%s): %w", p.Type, err)
	}

	// Retry on 503 (model loading) for local providers — up to 30s with 2s backoff.
	if resp.StatusCode == http.StatusServiceUnavailable && isLocalProvider(p.Type) {
		resp.Body.Close()
		for attempt := 0; attempt < 15; attempt++ {
			select {
			case <-ctx.Done():
				return OAIMessage{}, "", TokenUsage{}, ctx.Err()
			case <-time.After(2 * time.Second):
			}
			resp, err = doOpenAICompatRequest(ctx, p, url, body)
			if err != nil {
				return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("AI non-streaming request failed (%s): %w", p.Type, err)
			}
			if resp.StatusCode != http.StatusServiceUnavailable {
				break
			}
			resp.Body.Close()
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody)
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("AI error %d (%s): %s", resp.StatusCode, p.Type, errBody.Error.Message)
	}

	var completion struct {
		Choices []struct {
			Message      json.RawMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("AI response parse failed (%s): %w", p.Type, err)
	}
	if len(completion.Choices) == 0 {
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("AI returned no choices (%s)", p.Type)
	}

	usage := TokenUsage{
		InputTokens:       completion.Usage.PromptTokens,
		OutputTokens:      completion.Usage.CompletionTokens,
		CachedInputTokens: completion.Usage.PromptTokensDetails.CachedTokens,
	}

	choice := completion.Choices[0]
	var rawMsg struct {
		Role      string        `json:"role"`
		Content   *string       `json:"content"`
		ToolCalls []OAIToolCall `json:"tool_calls"`
	}
	if err := json.Unmarshal(choice.Message, &rawMsg); err != nil {
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("AI message parse failed (%s): %w", p.Type, err)
	}

	msg := OAIMessage{
		Role:      rawMsg.Role,
		ToolCalls: rawMsg.ToolCalls,
	}
	if rawMsg.Content != nil {
		msg.Content = *rawMsg.Content
	}
	return msg, choice.FinishReason, usage, nil
}


// ── Anthropic ─────────────────────────────────────────────────────────────────

const anthropicBaseURL = "https://api.anthropic.com/v1"
const anthropicVersion = "2023-06-01"
const anthropicCachingBeta = "prompt-caching-2024-07-31"

// anthropicCachedSystem wraps the system prompt in the array format required
// for Anthropic prompt caching. The cache_control block is placed on the final
// content block so the entire prefix (system + tools) is cacheable.
//
// Cache economics (claude-3-5-sonnet):
//   - Write: 25 % more than normal input price (charged once per cache miss)
//   - Read:  ~10 % of normal input price (charged on every cache hit)
//   - TTL:   5 minutes (refreshed on each API call within that window)
//
// Break-even is 2 calls with the same prefix. Since Atlas sends the same
// system prompt on every turn, every turn after the first is a cache hit.
func anthropicCachedSystem(prompt string) []map[string]any {
	return []map[string]any{
		{
			"type":          "text",
			"text":          prompt,
			"cache_control": map[string]any{"type": "ephemeral"},
		},
	}
}

// anthropicCachedTools appends a cache_control block to the last tool so the
// entire tool list is included in the cached prefix.
func anthropicCachedTools(tools []map[string]any) []map[string]any {
	if len(tools) == 0 {
		return tools
	}
	out := make([]map[string]any, len(tools))
	copy(out, tools)
	last := make(map[string]any, len(out[len(out)-1])+1)
	for k, v := range out[len(out)-1] {
		last[k] = v
	}
	last["cache_control"] = map[string]any{"type": "ephemeral"}
	out[len(out)-1] = last
	return out
}

// convertContentToAnthropic converts an OAIMessage.Content value to the format
// expected by Anthropic's Messages API.
//
//   - string → returned as-is (Anthropic accepts plain string content).
//   - []map[string]any content parts (built by buildUserContent in chat/service.go):
//   - {"type":"text","text":"..."} → {"type":"text","text":"..."}
//   - {"type":"image_url","image_url":{"url":"data:image/*;base64,..."}} →
//     {"type":"image","source":{"type":"base64","media_type":"image/*","data":"..."}}
//   - {"type":"image_url","image_url":{"url":"data:application/pdf;base64,..."}} →
//     {"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"..."}}
func convertContentToAnthropic(content any) any {
	if s, ok := content.(string); ok {
		return s
	}

	parts, ok := content.([]map[string]any)
	if !ok {
		return ""
	}

	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		partType, _ := part["type"].(string)
		switch partType {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": part["text"]})
		case "image_url":
			imgURL, _ := part["image_url"].(map[string]any)
			if imgURL == nil {
				continue
			}
			dataURL, _ := imgURL["url"].(string)
			if !strings.HasPrefix(dataURL, "data:") {
				continue
			}
			rest := strings.TrimPrefix(dataURL, "data:")
			semi := strings.Index(rest, ";base64,")
			if semi < 0 {
				continue
			}
			mimeType := rest[:semi]
			b64data := rest[semi+len(";base64,"):]
			if mimeType == "application/pdf" {
				out = append(out, map[string]any{
					"type": "document",
					"source": map[string]any{
						"type":       "base64",
						"media_type": mimeType,
						"data":       b64data,
					},
				})
			} else {
				out = append(out, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": mimeType,
						"data":       b64data,
					},
				})
			}
		}
	}
	return out
}

// convertToAnthropicMessages splits out the system prompt and converts the
// remaining messages into Anthropic's format, grouping consecutive tool-result
// messages into a single user message with a content array.
func convertToAnthropicMessages(messages []OAIMessage) (systemPrompt string, out []map[string]any) {
	for i := 0; i < len(messages); i++ {
		m := messages[i]

		if m.Role == "system" {
			if s, ok := m.Content.(string); ok {
				systemPrompt = s
			}
			continue
		}

		if m.Role == "tool" {
			var blocks []map[string]any
			for i < len(messages) && messages[i].Role == "tool" {
				t := messages[i]
				content := ""
				if s, ok := t.Content.(string); ok {
					content = s
				}
				blocks = append(blocks, map[string]any{
					"type":        "tool_result",
					"tool_use_id": t.ToolCallID,
					"content":     content,
				})
				i++
			}
			i-- // outer loop will increment
			out = append(out, map[string]any{"role": "user", "content": blocks})
			continue
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var contentArr []map[string]any
			if s, ok := m.Content.(string); ok && s != "" {
				contentArr = append(contentArr, map[string]any{"type": "text", "text": s})
			}
			for _, tc := range m.ToolCalls {
				var input any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil || input == nil {
					input = map[string]any{}
				}
				contentArr = append(contentArr, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
			out = append(out, map[string]any{"role": "assistant", "content": contentArr})
			continue
		}

		out = append(out, map[string]any{"role": m.Role, "content": convertContentToAnthropic(m.Content)})
	}
	return
}

// convertToAnthropicTools converts OpenAI-format tool definitions to Anthropic format.
func convertToAnthropicTools(tools []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn, ok := t["function"].(map[string]any)
		if !ok {
			continue
		}
		atool := map[string]any{
			"name": fn["name"],
		}
		if desc, ok := fn["description"]; ok {
			atool["description"] = desc
		}
		if params, ok := fn["parameters"]; ok {
			atool["input_schema"] = params
		} else {
			atool["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, atool)
	}
	return out
}

func callAnthropicNonStreaming(
	ctx context.Context,
	p ProviderConfig,
	messages []OAIMessage,
	tools []map[string]any,
) (OAIMessage, string, TokenUsage, error) {
	systemPrompt, anthropicMsgs := convertToAnthropicMessages(messages)

	reqBody := map[string]any{
		"model":      p.Model,
		"messages":   anthropicMsgs,
		"max_tokens": 4096,
	}
	if systemPrompt != "" {
		// Use the cached-system array format to enable prompt caching on the
		// system prompt prefix. Cache TTL is 5 minutes; refreshed each call.
		reqBody["system"] = anthropicCachedSystem(systemPrompt)
	}
	if len(tools) > 0 {
		// Place cache_control on the last tool so the full tool list is cached.
		reqBody["tools"] = anthropicCachedTools(convertToAnthropicTools(tools))
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return OAIMessage{}, "", TokenUsage{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		anthropicBaseURL+"/messages",
		bytes.NewReader(body),
	)
	if err != nil {
		return OAIMessage{}, "", TokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("anthropic-beta", anthropicCachingBeta)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("Anthropic non-streaming request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody)
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("Anthropic error %d: %s", resp.StatusCode, errBody.Error.Message)
	}

	var completion struct {
		Content    []map[string]any `json:"content"`
		StopReason string           `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return OAIMessage{}, "", TokenUsage{}, fmt.Errorf("Anthropic response parse failed: %w", err)
	}

	usage := TokenUsage{
		InputTokens:  completion.Usage.InputTokens,
		OutputTokens: completion.Usage.OutputTokens,
	}

	msg := OAIMessage{Role: "assistant"}
	var textParts []string
	for _, block := range completion.Content {
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			if t, ok := block["text"].(string); ok {
				textParts = append(textParts, t)
			}
		case "tool_use":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			inputRaw := block["input"]
			argsJSON, _ := json.Marshal(inputRaw)
			msg.ToolCalls = append(msg.ToolCalls, OAIToolCall{
				ID:   id,
				Type: "function",
				Function: OAIFunctionCall{
					Name:      name,
					Arguments: string(argsJSON),
				},
			})
		}
	}
	msg.Content = strings.Join(textParts, "")

	finishReason := "stop"
	if completion.StopReason == "tool_use" || len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	return msg, finishReason, usage, nil
}

