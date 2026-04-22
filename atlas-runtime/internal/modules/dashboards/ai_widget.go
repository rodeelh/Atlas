package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"atlas-runtime-go/internal/agent"
)

type AIWidgetAuthor interface {
	Generate(ctx context.Context, req AIWidgetPromptRequest) (GeneratedWidgetSpec, error)
}

type AIWidgetPromptRequest struct {
	Prompt     string
	SourceName string
	SourceData any
	TitleHint  string
	SizeHint   string
}

type GeneratedWidgetSpec struct {
	Mode        string         `json:"mode"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Size        string         `json:"size,omitempty"`
	Preset      string         `json:"preset,omitempty"`
	Options     map[string]any `json:"options,omitempty"`
	TSX         string         `json:"tsx,omitempty"`
}

type AIWidgetGenerator struct {
	resolver func() (providerConfig, error)
}

func NewAIWidgetGenerator(resolver func() (providerConfig, error)) *AIWidgetGenerator {
	return &AIWidgetGenerator{resolver: resolver}
}

const aiWidgetSystemPrompt = `You generate exactly one dashboard widget spec for Atlas.
Respond with ONLY valid JSON matching this shape:
{
  "mode": "preset" | "code",
  "title": "short title",
  "description": "optional short description",
  "size": "quarter" | "third" | "half" | "tall" | "full",
  "preset": "metric" | "table" | "line_chart" | "area_chart" | "bar_chart" | "pie_chart" | "donut_chart" | "scatter_chart" | "stacked_chart" | "list" | "markdown" | "timeline" | "heatmap" | "progress" | "gauge" | "status_grid" | "kpi_group",
  "options": {},
  "tsx": "code widget source when mode=code"
}

Rules:
- Prefer mode="preset" when a built-in preset can satisfy the request well.
- Use mode="code" only when the request genuinely needs custom rendering or interaction.
- If mode="preset", include preset and options. Do not include tsx.
- If mode="code", include tsx. Do not include preset.
- Keep title concise and user-facing.
- Keep description short.
- Choose a reasonable size for the widget's content density.
- If source data is provided, shape the widget around that data and do not invent fields not present.
- For code widgets, only import from @atlas/ui, preact, or preact/hooks.
- For markdown summaries based on source data, prefer a preset markdown widget over a code widget.
- Return JSON only. No markdown fences or commentary.`

func (g *AIWidgetGenerator) Generate(ctx context.Context, req AIWidgetPromptRequest) (GeneratedWidgetSpec, error) {
	cfg, err := g.resolver()
	if err != nil {
		return GeneratedWidgetSpec{}, fmt.Errorf("ai widget: provider resolver: %w", err)
	}
	p := agent.ProviderConfig{
		Type:         agent.ProviderType(cfg.Type),
		APIKey:       cfg.APIKey,
		Model:        cfg.Model,
		BaseURL:      cfg.BaseURL,
		ExtraHeaders: cfg.ExtraHeaders,
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "User request: %s\n", req.Prompt)
	if strings.TrimSpace(req.TitleHint) != "" {
		fmt.Fprintf(&sb, "Title hint: %s\n", req.TitleHint)
	}
	if strings.TrimSpace(req.SizeHint) != "" {
		fmt.Fprintf(&sb, "Preferred size: %s\n", req.SizeHint)
	}
	if req.SourceName != "" {
		sample, _ := json.Marshal(req.SourceData)
		fmt.Fprintf(&sb, "Bound source: %s\nSource sample JSON:\n%s\n", req.SourceName, string(sample))
	} else {
		sb.WriteString("No source is currently bound. Generate a widget that can stand alone.\n")
	}

	msgs := []agent.OAIMessage{
		{Role: "system", Content: aiWidgetSystemPrompt},
		{Role: "user", Content: sb.String()},
	}
	reply, _, _, err := agent.CallAINonStreamingExported(ctx, p, msgs, nil)
	if err != nil {
		return GeneratedWidgetSpec{}, fmt.Errorf("ai widget: AI call failed: %w", err)
	}
	text := liveStripFences(liveExtractText(reply.Content))
	var spec GeneratedWidgetSpec
	if err := json.Unmarshal([]byte(text), &spec); err != nil {
		return GeneratedWidgetSpec{}, fmt.Errorf("ai widget: invalid JSON response: %w", err)
	}
	if err := validateGeneratedWidgetSpec(spec); err != nil {
		return GeneratedWidgetSpec{}, err
	}
	return spec, nil
}

func validateGeneratedWidgetSpec(spec GeneratedWidgetSpec) error {
	switch spec.Mode {
	case ModePreset:
		if spec.Preset == "" {
			return errors.New("ai widget: preset mode requires preset")
		}
		code := WidgetCode{Mode: ModePreset, Preset: spec.Preset, Options: spec.Options}
		return compileWidget(&code)
	case ModeCode:
		if strings.TrimSpace(spec.TSX) == "" {
			return errors.New("ai widget: code mode requires tsx")
		}
		code := WidgetCode{Mode: ModeCode, TSX: spec.TSX}
		return compileWidget(&code)
	default:
		return errors.New("ai widget: mode must be preset or code")
	}
}
