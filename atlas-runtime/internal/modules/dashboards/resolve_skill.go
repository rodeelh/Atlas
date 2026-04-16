package dashboards

// resolve_skill.go — calls a read-only skill action and returns its summary
// (parsed as JSON if possible). Skills are not further allowlisted here
// because the registry is expected to enforce per-skill policy via
// ActionClass; the permission-error path still catches "is not read-only".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// resolveSkill dispatches to a registered read-only skill action.
// cfg shape: { "action": "websearch.query", "args": { ... } }
func resolveSkill(ctx context.Context, exec SkillExecutor, cfg map[string]any) (any, error) {
	if exec == nil {
		return nil, errors.New("skill executor is not wired")
	}
	action, _ := cfg["action"].(string)
	if action == "" {
		return nil, errors.New("skill action is required")
	}
	args := map[string]any{}
	if raw, ok := cfg["args"].(map[string]any); ok {
		args = raw
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal skill args: %w", err)
	}
	res, err := exec.Execute(ctx, action, argsJSON)
	if err != nil {
		return nil, err
	}
	if !res.Success {
		return nil, fmt.Errorf("skill %s failed: %s", action, res.Summary)
	}
	// Prefer Artifacts when present — most built-in skills return
	// structured data there and a human-readable sentence in Summary.
	if len(res.Artifacts) > 0 {
		return res.Artifacts, nil
	}
	// Fall back to parsing Summary as JSON, then as a plain text wrapper.
	var parsed any
	if err := json.Unmarshal([]byte(res.Summary), &parsed); err == nil {
		return parsed, nil
	}
	return map[string]any{"text": res.Summary}, nil
}
