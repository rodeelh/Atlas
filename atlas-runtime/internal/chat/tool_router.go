package chat

// Phase 3 — LLM Tool Router
//
// When ToolSelectionMode == "llm", this file handles tool selection by sending
// the user message to the background provider (Engine LM router when available,
// cloud fast model otherwise). The model returns a JSON array of tool names;
// only those tools are injected into the main agent turn.
//
// Falls back to heuristic selection transparently on any failure.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/skills"
)

var callAINonStreamingFn = agent.CallAINonStreamingExported

// selectToolsWithLLM is the Phase 3 entry point. It resolves the background
// provider (Engine LM router → cloud fast model fallback), sends the user
// message with a tool-selection prompt, and returns the filtered tool subset.
// Falls back to heuristic on any failure so the main agent turn is never blocked.
func selectToolsWithLLM(
	ctx context.Context,
	cfg config.RuntimeConfigSnapshot,
	turn *turnContext,
	message string,
	registry *skills.Registry,
) []map[string]any {
	bgProvider, usedFallback, err := resolveBackgroundProvider(cfg, turn)
	if err != nil {
		logstore.Write("warn",
			fmt.Sprintf("Tool router: no background provider (%v), using heuristic", err), nil)
		return registry.SelectiveToolDefs(message)
	}
	if usedFallback && turn != nil {
		turn.markRouterFallback("router_unhealthy")
	}

	allTools := registry.ToolDefinitions()

	// Build compact tool list for the prompt.
	// Descriptions are truncated to 80 chars to keep the prompt within the
	// router model's context window (tool list alone can exceed 3K tokens).
	var sb strings.Builder
	byName := make(map[string]map[string]any, len(allTools))
	for _, t := range allTools {
		fn, _ := t["function"].(map[string]any)
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		if name == "" {
			continue
		}
		byName[name] = t
		if len(desc) > 80 {
			desc = desc[:80] + "…"
		}
		fmt.Fprintf(&sb, "- %s: %s\n", name, desc)
	}

	prompt := "Select the tools needed to handle the user message below. " +
		"Return ONLY a JSON array of tool names (e.g. [\"weather.get\",\"calendar.list\"]). " +
		"If no tools are needed return [].\n\n" +
		"Available tools:\n" + sb.String() +
		"\nUser message: " + message

	messages := []agent.OAIMessage{
		{Role: "user", Content: prompt},
	}

	reply, _, _, err := callAINonStreamingFn(ctx, bgProvider, messages, nil)
	if err != nil && bgProvider.Type == agent.ProviderAtlasEngine && turn != nil {
		turn.markRouterFallback("router_call_failed")
		fallbackProvider, _, fbErr := resolveBackgroundProvider(cfg, turn)
		if fbErr == nil {
			reply, _, _, err = callAINonStreamingFn(ctx, fallbackProvider, messages, nil)
			bgProvider = fallbackProvider
		}
	}
	if err != nil {
		logstore.Write("warn",
			fmt.Sprintf("Tool router: call failed (%v), using heuristic", err), nil)
		return registry.SelectiveToolDefs(message)
	}

	content, ok := reply.Content.(string)
	if !ok {
		return registry.SelectiveToolDefs(message)
	}
	content = strings.TrimSpace(content)

	// Extract JSON array — model may wrap it in prose.
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end <= start {
		logstore.Write("warn",
			fmt.Sprintf("Tool router: no JSON array in response %q, using heuristic", content), nil)
		return registry.SelectiveToolDefs(message)
	}

	var names []string
	if err := json.Unmarshal([]byte(content[start:end+1]), &names); err != nil {
		logstore.Write("warn",
			fmt.Sprintf("Tool router: parse failed (%v), using heuristic", err), nil)
		return registry.SelectiveToolDefs(message)
	}

	// Empty array → router determined no tools are needed for this message.
	// Return the heuristic baseline (core + management + custom) so the model
	// still has self-awareness and management tools, but not the full set.
	if len(names) == 0 {
		logstore.Write("info", "Tool router: no tools selected — using heuristic baseline", nil)
		return registry.SelectiveToolDefs(message)
	}

	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	var out []map[string]any
	for _, t := range allTools {
		fn, _ := t["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if nameSet[name] {
			out = append(out, t)
		}
	}

	logstore.Write("info",
		fmt.Sprintf("Tool router: selected %d / %d tools via %s/%s",
			len(out), len(allTools), bgProvider.Type, bgProvider.Model),
		map[string]string{"mode": "llm"})

	return out
}
