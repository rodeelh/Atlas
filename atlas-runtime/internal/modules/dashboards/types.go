// Package dashboards persists user-visible dashboards composed of agent-authored
// widgets with named reusable data sources, live refresh, and a deterministic
// layout packer.
//
// v2 schema (SchemaVersion = 2):
//   - Named data sources (reusable across widgets in the same dashboard).
//   - Widget code is either a preset kind or agent-authored TSX, compiled
//     server-side via esbuild and rendered in a sandboxed iframe.
//   - Refresh modes: interval (SSE push), manual, or external push.
//   - Draft / live status lifecycle — agents build drafts and commit them.
package dashboards

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// SchemaVersion is the current dashboard schema version. Every dashboard
// persisted by this module is stamped with this value; loader rejects any
// other value (v1 data is archived separately, see store.go).
const SchemaVersion = 2

// Widget size tokens used by the layout packer.
const (
	SizeQuarter = "quarter" // 3 columns wide
	SizeThird   = "third"   // 4 columns wide
	SizeHalf    = "half"    // 6 columns wide
	SizeTall    = "tall"    // 6 wide × 4 rows
	SizeFull    = "full"    // 12 columns wide
)

// Widget code modes.
const (
	ModePreset = "preset" // built-in preset renderer keyed by Preset
	ModeCode   = "code"   // agent-authored TSX compiled to JS
)

// Built-in preset identifiers. Presets are rendered by the frontend stdlib.
const (
	PresetMetric     = "metric"
	PresetTable      = "table"
	PresetLineChart  = "line_chart"
	PresetAreaChart  = "area_chart"
	PresetBarChart   = "bar_chart"
	PresetPieChart   = "pie_chart"
	PresetDonutChart = "donut_chart"
	PresetScatter    = "scatter_chart"
	PresetStacked    = "stacked_chart"
	PresetList       = "list"
	PresetMarkdown   = "markdown"
	PresetTimeline   = "timeline"
	PresetHeatmap    = "heatmap"
	PresetProgress   = "progress"
	PresetGauge      = "gauge"
	PresetStatusGrid = "status_grid"
	PresetKPIGroup   = "kpi_group"
)

// Data source kinds.
const (
	SourceKindRuntime       = "runtime"
	SourceKindSkill         = "skill"
	SourceKindSQL           = "sql"
	SourceKindChatAnalytics = "chat_analytics"
	SourceKindGremlin       = "gremlin"
	SourceKindLiveCompute   = "live_compute"
)

// Refresh modes for a data source.
const (
	RefreshManual   = "manual"
	RefreshInterval = "interval"
	RefreshPush     = "push"
)

// Dashboard status.
const (
	StatusDraft = "draft"
	StatusLive  = "live"
)

// DataSource is a named, reusable data feed. Widgets bind to one by name.
type DataSource struct {
	Name    string         `json:"name"`   // unique within dashboard
	Kind    string         `json:"kind"`   // one of SourceKind*
	Config  map[string]any `json:"config"` // kind-specific config
	Refresh RefreshPolicy  `json:"refresh"`
}

// RefreshPolicy controls when a source re-fetches. IntervalSeconds applies
// only to RefreshInterval.
type RefreshPolicy struct {
	Mode            string `json:"mode"`
	IntervalSeconds int    `json:"intervalSeconds,omitempty"`
}

// DataSourceBinding connects a widget to a data source by name and optionally
// projects/filters the feed for that widget.
type DataSourceBinding struct {
	Source  string         `json:"source"`            // matches DataSource.Name
	Path    string         `json:"path,omitempty"`    // dot-path into source data
	Options map[string]any `json:"options,omitempty"` // per-binding renderer hints
}

// WidgetCode describes how a widget is rendered.
//
// Mode == ModePreset: Preset names a built-in renderer; Options are renderer
// props; TSX/Compiled/Hash are unused.
//
// Mode == ModeCode: TSX is the source the agent wrote; Compiled is the
// esbuild output; Hash is the sha256 of TSX used as a cache key. Preset is
// ignored.
type WidgetCode struct {
	Mode     string         `json:"mode"`
	Preset   string         `json:"preset,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
	TSX      string         `json:"tsx,omitempty"`
	Compiled string         `json:"compiled,omitempty"`
	Hash     string         `json:"hash,omitempty"`
}

// Widget is a single grid cell. Layout (GridX/Y/W/H) is always computed by the
// packer from Size + Group; persisted values are authoritative after commit.
type Widget struct {
	ID          string              `json:"id"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Size        string              `json:"size"`            // one of Size*
	Group       string              `json:"group,omitempty"` // optional grouping hint
	Bindings    []DataSourceBinding `json:"bindings,omitempty"`
	Code        WidgetCode          `json:"code"`
	// Layout outputs — populated by the packer at commit time.
	GridX int `json:"gridX"`
	GridY int `json:"gridY"`
	GridW int `json:"gridW"`
	GridH int `json:"gridH"`
}

func (w Widget) TitleOrID() string {
	if strings.TrimSpace(w.Title) != "" {
		return w.Title
	}
	return w.ID
}

// LayoutHints controls the packer. Columns defaults to 12.
type LayoutHints struct {
	Columns int `json:"columns"`
}

// Dashboard is the persisted shape of a v2 dashboard.
type Dashboard struct {
	SchemaVersion   int          `json:"schemaVersion"`
	ID              string       `json:"id"`
	BaseDashboardID string       `json:"baseDashboardId,omitempty"`
	Name            string       `json:"name"`
	Description     string       `json:"description,omitempty"`
	Status          string       `json:"status"` // draft | live
	Sources         []DataSource `json:"sources"`
	Widgets         []Widget     `json:"widgets"`
	Layout          LayoutHints  `json:"layout"`
	CreatedAt       time.Time    `json:"createdAt"`
	UpdatedAt       time.Time    `json:"updatedAt"`
	CommittedAt     *time.Time   `json:"committedAt,omitempty"`
}

// Summary is the lightweight shape returned by GET /dashboards.
type Summary struct {
	ID              string     `json:"id"`
	BaseDashboardID string     `json:"baseDashboardId,omitempty"`
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	Status          string     `json:"status"`
	WidgetCount     int        `json:"widgetCount"`
	SourceCount     int        `json:"sourceCount"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	CommittedAt     *time.Time `json:"committedAt,omitempty"`
}

// SummaryFor projects a dashboard into its list shape.
func SummaryFor(d Dashboard) Summary {
	return Summary{
		ID:              d.ID,
		BaseDashboardID: d.BaseDashboardID,
		Name:            d.Name,
		Description:     d.Description,
		Status:          d.Status,
		WidgetCount:     len(d.Widgets),
		SourceCount:     len(d.Sources),
		CreatedAt:       d.CreatedAt,
		UpdatedAt:       d.UpdatedAt,
		CommittedAt:     d.CommittedAt,
	}
}

// WidgetData is the per-widget payload returned by resolve endpoints.
type WidgetData struct {
	WidgetID   string `json:"widgetId"`
	SourceKind string `json:"sourceKind,omitempty"`
	Source     string `json:"source,omitempty"` // named source, when bound
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	Data       any    `json:"data,omitempty"`
	ResolvedAt string `json:"resolvedAt"`
	DurationMs int64  `json:"durationMs"`
}

// NewDashboardID returns a new random dashboard identifier.
func NewDashboardID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fall back to a timestamp-based id if entropy is unavailable.
		return "dashboard-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "dashboard-" + hex.EncodeToString(buf[:])
}

// NewWidgetID returns a new random widget identifier.
func NewWidgetID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "widget-" + time.Now().UTC().Format("150405.000000000")
	}
	return "widget-" + hex.EncodeToString(buf[:])
}
