package skills

import "context"

// proactiveSenderKey is the unexported context key for the proactive sender.
type proactiveSenderKey struct{}

// WithProactiveSender injects a callback into ctx that skills can call to
// deliver an unprompted assistant message back to the active conversation.
// The callback persists the message to the DB and emits SSE events.
func WithProactiveSender(ctx context.Context, fn func(text string)) context.Context {
	return context.WithValue(ctx, proactiveSenderKey{}, fn)
}

// ProactiveSenderFromContext retrieves the proactive sender injected by the
// chat service. Returns nil, false if none was injected.
func ProactiveSenderFromContext(ctx context.Context) (func(text string), bool) {
	fn, ok := ctx.Value(proactiveSenderKey{}).(func(text string))
	return fn, ok
}
