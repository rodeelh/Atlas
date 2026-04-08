package mind

import (
	"strings"
	"testing"
	"time"

	"atlas-runtime-go/internal/mind/thoughts"
	"atlas-runtime-go/internal/storage"
)

func TestParseNapEnvelope_PlainJSON(t *testing.T) {
	raw := `{"rationale":"tested","ops":[{"op":"add","body":"hi","confidence":80,"value":70,"class":"read","source":"s"}]}`
	env, err := parseNapEnvelope(raw)
	if err != nil {
		t.Fatalf("parseNapEnvelope: %v", err)
	}
	if env.Rationale != "tested" {
		t.Errorf("rationale: got %q", env.Rationale)
	}
	if len(env.Ops) != 1 || env.Ops[0].Kind != thoughts.OpAdd {
		t.Errorf("ops: got %+v", env.Ops)
	}
}

func TestParseNapEnvelope_CodeFences(t *testing.T) {
	raw := "```json\n" +
		`{"rationale":"fenced","ops":[]}` +
		"\n```"
	env, err := parseNapEnvelope(raw)
	if err != nil {
		t.Fatalf("parseNapEnvelope with fences: %v", err)
	}
	if env.Rationale != "fenced" {
		t.Errorf("rationale: got %q", env.Rationale)
	}
}

func TestParseNapEnvelope_LeadingProse(t *testing.T) {
	raw := `Here's my nap result:

{"rationale":"proseful","ops":[]}

Let me know if you need anything else.`
	env, err := parseNapEnvelope(raw)
	if err != nil {
		t.Fatalf("parseNapEnvelope with prose: %v", err)
	}
	if env.Rationale != "proseful" {
		t.Errorf("rationale: got %q", env.Rationale)
	}
}

func TestParseNapEnvelope_NoJSON(t *testing.T) {
	raw := "I don't have anything to report this time."
	_, err := parseNapEnvelope(raw)
	if err == nil {
		t.Error("expected error when output contains no JSON")
	}
}

func TestParseNapEnvelope_Unterminated(t *testing.T) {
	raw := `{"rationale":"incomplete","ops":[`
	_, err := parseNapEnvelope(raw)
	if err == nil {
		t.Error("expected error for unterminated JSON")
	}
}

func TestParseNapEnvelope_NestedBraces(t *testing.T) {
	// Actions contain a nested JSON object; the brace counter must handle it.
	raw := `{
		"rationale":"with action",
		"ops":[{
			"op":"add",
			"body":"pull openclaw notes",
			"confidence":95,
			"value":90,
			"class":"read",
			"source":"conv-a",
			"provenance":"recurrent mention",
			"action":{"skill":"openclaw.check","args":{"repo":"openclaw/openclaw","branch":"main"}}
		}]
	}`
	env, err := parseNapEnvelope(raw)
	if err != nil {
		t.Fatalf("parseNapEnvelope with nested action: %v", err)
	}
	if len(env.Ops) != 1 {
		t.Fatalf("ops: got %d", len(env.Ops))
	}
	if env.Ops[0].Action == nil || env.Ops[0].Action.SkillID != "openclaw.check" {
		t.Errorf("action: %+v", env.Ops[0].Action)
	}
}

func TestBuildNapPrompt_IncludesAllSections(t *testing.T) {
	now := mustT(t, "2026-04-07T12:00:00Z")
	inputs := NapInputs{
		CurrentThoughts: []thoughts.Thought{{
			ID: "T-01", Body: "existing thought",
			Confidence: 50, Value: 50, Class: thoughts.ClassRead, Score: 25,
			Created: now, Reinforced: now, SurfacedMax: 2, Source: "s",
		}},
		RecentTurns: []TurnRecord{
			{UserMessage: "hi", AssistantResponse: "hello there"},
			{UserMessage: "how are you", AssistantResponse: "fine"},
		},
		MindMD:       "# Mind of Atlas\n\n## Identity\n\nI am Atlas.",
		DiaryContext: "2026-04-06: notable event",
		RelevantMemories: []storage.MemoryRow{
			{Title: "user preference", Content: "prefers concise replies"},
		},
		EngagementEvents: []thoughts.Event{
			{ThoughtID: "T-01", ConvID: "c1", Timestamp: now, Signal: thoughts.SignalPositive, UserMessage: "thanks"},
		},
		Timestamp: now,
	}
	skills := []string{"openclaw.check — fetch latest release notes"}

	system, user := buildNapPrompt(inputs, skills)

	// System prompt must contain the load-bearing rules.
	if !strings.Contains(system, "Two negatives is enough to discard") {
		t.Error("system: missing two-negatives rule")
	}
	if !strings.Contains(system, "Three ignores is enough") {
		t.Error("system: missing three-ignores rule")
	}
	if !strings.Contains(system, "thoughts are fleeting") {
		t.Error("system: missing fleeting principle")
	}
	if !strings.Contains(system, "0–3 operations") {
		t.Error("system: missing 0-3 ops budget")
	}
	if !strings.Contains(system, "confidence") || !strings.Contains(system, "class") {
		t.Error("system: missing op format guidance")
	}

	// User prompt must include every input section tag.
	for _, tag := range []string{"<THOUGHTS>", "<RECENT_TURNS>", "<MIND>", "<DIARY>",
		"<MEMORIES>", "<SKILLS>", "<ENGAGEMENT_EVENTS>"} {
		if !strings.Contains(user, tag) {
			t.Errorf("user prompt missing %s", tag)
		}
	}

	// Content spot-checks.
	if !strings.Contains(user, "existing thought") {
		t.Error("user prompt missing current thought body")
	}
	if !strings.Contains(user, "how are you") {
		t.Error("user prompt missing recent turn")
	}
	if !strings.Contains(user, "notable event") {
		t.Error("user prompt missing diary entry")
	}
	if !strings.Contains(user, "prefers concise replies") {
		t.Error("user prompt missing memory")
	}
	if !strings.Contains(user, "openclaw.check") {
		t.Error("user prompt missing skill list")
	}
	if !strings.Contains(user, "positive") {
		t.Error("user prompt missing engagement event signal")
	}
}

func TestBuildNapPrompt_EmptyInputs(t *testing.T) {
	// A nap can fire with nothing in any section — prompt should still be
	// well-formed and mention the placeholder strings.
	inputs := NapInputs{Timestamp: time.Now()}
	_, user := buildNapPrompt(inputs, nil)
	for _, expected := range []string{
		"(no active thoughts)",
		"(no recent turns)",
		"(no recent diary entries)",
		"(no relevant memories)",
		"(no skills available)",
		"(no engagement events",
	} {
		if !strings.Contains(user, expected) {
			t.Errorf("missing placeholder %q in empty prompt", expected)
		}
	}
}

func TestMessagesToTurns(t *testing.T) {
	msgs := []storage.MessageRow{
		{Role: "user", Content: "first user msg", Timestamp: "2026-04-07T12:00:00Z", ConversationID: "c1"},
		{Role: "assistant", Content: "first assistant msg", Timestamp: "2026-04-07T12:00:01Z", ConversationID: "c1"},
		{Role: "user", Content: "second user msg", Timestamp: "2026-04-07T12:01:00Z", ConversationID: "c1"},
		{Role: "assistant", Content: "second assistant msg", Timestamp: "2026-04-07T12:01:01Z", ConversationID: "c1"},
	}
	turns := messagesToTurns(msgs, 10)
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(turns))
	}
	if turns[0].UserMessage != "first user msg" || turns[0].AssistantResponse != "first assistant msg" {
		t.Errorf("turn 0: %+v", turns[0])
	}
	if turns[1].UserMessage != "second user msg" || turns[1].AssistantResponse != "second assistant msg" {
		t.Errorf("turn 1: %+v", turns[1])
	}
}

func TestMessagesToTurns_Limit(t *testing.T) {
	msgs := []storage.MessageRow{
		{Role: "user", Content: "a", Timestamp: "2026-04-07T10:00:00Z", ConversationID: "c1"},
		{Role: "assistant", Content: "a'", Timestamp: "2026-04-07T10:00:01Z", ConversationID: "c1"},
		{Role: "user", Content: "b", Timestamp: "2026-04-07T10:01:00Z", ConversationID: "c1"},
		{Role: "assistant", Content: "b'", Timestamp: "2026-04-07T10:01:01Z", ConversationID: "c1"},
		{Role: "user", Content: "c", Timestamp: "2026-04-07T10:02:00Z", ConversationID: "c1"},
		{Role: "assistant", Content: "c'", Timestamp: "2026-04-07T10:02:01Z", ConversationID: "c1"},
	}
	// Limit to 2 turns — should keep the most recent two.
	turns := messagesToTurns(msgs, 2)
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(turns))
	}
	if turns[0].UserMessage != "b" || turns[1].UserMessage != "c" {
		t.Errorf("wrong turns kept: %+v", turns)
	}
}

func TestApproxTokens(t *testing.T) {
	// Just a sanity check — exact numbers don't matter, we want the
	// function to be monotonic and cheap.
	a := approxTokens("hello")
	b := approxTokens("hello world this is a longer string")
	if b <= a {
		t.Errorf("approxTokens not monotonic: a=%d b=%d", a, b)
	}
	if approxTokens("") != 0 {
		t.Errorf("empty: got %d, want 0", approxTokens(""))
	}
}

func TestFormatSkillsList(t *testing.T) {
	got := FormatSkillsList([]SkillLine{
		{ID: "weather.current", Description: "get the current weather"},
		{ID: "openclaw.check", Description: "pull latest release notes"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2", len(got))
	}
	if !strings.Contains(got[0], "weather.current") || !strings.Contains(got[0], "current weather") {
		t.Errorf("line 0: %q", got[0])
	}
}
