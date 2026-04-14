package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rodeelh/atlas-tui/client"
)

// ── Message types ─────────────────────────────────────────────────────────────

type logsLoadedMsg struct {
	entries []client.LogEntry
	err     error
}

type logsTickMsg struct{}

func logsTickCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return logsTickMsg{}
	})
}

// ── Model ─────────────────────────────────────────────────────────────────────

// LogsModel renders the live log stream view.
type LogsModel struct {
	client   *client.Client
	width    int
	height   int
	viewport viewport.Model
	entries  []client.LogEntry
	tail     bool // if true, auto-scroll to bottom
	loading  bool
	err      string
}

func NewLogsModel(c *client.Client) LogsModel {
	vp := viewport.New(80, 20)
	vp.SetContent("")
	return LogsModel{
		client:   c,
		viewport: vp,
		tail:     true,
		loading:  true,
	}
}

func (m LogsModel) Init() tea.Cmd {
	return tea.Batch(fetchLogs(m.client), logsTickCmd())
}

// OnFocus is called when the user switches to this tab.
func (m LogsModel) OnFocus() tea.Cmd {
	return fetchLogs(m.client)
}

func fetchLogs(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		entries, err := c.GetLogs(200)
		return logsLoadedMsg{entries: entries, err: err}
	}
}

func (m LogsModel) Update(msg tea.Msg) (LogsModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case logsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.err = ""
			m.entries = msg.entries
			m.syncViewport()
		}
		return m, nil

	case logsTickMsg:
		cmds = append(cmds, fetchLogs(m.client), logsTickCmd())

	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			m.loading = true
			return m, fetchLogs(m.client)
		case "G", "end":
			m.tail = true
			m.viewport.GotoBottom()
			return m, nil
		case "g", "home":
			m.tail = false
			m.viewport.GotoTop()
			return m, nil
		}
		// If user scrolls up, disable auto-tail.
		prevOffset := m.viewport.YOffset
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		cmds = append(cmds, vpCmd)
		if m.viewport.YOffset < prevOffset {
			m.tail = false
		}
		return m, tea.Batch(cmds...)
	}

	// Forward viewport scrolling (mouse wheel etc).
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

func (m *LogsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.viewport.Height = h - 2 // leave room for header + hint
	m.syncViewport()
}

func (m *LogsModel) syncViewport() {
	m.viewport.SetContent(m.renderEntries())
	if m.tail {
		m.viewport.GotoBottom()
	}
}

func (m LogsModel) renderEntries() string {
	if len(m.entries) == 0 {
		return textMuted.Render("  no log entries")
	}
	lines := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		lines = append(lines, m.renderEntry(e))
	}
	return strings.Join(lines, "\n")
}

func (m LogsModel) renderEntry(e client.LogEntry) string {
	ts := e.Timestamp
	if len(ts) >= 19 {
		ts = ts[11:19] // HH:MM:SS from ISO timestamp
	}

	level := strings.ToUpper(e.Level)
	var levelStr string
	switch strings.ToLower(e.Level) {
	case "error", "fatal":
		levelStr = logErrorStyle.Render(fmt.Sprintf("%-5s", level))
	case "warn", "warning":
		levelStr = logWarnStyle.Render(fmt.Sprintf("%-5s", level))
	case "debug", "trace":
		levelStr = logDebugStyle.Render(fmt.Sprintf("%-5s", level))
	default:
		levelStr = logInfoStyle.Render(fmt.Sprintf("%-5s", level))
	}

	tsStr := textMuted.Render(ts)
	msgStr := textNormal.Render(e.Message)

	return fmt.Sprintf("  %s  %s  %s", tsStr, levelStr, msgStr)
}

func (m LogsModel) View() string {
	if m.loading {
		return textMuted.Render("\n  loading…")
	}
	if m.err != "" {
		return textError.Render("\n  ✕ "+m.err+"\n\n") +
			textMuted.Render("  r to retry")
	}

	hint := textMuted.Render(fmt.Sprintf("  %d entries  ↑↓ scroll  G tail  r refresh",
		len(m.entries)))

	return m.viewport.View() + "\n" + hint
}
