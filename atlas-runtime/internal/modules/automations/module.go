package automations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/comms"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
	"atlas-runtime-go/internal/workflowexec"
)

const (
	triggeredEventName = "automation.triggered.v1"
	completedEventName = "automation.completed.v1"
)

type Module struct {
	supportDir string
	store      platform.AutomationStore
	commsStore platform.CommunicationsStore
	workflows  platform.WorkflowStore
	agent      platform.AgentRuntime
	bus        platform.EventBus
	delivery   AutomationDelivery
	skills     *skills.Registry

	schedulerMu       sync.Mutex
	schedulerCancel   context.CancelFunc
	schedulerInterval time.Duration
	schedulerRuns     map[string]string
	now               func() time.Time
}

type AutomationDelivery interface {
	SendAutomationResult(ctx context.Context, dest comms.AutomationDestination, text string) error
}

type RunResult struct {
	RunID          string
	GremlinID      string
	ConversationID string
	AutomationName string
	Status         string
	Output         string
}

func New(supportDir string) *Module {
	return &Module{
		supportDir:        supportDir,
		schedulerInterval: time.Minute,
		schedulerRuns:     map[string]string{},
		now:               time.Now,
	}
}

func (m *Module) ID() string { return "automations" }

func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{
		Version:   "v1",
		Publishes: []string{triggeredEventName, completedEventName},
	}
}

func (m *Module) Register(host platform.Host) error {
	m.store = host.Storage().Automations()
	m.commsStore = host.Storage().Communications()
	m.workflows = host.Storage().Workflows()
	m.agent = host.AgentRuntime()
	m.bus = host.Bus()
	if err := m.importLegacyDefinitions(false); err != nil {
		return fmt.Errorf("import legacy automations: %w", err)
	}
	m.registerAgentActions()
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m.agent == nil || m.store == nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.schedulerMu.Lock()
	m.schedulerCancel = cancel
	m.schedulerMu.Unlock()
	go m.schedulerLoop(runCtx)
	return nil
}

func (m *Module) Stop(context.Context) error {
	m.schedulerMu.Lock()
	cancel := m.schedulerCancel
	m.schedulerCancel = nil
	m.schedulerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Module) SetDeliveryService(delivery AutomationDelivery) {
	m.delivery = delivery
}

func (m *Module) SetSkillRegistry(registry *skills.Registry) {
	m.skills = registry
}

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/automations", m.listAutomations)
	r.Get("/automations/summaries", m.listAutomationSummaries)
	r.Post("/automations", m.createAutomation)
	// Advanced import/export escape hatch for migrating or repairing legacy GREMLINS.md.
	r.Get("/automations/advanced/file", m.getAutomationsFile)
	r.Put("/automations/advanced/import", m.putAutomationsFile)
	// Backward-compatible legacy aliases. Normal clients should use structured automation routes.
	r.Get("/automations/file", m.getAutomationsFile)
	r.Put("/automations/file", m.putAutomationsFile)
	r.Get("/automations/{id}", m.getAutomation)
	r.Put("/automations/{id}", m.updateAutomation)
	r.Delete("/automations/{id}", m.deleteAutomation)
	r.Get("/automations/{id}/runs", m.getAutomationRuns)
	r.Post("/automations/{id}/enable", m.enableAutomation)
	r.Post("/automations/{id}/disable", m.disableAutomation)
	r.Post("/automations/{id}/run", m.runAutomation)
}

func (m *Module) listAutomations(w http.ResponseWriter, _ *http.Request) {
	items, err := m.listDefinitions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list automations: "+err.Error())
		return
	}
	if items == nil {
		items = []features.GremlinItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

type AutomationSummary struct {
	ID               string                             `json:"id"`
	Name             string                             `json:"name"`
	Emoji            string                             `json:"emoji"`
	Prompt           string                             `json:"prompt"`
	ScheduleRaw      string                             `json:"scheduleRaw"`
	IsEnabled        bool                               `json:"isEnabled"`
	SourceType       string                             `json:"sourceType"`
	CreatedAt        string                             `json:"createdAt"`
	Destination      *features.CommunicationDestination `json:"communicationDestination,omitempty"`
	LastRunAt        *string                            `json:"lastRunAt,omitempty"`
	LastRunStatus    *string                            `json:"lastRunStatus,omitempty"`
	LastRunError     *string                            `json:"lastRunError,omitempty"`
	NextRunAt        *string                            `json:"nextRunAt,omitempty"`
	Health           string                             `json:"health"`
	DeliveryHealth   string                             `json:"deliveryHealth"`
	DestinationLabel string                             `json:"destinationLabel,omitempty"`
}

func (m *Module) listAutomationSummaries(w http.ResponseWriter, _ *http.Request) {
	summaries, err := m.automationSummaries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build automation summaries: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (m *Module) getAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	item, ok, err := m.getDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load automation: "+err.Error())
		return
	}
	if ok {
		writeJSON(w, http.StatusOK, item)
		return
	}
	writeError(w, http.StatusNotFound, "automation not found: "+id)
}

func (m *Module) getAutomationsFile(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"content": features.ReadGremlinsRaw(m.supportDir),
	})
}

func (m *Module) putAutomationsFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := features.WriteGremlinsRaw(m.supportDir, body.Content); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write automations file: "+err.Error())
		return
	}
	if err := m.importLegacyDefinitions(true); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to import automations file: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (m *Module) getAutomationRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rows, err := m.store.ListGremlinRuns(id, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read runs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toGremlinRunRecords(rows))
}

func (m *Module) enableAutomation(w http.ResponseWriter, r *http.Request) {
	m.setAutomationState(w, r, true)
}

func (m *Module) disableAutomation(w http.ResponseWriter, r *http.Request) {
	m.setAutomationState(w, r, false)
}

func (m *Module) setAutomationState(w http.ResponseWriter, r *http.Request, enabled bool) {
	id := chi.URLParam(r, "id")
	item, ok, err := m.getDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load automation: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "automation not found: "+id)
		return
	}
	item.IsEnabled = enabled
	item.LastModifiedAt = strPtr(time.Now().UTC().Format(time.RFC3339))
	updated, err := m.saveDefinition(item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update automation: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (m *Module) createAutomation(w http.ResponseWriter, r *http.Request) {
	var item features.GremlinItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if item.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	created, err := m.createDefinition(item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (m *Module) updateAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	existing, ok, err := m.getDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load automation: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "automation not found: "+id)
		return
	}
	updatedItem, err := mergeAutomationPatch(existing, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := m.saveDefinition(updatedItem)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (m *Module) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	found, err := m.deleteDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "automation not found: "+id)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) RunNowForAgent(ctx context.Context, id string) (string, error) {
	result, err := m.runAutomationSync(ctx, id, "agent")
	if err != nil {
		return "", err
	}
	if result.Status != "completed" {
		return "", fmt.Errorf("automation %q failed: %s", result.AutomationName, result.Output)
	}
	return fmt.Sprintf("Automation %q ran successfully.\n\n%s", result.AutomationName, result.Output), nil
}

func (m *Module) runAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	item, ok := m.findAutomation(id)
	if !ok {
		writeError(w, http.StatusNotFound, "automation not found: "+id)
		return
	}

	if m.agent == nil {
		writeError(w, http.StatusNotImplemented, "agent loop not available")
		return
	}

	runID := newID()
	now := time.Now().UTC()
	nowUnix := float64(now.UnixNano()) / 1e9
	convID := newID()

	if err := m.store.SaveGremlinRun(storage.GremlinRunRow{
		RunID:           runID,
		GremlinID:       item.ID,
		StartedAt:       nowUnix,
		Status:          "running",
		ConversationID:  &convID,
		TriggerSource:   "manual",
		ExecutionStatus: "running",
		DeliveryStatus:  "pending",
		DestinationJSON: destinationJSON(item),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create run: "+err.Error())
		return
	}

	if m.bus != nil {
		_ = m.bus.Publish(context.Background(), triggeredEventName, map[string]string{
			"id":             runID,
			"gremlinID":      item.ID,
			"conversationID": convID,
			"trigger":        "manual",
		})
	}

	go func(item features.GremlinItem) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		_ = m.executeAutomationRun(ctx, item, runID, convID)
	}(item)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":             runID,
		"gremlinID":      item.ID,
		"conversationID": convID,
		"status":         "running",
	})
}

func (m *Module) runAutomationSync(ctx context.Context, id, trigger string) (RunResult, error) {
	item, ok := m.findAutomation(id)
	if !ok {
		return RunResult{}, fmt.Errorf("automation not found: %s", id)
	}
	if m.agent == nil {
		return RunResult{}, fmt.Errorf("agent loop not available")
	}

	runID := newID()
	convID := newID()
	nowUnix := float64(time.Now().UTC().UnixNano()) / 1e9
	if err := m.store.SaveGremlinRun(storage.GremlinRunRow{
		RunID:           runID,
		GremlinID:       item.ID,
		StartedAt:       nowUnix,
		Status:          "running",
		ConversationID:  &convID,
		TriggerSource:   trigger,
		ExecutionStatus: "running",
		DeliveryStatus:  "pending",
		DestinationJSON: destinationJSON(item),
	}); err != nil {
		return RunResult{}, fmt.Errorf("failed to create run: %w", err)
	}

	if m.bus != nil {
		_ = m.bus.Publish(context.Background(), triggeredEventName, map[string]string{
			"id":             runID,
			"gremlinID":      item.ID,
			"conversationID": convID,
			"trigger":        trigger,
		})
	}

	result := m.executeAutomationRun(ctx, item, runID, convID)
	return result, nil
}

func (m *Module) executeAutomationRun(ctx context.Context, item features.GremlinItem, runID, convID string) RunResult {
	started := time.Now()
	prompt, workflowRunID, workflowStartedAt, prepErr := m.prepareAutomationPrompt(item, runID, convID)
	if workflowRunID != "" {
		_ = m.store.UpdateGremlinRunWorkflowRunID(runID, workflowRunID)
	}
	if prepErr != nil {
		finishedAt := float64(time.Now().UnixNano()) / 1e9
		output := prepErr.Error()
		durationMs := time.Since(started).Milliseconds()
		_ = m.store.CompleteGremlinRun(runID, "failed", nil, &output, finishedAt, "skipped", nil, durationMs, runArtifactsJSON("failed", "skipped", nil))
		return RunResult{
			RunID:          runID,
			GremlinID:      item.ID,
			ConversationID: convID,
			AutomationName: item.Name,
			Status:         "failed",
			Output:         output,
		}
	}
	req := chat.MessageRequest{
		Message:        prompt,
		ConversationID: convID,
		Platform:       "automation",
	}
	resp, err := m.agent.HandleMessage(ctx, req)
	finishedAt := float64(time.Now().UnixNano()) / 1e9

	status := "completed"
	deliveryStatus := "skipped"
	out := ""
	var errorMessage *string
	var deliveryError *string
	if err != nil {
		status = "failed"
		out = err.Error()
		errorMessage = &out
	} else {
		out = resp.Response.AssistantMessage
		if resp.Response.Status == "error" {
			status = "failed"
			out = resp.Response.ErrorMessage
			errorMessage = &out
		}
	}

	output := out
	if status == "completed" && strings.TrimSpace(output) != "" {
		if m.hasDeliveryDestination(item) {
			deliveryStatus = "completed"
			if err := m.deliverAutomationOutput(ctx, item, output); err != nil {
				deliveryStatus = "failed"
				errText := err.Error()
				deliveryError = &errText
			}
		}
	}
	if workflowRunID != "" {
		_ = workflowexec.CompleteRun(m.workflows, workflowRunID, status, output, strVal(errorMessage), workflowStartedAt)
	}
	durationMs := time.Since(started).Milliseconds()
	artifactsJSON := runArtifactsJSON(status, deliveryStatus, deliveryError)
	_ = m.store.CompleteGremlinRun(runID, status, &output, errorMessage, finishedAt, deliveryStatus, deliveryError, durationMs, artifactsJSON)
	if deliveryError != nil {
		logstore.Write("error", "automation delivery failed: "+*deliveryError, map[string]string{
			"module":    "automations",
			"gremlinID": item.ID,
		})
	}
	if m.bus != nil {
		payload := map[string]string{
			"id":             runID,
			"gremlinID":      item.ID,
			"status":         status,
			"deliveryStatus": deliveryStatus,
			"output":         output,
		}
		_ = m.bus.Publish(context.Background(), completedEventName, payload)
	}
	return RunResult{
		RunID:          runID,
		GremlinID:      item.ID,
		ConversationID: convID,
		AutomationName: item.Name,
		Status:         status,
		Output:         output,
	}
}

func (m *Module) prepareAutomationPrompt(item features.GremlinItem, runID, convID string) (string, string, time.Time, error) {
	basePrompt := buildAutomationPrompt(item)
	if item.WorkflowID == nil || strings.TrimSpace(*item.WorkflowID) == "" {
		return basePrompt, "", time.Time{}, nil
	}
	workflowID := strings.TrimSpace(*item.WorkflowID)
	workflowRunID := "workflow-" + runID
	extraInstruction := ""
	if strings.TrimSpace(item.Prompt) != "" {
		extraInstruction = basePrompt
	}
	prepared, err := workflowexec.PrepareRun(m.workflows, workflowID, workflowRunID, convID, "automation", item.WorkflowInputValues, extraInstruction)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return prepared.Prompt, workflowRunID, prepared.StartedAt, nil
}

func (m *Module) deliverAutomationOutput(ctx context.Context, item features.GremlinItem, output string) error {
	if m.delivery == nil {
		return nil
	}
	dest := item.CommunicationDestination
	if dest == nil || strings.TrimSpace(dest.Platform) == "" || strings.TrimSpace(dest.ChannelID) == "" {
		return nil
	}
	if err := m.validateDestination(dest); err != nil {
		return err
	}
	err := m.delivery.SendAutomationResult(ctx, comms.AutomationDestination{
		Platform:  dest.Platform,
		ChannelID: dest.ChannelID,
		ThreadID:  strVal(dest.ThreadID),
	}, output)
	if err != nil {
		return err
	}
	return nil
}

func (m *Module) hasDeliveryDestination(item features.GremlinItem) bool {
	dest := item.CommunicationDestination
	return dest != nil && strings.TrimSpace(dest.Platform) != "" && strings.TrimSpace(dest.ChannelID) != ""
}

func (m *Module) findAutomation(id string) (features.GremlinItem, bool) {
	item, ok, err := m.getDefinition(id)
	if err != nil {
		logstore.Write("error", "automation lookup failed: "+err.Error(), map[string]string{
			"module": "automations",
			"id":     id,
		})
		return features.GremlinItem{}, false
	}
	return item, ok
}

func (m *Module) automationSummaries() ([]AutomationSummary, error) {
	items, err := m.listDefinitions()
	if err != nil {
		return nil, err
	}
	summaries := make([]AutomationSummary, 0, len(items))
	for _, item := range items {
		summary := AutomationSummary{
			ID:               item.ID,
			Name:             item.Name,
			Emoji:            item.Emoji,
			Prompt:           item.Prompt,
			ScheduleRaw:      item.ScheduleRaw,
			IsEnabled:        item.IsEnabled,
			SourceType:       item.SourceType,
			CreatedAt:        item.CreatedAt,
			Destination:      item.CommunicationDestination,
			Health:           "unknown",
			DeliveryHealth:   "not_configured",
			DestinationLabel: destinationLabel(item.CommunicationDestination),
		}
		if item.IsEnabled {
			if item.NextRunAt != nil && strings.TrimSpace(*item.NextRunAt) != "" {
				summary.NextRunAt = item.NextRunAt
			} else if _, next, ok := scheduleState(item.ScheduleRaw, m.schedulerNow()); ok {
				nextValue := next.UTC().Format(time.RFC3339)
				summary.NextRunAt = &nextValue
			}
		}
		if item.CommunicationDestination != nil {
			summary.DeliveryHealth = "unknown"
		}
		if m.store != nil {
			rows, err := m.store.ListGremlinRuns(item.ID, 1)
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 {
				run := rows[0]
				lastRunAt := time.Unix(int64(run.StartedAt), 0).UTC().Format(time.RFC3339)
				summary.LastRunAt = &lastRunAt
				summary.LastRunStatus = &run.Status
				summary.LastRunError = run.ErrorMessage
				if run.DeliveryError != nil && summary.LastRunError == nil {
					summary.LastRunError = run.DeliveryError
				}
				summary.Health = healthFromRun(run)
				summary.DeliveryHealth = deliveryHealthFromRun(run, item)
			}
		}
		if summary.Health == "unknown" && !item.IsEnabled {
			summary.Health = "disabled"
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func healthFromRun(run storage.GremlinRunRow) string {
	switch run.Status {
	case "completed":
		if run.DeliveryStatus == "failed" {
			return "degraded"
		}
		return "healthy"
	case "failed":
		return "failing"
	case "running":
		return "running"
	default:
		return "unknown"
	}
}

func deliveryHealthFromRun(run storage.GremlinRunRow, item features.GremlinItem) string {
	if item.CommunicationDestination == nil {
		return "not_configured"
	}
	switch run.DeliveryStatus {
	case "completed":
		return "healthy"
	case "failed":
		return "failing"
	case "skipped":
		return "not_configured"
	default:
		return "unknown"
	}
}

func destinationLabel(dest *features.CommunicationDestination) string {
	if dest == nil {
		return ""
	}
	if dest.ChannelName != nil && strings.TrimSpace(*dest.ChannelName) != "" {
		return strings.TrimSpace(*dest.ChannelName)
	}
	if strings.TrimSpace(dest.Platform) == "" {
		return strings.TrimSpace(dest.ChannelID)
	}
	if strings.TrimSpace(dest.ChannelID) == "" {
		return strings.TrimSpace(dest.Platform)
	}
	return strings.TrimSpace(dest.Platform) + ":" + strings.TrimSpace(dest.ChannelID)
}

func toGremlinRunRecords(rows []storage.GremlinRunRow) []features.GremlinRunRecord {
	out := make([]features.GremlinRunRecord, 0, len(rows))
	for _, row := range rows {
		rec := features.GremlinRunRecord{
			RunID:           row.RunID,
			GremlinID:       row.GremlinID,
			StartedAt:       time.Unix(int64(row.StartedAt), 0).UTC().Format(time.RFC3339),
			Status:          row.Status,
			Output:          row.Output,
			ErrorMessage:    row.ErrorMessage,
			ConversationID:  row.ConversationID,
			WorkflowRunID:   row.WorkflowRunID,
			TriggerSource:   row.TriggerSource,
			ExecutionStatus: row.ExecutionStatus,
			DeliveryStatus:  row.DeliveryStatus,
			DeliveryError:   row.DeliveryError,
			DestinationJSON: row.DestinationJSON,
			DurationMs:      row.DurationMs,
			RetryCount:      row.RetryCount,
			ArtifactsJSON:   row.ArtifactsJSON,
		}
		if row.FinishedAt != nil {
			ts := time.Unix(int64(*row.FinishedAt), 0).UTC().Format(time.RFC3339)
			rec.FinishedAt = &ts
		}
		out = append(out, rec)
	}
	return out
}

func newID() string {
	return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func strVal(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func destinationJSON(item features.GremlinItem) *string {
	if item.CommunicationDestination == nil {
		return nil
	}
	data, err := json.Marshal(item.CommunicationDestination)
	if err != nil {
		return nil
	}
	out := string(data)
	return &out
}

func runArtifactsJSON(status, deliveryStatus string, deliveryError *string) *string {
	artifacts := map[string]any{
		"executionStatus": status,
		"deliveryStatus":  deliveryStatus,
	}
	if deliveryError != nil {
		artifacts["deliveryError"] = *deliveryError
	}
	data, err := json.Marshal(artifacts)
	if err != nil {
		return nil
	}
	out := string(data)
	return &out
}

func buildAutomationPrompt(item features.GremlinItem) string {
	prompt := strings.TrimSpace(item.Prompt)
	if prompt == "" {
		return prompt
	}
	var guards []string

	// Keep this as an inline instruction so we don't need model/system prompt
	// changes. It prevents channel-capability disclaimers in automation output
	// because Atlas handles destination delivery outside the model.
	if item.CommunicationDestination != nil {
		guards = append(guards,
			"Automation context: Atlas will deliver your final output to the selected destination automatically. "+
				"Do not mention delivery limitations (for example \"I can't send to Telegram/WhatsApp\"). "+
				"Return only the final user-facing content.",
		)
	}

	if isBriefingPrompt(prompt) {
		guards = append(guards,
			"Briefing format requirements: Use this exact structure with concise bullets and no extra commentary.\n"+
				"Daily Briefing - <Location> - <Weekday Mon DD>\n"+
				"Weather:\n"+
				"- <one line>\n"+
				"U.S. headlines:\n"+
				"1. <headline + source>\n"+
				"2. <headline + source>\n"+
				"3. <headline + source>\n"+
				"4. <headline + source>\n"+
				"5. <headline + source>\n"+
				"Calendar:\n"+
				"- <events for today or 'No events today.'>\n\n"+
				"Data reliability rules:\n"+
				"- Always fetch weather via weather.brief for the requested location.\n"+
				"- Always fetch calendar via applescript.calendar_read for today (use local date bounds).\n"+
				"- Only output 'No events today.' when the calendar tool returns no events.\n"+
				"- If weather or calendar tools fail, state that section as unavailable instead of fabricating data.",
		)
	}

	if len(guards) == 0 {
		return prompt
	}
	return strings.TrimSpace(strings.Join(guards, "\n\n") + "\n\n" + prompt)
}

func isBriefingPrompt(prompt string) bool {
	p := strings.ToLower(prompt)
	if !strings.Contains(p, "briefing") {
		return false
	}
	return strings.Contains(p, "weather") || strings.Contains(p, "news") || strings.Contains(p, "headline") || strings.Contains(p, "calendar")
}
