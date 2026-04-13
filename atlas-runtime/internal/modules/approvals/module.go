package approvals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

const resolvedEventName = "approval.resolved.v1"

type resolvedEvent struct {
	ToolCallID string `json:"toolCallID"`
	Status     string `json:"status"`
}

type approvalToolCall struct {
	ID               string `json:"id"`
	ToolName         string `json:"toolName"`
	ArgumentsJSON    string `json:"argumentsJSON"`
	PermissionLevel  string `json:"permissionLevel"`
	RequiresApproval bool   `json:"requiresApproval"`
	Status           string `json:"status,omitempty"`
	Timestamp        string `json:"timestamp,omitempty"`
}

type approvalJSON struct {
	ID                      string           `json:"id"`
	Status                  string           `json:"status"`
	Source                  string           `json:"source,omitempty"` // "agent" (default, omitted) or "thought"
	AgentID                 *string          `json:"agentID,omitempty"`
	ConversationID          *string          `json:"conversationID,omitempty"`
	CreatedAt               string           `json:"createdAt"`
	ResolvedAt              *string          `json:"resolvedAt,omitempty"`
	DeferredExecutionID     *string          `json:"deferredExecutionID,omitempty"`
	DeferredExecutionStatus *string          `json:"deferredExecutionStatus,omitempty"`
	LastError               *string          `json:"lastError,omitempty"`
	PreviewDiff             *string          `json:"previewDiff,omitempty"`
	ToolCall                approvalToolCall `json:"toolCall"`
}

type Module struct {
	supportDir string

	mu         sync.Mutex
	thoughtMu  sync.Mutex // guards the thought-sourced resolver callback
	store      platform.ApprovalStore
	memories   platform.MemoryStore
	agent      platform.AgentRuntime
	bus        platform.EventBus
	policyPath string
}

func New(supportDir string) *Module {
	return &Module{
		supportDir: supportDir,
		policyPath: filepath.Join(supportDir, "action-policies.json"),
	}
}

func (m *Module) ID() string { return "approvals" }

func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{
		Version:   "v1",
		Publishes: []string{resolvedEventName},
	}
}

func (m *Module) Register(host platform.Host) error {
	m.store = host.Storage().Approvals()
	m.memories = host.Storage().Memories()
	m.agent = host.AgentRuntime()
	m.bus = host.Bus()
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) Resolve(toolCallID string, approved bool) error {
	status := "denied"
	if approved {
		status = "approved"
	}
	_, err := m.resolve(toolCallID, status)
	return err
}

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/approvals", m.listApprovals)
	r.Post("/approvals/{id}/approve", m.approveToolCall)
	r.Post("/approvals/{id}/deny", m.denyToolCall)
	r.Get("/action-policies", m.getActionPolicies)
	r.Get("/action-policies/{id}", m.getActionPolicy)
	r.Put("/action-policies/{id}", m.setActionPolicy)
	r.Post("/action-policies/{id}", m.setActionPolicy)
}

func (m *Module) listApprovals(w http.ResponseWriter, _ *http.Request) {
	rows, err := m.store.ListAllApprovals(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read approvals: "+err.Error())
		return
	}
	out := make([]approvalJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, rowToApproval(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) approveToolCall(w http.ResponseWriter, r *http.Request) {
	m.resolveHTTP(w, r, "approved")
}

func (m *Module) denyToolCall(w http.ResponseWriter, r *http.Request) {
	m.resolveHTTP(w, r, "denied")
}

func (m *Module) resolveHTTP(w http.ResponseWriter, r *http.Request, status string) {
	toolCallID := chi.URLParam(r, "id")
	resp, err := m.resolve(toolCallID, status)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "not found"):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (m *Module) resolve(toolCallID, newStatus string) (approvalJSON, error) {
	row, err := m.store.FetchDeferredByToolCallID(toolCallID)
	if err != nil {
		return approvalJSON{}, fmt.Errorf("database error: %w", err)
	}
	if row == nil {
		return approvalJSON{}, fmt.Errorf("approval not found for toolCallID: %s", toolCallID)
	}

	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.store.UpdateDeferredStatus(toolCallID, newStatus, updatedAt); err != nil {
		return approvalJSON{}, fmt.Errorf("failed to update approval: %w", err)
	}

	row.Status = newStatus
	row.UpdatedAt = updatedAt

	toolName := row.Summary
	if row.ActionID != nil && *row.ActionID != "" {
		toolName = *row.ActionID
	}

	logMsg := "Approval approved: " + toolName
	if newStatus == "denied" {
		logMsg = "Approval denied: " + toolName
	}
	logstore.Write("info", logMsg, map[string]string{"toolCallID": toolCallID})

	if newStatus == "denied" {
		go writeRejectionMemory(m.memories, toolName, extractToolArguments(row.NormalizedInputJSON, toolCallID))
	}

	if m.bus != nil {
		_ = m.bus.Publish(context.Background(), resolvedEventName, resolvedEvent{ToolCallID: toolCallID, Status: newStatus})
	}
	// Thought-sourced approvals don't have a paused agent loop to resume.
	// When approved, we run the wired ThoughtResolver which executes the
	// skill and enqueues the result to the greeting queue. Denied is a
	// no-op — the thought stays in MIND.md and the next nap decides.
	if row.SourceType == "thought" {
		if newStatus == "approved" {
			go m.resolveThoughtSourced(row)
		}
	} else if m.agent != nil {
		go m.agent.Resume(toolCallID, newStatus == "approved")
	}

	return rowToApproval(*row), nil
}

func (m *Module) loadPolicies() (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.policyPath)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var policies map[string]string
	if err := json.Unmarshal(data, &policies); err != nil {
		return map[string]string{}, nil
	}
	return policies, nil
}

func (m *Module) savePolicies(policies map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := json.Marshal(policies)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(m.policyPath), "action-policies-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, m.policyPath)
}

func (m *Module) getActionPolicies(w http.ResponseWriter, _ *http.Request) {
	policies, err := m.loadPolicies()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read policies: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policies)
}

func (m *Module) getActionPolicy(w http.ResponseWriter, r *http.Request) {
	actionID := chi.URLParam(r, "id")
	policies, err := m.loadPolicies()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read policies: "+err.Error())
		return
	}
	policy, ok := policies[actionID]
	if !ok {
		writeError(w, http.StatusNotFound, "no policy for action: "+actionID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"policy": policy})
}

func (m *Module) setActionPolicy(w http.ResponseWriter, r *http.Request) {
	actionID := chi.URLParam(r, "id")
	var body struct {
		Policy string `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Policy == "" {
		writeError(w, http.StatusBadRequest, "body must be {\"policy\": \"<value>\"}")
		return
	}
	policies, err := m.loadPolicies()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read policies: "+err.Error())
		return
	}
	policies[actionID] = body.Policy
	if err := m.savePolicies(policies); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save policies: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policies)
}

func rowToApproval(r storage.DeferredExecRow) approvalJSON {
	toolName := r.Summary
	if r.ActionID != nil && *r.ActionID != "" {
		toolName = *r.ActionID
	} else if r.SkillID != nil && *r.SkillID != "" {
		toolName = *r.SkillID
	}

	deferredStatus := r.Status
	approvalStatus := deferredStatusToApprovalStatus(r.Status)

	// SourceType "thought" surfaces to the UI as a "from a thought" badge.
	// The agent-initiated default ("agent") is omitted from the JSON.
	source := ""
	if r.SourceType == "thought" {
		source = "thought"
	}

	var resolvedAt *string
	if approvalStatus != "pending" && r.UpdatedAt != "" {
		resolvedAt = &r.UpdatedAt
	}

	return approvalJSON{
		ID:                      r.ApprovalID,
		Status:                  approvalStatus,
		Source:                  source,
		AgentID:                 r.AgentID,
		ConversationID:          r.ConversationID,
		CreatedAt:               r.CreatedAt,
		ResolvedAt:              resolvedAt,
		DeferredExecutionID:     &r.DeferredID,
		DeferredExecutionStatus: &deferredStatus,
		LastError:               r.LastError,
		PreviewDiff:             r.PreviewDiff,
		ToolCall: approvalToolCall{
			ID:               r.ToolCallID,
			ToolName:         toolName,
			ArgumentsJSON:    extractToolArguments(r.NormalizedInputJSON, r.ToolCallID),
			PermissionLevel:  r.PermissionLevel,
			RequiresApproval: true,
			Status:           approvalStatus,
			Timestamp:        r.CreatedAt,
		},
	}
}

func deferredStatusToApprovalStatus(s string) string {
	switch s {
	case "pending_approval":
		return "pending"
	case "approved", "running", "completed":
		return "approved"
	case "denied":
		return "denied"
	default:
		return "pending"
	}
}

func extractToolArguments(normalizedInputJSON, toolCallID string) string {
	var state struct {
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(normalizedInputJSON), &state); err != nil {
		return "{}"
	}
	for _, tc := range state.ToolCalls {
		if tc.ID == toolCallID {
			if tc.Function.Arguments != "" {
				return tc.Function.Arguments
			}
			return "{}"
		}
	}
	return "{}"
}

func writeRejectionMemory(store platform.MemoryStore, toolName, argsJSON string) {
	if store == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	title := "User rejected: " + toolName
	if len(title) > 48 {
		title = title[:48]
	}

	skillBase := toolName
	if idx := strings.Index(toolName, "."); idx > 0 {
		skillBase = toolName[:idx]
	}
	tagsBytes, _ := json.Marshal([]string{skillBase, toolName, "rejection"})
	content := fmt.Sprintf("User explicitly denied %s. Args: %s", toolName, truncateApprovalArgs(argsJSON, 150))

	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck

	row := storage.MemoryRow{
		ID:         hex.EncodeToString(b),
		Category:   "tool_learning",
		Title:      title,
		Content:    content,
		Source:     "approval_rejection",
		Confidence: 0.60,
		Importance: 0.75,
		CreatedAt:  now,
		UpdatedAt:  now,
		TagsJSON:   string(tagsBytes),
	}
	if err := store.SaveMemory(row); err != nil {
		logstore.Write("warn", "approvals: failed to write rejection memory: "+err.Error(), nil)
	}
}

func truncateApprovalArgs(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
