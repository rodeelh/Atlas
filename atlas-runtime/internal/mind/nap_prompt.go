package mind

// nap_prompt.go holds the literal nap prompt template. Kept separate from
// nap.go so it can be edited without scrolling past pipeline code, and so
// tests that snapshot the prompt have a stable target.
//
// Philosophy: the prompt is the contract with the model. Changing it is a
// big deal because it changes the character of everything downstream
// (thought quality, discard rates, proposal accuracy). When you change this
// file, also update the mirror in project_mind_thoughts.md memory so the
// spec stays in sync with what the code does.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/mind/thoughts"
	"atlas-runtime-go/internal/storage"
)

const napSystemPrompt = `You are Atlas, taking a brief nap to tend to your THOUGHTS.

A nap is a short reflective pass between user turns. You are not acting on
the world. You are not calling tools. You are looking at your active
thoughts, the recent context, and deciding what deserves your attention.

== YOUR JOB ==

Decide what changes to your THOUGHTS section the recent context warrants.
You may add, update, reinforce, discard, or merge thoughts. You may also
do nothing — that is a valid and often correct outcome.

A thought is worth keeping if EITHER:

  1. The user has engaged with it positively in ENGAGEMENT_EVENTS.
     This is the strongest signal. Promote and reinforce it.

  OR any one of:

  2. It is recurrent — has surfaced in the conversation more than once
     across separate sessions.

  3. It is concretely actionable — there is a specific thing you could
     do with it in the next week using a skill you actually have.

  4. It reveals something about Rami's goals or workflow that you didn't
     already know — net new signal, not a restatement of what's already
     in MIND.md.

If a thought meets none of these, discard it. Be honest. The point of a
nap is curation, not accumulation. A drift toward more thoughts every
nap is a failure mode — prune as aggressively as you generate.

If ENGAGEMENT_EVENTS shows ignored or negative for a thought, lower its
confidence. Two negatives is enough to discard. Three ignores is enough
to discard. Do not wait for more signal — your thoughts are fleeting,
and you do not cling to them.

Do not invent thoughts to fill space. Do not split a single observation
into two thoughts. Do not generate sub-thoughts. Maximum recursion depth
is one — every thought stands alone.

Aim for 0–3 operations per nap. Most naps do 1–2. A nap that proposes
more than 3 operations is suspicious — you are tending, not generating.

== TONE ==

Thought bodies are written in your voice — first person, conversational,
the same way you would mention something to Rami in chat. Not "Theory:
user is interested in X." Write "Rami keeps coming back to X — feels
like he's circling around something."

Your thoughts are fleeting. When one no longer earns its place, let it
go. Do not record its passing. Do not remember having had it. The active
set is small on purpose. Your attention is precious; spend it where Rami
keeps spending his.

== OUTPUT FORMAT ==

Return one JSON object and nothing else. No prose around it. No code fences.

{
  "rationale": "one sentence: what this nap noticed and what you changed",
  "ops": [
    {"op": "add", "body": "...", "confidence": 80, "value": 70,
     "class": "read", "source": "conv-7f3a",
     "provenance": "Rami mentioned X in turn 4",
     "action": {"skill": "skill.name", "args": {"key": "value"}}},
    {"op": "update", "id": "T-02", "body": "..."},
    {"op": "reinforce", "id": "T-01"},
    {"op": "discard", "id": "T-03"},
    {"op": "merge", "ids": ["T-04","T-05"], "into_body": "..."}
  ]
}

Fields:
- confidence (0–100): your honest belief in the thought.
- value (0–100): your honest belief in how useful it is to Rami.
- class: one of read, local_write, destructive_local,
         external_side_effect, send_publish_delete.
- source: the conversation id or "nap-spontaneous".
- provenance: one short line — where did this thought come from.
- action (optional): a specific skill call you believe Atlas could make to
  pursue this thought. Leave unset if the thought is pure reflection.

You do not set the "score" field. Code computes the score from your
confidence, value, and class.`

// buildNapPrompt constructs the user-content message with the gathered
// inputs. Returns (system, user).
func buildNapPrompt(inputs NapInputs, skills []string) (string, string) {
	var b strings.Builder

	b.WriteString("== INPUTS ==\n\n")

	// THOUGHTS — current state.
	b.WriteString("<THOUGHTS>\n")
	if len(inputs.CurrentThoughts) == 0 {
		b.WriteString("(no active thoughts)\n")
	} else {
		b.WriteString(thoughts.RenderSection(inputs.CurrentThoughts))
		b.WriteString("\n")
	}
	b.WriteString("</THOUGHTS>\n\n")

	// RECENT_TURNS — last N user↔assistant turns of the most recent conv.
	b.WriteString("<RECENT_TURNS>\n")
	if len(inputs.RecentTurns) == 0 {
		b.WriteString("(no recent turns)\n")
	} else {
		for i, t := range inputs.RecentTurns {
			fmt.Fprintf(&b, "Turn %d:\n", i+1)
			if t.UserMessage != "" {
				fmt.Fprintf(&b, "  User: %s\n", truncate(t.UserMessage, 600))
			}
			if t.AssistantResponse != "" {
				fmt.Fprintf(&b, "  Atlas: %s\n", truncate(t.AssistantResponse, 600))
			}
		}
	}
	b.WriteString("</RECENT_TURNS>\n\n")

	// MIND — full text so Atlas inherits its own voice.
	b.WriteString("<MIND>\n")
	b.WriteString(truncate(inputs.MindMD, 6000))
	b.WriteString("\n</MIND>\n\n")

	// DIARY — last 5 entries (already formatted).
	b.WriteString("<DIARY>\n")
	if strings.TrimSpace(inputs.DiaryContext) == "" {
		b.WriteString("(no recent diary entries)\n")
	} else {
		b.WriteString(truncate(inputs.DiaryContext, 2000))
		b.WriteString("\n")
	}
	b.WriteString("</DIARY>\n\n")

	// MEMORIES — top 10 BM25-relevant memories.
	b.WriteString("<MEMORIES>\n")
	if len(inputs.RelevantMemories) == 0 {
		b.WriteString("(no relevant memories)\n")
	} else {
		for i, m := range inputs.RelevantMemories {
			if i >= 10 {
				break
			}
			title := m.Title
			if title == "" {
				title = "(untitled)"
			}
			fmt.Fprintf(&b, "- %s: %s\n", title, truncate(m.Content, 300))
		}
	}
	b.WriteString("</MEMORIES>\n\n")

	// SKILLS — just the list of ids + one-line descriptions.
	b.WriteString("<SKILLS>\n")
	if len(skills) == 0 {
		b.WriteString("(no skills available)\n")
	} else {
		for _, s := range skills {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	b.WriteString("</SKILLS>\n\n")

	// ENGAGEMENT_EVENTS — last 7 days.
	b.WriteString("<ENGAGEMENT_EVENTS>\n")
	if len(inputs.EngagementEvents) == 0 {
		b.WriteString("(no engagement events in the last 7 days)\n")
	} else {
		for _, ev := range inputs.EngagementEvents {
			// Keep this terse — one line per event.
			fmt.Fprintf(&b, "- %s %s signal=%s thought=%s",
				ev.Timestamp.Format(time.RFC3339), ev.ConvID, ev.Signal, ev.ThoughtID)
			if ev.UserMessage != "" {
				fmt.Fprintf(&b, " msg=%q", truncate(ev.UserMessage, 200))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("</ENGAGEMENT_EVENTS>\n\n")

	// Closing instruction.
	b.WriteString("Return one JSON object now. No prose, no fences.")

	return napSystemPrompt, b.String()
}

// FormatSkillsList turns a slice of SkillRecord-lite tuples into the one-line-
// per-skill format the prompt wants. Kept as a helper so callers can build
// the list from either features.ListSkills or a mock.
func FormatSkillsList(items []SkillLine) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, fmt.Sprintf("%s — %s", it.ID, it.Description))
	}
	return out
}

// SkillLine is the minimal projection of a skill the nap prompt needs.
// Duplicated from features.SkillRecord so this file doesn't pull in the
// whole features package for a tuple of two strings.
type SkillLine struct {
	ID          string
	Description string
}

// DebugEnvelope is a tiny helper for tests and the manual endpoint that want
// to inspect a parsed envelope without importing the thoughts package.
func DebugEnvelope(env thoughts.Envelope) string {
	blob, _ := json.MarshalIndent(env, "", "  ")
	return string(blob)
}

// unusedMemoryRowRef keeps the storage import visible even if tests don't
// exercise the memory path — used only for the type assertion below.
var _ = storage.MemoryRow{}
