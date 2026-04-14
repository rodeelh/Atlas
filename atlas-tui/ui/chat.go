package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rodeelh/atlas-tui/client"
)

// ── Message types ─────────────────────────────────────────────────────────────

type chatMsg struct {
	role      string // "user" | "assistant" | "tool"
	content   string
	timestamp time.Time
	toolName  string
}

// chatHistoryLoadedMsg carries the initial conversation history.
type chatHistoryLoadedMsg struct {
	convID   string
	messages []chatMsg
	err      error
}

// chatFirstMsgResultMsg is returned after a first-turn POST (no SSE).
type chatFirstMsgResultMsg struct {
	convID   string
	content  string // last assistant message content
	err      error
}

// chatStreamOpenedMsg is returned once the SSE stream is open and POST is fired.
type chatStreamOpenedMsg struct {
	convID string
	stream *client.SSEStream
}

// chatSSENextMsg carries one SSE event from the stream.
type chatSSENextMsg struct {
	event *client.SSEEvent
	done  bool
	err   error
}

// ── SSE reading ───────────────────────────────────────────────────────────────

func readNextSSE(stream *client.SSEStream) tea.Cmd {
	return func() tea.Msg {
		event, ok := stream.Next()
		if !ok {
			return chatSSENextMsg{done: true}
		}
		done := event.Type == "error" ||
			event.Type == "cancelled" ||
			(event.Type == "done" && event.Status != "waitingForApproval")
		return chatSSENextMsg{event: event, done: done}
	}
}

// ── Model ─────────────────────────────────────────────────────────────────────

type ChatModel struct {
	client *client.Client
	width  int
	height int

	messages  []chatMsg
	convID    string
	streaming bool
	streamBuf string
	sseStream *client.SSEStream

	viewport viewport.Model
	input    textinput.Model
	err      string
}

func NewChatModel(c *client.Client) ChatModel {
	ti := textinput.New()
	ti.Placeholder = "message atlas…"
	ti.Prompt = ""  // remove the default "> " prompt — we render our own
	ti.CharLimit = 4096
	ti.Focus()

	vp := viewport.New(80, 20)
	vp.SetContent("")

	return ChatModel{
		client:   c,
		viewport: vp,
		input:    ti,
	}
}

func (m ChatModel) Init() tea.Cmd {
	return loadChatHistory(m.client)
}

func loadChatHistory(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		convs, err := c.GetConversations()
		if err != nil || len(convs) == 0 {
			return chatHistoryLoadedMsg{err: err}
		}
		latest := convs[0]
		resp, err := c.GetConversation(latest.ID)
		if err != nil {
			return chatHistoryLoadedMsg{convID: latest.ID, err: err}
		}
		msgs := make([]chatMsg, 0, len(resp.Conversation.Messages))
		for _, m := range resp.Conversation.Messages {
			ts, _ := time.Parse(time.RFC3339, m.Timestamp)
			msgs = append(msgs, chatMsg{
				role:      m.Role,
				content:   m.Content,
				timestamp: ts,
			})
		}
		return chatHistoryLoadedMsg{convID: latest.ID, messages: msgs}
	}
}

func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case chatHistoryLoadedMsg:
		if msg.err == nil {
			m.convID = msg.convID
			m.messages = msg.messages
			m.syncViewport()
		}
		return m, nil

	case chatFirstMsgResultMsg:
		// First turn: no SSE — display final response directly.
		m.streaming = false
		if msg.err != nil {
			m.err = msg.err.Error()
			// Remove the placeholder assistant message we added.
			if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" {
				m.messages = m.messages[:len(m.messages)-1]
			}
		} else {
			m.convID = msg.convID
			if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" {
				m.messages[len(m.messages)-1].content = msg.content
			}
		}
		m.syncViewport()
		return m, nil

	case chatStreamOpenedMsg:
		// SSE stream is open and POST is in-flight — start reading.
		m.convID = msg.convID
		m.sseStream = msg.stream
		m.streamBuf = ""
		// Ensure there's a placeholder assistant message.
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
			m.messages = append(m.messages, chatMsg{
				role:      "assistant",
				timestamp: time.Now(),
			})
		}
		m.syncViewport()
		return m, readNextSSE(msg.stream)

	case chatSSENextMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			m.streaming = false
			m.closeStream()
			return m, nil
		}
		if msg.event != nil {
			m.handleSSEEvent(msg.event)
		}
		if msg.done {
			m.streaming = false
			m.closeStream()
			return m, nil
		}
		return m, readNextSSE(m.sseStream)

	case tea.KeyMsg:
		if msg.String() == "enter" {
			if m.streaming {
				return m, nil
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.SetValue("")
			m.err = ""
			m.streaming = true
			m.messages = append(m.messages, chatMsg{
				role:      "user",
				content:   text,
				timestamp: time.Now(),
			})
			// Add placeholder assistant message immediately.
			m.messages = append(m.messages, chatMsg{
				role:      "assistant",
				timestamp: time.Now(),
			})
			m.syncViewport()
			return m, sendMessage(m.client, text, m.convID)
		}
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)
	return m, tea.Batch(cmds...)
}

func (m *ChatModel) handleSSEEvent(e *client.SSEEvent) {
	switch e.Type {
	case "assistant_started":
		m.streamBuf = ""
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
			m.messages = append(m.messages, chatMsg{
				role:      "assistant",
				timestamp: time.Now(),
			})
		}
		m.syncViewport()

	case "token", "assistant_delta":
		m.streamBuf += e.Content
		// Update the last assistant message in place.
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].role == "assistant" {
				m.messages[i].content = m.streamBuf
				break
			}
		}
		m.syncViewport()

	case "assistant_done":
		m.syncViewport()

	case "tool_call":
		name := e.ToolName
		if name == "" {
			name = "tool"
		}
		m.messages = append(m.messages, chatMsg{
			role:      "tool",
			content:   "calling…",
			timestamp: time.Now(),
			toolName:  name,
		})
		m.syncViewport()

	case "tool_result", "tool_finished":
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].role == "tool" {
				m.messages[i].content = "done"
				break
			}
		}
		// Reset streamBuf so the next assistant token starts fresh.
		m.streamBuf = ""
		// Ensure there's a fresh assistant placeholder for the continuation.
		last := m.messages[len(m.messages)-1]
		if last.role != "assistant" {
			m.messages = append(m.messages, chatMsg{
				role:      "assistant",
				timestamp: time.Now(),
			})
		}
		m.syncViewport()

	case "error":
		errMsg := e.Error
		if errMsg == "" {
			errMsg = "unknown error"
		}
		m.err = errMsg

	case "cancelled":
		m.err = "request cancelled"
	}
}

// sendMessage returns the right command depending on whether this is the
// first turn (no convID — blocking POST, no streaming) or a subsequent
// turn (open SSE first, then fire POST concurrently).
func sendMessage(c *client.Client, text, convID string) tea.Cmd {
	if convID == "" {
		// First turn: POST blocks (no convID to open SSE on).
		return func() tea.Msg {
			resp, err := c.SendMessage(client.MessageRequest{Message: text})
			if err != nil {
				return chatFirstMsgResultMsg{err: err}
			}
			return chatFirstMsgResultMsg{
				convID:  resp.Conversation.ID,
				content: lastAssistantContent(resp),
			}
		}
	}
	// Subsequent turns: register SSE listener first, then POST concurrently.
	// Using chatHTTP (no timeout) in the goroutine so the agent is never
	// cancelled mid-turn.
	return func() tea.Msg {
		stream, err := c.OpenSSEStream(convID)
		if err != nil {
			// SSE unavailable — fall back to blocking POST.
			resp, postErr := c.SendMessage(client.MessageRequest{
				Message:        text,
				ConversationID: convID,
			})
			if postErr != nil {
				return chatFirstMsgResultMsg{err: postErr}
			}
			return chatFirstMsgResultMsg{
				convID:  resp.Conversation.ID,
				content: lastAssistantContent(resp),
			}
		}
		// Fire POST on a no-timeout client so the agent runs to completion.
		go func() {
			c.SendMessage(client.MessageRequest{ //nolint:errcheck
				Message:        text,
				ConversationID: convID,
			})
		}()
		return chatStreamOpenedMsg{convID: convID, stream: stream}
	}
}

func (m *ChatModel) closeStream() {
	if m.sseStream != nil {
		m.sseStream.Close()
		m.sseStream = nil
	}
}

func (m *ChatModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.viewport.Height = viewportHeight(h)
	m.input.Width = w - 4
	m.syncViewport()
}

func viewportHeight(h int) int {
	vh := h - 3 // separator + input + padding
	if vh < 1 {
		return 1
	}
	return vh
}

func (m *ChatModel) syncViewport() {
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}

// ── Rendering ─────────────────────────────────────────────────────────────────

func (m ChatModel) renderMessages() string {
	if len(m.messages) == 0 {
		return textMuted.Render("\n  start a conversation with atlas…")
	}
	var sb strings.Builder
	for i, msg := range m.messages {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(m.renderMessage(msg))
		sb.WriteString("\n")
	}
	return sb.String()
}

func (m ChatModel) renderMessage(msg chatMsg) string {
	ts := msg.timestamp.Format("15:04")

	switch msg.role {
	case "user":
		header := userLabelStyle.Render("you") +
			"  " + textMuted.Render(ts)
		return header + "\n" + textNormal.Render(msg.content)

	case "tool":
		icon := textTool.Render("⚡")
		label := toolLabelStyle.Render(msg.toolName)
		status := textMuted.Render(msg.content)
		return fmt.Sprintf("  %s %s  %s", icon, label, status)

	default: // assistant
		header := assistantLabelStyle.Render("atlas") +
			"  " + textMuted.Render(ts)
		content := msg.content
		if content == "" && m.streaming {
			content = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colorPrimary)).
				Render("▍")
		}
		return header + "\n" + textNormal.Render(content)
	}
}

func (m ChatModel) View() string {
	if m.width == 0 {
		return ""
	}

	var top string
	if m.err != "" {
		errLine := textError.Render("  ✕ " + m.err)
		top = lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), errLine)
	} else {
		top = m.viewport.View()
	}

	sep := divider(m.width)
	prompt := m.renderPrompt()

	return lipgloss.JoinVertical(lipgloss.Left, top, sep, prompt)
}

func (m ChatModel) renderPrompt() string {
	var prefix string
	if m.streaming {
		prefix = textMuted.Render("  ")
	} else {
		prefix = inputPromptStyle.Render("❯ ")
	}
	return prefix + m.input.View()
}

// lastAssistantContent extracts the content of the last assistant message
// from a MessageResponse. Prefers Conversation.Messages over
// Response.AssistantMessage which may be empty in some code paths.
func lastAssistantContent(resp *client.MessageResponse) string {
	for i := len(resp.Conversation.Messages) - 1; i >= 0; i-- {
		if resp.Conversation.Messages[i].Role == "assistant" {
			return resp.Conversation.Messages[i].Content
		}
	}
	// Fallback to the top-level field.
	return resp.Response.AssistantMessage
}
