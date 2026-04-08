package storage

// mind_telemetry.go provides typed SQL helpers for the mind_telemetry table.
// Schema lives in db.go migrate().
//
// The telemetry table is the persistent view of every interesting event in
// the mind-thoughts subsystem. Atlas itself has no access to its own
// graveyard (the "thoughts are fleeting" principle), but designers do — so
// this table preserves the lifecycle of every thought ever generated,
// including discarded ones, for tuning.

import (
	"database/sql"
	"fmt"
	"time"
)

// MindTelemetryRow is one row in the mind_telemetry table.
type MindTelemetryRow struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	ThoughtID string    `json:"thought_id,omitempty"`
	ConvID    string    `json:"conv_id,omitempty"`
	Payload   string    `json:"payload"` // raw JSON blob
}

// MindTelemetryFilter is the criteria used by QueryMindTelemetry. All fields
// are optional; zero values are treated as "no constraint on this field".
type MindTelemetryFilter struct {
	Kinds     []string  // empty = all kinds
	ThoughtID string    // empty = all thoughts
	ConvID    string    // empty = all conversations
	Since     time.Time // zero = no lower bound
	Until     time.Time // zero = no upper bound
	Limit     int       // 0 = default (500)
}

// InsertMindTelemetry appends one row. thoughtID and convID may be empty.
// payload must be a valid JSON string — the caller is responsible for
// marshalling the payload before calling.
func (db *DB) InsertMindTelemetry(ts time.Time, kind, thoughtID, convID, payload string) error {
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	var tid, cid sql.NullString
	if thoughtID != "" {
		tid = sql.NullString{String: thoughtID, Valid: true}
	}
	if convID != "" {
		cid = sql.NullString{String: convID, Valid: true}
	}
	_, err := db.conn.Exec(
		`INSERT INTO mind_telemetry (ts, kind, thought_id, conv_id, payload) VALUES (?, ?, ?, ?, ?)`,
		ts.UTC().Format(time.RFC3339Nano), kind, tid, cid, payload,
	)
	if err != nil {
		return fmt.Errorf("insert mind_telemetry: %w", err)
	}
	return nil
}

// QueryMindTelemetry returns rows matching the filter, sorted by ts DESC
// (most recent first). Used by the /mind/telemetry analysis endpoint and by
// the future Mind Health dashboard widgets.
func (db *DB) QueryMindTelemetry(f MindTelemetryFilter) ([]MindTelemetryRow, error) {
	var (
		clauses []string
		args    []any
	)
	if len(f.Kinds) > 0 {
		placeholders := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			placeholders[i] = "?"
			args = append(args, k)
		}
		clauses = append(clauses, "kind IN ("+joinStrings(placeholders, ", ")+")")
	}
	if f.ThoughtID != "" {
		clauses = append(clauses, "thought_id = ?")
		args = append(args, f.ThoughtID)
	}
	if f.ConvID != "" {
		clauses = append(clauses, "conv_id = ?")
		args = append(args, f.ConvID)
	}
	if !f.Since.IsZero() {
		clauses = append(clauses, "ts >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	if !f.Until.IsZero() {
		clauses = append(clauses, "ts <= ?")
		args = append(args, f.Until.UTC().Format(time.RFC3339Nano))
	}
	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + joinStrings(clauses, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 500
	}
	args = append(args, limit)

	// Tie-break on id DESC so events emitted inside the same nanosecond
	// (e.g. a burst of thought_* events during a nap) have a deterministic
	// order — the later-inserted row sorts first, matching insertion order.
	query := fmt.Sprintf(
		"SELECT id, ts, kind, thought_id, conv_id, payload FROM mind_telemetry %s ORDER BY ts DESC, id DESC LIMIT ?",
		where,
	)
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query mind_telemetry: %w", err)
	}
	defer rows.Close()

	var out []MindTelemetryRow
	for rows.Next() {
		var (
			r         MindTelemetryRow
			tsStr     string
			thoughtID sql.NullString
			convID    sql.NullString
		)
		if err := rows.Scan(&r.ID, &tsStr, &r.Kind, &thoughtID, &convID, &r.Payload); err != nil {
			return nil, fmt.Errorf("scan mind_telemetry: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
			r.Timestamp = t
		} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
			r.Timestamp = t
		}
		if thoughtID.Valid {
			r.ThoughtID = thoughtID.String
		}
		if convID.Valid {
			r.ConvID = convID.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountMindTelemetryByKind returns a map of kind → count for events after
// `since`. Used by the Mind Health dashboard for the event breakdown widgets.
func (db *DB) CountMindTelemetryByKind(since time.Time) (map[string]int, error) {
	rows, err := db.conn.Query(
		`SELECT kind, COUNT(*) FROM mind_telemetry WHERE ts >= ? GROUP BY kind`,
		since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("count mind_telemetry: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, fmt.Errorf("scan count: %w", err)
		}
		out[kind] = n
	}
	return out, rows.Err()
}

// DeleteMindTelemetryBefore removes rows older than the given cutoff. Used
// by a future cleanup job — the telemetry table is small in practice but
// we want a bounded-growth escape hatch.
func (db *DB) DeleteMindTelemetryBefore(cutoff time.Time) (int64, error) {
	res, err := db.conn.Exec(
		`DELETE FROM mind_telemetry WHERE ts < ?`,
		cutoff.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("delete mind_telemetry: %w", err)
	}
	return res.RowsAffected()
}

// joinStrings is a tiny local helper to avoid importing strings just for Join.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}
