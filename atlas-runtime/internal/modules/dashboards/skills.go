package dashboards

// skills.go — registers the dashboard.* skill family on the runtime skills
// registry. The skills close over the module's store and provider resolver,
// so they share state with the HTTP routes — every change made through chat
// is reflected in the web UI without an extra sync step.
//
// Action classes:
//   dashboard.list   → read              (auto-approve)
//   dashboard.get    → read              (auto-approve)
//   dashboard.create → local_write       (needs approval — writes a JSON file)
//   dashboard.delete → destructive_local (needs approval — removes user data)
//
// Errors are returned as a string result with success=false rather than as a
// Go error so the model gets a clean message it can react to without the
// runtime treating the call as a hard failure.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"atlas-runtime-go/internal/skills"
)

// RegisterSkills installs the dashboard.* skill family on reg. Safe to call
// once at startup; not idempotent (skills.Registry.register panics on dup).
func (m *Module) RegisterSkills(reg *skills.Registry) {
	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.list",
			Description: "List all dashboards the user has saved. Returns id, name, description, and widget count for each.",
			Properties:  map[string]skills.ToolParam{},
			Required:    []string{},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult:    m.skillList,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.get",
			Description: "Fetch the full definition of a saved dashboard by id, including all widgets and their data sources.",
			Properties: map[string]skills.ToolParam{
				"id": {Description: "The dashboard id (e.g. 'dashboard-20260407…').", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult:    m.skillGet,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name: "dashboard.create",
			Description: "Generate and save a new dashboard from a natural-language description. " +
				"The dashboard's widgets will pull live data from runtime endpoints, read-only skills, or read-only SQL — " +
				"never from arbitrary web URLs or hand-coded HTML. Returns the saved dashboard id and name on success.",
			Properties: map[string]skills.ToolParam{
				"name": {
					Description: "Short, human-friendly name for the dashboard (e.g. 'Token Spend This Month').",
					Type:        "string",
				},
				"prompt": {
					Description: "Free-form description of what the dashboard should show. Be specific about data sources " +
						"(memories? token usage? workflow runs?) and how the user wants to see it (table, chart, single metric).",
					Type: "string",
				},
			},
			Required: []string{"name", "prompt"},
		},
		PermLevel:   "draft",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillCreate,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.delete",
			Description: "Permanently delete a saved dashboard by id. This cannot be undone.",
			Properties: map[string]skills.ToolParam{
				"id": {Description: "The dashboard id to delete.", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassDestructiveLocal,
		FnResult:    m.skillDelete,
	})
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (m *Module) skillList(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	defs := m.store.List()
	summaries := make([]Summary, 0, len(defs))
	for _, d := range defs {
		summaries = append(summaries, SummaryFor(d))
	}
	count := len(summaries)
	summary := fmt.Sprintf("%d dashboard(s).", count)
	if count == 0 {
		summary = "No dashboards saved yet. Use dashboard.create to make one."
	}
	return skills.OKResult(summary, map[string]any{
		"count":      count,
		"dashboards": summaries,
	}), nil
}

func (m *Module) skillGet(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ErrResult("get dashboard", "arg validation", false, err), nil
	}
	if p.ID == "" {
		return skills.ErrResult("get dashboard", "arg validation", false, errors.New("id is required")), nil
	}
	def, err := m.store.Get(p.ID)
	if errors.Is(err, ErrNotFound) {
		return skills.ErrResult("get dashboard "+p.ID, "store lookup", false, err), nil
	}
	if err != nil {
		return skills.ErrResult("get dashboard "+p.ID, "store lookup", false, err), nil
	}
	return skills.OKResult(
		fmt.Sprintf("Dashboard %q has %d widget(s).", def.Name, len(def.Widgets)),
		map[string]any{"dashboard": def},
	), nil
}

func (m *Module) skillCreate(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ErrResult("create dashboard", "arg validation", false, err), nil
	}
	if strings.TrimSpace(p.Name) == "" {
		return skills.ErrResult("create dashboard", "arg validation", false, errors.New("name is required")), nil
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return skills.ErrResult("create dashboard", "arg validation", false, errors.New("prompt is required")), nil
	}
	if m.providerResolver == nil {
		return skills.ErrResult("create dashboard", "wiring", false, errors.New("AI provider resolver not configured")), nil
	}

	def, err := Generate(ctx, m.providerResolver, p.Name, p.Prompt)
	if err != nil {
		return skills.ErrResult("generate dashboard "+p.Name, "AI generation", false, err), nil
	}
	if def.ID == "" {
		def.ID = newDashboardID()
	}
	saved, err := m.store.Save(*def)
	if err != nil {
		return skills.ErrResult("save dashboard "+p.Name, "store write", false, err), nil
	}
	return skills.OKResult(
		fmt.Sprintf("Created dashboard %q (id: %s) with %d widget(s).", saved.Name, saved.ID, len(saved.Widgets)),
		map[string]any{
			"id":          saved.ID,
			"name":        saved.Name,
			"widgetCount": len(saved.Widgets),
			"url":         "/web/#dashboards/" + saved.ID,
		},
	), nil
}

func (m *Module) skillDelete(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ErrResult("delete dashboard", "arg validation", false, err), nil
	}
	if p.ID == "" {
		return skills.ErrResult("delete dashboard", "arg validation", false, errors.New("id is required")), nil
	}
	// Capture the name first so the success summary is meaningful even though
	// the record is gone by the time we return.
	name := p.ID
	if def, err := m.store.Get(p.ID); err == nil {
		name = def.Name
	}
	if err := m.store.Delete(p.ID); errors.Is(err, ErrNotFound) {
		return skills.ErrResult("delete dashboard "+p.ID, "store lookup", false, err), nil
	} else if err != nil {
		return skills.ErrResult("delete dashboard "+p.ID, "store delete", false, err), nil
	}
	return skills.OKResult(
		fmt.Sprintf("Deleted dashboard %q.", name),
		map[string]any{"id": p.ID},
	), nil
}
