// Package client provides a typed HTTP client for the Atlas runtime API.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client speaks to the Atlas runtime over HTTP.
type Client struct {
	baseURL  string
	http     *http.Client // 15s timeout — for status, config, logs, ping
	chatHTTP *http.Client // no timeout — for POST /message which blocks while agent runs
}

// New returns a Client targeting baseURL (e.g. "http://localhost:1984").
func New(baseURL string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		http:     &http.Client{Timeout: 15 * time.Second},
		chatHTTP: &http.Client{}, // no timeout
	}
}

// BaseURL returns the runtime base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// ── Response types ────────────────────────────────────────────────────────────

// RuntimeStatus mirrors the GET /status JSON shape.
type RuntimeStatus struct {
	IsRunning            bool    `json:"isRunning"`
	State                string  `json:"state"`
	RuntimePort          int     `json:"runtimePort"`
	StartedAt            *string `json:"startedAt,omitempty"`
	ActiveRequests       int32   `json:"activeRequests"`
	PendingApprovalCount int     `json:"pendingApprovalCount"`
	Details              string  `json:"details"`
	TokensIn             int64   `json:"tokensIn"`
	TokensOut            int64   `json:"tokensOut"`
}

// LogEntry is one line from GET /logs.
type LogEntry struct {
	Level     string            `json:"level"`
	Message   string            `json:"message"`
	Timestamp string            `json:"timestamp"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// RuntimeConfig are the editable fields from GET /config.
type RuntimeConfig struct {
	DefaultOpenAIModel          string  `json:"defaultOpenAIModel"`
	ActiveAIProvider            string  `json:"activeAIProvider"`
	MaxAgentIterations          int     `json:"maxAgentIterations"`
	MaxRetrievedMemoriesPerTurn int     `json:"maxRetrievedMemoriesPerTurn"`
	ActionSafetyMode            string  `json:"actionSafetyMode"`
	PersonaName                 string  `json:"personaName"`
	UserName                    string  `json:"userName"`
	RuntimePort                 int     `json:"runtimePort"`
	MemoryEnabled               bool    `json:"memoryEnabled"`
	MemoryAutoSaveThreshold     float64 `json:"memoryAutoSaveThreshold"`
}

// ApprovalToolCall is the tool call embedded in an Approval.
type ApprovalToolCall struct {
	ID              string `json:"id"`
	ToolName        string `json:"toolName"`
	ArgumentsJSON   string `json:"argumentsJSON"`
	PermissionLevel string `json:"permissionLevel"`
	Status          string `json:"status,omitempty"`
	Timestamp       string `json:"timestamp,omitempty"`
}

// Approval is one record from GET /approvals.
type Approval struct {
	ID             string           `json:"id"`
	Status         string           `json:"status"`
	Source         string           `json:"source,omitempty"`
	ConversationID *string          `json:"conversationID,omitempty"`
	ToolCall       ApprovalToolCall `json:"toolCall"`
}

// MessageRequest is the body for POST /message.
type MessageRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversationId,omitempty"`
}

// MessageResponse is returned by POST /message.
type MessageResponse struct {
	Conversation struct {
		ID       string        `json:"id"`
		Messages []MessageItem `json:"messages"`
	} `json:"conversation"`
	Response struct {
		AssistantMessage string `json:"assistantMessage,omitempty"`
		Status           string `json:"status"`
		ErrorMessage     string `json:"errorMessage,omitempty"`
	} `json:"response"`
}

// MessageItem is a single message in a conversation.
type MessageItem struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

// Conversation is a conversation summary from GET /conversations.
type Conversation struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SSEEvent is one parsed event from the message/stream endpoint.
type SSEEvent struct {
	Type           string `json:"type"`
	Content        string `json:"content"`
	Role           string `json:"role"`
	ConversationID string `json:"conversationID"`
	Error          string `json:"error"`
	Status         string `json:"status"`
	ToolName       string `json:"toolName"`
	ApprovalID     string `json:"approvalID"`
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *Client) doJSON(method, path string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e) //nolint:errcheck
		if e.Error != "" {
			return fmt.Errorf("API %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("API error %d", resp.StatusCode)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ── API methods ───────────────────────────────────────────────────────────────

// Ping returns true if the runtime is reachable.
func (c *Client) Ping() bool {
	req, err := http.NewRequest("GET", c.baseURL+"/auth/ping", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// GetStatus fetches runtime status.
func (c *Client) GetStatus() (*RuntimeStatus, error) {
	var s RuntimeStatus
	return &s, c.doJSON("GET", "/status", nil, &s)
}

// GetLogs fetches the most recent log entries.
func (c *Client) GetLogs(limit int) ([]LogEntry, error) {
	var entries []LogEntry
	return entries, c.doJSON("GET", fmt.Sprintf("/logs?limit=%d", limit), nil, &entries)
}

// GetConfig fetches the current runtime config.
func (c *Client) GetConfig() (*RuntimeConfig, error) {
	var cfg RuntimeConfig
	return &cfg, c.doJSON("GET", "/config", nil, &cfg)
}

// UpdateConfig patches the runtime config with the given fields.
func (c *Client) UpdateConfig(patch map[string]any) error {
	return c.doJSON("PUT", "/config", patch, nil)
}

// SendMessage posts a message and returns the full response (blocking).
// Uses chatHTTP (no timeout) because agent turns can take minutes.
func (c *Client) SendMessage(req MessageRequest) (*MessageResponse, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", c.baseURL+"/message", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := c.chatHTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(httpResp.Body).Decode(&e) //nolint:errcheck
		if e.Error != "" {
			return nil, fmt.Errorf("API %d: %s", httpResp.StatusCode, e.Error)
		}
		return nil, fmt.Errorf("API error %d", httpResp.StatusCode)
	}
	var resp MessageResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetConversations returns a list of recent conversations.
func (c *Client) GetConversations() ([]Conversation, error) {
	var convs []Conversation
	return convs, c.doJSON("GET", "/conversations", nil, &convs)
}

// GetConversation returns a full conversation by ID.
func (c *Client) GetConversation(id string) (*MessageResponse, error) {
	var resp MessageResponse
	return &resp, c.doJSON("GET", "/conversations/"+id, nil, &resp)
}

// ── SSE streaming ─────────────────────────────────────────────────────────────

// SSEStream is an open server-sent-events connection to /message/stream.
type SSEStream struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
	cancel  context.CancelFunc
}

// Next returns the next SSEEvent from the stream (blocks until one arrives).
// ok is false when the stream ends or errors.
func (s *SSEStream) Next() (*SSEEvent, bool) {
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var event SSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		return &event, true
	}
	return nil, false
}

// Close cancels the request and closes the stream body.
func (s *SSEStream) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.body != nil {
		s.body.Close()
	}
}

// OpenSSEStream opens a streaming connection for a conversationID.
func (c *Client) OpenSSEStream(conversationID string) (*SSEStream, error) {
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET",
		c.baseURL+"/message/stream?conversationID="+conversationID, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	// No timeout for SSE streams.
	sseHTTP := &http.Client{}
	resp, err := sseHTTP.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		return nil, fmt.Errorf("SSE stream: status %d", resp.StatusCode)
	}
	return &SSEStream{
		scanner: bufio.NewScanner(resp.Body),
		body:    resp.Body,
		cancel:  cancel,
	}, nil
}

// ── Approvals ─────────────────────────────────────────────────────────────────

// GetApprovals returns all approval records (pending and resolved).
func (c *Client) GetApprovals() ([]Approval, error) {
	var approvals []Approval
	return approvals, c.doJSON("GET", "/approvals", nil, &approvals)
}

// ApproveToolCall approves the pending approval identified by toolCallID.
func (c *Client) ApproveToolCall(toolCallID string) error {
	return c.doJSON("POST", "/approvals/"+toolCallID+"/approve", nil, nil)
}

// DenyToolCall denies the pending approval identified by toolCallID.
func (c *Client) DenyToolCall(toolCallID string) error {
	return c.doJSON("POST", "/approvals/"+toolCallID+"/deny", nil, nil)
}
