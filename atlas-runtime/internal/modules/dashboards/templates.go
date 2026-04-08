package dashboards

// templates.go — built-in starter dashboards. Cloned via POST /dashboards
// with { "template": "<id>" } in stage 2.
//
// Templates use only runtime data sources, so they work the moment the module
// is mounted (no extra credentials, no skill calls, no SQL).

// Template is a named, ready-to-clone dashboard skeleton.
type Template struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Definition  DashboardDefinition `json:"definition"`
}

// Templates returns the built-in dashboard templates.
func Templates() []Template {
	return []Template{
		systemHealthTemplate(),
		usageTemplate(),
		memoryAtlasTemplate(),
		mindHealthTemplate(),
	}
}

// mindHealthTemplate — the "Mind Health" dashboard used during the few-day
// review to see how the mind-thoughts subsystem is behaving. Every threshold
// in the spec (95 auto-execute, 80 propose, 2 negatives, 3 ignores, 60 min
// idle, 6 hour floor) is testable by looking at the widgets this template
// renders.
func mindHealthTemplate() Template {
	return Template{
		ID:          "mind_health",
		Name:        "Mind Health",
		Description: "How the mind-thoughts subsystem is behaving — naps, thought lifecycle, auto-execute outcomes.",
		Definition: DashboardDefinition{
			Name:        "Mind Health",
			Description: "Naps, thoughts, auto-execute outcomes, engagement signals.",
			Template:    "mind_health",
			Widgets: []Widget{
				{
					ID:    "active-thoughts",
					Kind:  WidgetKindMetric,
					Title: "Active Thoughts",
					GridX: 0, GridY: 0, GridW: 3, GridH: 2,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/mind/thoughts",
					},
					Options: map[string]any{
						"path": "count",
					},
					RefreshIntervalSeconds: 15,
				},
				{
					ID:    "pending-greetings",
					Kind:  WidgetKindMetric,
					Title: "Pending Greetings",
					GridX: 3, GridY: 0, GridW: 3, GridH: 2,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/chat/pending-greetings",
					},
					Options: map[string]any{
						"path": "count",
					},
					RefreshIntervalSeconds: 10,
				},
				{
					ID:    "event-breakdown",
					Kind:  WidgetKindTable,
					Title: "Telemetry breakdown (last 24h)",
					GridX: 6, GridY: 0, GridW: 6, GridH: 4,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/mind/telemetry/summary",
						Query:    map[string]string{"since": "24h"},
					},
					Options: map[string]any{
						"path":    "by_kind",
						"columns": []string{"kind", "count"},
					},
					RefreshIntervalSeconds: 30,
				},
				{
					ID:    "recent-naps",
					Kind:  WidgetKindTable,
					Title: "Recent Naps",
					GridX: 0, GridY: 2, GridW: 6, GridH: 4,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/mind/telemetry",
						Query: map[string]string{
							"kind":  "nap_completed",
							"since": "24h",
							"limit": "20",
						},
					},
					Options: map[string]any{
						"path":    "rows",
						"columns": []string{"ts", "payload"},
					},
					RefreshIntervalSeconds: 30,
				},
				{
					ID:    "thought-lifecycle",
					Kind:  WidgetKindTable,
					Title: "Thought lifecycle events (last 24h)",
					GridX: 0, GridY: 6, GridW: 12, GridH: 5,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/mind/telemetry",
						Query: map[string]string{
							"since": "24h",
							"limit": "100",
						},
					},
					Options: map[string]any{
						"path":    "rows",
						"columns": []string{"ts", "kind", "thought_id", "payload"},
					},
					RefreshIntervalSeconds: 30,
				},
			},
		},
	}
}

// TemplateByID returns the template with the given ID, or false.
func TemplateByID(id string) (Template, bool) {
	for _, t := range Templates() {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

func systemHealthTemplate() Template {
	return Template{
		ID:          "system_health",
		Name:        "System Health",
		Description: "Live runtime status, recent log activity, and pending approvals.",
		Definition: DashboardDefinition{
			Name:        "System Health",
			Description: "Live runtime status, recent log activity, and pending approvals.",
			Template:    "system_health",
			Widgets: []Widget{
				{
					ID:    "status",
					Kind:  WidgetKindMarkdown,
					Title: "Runtime Status",
					GridX: 0, GridY: 0, GridW: 12, GridH: 2,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/status",
					},
					RefreshIntervalSeconds: 15,
				},
				{
					ID:    "recent-logs",
					Kind:  WidgetKindTable,
					Title: "Recent Logs",
					GridX: 0, GridY: 2, GridW: 12, GridH: 6,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/logs",
					},
					Options: map[string]any{
						"columns": []string{"timestamp", "level", "message"},
						"limit":   50,
					},
					RefreshIntervalSeconds: 10,
				},
			},
		},
	}
}

func usageTemplate() Template {
	return Template{
		ID:          "usage",
		Name:        "Token Usage",
		Description: "Token spend over time and breakdown by model.",
		Definition: DashboardDefinition{
			Name:        "Token Usage",
			Description: "Token spend over time and breakdown by model.",
			Template:    "usage",
			Widgets: []Widget{
				{
					ID:    "spend-30d",
					Kind:  WidgetKindMetric,
					Title: "Spend (last 30 days)",
					GridX: 0, GridY: 0, GridW: 4, GridH: 2,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/usage/summary",
						Query:    map[string]string{"days": "30"},
					},
					Options: map[string]any{
						"path":   "totalCostUSD",
						"format": "currency",
					},
				},
				{
					ID:    "tokens-30d",
					Kind:  WidgetKindMetric,
					Title: "Tokens (last 30 days)",
					GridX: 4, GridY: 0, GridW: 4, GridH: 2,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/usage/summary",
						Query:    map[string]string{"days": "30"},
					},
					Options: map[string]any{
						"path":   "totalTokens",
						"format": "integer",
					},
				},
				{
					ID:    "daily-series",
					Kind:  WidgetKindLineChart,
					Title: "Daily Spend",
					GridX: 0, GridY: 2, GridW: 12, GridH: 5,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/usage/summary",
						Query:    map[string]string{"days": "30"},
					},
					Options: map[string]any{
						"seriesPath": "dailySeries",
						"x":          "date",
						"y":          "costUSD",
					},
				},
			},
		},
	}
}

func memoryAtlasTemplate() Template {
	return Template{
		ID:          "memory_atlas",
		Name:        "Memory Atlas",
		Description: "Recent memories grouped by category.",
		Definition: DashboardDefinition{
			Name:        "Memory Atlas",
			Description: "Recent memories grouped by category.",
			Template:    "memory_atlas",
			Widgets: []Widget{
				{
					ID:    "recent-memories",
					Kind:  WidgetKindTable,
					Title: "Recent Memories",
					GridX: 0, GridY: 0, GridW: 12, GridH: 6,
					Source: &DataSource{
						Kind:     SourceKindRuntime,
						Endpoint: "/memories",
						Query:    map[string]string{"limit": "50"},
					},
					Options: map[string]any{
						"columns": []string{"category", "title", "importance", "updatedAt"},
					},
				},
			},
		},
	}
}
