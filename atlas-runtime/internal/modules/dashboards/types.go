// Package dashboards persists user-visible dashboards composed of widgets that
// pull data from runtime endpoints, skills, the open web, or local SQL.
//
// Stage 1 of the dashboards module: types, JSON-backed storage, and starter
// templates. No HTTP routes, no resolvers, no AI yet — those land in stages 2+.
package dashboards

import "time"

// Source kind identifiers used by Widget.Source.Kind.
const (
	SourceKindRuntime = "runtime" // GET against an allowlisted runtime endpoint
	SourceKindSkill   = "skill"   // call a read-only skill action
	SourceKindWeb     = "web"     // proxied GET against an external URL
	SourceKindSQL     = "sql"     // read-only SELECT against atlas.sqlite3
)

// Widget kind identifiers used by Widget.Kind.
const (
	WidgetKindMetric     = "metric"
	WidgetKindTable      = "table"
	WidgetKindLineChart  = "line_chart"
	WidgetKindBarChart   = "bar_chart"
	WidgetKindMarkdown   = "markdown"
	WidgetKindList       = "list"
	WidgetKindCustomHTML = "custom_html"
)

// DataSource describes how a widget loads its data. Exactly one of the
// kind-specific fields is meaningful per source.
type DataSource struct {
	Kind string `json:"kind"`

	// runtime
	Endpoint string            `json:"endpoint,omitempty"`
	Query    map[string]string `json:"query,omitempty"`

	// skill
	Action string         `json:"action,omitempty"`
	Args   map[string]any `json:"args,omitempty"`

	// web
	URL string `json:"url,omitempty"`

	// sql
	SQL string `json:"sql,omitempty"`
}

// Widget is one cell in the dashboard grid.
type Widget struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	GridX       int            `json:"gridX"`
	GridY       int            `json:"gridY"`
	GridW       int            `json:"gridW"`
	GridH       int            `json:"gridH"`
	Source      *DataSource    `json:"source,omitempty"`
	Options     map[string]any `json:"options,omitempty"`

	// custom_html only
	HTML string `json:"html,omitempty"`
	CSS  string `json:"css,omitempty"`
	JS   string `json:"js,omitempty"`

	// optional auto-refresh in seconds; 0 = no auto refresh
	RefreshIntervalSeconds int `json:"refreshIntervalSeconds,omitempty"`
}

// DashboardDefinition is the persisted shape of a dashboard.
type DashboardDefinition struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Template    string    `json:"template,omitempty"`
	Widgets     []Widget  `json:"widgets"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Summary is the lightweight shape returned by GET /dashboards.
type Summary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Template    string    `json:"template,omitempty"`
	WidgetCount int       `json:"widgetCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// SummaryFor projects a definition into the list shape.
func SummaryFor(d DashboardDefinition) Summary {
	return Summary{
		ID:          d.ID,
		Name:        d.Name,
		Description: d.Description,
		Template:    d.Template,
		WidgetCount: len(d.Widgets),
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}
