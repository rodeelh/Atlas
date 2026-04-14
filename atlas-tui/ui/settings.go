package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rodeelh/atlas-tui/client"
)

// ── Message types ─────────────────────────────────────────────────────────────

type settingsLoadedMsg struct {
	cfg *client.RuntimeConfig
	err error
}

type settingsSavedMsg struct {
	err error
}

// ── Field descriptor ──────────────────────────────────────────────────────────

type fieldKind int

const (
	fieldString fieldKind = iota
	fieldInt
	fieldBool
	fieldReadonly
)

type settingsField struct {
	key     string // JSON key for PUT /config
	label   string // display label
	value   string // current string representation
	kind    fieldKind
	editing bool
}

// ── Model ─────────────────────────────────────────────────────────────────────

// SettingsModel renders the config editor.
type SettingsModel struct {
	client  *client.Client
	width   int
	height  int
	fields  []settingsField
	cursor  int
	viewport viewport.Model
	input   textinput.Model
	loading bool
	saving  bool
	saveMsg string
	saveErr bool
	err     string
	rawCfg  *client.RuntimeConfig
}

func NewSettingsModel(c *client.Client) SettingsModel {
	ti := textinput.New()
	ti.CharLimit = 256
	vp := viewport.New(80, 20)
	vp.SetContent("")
	return SettingsModel{
		client:   c,
		viewport: vp,
		input:    ti,
		loading:  true,
	}
}

func (m SettingsModel) Init() tea.Cmd {
	return fetchSettings(m.client)
}

// OnFocus is called when the user switches to this tab.
func (m SettingsModel) OnFocus() tea.Cmd {
	return fetchSettings(m.client)
}

func fetchSettings(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		cfg, err := c.GetConfig()
		return settingsLoadedMsg{cfg: cfg, err: err}
	}
}

func configToFields(cfg *client.RuntimeConfig) []settingsField {
	return []settingsField{
		{key: "activeAIProvider", label: "provider", value: cfg.ActiveAIProvider, kind: fieldString},
		{key: "defaultOpenAIModel", label: "model", value: cfg.DefaultOpenAIModel, kind: fieldString},
		{key: "personaName", label: "persona name", value: cfg.PersonaName, kind: fieldString},
		{key: "userName", label: "user name", value: cfg.UserName, kind: fieldString},
		{key: "maxAgentIterations", label: "max iterations", value: fmt.Sprintf("%d", cfg.MaxAgentIterations), kind: fieldInt},
		{key: "maxRetrievedMemoriesPerTurn", label: "memories per turn", value: fmt.Sprintf("%d", cfg.MaxRetrievedMemoriesPerTurn), kind: fieldInt},
		{key: "memoryEnabled", label: "memory enabled", value: boolStr(cfg.MemoryEnabled), kind: fieldBool},
		{key: "actionSafetyMode", label: "safety mode", value: cfg.ActionSafetyMode, kind: fieldString},
		{key: "runtimePort", label: "port", value: fmt.Sprintf("%d", cfg.RuntimePort), kind: fieldReadonly},
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (m SettingsModel) Update(msg tea.Msg) (SettingsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case settingsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.err = ""
			m.rawCfg = msg.cfg
			m.fields = configToFields(msg.cfg)
		}
		m.syncViewport()
		return m, nil

	case settingsSavedMsg:
		m.saving = false
		if msg.err != nil {
			m.saveMsg = "✕ " + msg.err.Error()
			m.saveErr = true
		} else {
			m.saveMsg = "✓ saved"
			m.saveErr = false
		}
		m.syncViewport()
		return m, nil

	case tea.KeyMsg:
		// If currently editing a field.
		if m.isEditing() {
			switch msg.String() {
			case "enter":
				return m.commitEdit()
			case "esc":
				m.cancelEdit()
				m.syncViewport()
				return m, nil
			default:
				var inputCmd tea.Cmd
				m.input, inputCmd = m.input.Update(msg)
				m.syncViewport()
				return m, inputCmd
			}
		}

		// Navigation when not editing.
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.fields)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.startEdit()
		case "r":
			m.loading = true
			return m, fetchSettings(m.client)
		}
		m.syncViewport()
	}
	return m, nil
}

func (m SettingsModel) isEditing() bool {
	if m.cursor < 0 || m.cursor >= len(m.fields) {
		return false
	}
	return m.fields[m.cursor].editing
}

func (m *SettingsModel) startEdit() {
	if m.cursor < 0 || m.cursor >= len(m.fields) {
		return
	}
	f := &m.fields[m.cursor]
	if f.kind == fieldReadonly {
		return
	}
	f.editing = true
	m.input.SetValue(f.value)
	m.input.Focus()
	m.input.CursorEnd()
	m.saveMsg = ""
}

func (m *SettingsModel) cancelEdit() {
	for i := range m.fields {
		m.fields[i].editing = false
	}
	m.input.Blur()
}

func (m SettingsModel) commitEdit() (SettingsModel, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.fields) {
		return m, nil
	}
	f := &m.fields[m.cursor]
	newVal := strings.TrimSpace(m.input.Value())

	// Validate by type.
	if f.kind == fieldInt {
		if _, err := strconv.Atoi(newVal); err != nil {
			m.saveMsg = "✕ must be a number"
			m.saveErr = true
			m.cancelEdit()
			return m, nil
		}
	}
	if f.kind == fieldBool {
		if newVal != "true" && newVal != "false" {
			m.saveMsg = "✕ must be true or false"
			m.saveErr = true
			m.cancelEdit()
			return m, nil
		}
	}

	f.value = newVal
	f.editing = false
	m.input.Blur()
	m.saving = true
	m.saveMsg = ""

	return m, saveSettings(m.client, m.fields)
}

func saveSettings(c *client.Client, fields []settingsField) tea.Cmd {
	return func() tea.Msg {
		patch := make(map[string]any, len(fields))
		for _, f := range fields {
			if f.kind == fieldReadonly {
				continue
			}
			switch f.kind {
			case fieldInt:
				n, _ := strconv.Atoi(f.value)
				patch[f.key] = n
			case fieldBool:
				patch[f.key] = f.value == "true"
			default:
				patch[f.key] = f.value
			}
		}
		err := c.UpdateConfig(patch)
		return settingsSavedMsg{err: err}
	}
}

func (m *SettingsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.viewport.Height = max(1, h)
	m.input.Width = max(1, w-8)
	m.syncViewport()
}

func (m SettingsModel) View() string {
	if m.loading {
		return textMuted.Render("\n  loading…")
	}
	if m.err != "" {
		return textError.Render("\n  ✕ "+m.err+"\n\n") +
			textMuted.Render("  r to retry")
	}
	return m.viewport.View()
}

func (m SettingsModel) renderFields() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(sectionTitleStyle.Render("  settings"))
	sb.WriteString("\n\n")

	for i, f := range m.fields {
		cursor := "   "
		if i == m.cursor {
			cursor = textPrimary.Render(" › ")
		}

		label := labelStyle.Render(f.label)
		var value string

		if f.editing {
			value = inputPromptStyle.Render("[") + m.input.View() + inputPromptStyle.Render("]")
		} else {
			switch f.kind {
			case fieldReadonly:
				value = textMuted.Render(f.value)
			case fieldBool:
				if f.value == "true" {
					value = textSuccess.Render(f.value)
				} else {
					value = textMuted.Render(f.value)
				}
			default:
				value = valueStyle.Render(f.value)
			}
		}

		sb.WriteString(cursor + label + value + "\n")
	}

	sb.WriteString("\n")
	if m.saveMsg != "" {
		if m.saveErr {
			sb.WriteString(textError.Render("  " + m.saveMsg))
		} else {
			sb.WriteString(textSuccess.Render("  " + m.saveMsg))
		}
		sb.WriteString("\n\n")
	}
	if m.saving {
		sb.WriteString(textMuted.Render("  saving…\n\n"))
	}
	sb.WriteString(textMuted.Render("  ↑↓ navigate  enter edit  esc cancel  r reload"))

	return sb.String()
}

func (m *SettingsModel) syncViewport() {
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 {
		return
	}
	m.viewport.SetContent(m.renderFields())
	m.ensureCursorVisible()
}

func (m *SettingsModel) ensureCursorVisible() {
	line := 3 + m.cursor
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
