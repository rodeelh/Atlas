package ui

import "github.com/charmbracelet/lipgloss"

// Nord-inspired color palette.
const (
	colorBg        = "#1a1b2e"
	colorBgPanel   = "#2E3440"
	colorBgAlt     = "#3B4252"
	colorBgElement = "#434C5E"
	colorBorder    = "#4C566A"
	colorText      = "#D8DEE9"
	colorTextBold  = "#ECEFF4"
	colorTextMuted = "#6272a4"
	colorPrimary   = "#88C0D0"
	colorSecondary = "#81A1C1"
	colorAccent    = "#5E81AC"
	colorSuccess   = "#A3BE8C"
	colorError     = "#BF616A"
	colorWarning   = "#EBCB8B"
	colorUser      = "#B48EAD"
	colorTool      = "#D08770"
)

// ── Base styles ───────────────────────────────────────────────────────────────

var (
	textNormal  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorText))
	textBold    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTextBold)).Bold(true)
	textMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTextMuted))
	textPrimary = lipgloss.NewStyle().Foreground(lipgloss.Color(colorPrimary))
	textSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSuccess))
	textError   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
	textWarning = lipgloss.NewStyle().Foreground(lipgloss.Color(colorWarning))
	textUser    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorUser)).Bold(true)
	textTool    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTool))

	keyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorPrimary)).
			Background(lipgloss.Color(colorBgElement)).
			Padding(0, 1).
			Width(20)
)

// ── Layout styles ─────────────────────────────────────────────────────────────

var (
	headerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorBgAlt)).
			Foreground(lipgloss.Color(colorText))

	sidebarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorBgPanel)).
			BorderRight(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(colorBorder))

	contentStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorBg)).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(colorBgAlt)).
			Foreground(lipgloss.Color(colorTextMuted))
)

// ── Sidebar tab styles ────────────────────────────────────────────────────────

var (
	tabActiveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorPrimary)).
			Bold(true)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colorTextMuted))

	tabHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorBorder))
)

// ── Chat styles ───────────────────────────────────────────────────────────────

var (
	userLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorUser)).
			Bold(true)

	assistantLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colorPrimary)).
				Bold(true)

	toolLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorTool)).
			Italic(true)

	msgSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorBorder))

	inputBorderStyle = lipgloss.NewStyle().
				BorderTop(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color(colorBorder)).
				Padding(0, 0)

	inputPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colorPrimary)).
				Bold(true)
)

// ── Status / logs styles ──────────────────────────────────────────────────────

var (
	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorTextMuted)).
			Width(26)

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorText))

	sectionTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colorPrimary)).
				Bold(true).
				MarginBottom(1)

	logInfoStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSecondary))
	logDebugStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTextMuted))
	logWarnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorWarning))
	logErrorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// dot returns a colored status indicator.
func dot(online bool) string {
	if online {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(colorSuccess)).Render("●")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colorError)).Render("●")
}

// divider returns a horizontal rule of width w.
func divider(w int) string {
	if w <= 0 {
		return ""
	}
	s := ""
	for range w {
		s += "─"
	}
	return msgSepStyle.Render(s)
}
