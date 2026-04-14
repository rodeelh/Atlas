package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rodeelh/atlas-tui/client"
)

// ── Message types ─────────────────────────────────────────────────────────────

type statusLoadedMsg struct {
	status    *client.RuntimeStatus
	config    *client.RuntimeConfig
	approvals []client.Approval
	err       error
}

type approvalActionMsg struct {
	toolCallID string
	approved   bool
	err        error
}

type statusTickMsg struct{}

func statusTickCmd() tea.Cmd {
	return tea.Tick(8*time.Second, func(time.Time) tea.Msg {
		return statusTickMsg{}
	})
}

// ── Model ─────────────────────────────────────────────────────────────────────

// StatusModel renders the runtime status view.
type StatusModel struct {
	client  *client.Client
	width   int
	height  int
	status  *client.RuntimeStatus
	config  *client.RuntimeConfig
	viewport viewport.Model

	// approvals
	approvals      []client.Approval
	approvalCursor int   // index within pending slice
	actionMsg      string
	actionErr      bool

	loading bool
	err     string
}

func NewStatusModel(c *client.Client) StatusModel {
	vp := viewport.New(80, 20)
	vp.SetContent("")
	return StatusModel{client: c, viewport: vp, loading: true}
}

func (m StatusModel) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), statusTickCmd())
}

// OnFocus is called when the user switches to this tab.
func (m StatusModel) OnFocus() tea.Cmd {
	return fetchStatus(m.client)
}

func fetchStatus(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		s, err := c.GetStatus()
		if err != nil {
			return statusLoadedMsg{err: err}
		}
		cfg, _ := c.GetConfig()          // best-effort
		approvals, _ := c.GetApprovals() // best-effort
		return statusLoadedMsg{status: s, config: cfg, approvals: approvals}
	}
}

func approveCmd(c *client.Client, toolCallID string, approved bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if approved {
			err = c.ApproveToolCall(toolCallID)
		} else {
			err = c.DenyToolCall(toolCallID)
		}
		return approvalActionMsg{toolCallID: toolCallID, approved: approved, err: err}
	}
}

func (m StatusModel) pending() []client.Approval {
	var out []client.Approval
	for _, a := range m.approvals {
		if a.Status == "pending" {
			out = append(out, a)
		}
	}
	return out
}

func (m StatusModel) Update(msg tea.Msg) (StatusModel, tea.Cmd) {
	switch msg := msg.(type) {
	case statusLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.err = ""
			m.status = msg.status
			m.config = msg.config
			m.approvals = msg.approvals
			// clamp cursor
			if p := m.pending(); m.approvalCursor >= len(p) {
				m.approvalCursor = max(0, len(p)-1)
			}
		}
		m.syncViewport()
		return m, nil

	case approvalActionMsg:
		if msg.err != nil {
			m.actionMsg = "✕ " + msg.err.Error()
			m.actionErr = true
		} else {
			verb := "approved"
			if !msg.approved {
				verb = "denied"
			}
			m.actionMsg = "✓ " + verb
			m.actionErr = false
		}
		m.syncViewport()
		// Refresh to pick up the updated approval status.
		return m, fetchStatus(m.client)

	case statusTickMsg:
		return m, tea.Batch(fetchStatus(m.client), statusTickCmd())

	case tea.KeyMsg:
		pending := m.pending()

		switch msg.String() {
		case "r":
			m.loading = true
			m.actionMsg = ""
			return m, fetchStatus(m.client)

		case "up", "k":
			if m.approvalCursor > 0 {
				m.approvalCursor--
			}
			m.ensureApprovalVisible()

		case "down", "j":
			if m.approvalCursor < len(pending)-1 {
				m.approvalCursor++
			}
			m.ensureApprovalVisible()

		case "a":
			if len(pending) > 0 && m.approvalCursor < len(pending) {
				id := pending[m.approvalCursor].ToolCall.ID
				m.actionMsg = "approving…"
				m.actionErr = false
				return m, approveCmd(m.client, id, true)
			}

		case "d":
			if len(pending) > 0 && m.approvalCursor < len(pending) {
				id := pending[m.approvalCursor].ToolCall.ID
				m.actionMsg = "denying…"
				m.actionErr = false
				return m, approveCmd(m.client, id, false)
			}
		}
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	return m, vpCmd
}

func (m *StatusModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.viewport.Height = max(1, h)
	m.syncViewport()
}

func (m StatusModel) View() string {
	if m.loading {
		return textMuted.Render("\n  loading…")
	}
	if m.err != "" {
		return textError.Render("\n  ✕ "+m.err+"\n\n") +
			textMuted.Render("  press r to retry")
	}
	if m.status == nil {
		return textMuted.Render("\n  no data")
	}
	return m.viewport.View()
}

func (m StatusModel) renderStatus() string {
	var sb strings.Builder

	// ── Runtime ───────────────────────────────────────────────────────────────
	sb.WriteString("\n")
	sb.WriteString(sectionTitleStyle.Render("  runtime"))
	sb.WriteString("\n\n")

	stateColor := colorSuccess
	if m.status.State != "ready" {
		stateColor = colorWarning
	}
	if !m.status.IsRunning {
		stateColor = colorError
	}
	stateStr := lipgloss.NewStyle().Foreground(lipgloss.Color(stateColor)).Bold(true).Render(m.status.State)

	sb.WriteString(m.row("  state", stateStr))
	sb.WriteString(m.row("  port", fmt.Sprintf("%d", m.status.RuntimePort)))
	sb.WriteString(m.row("  active requests", fmt.Sprintf("%d", m.status.ActiveRequests)))

	if m.status.StartedAt != nil && *m.status.StartedAt != "" {
		sb.WriteString(m.row("  started at", *m.status.StartedAt))
	}
	if m.status.Details != "" {
		sb.WriteString(m.row("  details", m.status.Details))
	}

	// ── Token usage ───────────────────────────────────────────────────────────
	if m.status.TokensIn > 0 || m.status.TokensOut > 0 {
		sb.WriteString("\n")
		sb.WriteString(sectionTitleStyle.Render("  usage"))
		sb.WriteString("\n\n")
		sb.WriteString(m.row("  tokens in", formatTokens(m.status.TokensIn)))
		sb.WriteString(m.row("  tokens out", formatTokens(m.status.TokensOut)))
	}

	// ── Configuration ─────────────────────────────────────────────────────────
	if m.config != nil {
		sb.WriteString("\n")
		sb.WriteString(sectionTitleStyle.Render("  configuration"))
		sb.WriteString("\n\n")
		sb.WriteString(m.row("  provider", m.config.ActiveAIProvider))
		sb.WriteString(m.row("  model", m.config.DefaultOpenAIModel))
		sb.WriteString(m.row("  persona", m.config.PersonaName))
		sb.WriteString(m.row("  safety mode", m.config.ActionSafetyMode))
		memStatus := "off"
		if m.config.MemoryEnabled {
			memStatus = fmt.Sprintf("on  (%d/turn)", m.config.MaxRetrievedMemoriesPerTurn)
		}
		sb.WriteString(m.row("  memory", memStatus))
		sb.WriteString(m.row("  max iterations", fmt.Sprintf("%d", m.config.MaxAgentIterations)))
	}

	// ── Approvals ─────────────────────────────────────────────────────────────
	sb.WriteString("\n")
	sb.WriteString(m.renderApprovals())

	// ── Hint ──────────────────────────────────────────────────────────────────
	sb.WriteString("\n")
	sb.WriteString(textMuted.Render("  r refresh"))

	return sb.String()
}

func (m *StatusModel) syncViewport() {
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 || m.status == nil {
		return
	}
	m.viewport.SetContent(m.renderStatus())
}

func (m StatusModel) renderApprovals() string {
	pending := m.pending()

	// Section header with count badge.
	title := "  approvals"
	if n := len(pending); n > 0 {
		badge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorBg)).
			Background(lipgloss.Color(colorWarning)).
			Padding(0, 1).
			Bold(true).
			Render(fmt.Sprintf("%d pending", n))
		title += "  " + badge
	}

	var sb strings.Builder
	sb.WriteString(sectionTitleStyle.Render(title))
	sb.WriteString("\n\n")

	if len(pending) == 0 {
		sb.WriteString(textMuted.Render("  no pending approvals"))
		sb.WriteString("\n")
		return sb.String()
	}

	for i, a := range pending {
		cursor := "   "
		if i == m.approvalCursor {
			cursor = textWarning.Render(" › ")
		}

		toolName := a.ToolCall.ToolName
		level := a.ToolCall.PermissionLevel
		levelStyle := textMuted
		switch level {
		case "execute", "destructive_local":
			levelStyle = textError
		case "draft", "external_side_effect":
			levelStyle = textWarning
		}

		ts := ""
		if a.ToolCall.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339, a.ToolCall.Timestamp); err == nil {
				ts = "  " + textMuted.Render(formatAge(t))
			}
		}

		line := cursor +
			textBold.Render(toolName) +
			"  " + levelStyle.Render(level) +
			ts
		sb.WriteString(line + "\n")

		// Show truncated args for the selected item.
		if i == m.approvalCursor && a.ToolCall.ArgumentsJSON != "" {
			args := truncate(a.ToolCall.ArgumentsJSON, 80)
			sb.WriteString("       " + textMuted.Render(args) + "\n")
		}
	}

	// Action feedback / keybind hint.
	sb.WriteString("\n")
	if m.actionMsg != "" {
		style := textSuccess
		if m.actionErr {
			style = textError
		}
		sb.WriteString("  " + style.Render(m.actionMsg) + "\n")
	} else {
		sb.WriteString(textMuted.Render("  a approve  d deny  ↑↓ navigate") + "\n")
	}

	return sb.String()
}

func (m *StatusModel) ensureApprovalVisible() {
	pending := m.pending()
	if len(pending) == 0 || m.approvalCursor >= len(pending) {
		return
	}

	line := 16
	if m.status != nil && (m.status.TokensIn > 0 || m.status.TokensOut > 0) {
		line += 5
	}
	if m.config != nil {
		line += 9
	}
	line += m.approvalCursor
	if pending[m.approvalCursor].ToolCall.ArgumentsJSON != "" {
		line++
	}
	if line < m.viewport.YOffset {
		m.viewport.YOffset = line
	}
	if line >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.YOffset = line - m.viewport.Height + 1
	}
	if m.viewport.YOffset < 0 {
		m.viewport.YOffset = 0
	}
}

func (m StatusModel) row(label, value string) string {
	l := labelStyle.Render(label)
	v := valueStyle.Render(value)
	return l + v + "\n"
}

func formatTokens(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

func truncate(s string, max int) string {
	// Strip newlines for inline display.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
