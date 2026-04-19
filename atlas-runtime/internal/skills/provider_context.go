package skills

import "context"

type memoryEmbedderContextKey struct{}

// MemoryEmbedder embeds a memory-search query for semantic retrieval. It is
// injected by chat so skills do not need to import the agent package.
type MemoryEmbedder func(ctx context.Context, query string) ([]float32, error)

// WithMemoryEmbedder injects the active turn's embedding function.
func WithMemoryEmbedder(ctx context.Context, embedder MemoryEmbedder) context.Context {
	return context.WithValue(ctx, memoryEmbedderContextKey{}, embedder)
}

func memoryEmbedderFromContext(ctx context.Context) (MemoryEmbedder, bool) {
	embedder, ok := ctx.Value(memoryEmbedderContextKey{}).(MemoryEmbedder)
	return embedder, ok && embedder != nil
}
