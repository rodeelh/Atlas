package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// HelpModel renders a static keybinding reference.
type HelpModel struct {
	width    int
	height   int
	viewport viewport.Model
}

func NewHelpModel() HelpModel {
	vp := viewport.New(80, 20)
	vp.SetContent("")
	return HelpModel{viewport: vp}
}

func (m *HelpModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.viewport.Height = max(1, h)
	m.syncViewport()
}

func (m HelpModel) Update(msg tea.Msg) (HelpModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

type helpSection struct {
	title string
	rows  [][2]string // [key, description]
}

var helpSections = []helpSection{
	{
		title: "navigation",
		rows: [][2]string{
			{"1 2 3 4 5", "jump to tab"},
			{"tab", "next tab"},
			{"shift+tab", "previous tab"},
			{"ctrl+c", "quit"},
		},
	},
	{
		title: "chat",
		rows: [][2]string{
			{"enter", "send message"},
			{"type", "compose message"},
			{"⠿", "streaming in progress (input blocked)"},
		},
	},
	{
		title: "status",
		rows: [][2]string{
			{"r", "refresh"},
			{"↑ / k", "previous approval"},
			{"↓ / j", "next approval"},
			{"a", "approve selected"},
			{"d", "deny selected"},
		},
	},
	{
		title: "logs",
		rows: [][2]string{
			{"r", "refresh"},
			{"↑ / k", "scroll up"},
			{"↓ / j", "scroll down"},
			{"g / home", "jump to top"},
			{"G / end", "jump to bottom  (re-enable tail)"},
		},
	},
	{
		title: "settings",
		rows: [][2]string{
			{"↑ / k", "previous field"},
			{"↓ / j", "next field"},
			{"enter / space", "edit field"},
			{"esc", "cancel edit"},
			{"r", "reload from runtime"},
		},
	},
}

func (m HelpModel) View() string {
	if m.viewport.Width > 0 && m.viewport.Height > 0 {
		return m.viewport.View()
	}
	return m.renderContent()
}

func (m HelpModel) renderContent() string {
	var sb strings.Builder
	sb.WriteString("\n")

	for _, sec := range helpSections {
		sb.WriteString(sectionTitleStyle.Render("  "+sec.title) + "\n\n")
		for _, row := range sec.rows {
			key := keyStyle.Render(row[0])
			desc := textNormal.Render(row[1])
			sb.WriteString("  " + key + "  " + desc + "\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func (m *HelpModel) syncViewport() {
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 {
		return
	}
	m.viewport.SetContent(m.renderContent())
}
