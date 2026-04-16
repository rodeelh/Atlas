package dashboards

// resolve_chat.go — named, parametrized queries over the chat/memory tables.
// Each allowlisted query name maps to a fixed SQL string here; agents cannot
// inject arbitrary SQL via this source kind.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// chatAnalyticsQuerySQL returns the SQL template for a validated query name.
// The returned SQL is run with db.QueryContext against the main db handle
// (read-only by convention of this package).
func chatAnalyticsQuerySQL(name string) string {
	switch name {
	case "conversations_per_day":
		return `SELECT date(updated_at, 'unixepoch') AS day, COUNT(*) AS count
		        FROM conversations
		        GROUP BY day
		        ORDER BY day DESC
		        LIMIT 60`
	case "messages_per_day":
		return `SELECT date(timestamp, 'unixepoch') AS day, COUNT(*) AS count
		        FROM messages
		        GROUP BY day
		        ORDER BY day DESC
		        LIMIT 60`
	case "top_conversations":
		return `SELECT c.conversation_id AS id,
		               COALESCE(c.title, '(untitled)') AS title,
		               COUNT(m.message_id) AS message_count
		        FROM conversations c
		        LEFT JOIN messages m ON m.conversation_id = c.conversation_id
		        GROUP BY c.conversation_id
		        ORDER BY message_count DESC
		        LIMIT 10`
	case "recent_conversations":
		return `SELECT conversation_id AS id,
		               COALESCE(title, '(untitled)') AS title,
		               platform,
		               updated_at
		        FROM conversations
		        ORDER BY updated_at DESC
		        LIMIT 25`
	case "message_counts_by_role":
		return `SELECT role, COUNT(*) AS count
		        FROM messages
		        GROUP BY role
		        ORDER BY count DESC`
	case "token_usage_per_day":
		return `SELECT date(recorded_at, 'unixepoch') AS day,
		               SUM(input_tokens)  AS input_tokens,
		               SUM(output_tokens) AS output_tokens,
		               SUM(total_cost_usd) AS total_cost_usd
		        FROM token_usage
		        GROUP BY day
		        ORDER BY day DESC
		        LIMIT 60`
	case "token_usage_by_provider":
		return `SELECT provider,
		               SUM(input_tokens)  AS input_tokens,
		               SUM(output_tokens) AS output_tokens,
		               SUM(total_cost_usd) AS total_cost_usd
		        FROM token_usage
		        GROUP BY provider
		        ORDER BY total_cost_usd DESC`
	case "memory_counts_by_category":
		return `SELECT category, COUNT(*) AS count
		        FROM memories
		        GROUP BY category
		        ORDER BY count DESC`
	case "most_important_memories":
		return `SELECT memory_id AS id, category, title, importance, updated_at
		        FROM memories
		        ORDER BY importance DESC, updated_at DESC
		        LIMIT 20`
	case "recent_memories":
		return `SELECT memory_id AS id, category, title, importance, updated_at
		        FROM memories
		        ORDER BY updated_at DESC
		        LIMIT 20`
	}
	return ""
}

// resolveChatAnalytics runs an allowlisted query against the main db.
// cfg shape: { "query": "messages_per_day" }
func resolveChatAnalytics(ctx context.Context, db *sql.DB, cfg map[string]any) (any, error) {
	if db == nil {
		return nil, errors.New("chat_analytics: db handle not wired")
	}
	name, _ := cfg["query"].(string)
	if err := validateAnalyticsQuery(name); err != nil {
		return nil, err
	}
	sqlText := chatAnalyticsQuerySQL(name)
	if sqlText == "" {
		return nil, fmt.Errorf("chat_analytics: query %q has no sql", name)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("chat_analytics query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("chat_analytics cols: %w", err)
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
			return nil, fmt.Errorf("chat_analytics scan: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalizeSQLValue(vals[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chat_analytics rows: %w", err)
	}
	return map[string]any{
		"query":   name,
		"columns": cols,
		"rows":    out,
	}, nil
}
