# Atlas — Open TODOs

Tracked here: significant rewrites, cross-cutting features, and known gaps that require design before implementation.

---

## LLM Tool Router as Default (Option 3)

**Status:** Not started
**Priority:** Medium
**Scope:** `atlas-runtime/internal/chat/`

### Context

Tool selection currently uses a keyword heuristic (`SelectiveToolDefs`) with a hard cap safety net (`capToolsForProvider`). This works reliably but is coarse — the model occasionally won't see a tool it could have used.

A smarter path already exists (`tool_router.go`) that asks a fast model to select the exact tools needed before the main agent turn. It's architecturally sound but not the default.

### What this work covers

1. **Make `"llm"` the default `ToolSelectionMode`** when a fast cloud model is configured (Anthropic haiku, Gemini flash, OpenAI mini) — the pre-turn latency is acceptable and accuracy is much higher than keywords.

2. **Fix the fallback chain** — the LLM router currently falls back to `SelectiveToolDefs` on any failure, which is now the fixed heuristic. That's correct. But when the router returns an empty selection it falls back to `ToolDefinitions()` (all tools) — fix that to fall back to `SelectiveToolDefs` instead.

3. **Router result caching** — for multi-turn conversations, tool selection for the same context rarely changes. Cache the selection for N seconds to avoid a pre-turn API call on every follow-up.

4. **Observability** — log selected tool names (not just count) at debug level so the selection decision is auditable.

### Files to touch
| File | Change |
|------|--------|
| `internal/chat/tool_router.go` | Fix empty-array fallback; add optional result cache |
| `internal/chat/service.go` | Change default ToolSelectionMode to `"llm"` when fast provider available |
| `internal/config/snapshot.go` | No change needed — mode already configurable |

---

## Vision Pipeline Rewrite

**Status:** Not started
**Priority:** High
**Scope:** `atlas-runtime`, `atlas-web`

### Problem

The current image handling still behaves like a fragile patchwork, not a proper pipeline:

- Images are attached in the Chat UI and passed directly to provider-specific handling instead of going through one clear runtime-owned vision path.
- Vision capability and degradation behavior are still inconsistent across providers.
- Follow-up tool continuation turns still risk losing attachment context.
- Provider-specific fallback notes can still pollute conversation history if they are not kept out of persisted message content.

### What the rewrite should cover

#### 1. Provider vision capability registry
Add a static capability map in the runtime provider layer (or a shared model selector) declaring which providers and models support vision. The Chat UI and runtime should consult this before embedding images or suppressing vision actions.

#### 2. Gemini inline image support
- Add inline image support to the Gemini request builder in the Go runtime.
- Embed image attachments on the last user message, mirroring the existing OpenAI- and Anthropic-style provider behavior.

#### 3. Unified VisionSkill across cloud providers
Replace the current vision action with a provider-aware image analysis path that:
- Accepts either a URL or an inline base64 payload.
- Routes to the correct provider API (OpenAI vision, Anthropic vision, Gemini vision) using the active client.
- Is not stripped from the tool list when inline images are present — instead, coexists cleanly with inline image embedding.

#### 4. LM Studio degradation without history pollution
When LM Studio is active and an image is attached, any explanatory fallback note should **not** be stored in conversation history. Pass it only in the outgoing API payload or tag it so the conversation loader can strip it.

#### 5. UI feedback
- Show a small vision-support indicator in the Chat header next to the provider selector (e.g., a camera icon, greyed-out when the active provider/model does not support vision).
- Optionally: disable the attachment button entirely when the active model cannot process images, with a tooltip explaining why.

#### 6. Attachment handling for tool continuation turns
If the model issues a tool call on a vision turn, images must remain available on the follow-up turn. The continuation path needs to carry forward the original attachments.

### Files to touch
| File | Change |
|------|--------|
| `atlas-runtime/internal/agent/provider.go` | Add provider-aware vision capability declaration and embedding support |
| `atlas-runtime/internal/chat/service.go` | Preserve and thread attachments through runtime message handling |
| `atlas-runtime/internal/agent/loop.go` | Carry attachments through continuation turns |
| `atlas-runtime/internal/skills` | Keep any vision action provider-aware and consistent with inline image handling |
| `atlas-web/src/screens/Chat.tsx` | Vision capability indicator next to provider selector |

---
