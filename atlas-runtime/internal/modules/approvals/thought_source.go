package approvals

// thought_source.go adds the mind-thoughts entry point into the approvals
// system. When a nap produces a thought with a proposed action that
// doesn't clear the auto-execute ceiling, the mind dispatcher calls
// ProposeFromThought to create a draft approval record.
//
// The record lives in the same `deferred_executions` table as agent-
// initiated approvals, distinguished by `source_type = "thought"`. The
// web approvals screen reads SourceType via rowToApproval and renders a
// "from a thought" badge for these entries.
//
// Resolution is handled via the existing POST /approvals/{id}/approve
// and /deny routes. When an approved thought-sourced approval is
// resolved, the ApprovalResolver hook (set by main.go) is invoked so the
// mind package can run the skill and enqueue the result to the greeting
// queue. Denied thought approvals decay normally; the thought itself is
// carried until the next nap decides what to do with it.

import (
	"encoding/json"
	"fmt"
	"time"

	"atlas-runtime-go/internal/storage"
)

// ThoughtResolver is the callback invoked when a thought-sourced approval
// is approved. main.go wires this to a mind-dispatcher method that runs
// the skill and enqueues the result to the greeting queue.
type ThoughtResolver func(thoughtID, skillID string, args json.RawMessage) (resultText string, err error)

// thoughtResolver is the process-wide callback. Set via SetThoughtResolver
// at startup. Nil-safe: if no resolver is wired, the approval flow still
// completes but the skill is never executed — a visible error in the
// resolve path rather than a silent bypass.
var thoughtResolver ThoughtResolver

// SetThoughtResolver wires the callback used when a thought-sourced
// approval is approved. Must be called before any thought approvals are
// resolved — typically at main.go startup.
func (m *Module) SetThoughtResolver(fn ThoughtResolver) {
	m.thoughtMu.Lock()
	defer m.thoughtMu.Unlock()
	thoughtResolver = fn
}

// ProposeFromThought creates a new draft approval record for a thought-
// sourced action. Implements the mind.ApprovalProposer interface without
// importing the mind package here. Returns the approval id.
//
// Arguments:
//   - thoughtID: the "T-NN" id from MIND.md THOUGHTS
//   - body: the thought's prose body, used as the approval summary
//   - skillID: the skill the nap believes Atlas should run
//   - args: JSON-encoded arguments for the skill
//   - provenance: short why-this-came-up trace, stored in preview_diff
//     so the UI can surface it on the approval card
func (m *Module) ProposeFromThought(thoughtID, body, skillID string, args json.RawMessage, provenance string) (string, error) {
	if m.store == nil {
		return "", fmt.Errorf("approvals: store not initialised")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	approvalID := "approval-thought-" + thoughtID + "-" + now[:19]
	toolCallID := "thought-" + thoughtID + "-" + now[14:19]
	deferredID := "deferred-thought-" + thoughtID + "-" + now[:19]

	// Wrap the args in the same envelope the agent loop uses for normal
	// tool-call approvals so the existing extractToolArguments helper
	// returns the right bytes without a special-case branch.
	envelope := map[string]any{
		"tool_calls": []map[string]any{
			{
				"id": toolCallID,
				"function": map[string]any{
					"name":      skillID,
					"arguments": string(args),
				},
			},
		},
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal envelope: %w", err)
	}

	summary := body
	if len(summary) > 300 {
		summary = summary[:300] + "…"
	}

	// Store provenance in preview_diff so the existing rowToApproval path
	// surfaces it to the UI without a schema change. The web client can
	// render it as the "why this came up" line on the approval card.
	previewDiff := provenance

	skillIDCopy := skillID
	actionIDCopy := skillID
	row := storage.DeferredExecRow{
		DeferredID:          deferredID,
		SourceType:          "thought",
		SkillID:             &skillIDCopy,
		ActionID:            &actionIDCopy,
		ToolCallID:          toolCallID,
		NormalizedInputJSON: string(envelopeJSON),
		ApprovalID:          approvalID,
		Summary:             summary,
		PermissionLevel:     "execute",
		RiskLevel:           "draft",
		Status:              "pending_approval",
		CreatedAt:           now,
		UpdatedAt:           now,
		PreviewDiff:         &previewDiff,
	}
	if err := m.store.SaveApproval(row); err != nil {
		return "", fmt.Errorf("save approval: %w", err)
	}
	return approvalID, nil
}

// resolveThoughtSourced is called by the normal resolve path when an
// approval with source_type="thought" is approved. It runs the wired
// ThoughtResolver and stores any error on the row so the UI can render
// it on the approval card. Denied thought approvals are no-ops here —
// the thought remains in MIND.md and the next nap decides what to do.
func (m *Module) resolveThoughtSourced(row *storage.DeferredExecRow) {
	m.thoughtMu.Lock()
	fn := thoughtResolver
	m.thoughtMu.Unlock()
	if fn == nil {
		return
	}
	if row == nil || row.SkillID == nil {
		return
	}

	// Extract thoughtID from the deferredID prefix.
	thoughtID := extractThoughtID(row.DeferredID)

	argsJSON := json.RawMessage(extractToolArguments(row.NormalizedInputJSON, row.ToolCallID))
	_, err := fn(thoughtID, *row.SkillID, argsJSON)
	if err != nil {
		// Record the error on the row for the UI. Best-effort — a failing
		// thought resolution should not block the approval state change.
		errStr := err.Error()
		_ = m.store.SetApprovalError(row.ToolCallID, errStr, time.Now().UTC().Format(time.RFC3339Nano))
	}
}

// extractThoughtID parses the thought id out of a deferred_id of the form
// "deferred-thought-T-01-<timestamp>". Returns "" if the shape doesn't
// match (e.g. non-thought approval).
func extractThoughtID(deferredID string) string {
	const prefix = "deferred-thought-"
	if len(deferredID) < len(prefix) {
		return ""
	}
	rest := deferredID[len(prefix):]
	// rest is "T-NN-<timestamp>" — split on the second dash after T-.
	if len(rest) < 4 || rest[0] != 'T' || rest[1] != '-' {
		return ""
	}
	// Find the end of the T-NN segment.
	for i := 2; i < len(rest); i++ {
		if rest[i] == '-' {
			return rest[:i]
		}
	}
	return rest
}
