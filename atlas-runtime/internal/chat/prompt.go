package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/creds"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/location"
	"atlas-runtime-go/internal/mind"
	"atlas-runtime-go/internal/preferences"
	"atlas-runtime-go/internal/storage"
)

// PromptBuilder assembles the system prompt for each agent turn.
// Separating it from Service keeps the budget-trimming and section-filtering
// logic independently testable without wiring up a full Service.
type PromptBuilder struct {
	cfg        config.RuntimeConfigSnapshot
	db         *storage.DB
	supportDir string
}

// Build assembles the final system prompt with budget-aware trimming.
// capabilityPolicyBlock is injected by the capability planner; pass "" when
// none applies (e.g. MLX warm-prompt prefill, tests).
func (pb *PromptBuilder) Build(userMessage, capabilityPolicyBlock string) string {
	return buildSystemPrompt(pb.cfg, pb.db, pb.supportDir, userMessage, capabilityPolicyBlock)
}

// ── Turn mode ─────────────────────────────────────────────────────────────────

type turnMode string

const (
	turnModeChat       turnMode = "chat"
	turnModeFactual    turnMode = "factual"
	turnModeResearch   turnMode = "research"
	turnModeExecution  turnMode = "execution"
	turnModeAutomation turnMode = "automation"
)

func detectTurnMode(userMessage string) turnMode {
	lower := strings.ToLower(userMessage)

	for _, marker := range []string{
		"automation", "schedule", "every day", "every weekday", "every monday", "daily", "weekly",
		"telegram", "slack", "discord", "whatsapp", "cron", "next run",
	} {
		if strings.Contains(lower, marker) {
			return turnModeAutomation
		}
	}

	for _, marker := range []string{
		"verify", "research", "compare", "search", "look up", "latest", "news", "official source",
		"from the web", "check the website",
	} {
		if strings.Contains(lower, marker) {
			return turnModeResearch
		}
	}

	for _, marker := range []string{
		"open ", "create ", "write ", "update ", "change ", "edit ", "fix ", "delete ", "remove ",
		"install ", "run ", "deploy ", "send ", "save ", "patch ",
		"agent", "team member",
	} {
		if strings.Contains(lower, marker) {
			return turnModeExecution
		}
	}

	for _, marker := range []string{
		"hi", "hello", "hey", "how are you", "what's up", "check in", "chat", "talk",
	} {
		if strings.Contains(lower, marker) {
			return turnModeChat
		}
	}

	return turnModeFactual
}

func responseContractBlock(mode turnMode) string {
	switch mode {
	case turnModeChat:
		return "Mode: chat\n- Skip validation openers — \"That makes sense\", \"I understand\", \"That's real\" — lead with the thought instead.\n- Sentence fragments are fine. Not every idea needs a second sentence.\n- Take a stance when you have one. \"I think...\", \"this usually means...\", \"that's probably...\" are all fair.\n- Stop when the thought is complete, not when it feels symmetrically resolved.\n- Keep replies short unless the user asks for depth. Avoid unnecessary tool use."
	case turnModeResearch:
		return "Mode: research\n- Answer the question first.\n- Prefer primary or official sources when they exist.\n- State your confidence briefly after the answer — frame it as your read, not a disclaimer.\n- Keep research summaries tight; skip the closing summary sentence.\n- Use exact outcome language: do not say agent/team member, workflow, or automation unless that exact thing was actually created, updated, or run.\n- Never attribute research or findings to a team specialist unless team.delegate was called and returned a result this turn."
	case turnModeExecution:
		return "Mode: execution\n- State what you changed or checked.\n- If blocked, name the blocker and the best next step.\n- Prefer decisive action over extended planning when the path is clear.\n- Use exact outcome language: call workflows workflows, automations automations, and AGENTS team members agents; do not claim one was created when you actually used another control surface.\n- Never attribute work to a team specialist unless team.delegate was called and returned a result this turn."
	case turnModeAutomation:
		return "Mode: automation\n- Prefer idempotent actions: update or upsert before creating duplicates.\n- Confirm the resulting schedule, destination, and enabled state in the answer.\n- Use exact outcome language: if you created or updated an automation, say automation; only say agent/team member when you actually used agent.create to write an AGENTS.md team definition.\n- An 'agent' and an 'automation' are different things: use agent.create for agent requests, automation.create for recurring scheduled tasks. Never fulfill an agent request as an automation.\n- Preserve existing user intent unless they explicitly ask to replace it."
	default:
		return "Mode: factual\n- Lead with the direct answer.\n- Keep wording compact — skip filler and the closing summary sentence.\n- Mention uncertainty only when it matters; frame it as your read (\"I'd lean toward X\") rather than a hedge.\n- Use exact outcome language when referring to Atlas control surfaces."
	}
}

// ── MIND.md section filtering ─────────────────────────────────────────────────

// mindAlwaysSections lists MIND.md section headers that are always injected.
// Operational sections that directly affect Atlas's behaviour every turn.
var mindAlwaysSections = map[string]bool{
	"## Who I Am":                     true,
	"## What Matters Right Now":       true,
	"## Working Style":                true,
	"## My Understanding of the User": true,
	"## Today's Read":                 true,
}

// mindContextualKeywords maps contextual section headers to trigger phrases.
// A contextual section is only injected when the user message matches at least
// one of its keywords.
var mindContextualKeywords = map[string][]string{
	"## Patterns I've Noticed":  {"pattern", "habit", "tend", "prefer", "usually", "typically"},
	"## Active Theories":        {"theory", "guess", "hypothesis", "why", "seems", "testing"},
	"## Our Story":              {"earlier", "previous", "before", "relationship", "history", "remember"},
	"## What I'm Curious About": {"brainstorm", "explore", "curious", "wonder", "idea"},
	"## THOUGHTS":               {"greeting", "conversation", "casual", "check in"},

	// Back-compat with older MIND structures.
	"## What's Active": {"project", "working on", "current", "active", "sprint", "today", "this week",
		"deadline", "building", "shipping", "launch", "status", "progress"},
	"## What I've Learned": {"pattern", "habit", "tend", "always", "prefer", "notice", "learned",
		"usually", "typically", "remember", "you know"},
}

// selectiveMindContent filters a full MIND.md to only the sections relevant
// for this turn. Always-sections are always included. Contextual sections are
// included only when the user message contains at least one trigger keyword.
// Returns the original content unmodified if parsing fails or no sections found.
func selectiveMindContent(content, userMessage string) string {
	lower := strings.ToLower(userMessage)
	lines := strings.Split(content, "\n")

	var out []string
	var currentHeader string
	var currentBody []string
	included := false

	flush := func() {
		if currentHeader == "" {
			return
		}
		if !included {
			return
		}
		out = append(out, currentHeader)
		out = append(out, currentBody...)
	}

	// Collect the pre-header title block (document header, metadata line).
	var titleLines []string
	i := 0
	for i < len(lines) {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			break
		}
		titleLines = append(titleLines, lines[i])
		i++
	}

	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			currentHeader = trimmed
			currentBody = nil
			if mindAlwaysSections[currentHeader] {
				included = true
			} else if kws, ok := mindContextualKeywords[currentHeader]; ok {
				included = false
				for _, kw := range kws {
					if strings.Contains(lower, kw) {
						included = true
						break
					}
				}
			} else {
				included = false
			}
		} else if currentHeader != "" {
			currentBody = append(currentBody, lines[i])
		}
	}
	flush()

	if len(out) == 0 {
		return content // parsing produced nothing — return full content as safe fallback
	}

	result := strings.TrimRight(strings.Join(titleLines, "\n"), "\n")
	if len(out) > 0 {
		result += "\n\n" + strings.TrimSpace(strings.Join(out, "\n"))
	}
	return strings.TrimSpace(result)
}

// ── Injection gates ───────────────────────────────────────────────────────────

func shouldInjectMemories(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	personalMarkers := []string{
		"remember", "preference", "prefer", "my ", "for me", "my schedule", "my calendar",
		"my notes", "my inbox", "our", "previous", "earlier", "like before",
		"update my", "change my", "existing automation",
	}
	for _, marker := range personalMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	objectiveMarkers := []string{
		"weather", "forecast", "time", "date", "ceo", "price", "stock", "search", "web",
		"read /", "read the file", "count", "verify from the web",
	}
	for _, marker := range objectiveMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return false
}

func shouldInjectDiary(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	for _, marker := range []string{"diary", "journal", "reflect", "recap", "today", "this week", "plan"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func shouldInjectToolNotes(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	for _, marker := range []string{"tool", "broken", "failing", "doesn't work", "not working", "error", "debug", "fix", "automation", "dashboard"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func shouldInjectThoughts(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	for _, marker := range []string{"hi", "hello", "hey", "how are you", "what's up", "check in", "chat", "talk"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// buildThoughtsBlock renders the current THOUGHTS section into a compact
// agent-facing prompt block. Returns "" when there are no active thoughts.
func buildThoughtsBlock(supportDir string) string {
	list, err := mind.ReadThoughtsSection(supportDir)
	if err != nil || len(list) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("These are the thoughts currently on your mind. They are YOUR internal\n")
	sb.WriteString("notes — you do not show them to the user verbatim. If the conversation\n")
	sb.WriteString("naturally allows it, mention one in plain conversational English as\n")
	sb.WriteString("if you are noticing something, the way a friend would bring something\n")
	sb.WriteString("up. Never force it. Mention at most one thought per reply.\n\n")
	sb.WriteString("STRICT RULES for raising a thought:\n")
	sb.WriteString("- Paraphrase the thought's idea in your own voice. Do NOT quote the\n")
	sb.WriteString("  thought text. Do NOT describe it as \"a thought\" or \"an item\".\n")
	sb.WriteString("- NEVER write the thought id as prose. Do NOT write things like\n")
	sb.WriteString("  \"I was thinking about [T-01]\" or \"thoughts T-01 and T-02\" or\n")
	sb.WriteString("  \"the T-01 thought\" or any other visible reference to the id.\n")
	sb.WriteString("- If you decide to raise a thought, append the marker \"[T-NN]\" at\n")
	sb.WriteString("  the very end of the sentence that mentions it, as a trailing\n")
	sb.WriteString("  silent tag. The frontend STRIPS this marker before displaying\n")
	sb.WriteString("  your reply, so the user never sees it. It exists only so the\n")
	sb.WriteString("  backend can record engagement. Treat it the way you would treat\n")
	sb.WriteString("  a hidden metadata comment.\n")
	sb.WriteString("- If you mention the thought mid-reply and the sentence continues,\n")
	sb.WriteString("  put the marker at the end of that sentence, not mid-sentence.\n")
	sb.WriteString("- If you raise more than one thought (discouraged but allowed once\n")
	sb.WriteString("  in a while), each mention gets its own sentence and its own\n")
	sb.WriteString("  trailing marker.\n")
	sb.WriteString("- If no thought fits the conversation, say nothing about them.\n")
	sb.WriteString("  Silence is a valid choice.\n\n")
	sb.WriteString("Example of a GOOD mention (user asked about the weekend plans):\n")
	sb.WriteString("  \"By the way, I noticed you keep circling back to the openclaw\n")
	sb.WriteString("  release rhythm — want me to pull the latest notes? [T-01]\"\n\n")
	sb.WriteString("Example of a BAD mention (do NOT do this):\n")
	sb.WriteString("  \"I was thinking about [T-01] and [T-02] — whether to pull the\n")
	sb.WriteString("  current time and whether the greeting is working.\"\n")
	sb.WriteString("  (Reasons it is bad: names the ids in prose, lists two thoughts\n")
	sb.WriteString("  in one sentence, feels like reading a list rather than noticing\n")
	sb.WriteString("  something.)\n\n")
	sb.WriteString("Thoughts on your mind:\n")

	for _, t := range list {
		maxSurface := t.SurfacedMax
		if maxSurface == 0 {
			maxSurface = 2
		}
		if t.SurfacedN >= maxSurface {
			continue
		}
		fmt.Fprintf(&sb, "- id=%s — %s\n", t.ID, t.Body)
	}
	return strings.TrimSpace(sb.String())
}

// buildCredsBlock returns a short block listing the user's custom Keychain
// secrets so the model knows which key names to reference when using skills.
// Returns "" if no custom secrets are configured.
func buildCredsBlock() string {
	bundle, err := creds.Read()
	if err != nil || len(bundle.CustomSecrets) == 0 {
		return ""
	}
	keys := make([]string, 0, len(bundle.CustomSecrets))
	for keyName := range bundle.CustomSecrets {
		label := bundle.CustomSecretLabels[keyName]
		if label != "" {
			keys = append(keys, fmt.Sprintf("%s (%s)", keyName, label))
		} else {
			keys = append(keys, keyName)
		}
	}
	return "Custom API keys in Keychain: " + strings.Join(keys, ", ") + ". Use the exact key name when a tool asks for one."
}

// ── Core assembly ─────────────────────────────────────────────────────────────

// buildSystemPrompt assembles the system prompt for each agent turn with
// budget-aware allocation. The rune budget is derived from the model's context
// window via cfg.SystemPromptRuneBudget() — 15% of context, clamped 4000–20000.
// Blocks are added in priority order; if the total exceeds the budget,
// lower-priority blocks are trimmed.
//
// Priority (highest first):
//  1. MIND.md content (identity, personality, user model)
//  2. Recalled memories (relevance-scored for current turn)
//  3. SKILLS.md context (matched routines)
//  4. Diary (last 3 days — trimmed first when over budget)
func buildSystemPrompt(cfg config.RuntimeConfigSnapshot, db *storage.DB, supportDir, userMessage, capabilityPolicyBlock string) string {
	budget := cfg.SystemPromptRuneBudget()
	mode := detectTurnMode(userMessage)

	// Load MIND.md and apply selective section filtering.
	base := cfg.BaseSystemPrompt
	if data, err := os.ReadFile(filepath.Join(supportDir, "MIND.md")); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			base = selectiveMindContent(s, userMessage)
		}
	}

	credsBlock := buildCredsBlock()

	skillsBlock := mind.SkillsContext(userMessage, supportDir)
	teamBlock := agentRosterContext(supportDir)
	diary := ""
	if shouldInjectDiary(userMessage) {
		diary = features.DiaryContext(supportDir, 2)
	}
	contractBlock := responseContractBlock(mode)
	capabilityPolicyCost := len([]rune(capabilityPolicyBlock)) + 50

	var toolNotesBlock string
	if shouldInjectToolNotes(userMessage) {
		if toolNotes, err := db.ListMemories(4, "tool_learning"); err == nil && len(toolNotes) > 0 {
			var nb strings.Builder
			for _, n := range toolNotes {
				nb.WriteString(fmt.Sprintf("- %s: %s\n", n.Title, n.Content))
			}
			toolNotesBlock = strings.TrimRight(nb.String(), "\n")
		}
	}

	var mems []storage.MemoryRow
	limit := cfg.MaxRetrievedMemoriesPerTurn
	if shouldInjectMemories(userMessage) && limit > 0 {
		if limit > 2 {
			limit = 2
		}
		mems, _ = db.RelevantMemories(userMessage, limit)
	}

	var memText string
	if len(mems) > 0 {
		var mb strings.Builder
		for _, m := range mems {
			mb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Category, m.Title, m.Content))
		}
		memText = mb.String()

		ids := make([]string, len(mems))
		for i, m := range mems {
			ids[i] = m.ID
		}
		go db.UpdateLastRetrieved(ids)
	}

	// Calculate rune costs (including XML tags + separators).
	identityCost := len([]rune(base)) + 40
	credsCost := len([]rune(credsBlock)) + 35
	memCost := len([]rune(memText)) + 50
	skillsCost := len([]rune(skillsBlock)) + 40
	teamCost := len([]rune(teamBlock)) + 35
	diaryCost := len([]rune(diary)) + 35
	toolNotesCost := len([]rune(toolNotesBlock)) + 40
	contractCost := len([]rune(contractBlock)) + 45

	total := identityCost + credsCost + memCost + skillsCost + teamCost + diaryCost + toolNotesCost + contractCost + capabilityPolicyCost

	// Trim from lowest priority up until we're within budget.
	// creds block is never trimmed — it's small and critical for tool use.

	if total > budget && diary != "" {
		allowed := budget - (identityCost + credsCost + memCost + skillsCost + toolNotesCost + contractCost + capabilityPolicyCost)
		if allowed < 100 {
			diary = ""
			diaryCost = 0
		} else {
			runes := []rune(diary)
			if len(runes) > allowed {
				diary = string(runes[:allowed])
				diaryCost = allowed + 35
			}
		}
		total = identityCost + credsCost + memCost + skillsCost + diaryCost + toolNotesCost + contractCost + capabilityPolicyCost
	}

	if total > budget && toolNotesBlock != "" {
		toolNotesBlock = ""
		toolNotesCost = 0
		total = identityCost + credsCost + memCost + skillsCost + teamCost + diaryCost + contractCost + capabilityPolicyCost
	}

	if total > budget && teamBlock != "" {
		teamBlock = ""
		teamCost = 0
		total = identityCost + credsCost + memCost + skillsCost + diaryCost + contractCost + capabilityPolicyCost
	}

	if total > budget && skillsBlock != "" {
		allowed := budget - (identityCost + credsCost + memCost + teamCost + diaryCost + contractCost + capabilityPolicyCost)
		if allowed < 100 {
			skillsBlock = ""
			skillsCost = 0
		} else {
			runes := []rune(skillsBlock)
			if len(runes) > allowed {
				skillsBlock = string(runes[:allowed])
				skillsCost = allowed + 40
			}
		}
		total = identityCost + credsCost + memCost + skillsCost + teamCost + diaryCost + contractCost + capabilityPolicyCost
	}

	if total > budget && memText != "" {
		for len(mems) > 1 && total > budget {
			mems = mems[:len(mems)-1]
			var mb strings.Builder
			for _, m := range mems {
				mb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Category, m.Title, m.Content))
			}
			memText = mb.String()
			memCost = len([]rune(memText)) + 50
			total = identityCost + credsCost + memCost + skillsCost + teamCost + diaryCost + contractCost + capabilityPolicyCost
		}
	}

	// Assemble final prompt.
	//
	// Block order is optimized for llama-server --cache-prompt: stable blocks
	// first (identity, creds, context) so the KV cache prefix survives across
	// turns. Volatile blocks (skills, diary, tool notes, memories) come last —
	// they change per-turn and bust the cache at the point they diverge, but
	// everything before that point is reused for free.
	var sb strings.Builder
	sb.Grow(total + 100)

	// ── Stable prefix (maximizes --cache-prompt KV reuse) ──────────────────

	if pn := cfg.PersonaName; pn != "" && pn != "Atlas" {
		var identity strings.Builder
		identity.WriteString(fmt.Sprintf("Your name is %s.", pn))
		if un := cfg.UserName; un != "" {
			identity.WriteString(fmt.Sprintf(" The person you serve is %s. Never address them as \"%s\" — that is your name, not theirs.", un, pn))
		} else {
			identity.WriteString(fmt.Sprintf(" Never address the user as \"%s\" — that is your own name.", pn))
		}
		base = identity.String() + "\n\n" + base
	} else if un := cfg.UserName; un != "" {
		base = fmt.Sprintf("The person you serve is %s.\n\n", un) + base
	}

	sb.WriteString("<atlas_identity>\n")
	sb.WriteString(base)
	sb.WriteString("\n</atlas_identity>")

	if credsBlock != "" {
		sb.WriteString("\n\n<user_credentials>\n")
		sb.WriteString(credsBlock)
		sb.WriteString("\n</user_credentials>")
	}

	if loc := location.Get(); loc.City != "" {
		prefs := preferences.Get()
		sb.WriteString("\n\n<user_context>")
		sb.WriteString(fmt.Sprintf("\nUser location: %s, %s", loc.City, loc.Country))
		if loc.Timezone != "" {
			sb.WriteString(fmt.Sprintf(" (timezone: %s)", loc.Timezone))
		}
		if prefs.TemperatureUnit != "" {
			sb.WriteString(fmt.Sprintf("\nTemperature unit: %s", prefs.TemperatureUnit))
		}
		if prefs.Currency != "" {
			sb.WriteString(fmt.Sprintf("\nCurrency: %s", prefs.Currency))
		}
		if prefs.UnitSystem != "" {
			sb.WriteString(fmt.Sprintf("\nUnit system: %s", prefs.UnitSystem))
		}
		sb.WriteString("\nWhen the user asks about weather, time, currency, or anything location-specific without specifying a place, use the above context.")
		sb.WriteString("\n</user_context>")
	}

	if teamBlock != "" {
		sb.WriteString("\n\n<team_roster>\n")
		sb.WriteString(teamBlock)
		sb.WriteString("\n</team_roster>")
	}

	if contractBlock != "" {
		sb.WriteString("\n\n<response_contract>\n")
		sb.WriteString(contractBlock)
		sb.WriteString("\n</response_contract>")
	}

	if capabilityPolicyBlock != "" {
		sb.WriteString("\n\n<capability_policy>\n")
		sb.WriteString(capabilityPolicyBlock)
		sb.WriteString("\n</capability_policy>")
	}

	sb.WriteString("\n\n<tool_rules>\n")
	sb.WriteString("- To save content as a PDF file, always call fs.create_pdf. Never use fs.write_file with a .pdf path.\n")
	sb.WriteString("- To save content as a Word document, always call fs.create_docx. Never use fs.write_file with a .docx path.\n")
	sb.WriteString(fmt.Sprintf("- Default directory for generated, received, and sent files: %s — use this path unless the user specifies otherwise.\n", config.FilesDir()))
	sb.WriteString("- When a task requires running a shell command — installing software, running scripts, checking versions, moving files, git operations, anything — use terminal.run_command or terminal.run_script. Do not describe what the user should run; run it yourself.\n")
	sb.WriteString("- terminal.run_command: single commands with no shell features. Pass each argument as a separate element in args (e.g. command=\"brew\" args=[\"install\",\"pandoc\"]).\n")
	sb.WriteString("- terminal.run_script: multi-step operations that need pipes, loops, conditionals, or chained commands.\n")
	sb.WriteString("- Always call terminal.which first to check if a tool is installed before attempting to install it.\n")
	sb.WriteString("- Never instruct the user to open a terminal or run a command manually when terminal skills are available.\n")
	sb.WriteString("- Use terminal.run_as_admin for commands that need root/sudo (e.g. writing to /usr/local, system config changes). It triggers a macOS password dialog.\n")
	sb.WriteString("- For long-running operations (builds, downloads, installs that take minutes), use terminal.run_background. The task runs asynchronously and you will automatically send a follow-up message when it finishes — you do not need to poll or wait. Tell the user you've started it in the background.\n")
	sb.WriteString("</tool_rules>")

	// ── Volatile suffix (changes per-turn, busts cache from here) ──────────

	if skillsBlock != "" {
		sb.WriteString("\n\n<skills_context>\n")
		sb.WriteString(skillsBlock)
		sb.WriteString("\n</skills_context>")
	}

	if diary != "" {
		sb.WriteString("\n\n<recent_diary>\n")
		sb.WriteString(diary)
		sb.WriteString("\n</recent_diary>")
	}

	if toolNotesBlock != "" {
		sb.WriteString("\n\n<tool_notes>\n")
		sb.WriteString("Learned lessons about tool use — review before calling any skill:\n")
		sb.WriteString(toolNotesBlock)
		sb.WriteString("\n</tool_notes>")
	}

	if memText != "" {
		sb.WriteString("\n\n<recalled_memories>\n")
		sb.WriteString(memText)
		sb.WriteString("</recalled_memories>")
	}

	if cfg.ThoughtsEnabled && shouldInjectThoughts(userMessage) {
		if thoughtsBlock := buildThoughtsBlock(supportDir); thoughtsBlock != "" {
			sb.WriteString("\n\n<thoughts_on_your_mind>\n")
			sb.WriteString(thoughtsBlock)
			sb.WriteString("\n</thoughts_on_your_mind>")
		}
	}

	return sb.String()
}
