package dashboards

// resolve_sql.go — runs a user-supplied single-statement SELECT against a
// read-only connection to atlas.sqlite3. The lexical validateSelectSQL check
// is the first gate; the read-only DSN is the second.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// resolveSQL opens a fresh read-only connection to the db path and runs the
// validated SELECT. Returns rows as []map[string]any.
// cfg shape: { "sql": "SELECT ..." }
func resolveSQL(ctx context.Context, dbPath string, cfg map[string]any) (any, error) {
	if dbPath == "" {
		return nil, errors.New("sql resolver: db path not configured")
	}
	raw, _ := cfg["sql"].(string)
	cleaned, err := validateSelectSQL(raw)
	if err != nil {
		return nil, err
	}

	// Per-resolve timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite (ro): %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, cleaned)
	if err != nil {
		return nil, fmt.Errorf("sql query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sql columns: %w", err)
	}

	const maxRows = 500
	out := make([]map[string]any, 0, 64)
	for rows.Next() {
		if len(out) >= maxRows {
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("sql scan: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalizeSQLValue(vals[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sql rows: %w", err)
	}
	return map[string]any{
		"columns": cols,
		"rows":    out,
	}, nil
}

// normalizeSQLValue coerces SQL scan values into JSON-friendly types.
func normalizeSQLValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	default:
		return v
	}
}
