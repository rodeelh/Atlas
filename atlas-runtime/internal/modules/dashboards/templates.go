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
