package dashboards

// live_runner.go — concrete AILiveComputeRunner that satisfies LiveComputeRunner
// by making a single non-streaming AI call. Wired automatically in Module.Start
// via the already-injected provider resolver; no extra wiring in main.go needed.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/logstore"
)

// AILiveComputeRunner implements LiveComputeRunner using the configured AI provider.
type AILiveComputeRunner struct {
	resolver func() (providerConfig, error)
}

// NewAILiveComputeRunner constructs a runner that resolves the provider
// configuration lazily on each call so model/key changes take effect immediately.
func NewAILiveComputeRunner(resolver func() (providerConfig, error)) *AILiveComputeRunner {
	return &AILiveComputeRunner{resolver: resolver}
}

// liveComputeSystemPrompt is used when the spec has input sources — the AI
// acts as a data-transformation step and must not fabricate beyond what the
// inputs provide.
const liveComputeSystemPrompt = `You are a data-transformation step inside a live dashboard widget.
Given one or more named JSON data sources and a task description, produce a single JSON value.
Rules:
- Respond with ONLY valid JSON — no markdown fences, no explanation, no prose.
- The output must be a JSON object or array (not a bare string or number).
- Do not invent data that is not present in the input sources.`

// liveComputeStandaloneSystemPrompt is used when the spec has NO input sources.
// In standalone mode the AI generates content from its own knowledge — the
// "no invention" rule does not apply; the AI IS the data source.
const liveComputeStandaloneSystemPrompt = `You are a live dashboard content generator for a real-time dashboard widget.
Your task is to produce informative, useful JSON content based on your knowledge.
Rules:
- Respond with ONLY valid JSON — no markdown fences, no explanation, no prose.
- The output must be a JSON object or array (not a bare string or number).
- Generate accurate, helpful content based on your training knowledge.
- Always produce substantive content — never return empty arrays or placeholder values.`

// Run executes spec: injects the resolved inputs as context, calls the AI
// provider, strips any markdown wrapping, and returns the parsed JSON result.
func (r *AILiveComputeRunner) Run(ctx context.Context, spec LiveComputeSpec, inputs map[string]any) (any, error) {
	cfg, err := r.resolver()
	if err != nil {
		return nil, fmt.Errorf("live_compute: provider resolver: %w", err)
	}
	p := agent.ProviderConfig{
		Type:         agent.ProviderType(cfg.Type),
		APIKey:       cfg.APIKey,
		Model:        cfg.Model,
		BaseURL:      cfg.BaseURL,
		ExtraHeaders: cfg.ExtraHeaders,
	}

	// Choose prompt: standalone (no inputs = AI is the data source) vs
	// transform mode (inputs provided = AI transforms existing data).
	system := liveComputeSystemPrompt
	if len(spec.Inputs) == 0 {
		system = liveComputeStandaloneSystemPrompt
	}
	if spec.OutputSchema != nil {
		schema, _ := json.Marshal(spec.OutputSchema)
		system += fmt.Sprintf("\n\nExpected output JSON schema:\n%s", schema)
	}

	// Build the user message: inline each input source as a named JSON block
	// in the order declared in spec.Inputs, then append the task prompt.
	var sb strings.Builder
	for _, name := range spec.Inputs {
		val := inputs[name]
		b, _ := json.Marshal(val)
		fmt.Fprintf(&sb, "### %s\n```json\n%s\n```\n\n", name, b)
	}
	user := spec.Prompt
	if sb.Len() > 0 {
		user = fmt.Sprintf("Input data:\n\n%s\nTask: %s", sb.String(), spec.Prompt)
	}

	msgs := []agent.OAIMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	reply, _, _, err := agent.CallAINonStreamingExported(ctx, p, msgs, nil)
	if err != nil {
		return nil, fmt.Errorf("live_compute: AI call failed: %w", err)
	}

	text := liveExtractText(reply.Content)
	text = liveStripFences(text)

	var out any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		logstore.Write("warn", "live_compute: AI response is not valid JSON: "+err.Error(),
			map[string]string{"preview": liveTruncate(text, 160)})
		return map[string]any{"text": text}, nil
	}
	return out, nil
}

func liveExtractText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		b, _ := json.Marshal(content)
		return strings.TrimSpace(string(b))
	}
}

// liveStripFences removes a single ```...``` wrapper that some models add
// even when instructed not to.
func liveStripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening ``` line (may have a language tag like ```json).
	idx := strings.Index(s, "\n")
	if idx < 0 {
		return s
	}
	s = s[idx+1:]
	// Drop the closing ```.
	if end := strings.LastIndex(s, "```"); end >= 0 {
		s = s[:end]
	}
	return strings.TrimSpace(s)
}

func liveTruncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
