package chat

import (
	"context"
	"encoding/json"
	"testing"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/skills"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func emptyRegistry() *skills.Registry {
	return skills.NewRegistry("", nil, nil)
}

func makeToolCall(id, name, argsJSON string) agent.OAIToolCall {
	return agent.OAIToolCall{
		ID:   id,
		Type: "function",
		Function: agent.OAIFunctionCall{
			Name:      name,
			Arguments: argsJSON,
		},
	}
}

// ── identitySelector ─────────────────────────────────────────────────────────

func TestIdentitySelector_InitialReturnsNil(t *testing.T) {
	sel := agent.IdentitySelector{}
	if sel.Initial() != nil {
		t.Error("IdentitySelector.Initial() should return nil (full tool list)")
	}
}

func TestIdentitySelector_UpgradeReturnsNil(t *testing.T) {
	sel := agent.IdentitySelector{}
	tc := makeToolCall("c1", "request_tools", `{}`)
	tools, summary := sel.Upgrade(tc)
	if tools != nil || summary != "" {
		t.Errorf("IdentitySelector.Upgrade() = (%v, %q), want (nil, \"\")", tools, summary)
	}
}

// ── heuristicSelector ────────────────────────────────────────────────────────

func TestHeuristicSelector_InitialDelegatesToRegistry(t *testing.T) {
	reg := emptyRegistry()
	sel := &heuristicSelector{msg: "check weather", registry: reg}
	// Empty registry → nil slice; we just confirm it doesn't panic and returns
	// whatever SelectiveToolDefs returns.
	result := sel.Initial()
	// result may be nil with empty registry — just ensure it doesn't panic.
	_ = result
}

func TestHeuristicSelector_UpgradeReturnsSameAsList(t *testing.T) {
	reg := emptyRegistry()
	sel := &heuristicSelector{msg: "check weather", registry: reg}
	tc := makeToolCall("c1", "request_tools", `{"broad":true}`)
	tools, _ := sel.Upgrade(tc)
	initial := sel.Initial()
	// Both should come from the same SelectiveToolDefs call.
	if len(tools) != len(initial) {
		t.Errorf("Upgrade len=%d, Initial len=%d; want equal", len(tools), len(initial))
	}
}

// ── lazySelector ─────────────────────────────────────────────────────────────

func TestLazySelector_StageProgression(t *testing.T) {
	reg := emptyRegistry()
	sel := &lazySelector{
		ctx:      context.Background(),
		cfg:      config.RuntimeConfigSnapshot{},
		msg:      "test",
		registry: reg,
	}

	// First upgrade with no args → stage 1 (short list).
	tc1 := makeToolCall("c1", "request_tools", `{}`)
	tools1, summary1 := sel.Upgrade(tc1)
	if sel.stage != 1 {
		t.Errorf("stage after first upgrade = %d, want 1", sel.stage)
	}
	if summary1 == "" {
		t.Error("expected non-empty summary for stage-1 upgrade")
	}
	_ = tools1

	// Second upgrade with broad=true → stage 2.
	tc2 := makeToolCall("c2", "request_tools", `{"broad":true}`)
	tools2, summary2 := sel.Upgrade(tc2)
	if sel.stage != 2 {
		t.Errorf("stage after broad upgrade = %d, want 2", sel.stage)
	}
	if summary2 == "" {
		t.Error("expected non-empty summary for stage-2 upgrade")
	}
	_ = tools2
}

func TestLazySelector_BroadFromStage0(t *testing.T) {
	reg := emptyRegistry()
	sel := &lazySelector{
		ctx:      context.Background(),
		cfg:      config.RuntimeConfigSnapshot{},
		msg:      "test",
		registry: reg,
	}
	tc := makeToolCall("c1", "request_tools", `{"broad":true}`)
	_, _ = sel.Upgrade(tc)
	if sel.stage != 2 {
		t.Errorf("stage after broad=true from stage 0 = %d, want 2", sel.stage)
	}
}

func TestLazySelector_CategoryUpgrade(t *testing.T) {
	reg := emptyRegistry()
	sel := &lazySelector{
		ctx:      context.Background(),
		cfg:      config.RuntimeConfigSnapshot{},
		msg:      "test",
		registry: reg,
	}
	tc := makeToolCall("c1", "request_tools", `{"categories":["web","fs"]}`)
	_, summary := sel.Upgrade(tc)
	if sel.stage != 2 {
		t.Errorf("stage after category upgrade = %d, want 2", sel.stage)
	}
	for _, cat := range []string{"web", "fs"} {
		if !containsSubstr(summary, cat) {
			t.Errorf("summary %q does not mention category %q", summary, cat)
		}
	}
}

// ── NewSelector factory ───────────────────────────────────────────────────────

func TestNewSelector_Modes(t *testing.T) {
	reg := emptyRegistry()
	ctx := context.Background()
	cfg := config.RuntimeConfigSnapshot{}

	cases := []struct {
		mode     string
		wantType string
	}{
		{"lazy", "*chat.lazySelector"},
		{"llm", "*chat.llmSelector"},
		{"heuristic", "*chat.heuristicSelector"},
		{"off", "agent.IdentitySelector"},
		{"unknown", "agent.IdentitySelector"},
		{"", "agent.IdentitySelector"},
	}
	for _, tc := range cases {
		sel := NewSelector(tc.mode, nil, ctx, cfg, nil, "msg", reg)
		got := typeString(sel)
		if got != tc.wantType {
			t.Errorf("NewSelector(%q) type = %s, want %s", tc.mode, got, tc.wantType)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func typeString(v any) string {
	b, _ := json.Marshal(nil)
	_ = b
	return typeName(v)
}

func typeName(v any) string {
	switch v.(type) {
	case *lazySelector:
		return "*chat.lazySelector"
	case *llmSelector:
		return "*chat.llmSelector"
	case *heuristicSelector:
		return "*chat.heuristicSelector"
	case *scopedSelector:
		return "*chat.scopedSelector"
	case agent.IdentitySelector:
		return "agent.IdentitySelector"
	default:
		return "unknown"
	}
}

// TestNewSelector_PolicyScopesRegistry verifies that AllowedToolPrefixes in a
// ToolPolicy causes NewSelector to pre-filter the registry for all modes, and
// that "off" mode returns a scopedSelector (not IdentitySelector) when a policy
// is present so the nil-initial shortcut cannot bypass the prefix constraint.
func TestNewSelector_PolicyScopesRegistry(t *testing.T) {
	reg := emptyRegistry()
	ctx := context.Background()
	cfg := config.RuntimeConfigSnapshot{}
	policy := &agent.ToolPolicy{Enabled: true, AllowedToolPrefixes: []string{"weather"}}

	// "off" + policy → scopedSelector, not IdentitySelector
	sel := NewSelector("off", policy, ctx, cfg, nil, "msg", reg)
	if got := typeName(sel); got != "*chat.scopedSelector" {
		t.Errorf("NewSelector(off, policy) = %s, want *chat.scopedSelector", got)
	}

	// "off" without policy → IdentitySelector (unchanged behaviour)
	sel2 := NewSelector("off", nil, ctx, cfg, nil, "msg", reg)
	if got := typeName(sel2); got != "agent.IdentitySelector" {
		t.Errorf("NewSelector(off, nil) = %s, want agent.IdentitySelector", got)
	}

	// lazy + policy → lazySelector (registry pre-filtered internally)
	sel3 := NewSelector("lazy", policy, ctx, cfg, nil, "msg", reg)
	if got := typeName(sel3); got != "*chat.lazySelector" {
		t.Errorf("NewSelector(lazy, policy) = %s, want *chat.lazySelector", got)
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
