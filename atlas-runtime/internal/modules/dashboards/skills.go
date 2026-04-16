package dashboards

// skills.go — 12 granular agent-facing skills for the v2 dashboards module.
//
// The canonical flow for an agent is:
//   1. dashboard.create_draft       — open a new draft
//   2. dashboard.add_data_source    — declare reusable feeds
//   3. dashboard.add_widget         — bind widgets to sources
//   4. dashboard.preview            — resolve a single widget to confirm data
//   5. dashboard.commit             — pack layout, flip draft → live
//
// Mutating skills operate only on dashboards whose Status == "draft".
// dashboard.list and dashboard.get work across both statuses.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"atlas-runtime-go/internal/skills"
)

// RegisterSkills registers the 12 dashboard skills onto the shared registry.
func (m *Module) RegisterSkills(reg *skills.Registry) {
	if reg == nil {
		return
	}

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.list",
			Description: "List all dashboards. Optional status filter: 'draft' or 'live'.",
			Properties: map[string]skills.ToolParam{
				"status": {Description: "Filter by status: draft or live. Omit for all.", Type: "string"},
			},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult:    m.skillList,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.get",
			Description: "Fetch a full dashboard definition by ID.",
			Properties: map[string]skills.ToolParam{
				"id": {Description: "Dashboard ID.", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult:    m.skillGet,
	})

	// dashboard.create is the v1 one-shot creation skill. It was replaced in
	// v2 by the create_draft → add_data_source → add_widget → commit workflow.
	// This stub gives the agent a clear migration message instead of an
	// "unknown action" error so it can self-correct.
	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.create",
			Description: "Deprecated — use the v2 workflow: (1) dashboard.create_draft, (2) dashboard.add_data_source, (3) dashboard.add_widget, (4) dashboard.commit.",
			Properties: map[string]skills.ToolParam{
				"name":   {Description: "Dashboard name.", Type: "string"},
				"prompt": {Description: "Description of the dashboard.", Type: "string"},
			},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult: func(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
			return skills.ErrResult("dashboard.create", "deprecated", false, errors.New(
				"dashboard.create was removed in v2. Build dashboards step-by-step: "+
					"(1) dashboard.create_draft {name} — open a new draft; "+
					"(2) dashboard.add_data_source {id, name, kind, config} — add data feeds (skill/runtime/sql/live_compute/chat_analytics/gremlin); "+
					"(3) dashboard.add_widget {id, preset, size, bindings} — attach widgets; "+
					"(4) dashboard.commit {id} — publish.")), nil
		},
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.create_draft",
			Description: "Create a new draft dashboard. Returns the draft ID; add sources and widgets, then call dashboard.commit.",
			Properties: map[string]skills.ToolParam{
				"name":        {Description: "Short display name.", Type: "string"},
				"description": {Description: "Optional description.", Type: "string"},
			},
			Required: []string{"name"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillCreateDraft,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.set_metadata",
			Description: "Update a draft dashboard's name or description.",
			Properties: map[string]skills.ToolParam{
				"id":          {Description: "Draft dashboard ID.", Type: "string"},
				"name":        {Description: "New display name.", Type: "string"},
				"description": {Description: "New description.", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillSetMetadata,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name: "dashboard.add_data_source",
			Description: "Attach a named, reusable data source to a draft dashboard. " +
				"kind must be one of: runtime, skill, sql, chat_analytics, gremlin, live_compute. " +
				"config shape per kind — " +
				"skill: {\"action\":\"weather.current\",\"args\":{\"location\":\"Orlando\"}} (action must be a read-only skill; use canonical dot form e.g. weather.current, not weather__current); " +
				"runtime: {\"endpoint\":\"/status\"} (must be on the runtime allowlist); " +
				"sql: {\"sql\":\"SELECT ...\"}; " +
				"chat_analytics: {\"query\":\"conversations_per_day\"} (Atlas conversation stats only — valid queries: conversations_per_day, messages_per_day, top_conversations, recent_conversations, message_counts_by_role, token_usage_per_day, token_usage_by_provider, memory_counts_by_category, most_important_memories, recent_memories); " +
				"gremlin: {\"gremlinID\":\"<id>\"}; " +
				"live_compute standalone (AI-generated, no external data): {\"prompt\":\"Write a brief AI news summary with 5 bullet points covering recent developments in artificial intelligence. Return JSON: {\\\"headline\\\":\\\"...\\\",\\\"items\\\":[{\\\"title\\\":\\\"\\\",\\\"summary\\\":\\\"\\\"}]}\",\"outputSchema\":{\"headline\":\"string\",\"items\":\"array\"}}; " +
				"live_compute with inputs (transform other sources): {\"prompt\":\"summarise the data\",\"inputs\":[\"source1\"],\"outputSchema\":{}}. " +
				"IMPORTANT: When external APIs are unavailable (TLS errors, no API key), use live_compute standalone — the AI model generates content from its own knowledge. No inputs array needed. " +
				"On success the result includes resolvedSample (the live data) and shape (suggestedPreset + paths with types + sample values) — use that to pick preset and options.path for dashboard.add_widget instead of guessing.",
			Properties: map[string]skills.ToolParam{
				"id":              {Description: "Draft dashboard ID.", Type: "string"},
				"name":            {Description: "Source name (unique within the dashboard).", Type: "string"},
				"kind":            {Description: "One of: runtime, skill, sql, chat_analytics, gremlin, live_compute.", Type: "string"},
				"config":          {Description: "Kind-specific config object. See skill description for per-kind shape.", Type: "string"},
				"refreshMode":     {Description: "manual, interval, or push. Defaults to manual.", Type: "string"},
				"intervalSeconds": {Description: "Seconds between refreshes when refreshMode is interval.", Type: "integer"},
			},
			Required: []string{"id", "name", "kind"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillAddDataSource,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.remove_data_source",
			Description: "Remove a named data source from a draft.",
			Properties: map[string]skills.ToolParam{
				"id":   {Description: "Draft dashboard ID.", Type: "string"},
				"name": {Description: "Source name.", Type: "string"},
			},
			Required: []string{"id", "name"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillRemoveDataSource,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name: "dashboard.add_widget",
			Description: "Add a widget to a draft dashboard. " +
				"SIZE (column width in a 12-col grid): quarter=3 cols, third=4 cols, half=6 cols, full=12 cols, tall=6 cols×4 rows. " +
				"ROW-FILLING — design rows that sum to 12 cols for a clean layout: " +
				"  • KPI strip (top row): 4×quarter or 3×third — great for 3-4 related metrics side-by-side; " +
				"  • Split row: 2×half — two equal charts or lists; " +
				"  • Asymmetric: half+quarter+quarter (6+3+3) or half+third+... ; " +
				"  • Wide: 1×full — use only for tables needing many columns or wide charts. " +
				"PRESET/SIZE pairing: " +
				"  metric → quarter or third (single KPI number; avoid half/full — too much empty space); " +
				"  line_chart / bar_chart → half or full (charts need width to be readable); " +
				"  list → half or third (scrollable, compact); " +
				"  table → half or full (multiple columns need width); " +
				"  markdown → half or full (prose/summary content). " +
				"OPTIONS per preset: " +
				"  metric: {path:\"fieldName\", label:\"Display label\", format:\"integer|currency|percent|compact\"}; " +
				"  table: {path:\"arrayField\", columns:[\"col1\",\"col2\"]}; " +
				"  line_chart/bar_chart: {path:\"arrayField\", x:\"dateField\", y:\"valueField\"}; " +
				"  list: {itemsPath:\"arrayField\", labelKey:\"title\", subKey:\"subtitle\"}; " +
				"  markdown: {path:\"textField\"} or omit path to render full source as JSON. " +
				"BINDINGS: JSON array [{source:\"sourceName\"}] — one binding per widget, referencing a source added via dashboard.add_data_source.",
			Properties: map[string]skills.ToolParam{
				"id":       {Description: "Draft dashboard ID.", Type: "string"},
				"title":    {Description: "Widget title.", Type: "string"},
				"size":     {Description: "quarter | third | half | tall | full", Type: "string"},
				"preset":   {Description: "metric | table | line_chart | bar_chart | list | markdown", Type: "string"},
				"group":    {Description: "Optional group name to co-locate related widgets in the same visual section.", Type: "string"},
				"bindings": {Description: "JSON array [{source:\"sourceName\"}].", Type: "string"},
				"options":  {Description: "JSON object of preset-specific display options (path, columns, label, format, x, y, etc.).", Type: "string"},
			},
			Required: []string{"id", "size", "preset"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillAddWidget,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.update_widget",
			Description: "Replace a widget in a draft by widgetId. Same fields as dashboard.add_widget.",
			Properties: map[string]skills.ToolParam{
				"id":       {Description: "Draft dashboard ID.", Type: "string"},
				"widgetId": {Description: "Widget ID to replace.", Type: "string"},
				"title":    {Description: "Widget title.", Type: "string"},
				"size":     {Description: "quarter | third | half | tall | full", Type: "string"},
				"preset":   {Description: "metric | table | line_chart | bar_chart | list | markdown", Type: "string"},
				"group":    {Description: "Optional group name.", Type: "string"},
				"bindings": {Description: "JSON array of bindings.", Type: "string"},
				"options":  {Description: "JSON options object.", Type: "string"},
			},
			Required: []string{"id", "widgetId"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillUpdateWidget,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.remove_widget",
			Description: "Remove a widget from a draft.",
			Properties: map[string]skills.ToolParam{
				"id":       {Description: "Draft dashboard ID.", Type: "string"},
				"widgetId": {Description: "Widget ID.", Type: "string"},
			},
			Required: []string{"id", "widgetId"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillRemoveWidget,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.preview",
			Description: "Resolve a single widget's data to verify bindings are correct. Does not commit.",
			Properties: map[string]skills.ToolParam{
				"id":       {Description: "Dashboard ID.", Type: "string"},
				"widgetId": {Description: "Widget ID.", Type: "string"},
			},
			Required: []string{"id", "widgetId"},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult:    m.skillPreview,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.commit",
			Description: "Pack the layout and flip a draft dashboard to live. Fails if widgets have invalid code or bindings.",
			Properties: map[string]skills.ToolParam{
				"id": {Description: "Draft dashboard ID.", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.skillCommit,
	})

	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "dashboard.delete",
			Description: "Delete a dashboard (draft or live) permanently.",
			Properties: map[string]skills.ToolParam{
				"id": {Description: "Dashboard ID.", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassDestructiveLocal,
		FnResult:    m.skillDelete,
	})
}

// ── skill implementations ─────────────────────────────────────────────────────

func (m *Module) skillList(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(raw, &args)
	defs := m.store.ListByStatus(args.Status)
	summary := make([]Summary, 0, len(defs))
	for _, d := range defs {
		summary = append(summary, SummaryFor(d))
	}
	artifacts := map[string]any{"dashboards": summary}
	return skills.OKResult(fmt.Sprintf("%d dashboards", len(summary)), artifacts), nil
}

func (m *Module) skillGet(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct{ ID string `json:"id"` }
	_ = json.Unmarshal(raw, &args)
	if args.ID == "" {
		return skills.ErrResult("dashboard.get", "arg validation", false, errors.New("id is required")), nil
	}
	d, err := m.store.Get(args.ID)
	if err != nil {
		return skills.ErrResult("dashboard.get", "store.Get", false, err), nil
	}
	return skills.OKResult(fmt.Sprintf("dashboard %q (%s)", d.Name, d.Status), map[string]any{"dashboard": d}), nil
}

func (m *Module) skillCreateDraft(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &args)
	if args.Name == "" {
		return skills.ErrResult("dashboard.create_draft", "arg validation", false, errors.New("name is required")), nil
	}
	d := Dashboard{
		ID:          NewDashboardID(),
		Name:        args.Name,
		Description: args.Description,
		Status:      StatusDraft,
		Sources:     []DataSource{},
		Widgets:     []Widget{},
		Layout:      LayoutHints{Columns: 12},
	}
	saved, err := m.store.Save(d)
	if err != nil {
		return skills.ErrResult("dashboard.create_draft", "store.Save", false, err), nil
	}
	return skills.OKResult(fmt.Sprintf("created draft %s", saved.ID), map[string]any{"id": saved.ID, "dashboard": saved}), nil
}

func (m *Module) skillSetMetadata(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &args)
	d, err := m.loadDraft(args.ID)
	if err != nil {
		return skills.ErrResult("dashboard.set_metadata", "loadDraft", false, err), nil
	}
	if args.Name != "" {
		d.Name = args.Name
	}
	if args.Description != "" {
		d.Description = args.Description
	}
	saved, err := m.store.Save(d)
	if err != nil {
		return skills.ErrResult("dashboard.set_metadata", "store.Save", false, err), nil
	}
	return skills.OKResult("metadata updated", map[string]any{"dashboard": saved}), nil
}

func (m *Module) skillAddDataSource(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Kind            string `json:"kind"`
		Config          any    `json:"config"` // may be object or JSON string
		RefreshMode     string `json:"refreshMode"`
		IntervalSeconds int    `json:"intervalSeconds"`
	}
	_ = json.Unmarshal(raw, &args)
	d, err := m.loadDraft(args.ID)
	if err != nil {
		return skills.ErrResult("dashboard.add_data_source", "loadDraft", false, err), nil
	}
	if args.Name == "" || args.Kind == "" {
		return skills.ErrResult("dashboard.add_data_source", "arg validation", false, errors.New("name and kind are required")), nil
	}
	cfg, err := coerceObject(args.Config)
	if err != nil {
		return skills.ErrResult("dashboard.add_data_source", "config parse", false, err), nil
	}
	refresh := RefreshPolicy{Mode: RefreshManual}
	switch args.RefreshMode {
	case "", RefreshManual:
		refresh.Mode = RefreshManual
	case RefreshInterval:
		if args.IntervalSeconds <= 0 {
			return skills.ErrResult("dashboard.add_data_source", "arg validation", false, errors.New("intervalSeconds must be > 0")), nil
		}
		refresh.Mode = RefreshInterval
		refresh.IntervalSeconds = args.IntervalSeconds
	case RefreshPush:
		refresh.Mode = RefreshPush
	default:
		return skills.ErrResult("dashboard.add_data_source", "arg validation", false, fmt.Errorf("unknown refreshMode: %q", args.RefreshMode)), nil
	}
	if err := validateSourceKindConfig(args.Kind, cfg); err != nil {
		return skills.ErrResult("dashboard.add_data_source", "config validate", false, err), nil
	}
	// For skill sources: normalize the action name to canonical dot form, then
	// verify the action exists in the registry and is read-only.
	if args.Kind == SourceKindSkill && m.skillsRegistry != nil {
		if action, _ := cfg["action"].(string); action != "" {
			canonical := m.skillsRegistry.Normalise(action)
			if !m.skillsRegistry.HasAction(canonical) {
				return skills.ErrResult("dashboard.add_data_source", "config validate", false,
					fmt.Errorf("skill action %q is not registered; use the canonical dot form (e.g. weather.current) and check available skills via skills.list", action)), nil
			}
			if m.skillsRegistry.GetActionClass(canonical) != skills.ActionClassRead {
				return skills.ErrResult("dashboard.add_data_source", "config validate", false,
					fmt.Errorf("skill action %q is not read-only; only ActionClassRead skills may be used as dashboard data sources", canonical)), nil
			}
			cfg["action"] = canonical
		}
	}
	// Replace by name if it already exists; append otherwise.
	replaced := false
	newSrc := DataSource{Name: args.Name, Kind: args.Kind, Config: cfg, Refresh: refresh}
	for i := range d.Sources {
		if d.Sources[i].Name == args.Name {
			d.Sources[i] = newSrc
			replaced = true
			break
		}
	}
	if !replaced {
		d.Sources = append(d.Sources, newSrc)
	}
	saved, err := m.store.Save(d)
	if err != nil {
		return skills.ErrResult("dashboard.add_data_source", "store.Save", false, err), nil
	}
	verb := "added"
	if replaced {
		verb = "replaced"
	}

	// Trial-resolve the source so the agent sees the actual data shape and a
	// preset hint before it calls dashboard.add_widget. Without this, the
	// agent is blind to what the source returns and has to guess widget
	// options — the single most common cause of broken dashboards.
	trialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	trialData, trialErr := resolveSource(trialCtx, m.resolverDeps(), newSrc, map[string]any{})
	artifacts := map[string]any{"dashboard": saved}
	if trialErr != nil {
		artifacts["resolveWarning"] = trialErr.Error()
		artifacts["hint"] = "Source saved but failed to resolve on first try. Check config args (e.g. missing 'location' for weather.*) and re-add."
	} else {
		artifacts["resolvedSample"] = truncateSample(trialData)
		artifacts["shape"] = describeShape(trialData)
	}
	return skills.OKResult(fmt.Sprintf("%s source %q", verb, args.Name), artifacts), nil
}

func (m *Module) skillRemoveDataSource(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct{ ID, Name string }
	_ = json.Unmarshal(raw, &args)
	d, err := m.loadDraft(args.ID)
	if err != nil {
		return skills.ErrResult("dashboard.remove_data_source", "loadDraft", false, err), nil
	}
	idx := -1
	for i, s := range d.Sources {
		if s.Name == args.Name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return skills.ErrResult("dashboard.remove_data_source", "lookup", false, fmt.Errorf("source %q not found", args.Name)), nil
	}
	d.Sources = append(d.Sources[:idx], d.Sources[idx+1:]...)
	saved, err := m.store.Save(d)
	if err != nil {
		return skills.ErrResult("dashboard.remove_data_source", "store.Save", false, err), nil
	}
	return skills.OKResult(fmt.Sprintf("removed source %q", args.Name), map[string]any{"dashboard": saved}), nil
}

func (m *Module) skillAddWidget(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	w, dID, err := m.parseWidgetArgs(raw, false)
	if err != nil {
		return skills.ErrResult("dashboard.add_widget", "arg parse", false, err), nil
	}
	d, err := m.loadDraft(dID)
	if err != nil {
		return skills.ErrResult("dashboard.add_widget", "loadDraft", false, err), nil
	}
	if err := validateBindings(d, w.Bindings); err != nil {
		return skills.ErrResult("dashboard.add_widget", "bindings", false, err), nil
	}
	w.ID = NewWidgetID()
	d.Widgets = append(d.Widgets, w)
	saved, err := m.store.Save(d)
	if err != nil {
		return skills.ErrResult("dashboard.add_widget", "store.Save", false, err), nil
	}
	return skills.OKResult(fmt.Sprintf("added widget %s", w.ID), map[string]any{"widget": w, "dashboard": saved}), nil
}

func (m *Module) skillUpdateWidget(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	w, dID, err := m.parseWidgetArgs(raw, true)
	if err != nil {
		return skills.ErrResult("dashboard.update_widget", "arg parse", false, err), nil
	}
	d, err := m.loadDraft(dID)
	if err != nil {
		return skills.ErrResult("dashboard.update_widget", "loadDraft", false, err), nil
	}
	idx := -1
	for i := range d.Widgets {
		if d.Widgets[i].ID == w.ID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return skills.ErrResult("dashboard.update_widget", "lookup", false, fmt.Errorf("widget %q not found", w.ID)), nil
	}
	if err := validateBindings(d, w.Bindings); err != nil {
		return skills.ErrResult("dashboard.update_widget", "bindings", false, err), nil
	}
	d.Widgets[idx] = w
	saved, err := m.store.Save(d)
	if err != nil {
		return skills.ErrResult("dashboard.update_widget", "store.Save", false, err), nil
	}
	return skills.OKResult("updated widget", map[string]any{"widget": w, "dashboard": saved}), nil
}

func (m *Module) skillRemoveWidget(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct{ ID, WidgetID string }
	_ = json.Unmarshal(raw, &args)
	d, err := m.loadDraft(args.ID)
	if err != nil {
		return skills.ErrResult("dashboard.remove_widget", "loadDraft", false, err), nil
	}
	idx := -1
	for i := range d.Widgets {
		if d.Widgets[i].ID == args.WidgetID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return skills.ErrResult("dashboard.remove_widget", "lookup", false, fmt.Errorf("widget %q not found", args.WidgetID)), nil
	}
	d.Widgets = append(d.Widgets[:idx], d.Widgets[idx+1:]...)
	saved, err := m.store.Save(d)
	if err != nil {
		return skills.ErrResult("dashboard.remove_widget", "store.Save", false, err), nil
	}
	return skills.OKResult(fmt.Sprintf("removed widget %s", args.WidgetID), map[string]any{"dashboard": saved}), nil
}

func (m *Module) skillPreview(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct{ ID, WidgetID string }
	_ = json.Unmarshal(raw, &args)
	d, err := m.store.Get(args.ID)
	if err != nil {
		return skills.ErrResult("dashboard.preview", "store.Get", false, err), nil
	}
	var widget *Widget
	for i := range d.Widgets {
		if d.Widgets[i].ID == args.WidgetID {
			widget = &d.Widgets[i]
			break
		}
	}
	if widget == nil {
		return skills.ErrResult("dashboard.preview", "lookup", false, fmt.Errorf("widget %q not found", args.WidgetID)), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, err := m.resolveWidgetData(ctx, d, *widget)
	if err != nil {
		return skills.ErrResult("dashboard.preview", "resolve", false, err), nil
	}
	return skills.OKResult("preview ok", map[string]any{
		"widgetId": args.WidgetID,
		"data":     truncateSample(data),
		"shape":    describeShape(data),
	}), nil
}

func (m *Module) skillCommit(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct{ ID string }
	_ = json.Unmarshal(raw, &args)
	d, err := m.loadDraft(args.ID)
	if err != nil {
		return skills.ErrResult("dashboard.commit", "loadDraft", false, err), nil
	}
	// Compile every widget's code (presets validate; code mode fails).
	for i := range d.Widgets {
		if err := compileWidget(&d.Widgets[i].Code); err != nil {
			return skills.ErrResult("dashboard.commit", fmt.Sprintf("widget %s compile", d.Widgets[i].ID), false, err), nil
		}
	}
	// Verify every binding points at a known source.
	for _, w := range d.Widgets {
		if err := validateBindings(d, w.Bindings); err != nil {
			return skills.ErrResult("dashboard.commit", fmt.Sprintf("widget %s bindings", w.ID), false, err), nil
		}
	}
	columns := d.Layout.Columns
	if columns <= 0 {
		columns = 12
		d.Layout.Columns = columns
	}
	d.Widgets = packGrid(d.Widgets, columns)
	d.Status = StatusLive
	now := nowUTC()
	d.CommittedAt = &now
	saved, err := m.store.Save(d)
	if err != nil {
		return skills.ErrResult("dashboard.commit", "store.Save", false, err), nil
	}
	return skills.OKResult(fmt.Sprintf("committed dashboard %s", saved.ID), map[string]any{"dashboard": saved}), nil
}

func (m *Module) skillDelete(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) {
	var args struct{ ID string }
	_ = json.Unmarshal(raw, &args)
	if args.ID == "" {
		return skills.ErrResult("dashboard.delete", "arg validation", false, errors.New("id is required")), nil
	}
	if err := m.store.Delete(args.ID); err != nil {
		return skills.ErrResult("dashboard.delete", "store.Delete", false, err), nil
	}
	return skills.OKResult(fmt.Sprintf("deleted dashboard %s", args.ID), map[string]any{"id": args.ID}), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (m *Module) loadDraft(id string) (Dashboard, error) {
	if id == "" {
		return Dashboard{}, errors.New("id is required")
	}
	d, err := m.store.Get(id)
	if err != nil {
		return Dashboard{}, err
	}
	if d.Status != StatusDraft {
		return Dashboard{}, fmt.Errorf("dashboard %s is not a draft (status=%s)", id, d.Status)
	}
	return d, nil
}

// parseWidgetArgs parses the common widget skill args. When requireID is
// true the widgetId must be present (for update); when false a new id is
// assigned by the caller.
func (m *Module) parseWidgetArgs(raw json.RawMessage, requireID bool) (Widget, string, error) {
	var args struct {
		ID       string `json:"id"`
		WidgetID string `json:"widgetId"`
		Title    string `json:"title"`
		Size     string `json:"size"`
		Preset   string `json:"preset"`
		Group    string `json:"group"`
		Bindings any    `json:"bindings"`
		Options  any    `json:"options"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Widget{}, "", fmt.Errorf("invalid args: %w", err)
	}
	if args.ID == "" {
		return Widget{}, "", errors.New("id is required")
	}
	if requireID && args.WidgetID == "" {
		return Widget{}, "", errors.New("widgetId is required")
	}
	w := Widget{
		ID:    args.WidgetID,
		Title: args.Title,
		Size:  args.Size,
		Group: args.Group,
		Code: WidgetCode{
			Mode:   ModePreset,
			Preset: args.Preset,
		},
	}
	if args.Options != nil {
		opts, err := coerceObject(args.Options)
		if err != nil {
			return Widget{}, "", fmt.Errorf("options: %w", err)
		}
		w.Code.Options = opts
	}
	if args.Bindings != nil {
		bindings, err := coerceBindings(args.Bindings)
		if err != nil {
			return Widget{}, "", fmt.Errorf("bindings: %w", err)
		}
		w.Bindings = bindings
	}
	return w, args.ID, nil
}

// coerceObject accepts either a map[string]any or a JSON string and returns
// a map. The dual shape is needed because the tool call layer sometimes
// passes object args as a stringified JSON object.
func coerceObject(v any) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	switch x := v.(type) {
	case map[string]any:
		return x, nil
	case string:
		if x == "" {
			return map[string]any{}, nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(x), &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

func coerceBindings(v any) ([]DataSourceBinding, error) {
	// Normalize to []any then map each entry.
	var list []any
	switch x := v.(type) {
	case []any:
		list = x
	case string:
		if x == "" {
			return nil, nil
		}
		if err := json.Unmarshal([]byte(x), &list); err != nil {
			return nil, err
		}
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
	}
	out := make([]DataSourceBinding, 0, len(list))
	for i, entry := range list {
		obj, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("bindings[%d] must be an object", i)
		}
		name, _ := obj["source"].(string)
		if name == "" {
			return nil, fmt.Errorf("bindings[%d] requires source", i)
		}
		b := DataSourceBinding{Source: name}
		if p, ok := obj["path"].(string); ok {
			b.Path = p
		}
		if opts, ok := obj["options"].(map[string]any); ok {
			b.Options = opts
		}
		out = append(out, b)
	}
	return out, nil
}

// validateBindings ensures every binding refers to a source on the dashboard.
func validateBindings(d Dashboard, bindings []DataSourceBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	names := map[string]bool{}
	for _, s := range d.Sources {
		names[s.Name] = true
	}
	for _, b := range bindings {
		if !names[b.Source] {
			return fmt.Errorf("binding references unknown source: %q", b.Source)
		}
	}
	return nil
}

// validateSourceKindConfig performs kind-specific config validation at add time.
func validateSourceKindConfig(kind string, cfg map[string]any) error {
	switch kind {
	case SourceKindRuntime:
		endpoint, _ := cfg["endpoint"].(string)
		if endpoint == "" {
			return errors.New("runtime source requires endpoint")
		}
		if !allowedRuntimeEndpoint(endpoint) {
			return fmt.Errorf("runtime endpoint %q is not on the dashboards allowlist", endpoint)
		}
	case SourceKindSkill:
		if _, ok := cfg["action"].(string); !ok {
			return errors.New("skill source requires action")
		}
	case SourceKindSQL:
		sqlText, _ := cfg["sql"].(string)
		if _, err := validateSelectSQL(sqlText); err != nil {
			return err
		}
	case SourceKindChatAnalytics:
		name, _ := cfg["query"].(string)
		if err := validateAnalyticsQuery(name); err != nil {
			return err
		}
	case SourceKindGremlin:
		if id, _ := cfg["gremlinID"].(string); id == "" {
			return errors.New("gremlin source requires gremlinID")
		}
	case SourceKindLiveCompute:
		if err := validateLiveCompute(cfg); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown source kind: %q", kind)
	}
	return nil
}
