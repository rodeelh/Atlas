package dashboards

// resolve_gremlin.go — surfaces recent runs for a named gremlin. The row is
// keyed by gremlin_id; optional config filters narrow the selection.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// resolveGremlin returns recent runs for a gremlin, plus its most recent
// output and artifacts.
// cfg shape: { "gremlinID": "morning-summary", "limit": 10 }
func resolveGremlin(ctx context.Context, db *sql.DB, cfg map[string]any) (any, error) {
	if db == nil {
		return nil, errors.New("gremlin resolver: db handle not wired")
	}
	gremlinID, _ := cfg["gremlinID"].(string)
	if gremlinID == "" {
		return nil, errors.New("gremlin resolver: gremlinID is required")
	}
	limit := 10
	if raw, ok := cfg["limit"].(float64); ok {
		if n := int(raw); n > 0 && n <= 100 {
			limit = n
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT run_id, started_at, finished_at, status, output, artifacts_json
		FROM gremlin_runs
		WHERE gremlin_id = ?
		ORDER BY started_at DESC
		LIMIT ?`, gremlinID, limit)
	if err != nil {
		return nil, fmt.Errorf("gremlin query: %w", err)
	}
	defer rows.Close()

	type runRow struct {
		RunID      string `json:"runId"`
		StartedAt  any    `json:"startedAt"`
		FinishedAt any    `json:"finishedAt,omitempty"`
		Status     string `json:"status"`
		Output     string `json:"output,omitempty"`
		Artifacts  any    `json:"artifacts,omitempty"`
	}
	runs := make([]runRow, 0, limit)
	for rows.Next() {
		var (
			runID         string
			startedAt     sql.NullFloat64
			finishedAt    sql.NullFloat64
			status        sql.NullString
			output        sql.NullString
			artifactsJSON sql.NullString
		)
		if err := rows.Scan(&runID, &startedAt, &finishedAt, &status, &output, &artifactsJSON); err != nil {
			return nil, fmt.Errorf("gremlin scan: %w", err)
		}
		r := runRow{RunID: runID}
		if startedAt.Valid {
			r.StartedAt = startedAt.Float64
		}
		if finishedAt.Valid {
			r.FinishedAt = finishedAt.Float64
		}
		if status.Valid {
			r.Status = status.String
		}
		if output.Valid {
			r.Output = output.String
		}
		if artifactsJSON.Valid && artifactsJSON.String != "" {
			var parsed any
			if err := json.Unmarshal([]byte(artifactsJSON.String), &parsed); err == nil {
				r.Artifacts = parsed
			} else {
				r.Artifacts = artifactsJSON.String
			}
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gremlin rows: %w", err)
	}
	return map[string]any{
		"gremlinID": gremlinID,
		"runs":      runs,
	}, nil
}
