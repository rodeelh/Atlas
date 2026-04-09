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
	"sort"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/skills"
)

var callAINonStreamingFn = agent.CallAINonStreamingExported

const toolRouterCacheTTL = 45 * time.Second

type toolRouterCacheEntry struct {
	groups    []string
	expiresAt time.Time
}

var toolRouterCache = struct {
	mu    sync.Mutex
	items map[string]toolRouterCacheEntry
}{
	items: make(map[string]toolRouterCacheEntry),
}

func routerCacheKey(p agent.ProviderConfig, message string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(message)), " ")
	return fmt.Sprintf("%s|%s|%s", p.Type, p.Model, normalized)
}

func readToolRouterCache(key string) ([]string, bool) {
	toolRouterCache.mu.Lock()
	defer toolRouterCache.mu.Unlock()
	entry, ok := toolRouterCache.items[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(toolRouterCache.items, key)
		}
		return nil, false
	}
	return append([]string(nil), entry.groups...), true
}

func writeToolRouterCache(key string, groups []string) {
	toolRouterCache.mu.Lock()
	defer toolRouterCache.mu.Unlock()
	toolRouterCache.items[key] = toolRouterCacheEntry{
		groups:    append([]string(nil), groups...),
		expiresAt: time.Now().Add(toolRouterCacheTTL),
	}
}

func buildToolRouterPrompt(manifest []skills.ToolCapabilityGroupManifest, message string) string {
	var sb strings.Builder
	sb.WriteString("Select the smallest capability-group set Atlas should expose for the user's first agent turn.\n")
	sb.WriteString("Return ONLY a JSON array of group names from the list below, for example [\"weather\",\"automation\"].\n")
	sb.WriteString("If no extra tools are needed, return []. Never return \"core\"; time/date helpers are always available.\n")
	sb.WriteString("Prefer the smallest sufficient set. Use multiple groups only when the request clearly spans them.\n\n")
	sb.WriteString("Available capability groups:\n")
	for _, group := range manifest {
		fmt.Fprintf(&sb, "- %s (%d tools): %s Examples: %s\n",
			group.Name, group.ToolCount, group.Description, strings.Join(group.ExampleTools, ", "))
	}
	sb.WriteString("\nUser message: ")
	sb.WriteString(message)
	return sb.String()
}

func parseToolRouterGroups(content string, manifest []skills.ToolCapabilityGroupManifest) ([]string, error) {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in response %q", content)
	}

	var names []string
	if err := json.Unmarshal([]byte(content[start:end+1]), &names); err != nil {
		return nil, err
	}

	valid := make(map[string]bool, len(manifest))
	for _, group := range manifest {
		valid[group.Name] = true
	}

	deduped := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || name == "core" || !valid[name] || seen[name] {
			continue
		}
		seen[name] = true
		deduped = append(deduped, name)
	}
	sort.Strings(deduped)
	return deduped, nil
}

// selectToolsWithLLM is the Phase 3 entry point. It resolves the background
// provider (Engine LM router → cloud fast model fallback), sends the user
// message plus a compact capability-group manifest, and returns the filtered
// tool subset. Falls back to heuristic on any failure so the main agent turn
// is never blocked.
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

	cacheKey := routerCacheKey(bgProvider, message)
	if groups, ok := readToolRouterCache(cacheKey); ok {
		logstore.Write("debug",
			fmt.Sprintf("Tool router: cache hit for %q → %v", message, groups),
			map[string]string{"mode": "llm"})
		return registry.ToolDefsForGroupsForMessage(groups, message)
	}

	manifest := registry.ToolCapabilityManifest()
	prompt := buildToolRouterPrompt(manifest, message)

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

	groups, err := parseToolRouterGroups(content, manifest)
	if err != nil {
		logstore.Write("warn",
			fmt.Sprintf("Tool router: parse failed (%v), using heuristic", err), nil)
		return registry.SelectiveToolDefs(message)
	}

	writeToolRouterCache(cacheKey, groups)
	out := registry.ToolDefsForGroupsForMessage(groups, message)

	logstore.Write("info",
		fmt.Sprintf("Tool router: selected groups=%v → %d tools via %s/%s",
			groups, len(out), bgProvider.Type, bgProvider.Model),
		map[string]string{"mode": "llm", "groups": strings.Join(groups, ",")})

	return out
}
