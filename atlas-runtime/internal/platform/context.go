package platform

import "context"

// TurnContext is the minimal shared shape for turn-scoped context assembly.
type TurnContext struct {
	ConversationID string
	AgentID        string
	Platform       string
	UserID         string
}

// ContextAssembler builds the system/context block for a turn. The initial
// platform tranche only defines this seam; the current runtime continues to use
// its existing chat-owned prompt assembly until later extractions.
type ContextAssembler interface {
	Assemble(ctx context.Context, turn TurnContext) (string, error)
}

type ContextAssemblerFunc func(ctx context.Context, turn TurnContext) (string, error)

func (f ContextAssemblerFunc) Assemble(ctx context.Context, turn TurnContext) (string, error) {
	return f(ctx, turn)
}

// NoopContextAssembler is the default placeholder used while prompt assembly
// still lives in the existing chat runtime.
type NoopContextAssembler struct{}

func (NoopContextAssembler) Assemble(context.Context, TurnContext) (string, error) {
	return "", nil
}
