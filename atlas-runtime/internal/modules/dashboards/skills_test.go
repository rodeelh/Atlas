package dashboards

// skills_test.go — exercises the dashboard.* skill handlers directly. We
// bypass the skills.Registry shell because the registration step would pull
// in *skills.Registry construction, which needs a DB. The handlers themselves
// are pure methods on Module and trivial to drive with synthetic args.
//
// The dashboard.create AI path is covered in generate_test.go. Here we cover
// the create handler's argument validation and wiring guards.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"atlas-runtime-go/internal/agent"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ── dashboard.list ────────────────────────────────────────────────────────────

func TestSkillList_Empty(t *testing.T) {
	m := New(t.TempDir(), "")
	res, err := m.skillList(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success: %+v", res)
	}
	if !strings.Contains(res.Summary, "No dashboards") {
		t.Errorf("summary should mention empty state, got %q", res.Summary)
	}
	if res.Artifacts["count"] != 0 {
		t.Errorf("count: %v", res.Artifacts["count"])
	}
}

func TestSkillList_AfterCreate(t *testing.T) {
	m := New(t.TempDir(), "")
	_, _ = m.store.Save(newDef("d1", "First"))
	_, _ = m.store.Save(newDef("d2", "Second"))

	res, err := m.skillList(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success: %+v", res)
	}
	if res.Artifacts["count"] != 2 {
		t.Errorf("count: %v", res.Artifacts["count"])
	}
}

// ── dashboard.get ─────────────────────────────────────────────────────────────

func TestSkillGet_HappyPath(t *testing.T) {
	m := New(t.TempDir(), "")
	_, _ = m.store.Save(newDef("d1", "First"))

	res, err := m.skillGet(context.Background(), mustJSON(t, map[string]string{"id": "d1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success: %+v", res)
	}
	if _, ok := res.Artifacts["dashboard"]; !ok {
		t.Errorf("expected dashboard artifact, got %+v", res.Artifacts)
	}
}

func TestSkillGet_MissingID(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillGet(context.Background(), mustJSON(t, map[string]string{}))
	if res.Success {
		t.Error("expected failure on missing id")
	}
}

func TestSkillGet_InvalidJSON(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillGet(context.Background(), json.RawMessage(`not-json`))
	if res.Success {
		t.Error("expected failure on invalid JSON")
	}
}

func TestSkillGet_NotFound(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillGet(context.Background(), mustJSON(t, map[string]string{"id": "ghost"}))
	if res.Success {
		t.Error("expected failure on unknown id")
	}
}

// ── dashboard.create ─────────────────────────────────────────────────────────

func TestSkillCreate_RejectsMissingName(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillCreate(context.Background(), mustJSON(t, map[string]string{"prompt": "x"}))
	if res.Success {
		t.Error("expected failure on missing name")
	}
}

func TestSkillCreate_RejectsMissingPrompt(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillCreate(context.Background(), mustJSON(t, map[string]string{"name": "x"}))
	if res.Success {
		t.Error("expected failure on missing prompt")
	}
}

func TestSkillCreate_RejectsWhitespaceOnlyName(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillCreate(context.Background(), mustJSON(t, map[string]string{"name": "   ", "prompt": "x"}))
	if res.Success {
		t.Error("expected failure on whitespace-only name")
	}
}

func TestSkillCreate_NoProviderResolver(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillCreate(context.Background(), mustJSON(t, map[string]string{
		"name":   "Spend",
		"prompt": "Show last 7 days of token spend",
	}))
	if res.Success {
		t.Error("expected failure when provider resolver not configured")
	}
	if !strings.Contains(res.Summary, "provider") {
		t.Errorf("error should mention provider wiring, got %q", res.Summary)
	}
}

func TestSkillCreate_ProviderResolverError(t *testing.T) {
	m := New(t.TempDir(), "")
	m.providerResolver = func() (agent.ProviderConfig, error) {
		return agent.ProviderConfig{}, errors.New("no API key in keychain")
	}
	res, _ := m.skillCreate(context.Background(), mustJSON(t, map[string]string{
		"name":   "Spend",
		"prompt": "Show last 7 days of token spend",
	}))
	if res.Success {
		t.Error("expected failure when resolver returns error")
	}
}

// ── dashboard.delete ─────────────────────────────────────────────────────────

func TestSkillDelete_HappyPath(t *testing.T) {
	m := New(t.TempDir(), "")
	_, _ = m.store.Save(newDef("d1", "First"))

	res, err := m.skillDelete(context.Background(), mustJSON(t, map[string]string{"id": "d1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success: %+v", res)
	}
	if _, err := m.store.Get("d1"); !errors.Is(err, ErrNotFound) {
		t.Error("dashboard should be gone after delete")
	}
}

func TestSkillDelete_NotFound(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillDelete(context.Background(), mustJSON(t, map[string]string{"id": "ghost"}))
	if res.Success {
		t.Error("expected failure on unknown id")
	}
}

func TestSkillDelete_MissingID(t *testing.T) {
	m := New(t.TempDir(), "")
	res, _ := m.skillDelete(context.Background(), mustJSON(t, map[string]string{}))
	if res.Success {
		t.Error("expected failure on missing id")
	}
}
