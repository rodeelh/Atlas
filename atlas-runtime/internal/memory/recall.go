package memory

import (
	"context"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/logstore"
)

// hydePrompt is the single-shot instruction sent to the LLM for HyDE generation.
// It asks for a short memory-style sentence — the resulting text looks like a
// stored memory entry, so its embedding lands close to real memories in vector
// space rather than close to the raw user query.
const hydePrompt = `You are a memory assistant. Given a user query, write ONE sentence describing what a stored memory about this topic would say. Write as if it is already known — a fact, preference, or insight. Do not ask questions. Do not explain. Output only the sentence.

Query: %s`

// hydeTimeout caps the non-streaming LLM call so it never stalls the turn.
const hydeTimeout = 4 * time.Second

// HyDEVector generates a Hypothetical Document Embedding for query.
// It asks the LLM to write a short memory-style sentence for the query,
// then embeds that sentence. The resulting vector is a better search key
// than embedding the raw query because it matches the distribution of the
// stored memory entries.
//
// Returns nil when:
//   - the provider does not support embeddings (Anthropic, local models)
//   - the LLM call or embed call fails for any reason
//
// Callers must treat nil as "no vector available" and fall back to FTS5.
func HyDEVector(ctx context.Context, provider agent.ProviderConfig, query string) []float32 {
	if query == "" || provider.Type == "" {
		return nil
	}

	// Non-streaming call to generate the hypothetical memory sentence.
	ctx, cancel := context.WithTimeout(ctx, hydeTimeout)
	defer cancel()

	prompt := strings.TrimSpace(query)
	if len(prompt) > 300 {
		prompt = prompt[:300]
	}

	messages := []agent.OAIMessage{
		{Role: "system", Content: "You are a concise memory assistant."},
		{Role: "user", Content: strings.Replace(hydePrompt, "%s", prompt, 1)},
	}
	_, hypo, _, err := agent.CallAINonStreamingExported(ctx, provider, messages, nil)
	if err != nil || hypo == "" {
		logstore.Write("info", "HyDE: LLM call failed or empty — falling back to BM25", nil)
		return nil
	}

	// Embed the hypothetical sentence with the query task prefix (Nomic v1.5 requirement).
	vec, err := agent.Embed(ctx, provider, agent.NomicPrefixQuery+strings.TrimSpace(hypo))
	if err != nil {
		logstore.Write("info", "HyDE: embed failed — falling back to BM25", nil)
		return nil
	}
	return vec
}
