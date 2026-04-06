package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

type Module struct {
	db *storage.DB
}

func New(db *storage.DB) *Module { return &Module{db: db} }

func (m *Module) ID() string { return "usage" }

func (m *Module) Manifest() platform.Manifest { return platform.Manifest{Version: "v1"} }

func (m *Module) Register(host platform.Host) error {
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/usage/summary", m.getSummary)
	r.Get("/usage/events", m.getEvents)
	r.Delete("/usage", m.deleteUsage)
}

func (m *Module) getSummary(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			days = n
		}
	}
	if since == "" && until == "" && days > 0 {
		since = time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -days).Format(time.RFC3339)
	}
	summary, err := m.db.GetTokenUsageSummary(since, until, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query usage: "+err.Error())
		return
	}
	type modelBreakdown struct {
		Provider     string  `json:"provider"`
		Model        string  `json:"model"`
		InputTokens  int64   `json:"inputTokens"`
		OutputTokens int64   `json:"outputTokens"`
		TotalTokens  int64   `json:"totalTokens"`
		TotalCostUSD float64 `json:"totalCostUSD"`
		TurnCount    int64   `json:"turnCount"`
	}
	type dailySeries struct {
		Date         string  `json:"date"`
		InputTokens  int64   `json:"inputTokens"`
		OutputTokens int64   `json:"outputTokens"`
		TotalTokens  int64   `json:"totalTokens"`
		CostUSD      float64 `json:"costUSD"`
		TurnCount    int64   `json:"turnCount"`
	}
	type response struct {
		TotalInputTokens  int64            `json:"totalInputTokens"`
		TotalOutputTokens int64            `json:"totalOutputTokens"`
		TotalTokens       int64            `json:"totalTokens"`
		TotalCostUSD      float64          `json:"totalCostUSD"`
		TurnCount         int64            `json:"turnCount"`
		ByModel           []modelBreakdown `json:"byModel"`
		DailySeries       []dailySeries    `json:"dailySeries"`
	}
	resp := response{
		TotalInputTokens:  summary.TotalInputTokens,
		TotalOutputTokens: summary.TotalOutputTokens,
		TotalTokens:       summary.TotalTokens,
		TotalCostUSD:      summary.TotalCostUSD,
		TurnCount:         summary.TurnCount,
		ByModel:           make([]modelBreakdown, 0, len(summary.ByModel)),
		DailySeries:       make([]dailySeries, 0, len(summary.DailySeries)),
	}
	for _, bm := range summary.ByModel {
		resp.ByModel = append(resp.ByModel, modelBreakdown{
			Provider: bm.Provider, Model: bm.Model, InputTokens: bm.InputTokens, OutputTokens: bm.OutputTokens,
			TotalTokens: bm.TotalTokens, TotalCostUSD: bm.TotalCostUSD, TurnCount: bm.TurnCount,
		})
	}
	for _, ds := range summary.DailySeries {
		resp.DailySeries = append(resp.DailySeries, dailySeries{
			Date: ds.Date, InputTokens: ds.InputTokens, OutputTokens: ds.OutputTokens,
			TotalTokens: ds.TotalTokens, CostUSD: ds.CostUSD, TurnCount: ds.TurnCount,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (m *Module) getEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := q.Get("since")
	until := q.Get("until")
	provider := q.Get("provider")
	model := q.Get("model")
	limit := 200
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	events, err := m.db.TokenUsageEvents(since, until, provider, model, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query events: "+err.Error())
		return
	}
	type eventRow struct {
		ID             string  `json:"id"`
		ConversationID string  `json:"conversationId"`
		Provider       string  `json:"provider"`
		Model          string  `json:"model"`
		InputTokens    int     `json:"inputTokens"`
		OutputTokens   int     `json:"outputTokens"`
		InputCostUSD   float64 `json:"inputCostUSD"`
		OutputCostUSD  float64 `json:"outputCostUSD"`
		TotalCostUSD   float64 `json:"totalCostUSD"`
		RecordedAt     string  `json:"recordedAt"`
	}
	rows := make([]eventRow, 0, len(events))
	for _, e := range events {
		rows = append(rows, eventRow{
			ID: e.ID, ConversationID: e.ConversationID, Provider: e.Provider, Model: e.Model,
			InputTokens: e.InputTokens, OutputTokens: e.OutputTokens, InputCostUSD: e.InputCostUSD,
			OutputCostUSD: e.OutputCostUSD, TotalCostUSD: e.TotalCostUSD, RecordedAt: e.RecordedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": rows})
}

func (m *Module) deleteUsage(w http.ResponseWriter, r *http.Request) {
	before := r.URL.Query().Get("before")
	if before == "" {
		writeError(w, http.StatusBadRequest, "before parameter is required")
		return
	}
	deleted, err := m.db.TokenUsageDeleteBefore(before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
