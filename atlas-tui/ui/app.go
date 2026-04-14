// Package ui implements the Atlas TUI using Bubble Tea.
package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rodeelh/atlas-tui/client"
)

// sidebarWidth is the content width of the left sidebar (border adds 1 more).
const sidebarWidth = 16

// tabKind identifies the active TUI tab.
type tabKind int

const (
	tabChat     tabKind = 0
	tabLogs     tabKind = 1
	tabStatus   tabKind = 2
	tabSettings tabKind = 3
	tabHelp     tabKind = 4
)

var tabNames = []string{"CHAT", "LOGS", "STATUS", "SETTINGS", "HELP"}
var tabIcons = []string{"◌", "≡", "◈", "⚙", "?"}

// pingResultMsg carries the result of a connectivity probe.
type pingResultMsg struct{ ok bool }

// pingCmd checks whether the runtime is reachable.
func pingCmd(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		return pingResultMsg{ok: c.Ping()}
	}
}

// pingTickMsg fires on the ping-check timer.
type pingTickMsg struct{}

func pingTickCmd() tea.Cmd {
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg {
		return pingTickMsg{}
	})
}

// AppModel is the root Bubble Tea model.
type AppModel struct {
	client    *client.Client
	port      int
	width     int
	height    int
	activeTab tabKind
	connected bool

	chat     ChatModel
	status   StatusModel
	logs     LogsModel
	settings SettingsModel
	help     HelpModel
}

// NewApp constructs the root application model.
func NewApp(c *client.Client, port int) AppModel {
	m := AppModel{
		client:    c,
		port:      port,
		activeTab: tabChat,
		chat:      NewChatModel(c),
		status:    NewStatusModel(c),
		logs:      NewLogsModel(c),
		settings:  NewSettingsModel(c),
		help:      NewHelpModel(),
	}
	return m
}

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		pingCmd(m.client),
		pingTickCmd(),
		m.chat.Init(),
		m.status.Init(),
		m.logs.Init(),
		m.settings.Init(),
	)
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		contentW, bodyH := m.contentDims()
		m.chat.SetSize(contentW, bodyH)
		m.status.SetSize(contentW, bodyH)
		m.logs.SetSize(contentW, bodyH)
		m.settings.SetSize(contentW, bodyH)
		m.help.SetSize(contentW, bodyH)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "1":
			return m.switchTab(tabChat, cmds)
		case "2":
			return m.switchTab(tabLogs, cmds)
		case "3":
			return m.switchTab(tabStatus, cmds)
		case "4":
			return m.switchTab(tabSettings, cmds)
		case "5":
			return m.switchTab(tabHelp, cmds)
		case "tab":
			next := tabKind((int(m.activeTab) + 1) % len(tabNames))
			return m.switchTab(next, cmds)
		case "shift+tab":
			prev := tabKind((int(m.activeTab) + len(tabNames) - 1) % len(tabNames))
			return m.switchTab(prev, cmds)
		}

	case pingResultMsg:
		m.connected = msg.ok
		cmds = append(cmds, pingTickCmd())

	case pingTickMsg:
		cmds = append(cmds, pingCmd(m.client))
	}

	// Route message to active tab.
	switch m.activeTab {
	case tabChat:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		cmds = append(cmds, cmd)
	case tabLogs:
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(msg)
		cmds = append(cmds, cmd)
	case tabStatus:
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
	case tabSettings:
		var cmd tea.Cmd
		m.settings, cmd = m.settings.Update(msg)
		cmds = append(cmds, cmd)
	case tabHelp:
		var cmd tea.Cmd
		m.help, cmd = m.help.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// switchTab changes the active tab and triggers its on-focus command.
func (m AppModel) switchTab(t tabKind, cmds []tea.Cmd) (AppModel, tea.Cmd) {
	m.activeTab = t
	switch t {
	case tabLogs:
		cmds = append(cmds, m.logs.OnFocus())
	case tabStatus:
		cmds = append(cmds, m.status.OnFocus())
	case tabSettings:
		cmds = append(cmds, m.settings.OnFocus())
	}
	return m, tea.Batch(cmds...)
}

// contentDims returns the usable width and height for the content pane.
// header = 1 line, statusbar = 1 line.
func (m AppModel) contentDims() (w, h int) {
	sidebarFrame := sidebarStyle.Padding(0, 1).GetHorizontalFrameSize()
	contentFrame := contentStyle.GetHorizontalFrameSize()
	w = m.width - sidebarWidth - sidebarFrame - contentFrame
	h = m.height - 2
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return
}

// View renders the full TUI.
func (m AppModel) View() string {
	if m.width == 0 {
		return ""
	}
	header := m.renderHeader()
	body := m.renderBody()
	statusBar := m.renderStatusBar()
	return lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar)
}

func (m AppModel) renderHeader() string {
	logo := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorPrimary)).
		Bold(true).
		Render("◆ ATLAS")

	// pad content to exactly m.width so the background fills the full line
	pad := m.width - lipgloss.Width(logo) - 2 // 2 = left margin space
	if pad < 0 {
		pad = 0
	}
	content := " " + logo + strings.Repeat(" ", pad)
	return headerStyle.Width(m.width).Render(content)
}

func (m AppModel) renderBody() string {
	bodyH := m.height - 2
	if bodyH < 0 {
		bodyH = 0
	}

	sidebar := m.renderSidebar(bodyH)
	contentTotalW := m.width - lipgloss.Width(sidebar)
	if contentTotalW < 0 {
		contentTotalW = 0
	}
	content := contentStyle.Width(contentTotalW).Height(bodyH).Render(m.activeView())
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, content)
}

func (m AppModel) renderSidebar(h int) string {
	lines := make([]string, 0, len(tabNames)+2)
	lines = append(lines, "") // top padding

	for i, name := range tabNames {
		t := tabKind(i)
		if t == m.activeTab {
			arrow := tabActiveStyle.Render("›")
			label := tabActiveStyle.Render(name)
			lines = append(lines, arrow+" "+label)
		} else {
			hint := tabHintStyle.Render(fmt.Sprintf("%d", i+1))
			label := tabInactiveStyle.Render(name)
			lines = append(lines, hint+" "+label)
		}
	}

	return sidebarStyle.
		Width(sidebarWidth).
		Height(h).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m AppModel) activeView() string {
	switch m.activeTab {
	case tabChat:
		return m.chat.View()
	case tabLogs:
		return m.logs.View()
	case tabStatus:
		return m.status.View()
	case tabSettings:
		return m.settings.View()
	case tabHelp:
		return m.help.View()
	}
	return ""
}

func (m AppModel) renderStatusBar() string {
	connDot := dot(m.connected)
	connLabel := "connected"
	if !m.connected {
		connLabel = "disconnected"
	}

	left := " " + connDot + " " + textMuted.Render(connLabel)
	right := textMuted.Render("1-5 switch  tab cycle  ctrl+c quit") + " "

	// fill the gap so the background covers the full width — no Padding needed
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	content := left + strings.Repeat(" ", gap) + right
	return statusBarStyle.Width(m.width).Render(content)
}
