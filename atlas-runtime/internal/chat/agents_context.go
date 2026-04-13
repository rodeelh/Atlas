package chat

import "context"

// RosterReader is set by the agents module during registration.
// It returns a formatted roster block for the system prompt.
// If nil, agentRosterContext returns "" (agents module not loaded).
var RosterReader func(supportDir string) string

// AsyncFollowUpSender is set by chat.Service during initialization.
// It delivers a follow-up message to the given conversation ID.
// Used by async_assignment goroutines in the agents module to push
// task-completion notifications to the originating Atlas conversation.
var AsyncFollowUpSender func(convID, text string)

// agentRosterContext returns a compact team roster block for injection into
// the system prompt. Delegates to RosterReader (set by the agents module) so
// that AGENTS.md is always parsed by the single canonical parser — no second
// implementation lives here.
func agentRosterContext(supportDir string) string {
	if RosterReader != nil {
		return RosterReader(supportDir)
	}
	return ""
}

type originConvIDKey struct{}

// WithOriginConvID injects the originating Atlas conversation ID into a context.
// Called in HandleMessage so async_assignment goroutines can deliver follow-ups.
func WithOriginConvID(ctx context.Context, convID string) context.Context {
	return context.WithValue(ctx, originConvIDKey{}, convID)
}

// OriginConvIDFromCtx extracts the originating conversation ID injected by
// WithOriginConvID, or returns "" if not present.
func OriginConvIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(originConvIDKey{}).(string)
	return v
}
