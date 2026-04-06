// Package mind — dream.go implements the nightly "dream" consolidation cycle.
// Inspired by Anthropic's "auto dream" and Park et al.'s reflection mechanism,
// it periodically prunes stale memories, merges near-duplicates, synthesizes
// diary entries into structured memories, and refreshes MIND.md.
package mind

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/storage"
)

// dreamHour is the local hour at which the dream cycle fires (3 AM).
const dreamHour = 3

// minRunInterval is the minimum time between dream cycle runs.
// If the daemon restarts and the last run was more than this long ago, a
// catch-up run fires immediately rather than waiting until the next 3 AM.
const minRunInterval = 20 * time.Hour

// catchupDelay is how long to wait after daemon startup before running a
// catch-up cycle — gives the daemon time to fully initialize.
const catchupDelay = 60 * time.Second

// dreamStateFile records the last successful run time.
const dreamStateFile = "dream-state.json"

type dreamState struct {
	LastRunAt string `json:"last_run_at"`
}

// ProviderResolver returns a fresh ProviderConfig from the current runtime
// config. Used to avoid capturing a stale provider at startup.
type ProviderResolver func() (agent.ProviderConfig, error)

// StartDreamCycle launches a background goroutine that runs the four-phase
// consolidation cycle once per day at dreamHour local time. Returns a stop
// function that cancels the scheduler.
//
// resolveProvider is called fresh on each cycle run so config changes (e.g.
// switching AI providers) are picked up without a restart.
// RunDreamNow executes a single dream cycle synchronously in the calling goroutine.
// The caller should run this in a goroutine if it must not block.
// dream-state.json is updated on success so the nightly scheduler doesn't
// double-fire within 20 hours.
func RunDreamNow(supportDir string, db *storage.DB, cfgStore *config.Store, resolveProvider ProviderResolver) {
	ctx := context.Background()
	runDreamCycle(ctx, supportDir, db, cfgStore, resolveProvider)
	saveDreamState(supportDir)
}

// StartDreamCycle launches a background goroutine that runs the four-phase
// consolidation cycle once per day at dreamHour local time. Returns a stop
// function that cancels the scheduler.
func StartDreamCycle(supportDir string, db *storage.DB, cfgStore *config.Store, resolveProvider ProviderResolver) func() {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		runDreamScheduler(ctx, supportDir, db, cfgStore, resolveProvider)
	}()

	return func() {
		cancel()
		wg.Wait()
	}
}

func runDreamScheduler(ctx context.Context, supportDir string, db *storage.DB, cfgStore *config.Store, resolveProvider ProviderResolver) {
	// Catch-up: if the last run was more than minRunInterval ago (or never ran),
	// fire a consolidation run after a short warmup delay instead of waiting
	// until the next scheduled 3 AM slot. This handles the common case where the
	// Mac was asleep or the daemon was offline at the scheduled time.
	if dreamNeedsCatchup(supportDir) {
		logstore.Write("info", fmt.Sprintf("Dream cycle: missed run detected — catch-up in %s", catchupDelay), nil)
		select {
		case <-ctx.Done():
			return
		case <-time.After(catchupDelay):
		}
		runDreamCycle(ctx, supportDir, db, cfgStore, resolveProvider)
		saveDreamState(supportDir)
	}

	for {
		// Calculate duration until next dreamHour.
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), dreamHour, 7, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		delay := next.Sub(now)

		logstore.Write("info", fmt.Sprintf("Dream cycle: next run in %s at %s",
			delay.Round(time.Minute), next.Format("15:04")), nil)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			runDreamCycle(ctx, supportDir, db, cfgStore, resolveProvider)
			saveDreamState(supportDir)
		}
	}
}

// dreamNeedsCatchup returns true if no dream cycle has run within minRunInterval.
func dreamNeedsCatchup(supportDir string) bool {
	data, err := os.ReadFile(filepath.Join(supportDir, dreamStateFile))
	if err != nil {
		// File doesn't exist — never run.
		return true
	}
	var state dreamState
	if err := json.Unmarshal(data, &state); err != nil || state.LastRunAt == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, state.LastRunAt)
	if err != nil {
		return true
	}
	return time.Since(last) > minRunInterval
}

// saveDreamState writes the current time as the last successful dream run.
func saveDreamState(supportDir string) {
	state := dreamState{LastRunAt: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	path := filepath.Join(supportDir, dreamStateFile)
	_ = atomicWrite(path, data, 0o600)
}

func runDreamCycle(ctx context.Context, supportDir string, db *storage.DB, cfgStore *config.Store, resolveProvider ProviderResolver) {
	start := time.Now()
	logstore.Write("info", "Dream cycle: starting", nil)

	cfg := cfgStore.Load()
	if !cfg.MemoryEnabled {
		logstore.Write("info", "Dream cycle: memory disabled, skipping", nil)
		return
	}

	// Resolve a fresh provider for AI calls.
	var provider agent.ProviderConfig
	if resolveProvider != nil {
		var err error
		provider, err = resolveProvider()
		if err != nil {
			logstore.Write("warn", "Dream cycle: no AI provider available: "+err.Error(), nil)
			// Still run non-AI phases (prune, merge).
		}
	}

	// Phase 1: Prune stale memories.
	pruned := phasePrune(db)

	// Phase 2: Merge near-duplicate memories.
	merged := phaseMerge(db)

	// Phase 3: Synthesize tool outcome memories into SKILLS.md (requires AI).
	if provider.Type != "" {
		phaseToolOutcomeSynthesis(ctx, provider, supportDir, db)
	}

	// Phase 4: Write diary entries from today's significant conversations (requires AI).
	// The dream cycle is the sole owner of the diary — no per-turn or skill writes.
	diaryWritten := 0
	if provider.Type != "" {
		diaryWritten = phaseDiaryWrite(ctx, provider, supportDir, db)
	}

	// Phase 5: Synthesize diary entries into long-term memories (requires AI).
	synthesized := 0
	if provider.Type != "" {
		synthesized = phaseDiarySynthesis(ctx, provider, supportDir, db)
	}

	// Phase 6: Refresh MIND.md with current memories + diary (requires AI).
	if provider.Type != "" {
		phaseMindRefresh(ctx, provider, supportDir, db)
	}

	elapsed := time.Since(start).Round(time.Second)
	logstore.Write("info", fmt.Sprintf(
		"Dream cycle: complete in %s — pruned %d, merged %d, diary %d, synthesized %d",
		elapsed, pruned, merged, diaryWritten, synthesized),
		map[string]string{
			"pruned":      fmt.Sprintf("%d", pruned),
			"merged":      fmt.Sprintf("%d", merged),
			"diary":       fmt.Sprintf("%d", diaryWritten),
			"synthesized": fmt.Sprintf("%d", synthesized),
		})
}

// ── Phase 1: Prune ──────────────────────────────────────────────────────────

func phasePrune(db *storage.DB) int {
	// Low-confidence memories older than 30 days.
	// Never-retrieved memories older than 60 days with importance < 0.7.
	pruned := db.DeleteStaleMemories(30, 60, 0.5, 0.7)
	if pruned > 0 {
		logstore.Write("info", fmt.Sprintf("Dream: pruned %d stale memories", pruned), nil)
	}
	return pruned
}

// ── Phase 2: Merge near-duplicates ──────────────────────────────────────────

func phaseMerge(db *storage.DB) int {
	all, err := db.ListAllMemories()
	if err != nil || len(all) < 2 {
		return 0
	}

	// Group by category.
	groups := map[string][]storage.MemoryRow{}
	for _, m := range all {
		groups[m.Category] = append(groups[m.Category], m)
	}

	merged := 0
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, mems := range groups {
		if len(mems) < 2 {
			continue
		}
		// Find pairs with identical titles (different IDs).
		// The regex extractor already deduplicates on (category, title), but
		// LLM extraction or diary synthesis could create near-duplicates.
		seen := map[string]*storage.MemoryRow{}
		for i := range mems {
			m := &mems[i]
			key := strings.ToLower(strings.TrimSpace(m.Title))
			if existing, ok := seen[key]; ok {
				// Merge: keep the one with higher importance, combine content.
				keeper, discard := existing, m
				if m.Importance > existing.Importance {
					keeper, discard = m, existing
				}
				// Prefer longer content.
				if len(discard.Content) > len(keeper.Content) {
					keeper.Content = discard.Content
				}
				// Take max scores.
				if discard.Importance > keeper.Importance {
					keeper.Importance = discard.Importance
				}
				if discard.Confidence > keeper.Confidence {
					keeper.Confidence = discard.Confidence
				}
				keeper.IsUserConfirmed = keeper.IsUserConfirmed || discard.IsUserConfirmed
				keeper.UpdatedAt = now

				// Merge tags.
				var keeperTags, discardTags []string
				json.Unmarshal([]byte(keeper.TagsJSON), &keeperTags)   //nolint:errcheck
				json.Unmarshal([]byte(discard.TagsJSON), &discardTags) //nolint:errcheck
				tagSet := map[string]bool{}
				for _, t := range keeperTags {
					tagSet[t] = true
				}
				for _, t := range discardTags {
					if !tagSet[t] {
						keeperTags = append(keeperTags, t)
					}
				}
				if b, err := json.Marshal(keeperTags); err == nil {
					keeper.TagsJSON = string(b)
				}

				db.UpdateMemory(*keeper) //nolint:errcheck
				db.DeleteMemory(discard.ID)
				merged++
				seen[key] = keeper
			} else {
				seen[key] = m
			}
		}
	}

	// Content-hash deduplication: merge memories with different titles but
	// highly similar content (>80% word overlap). Catches near-duplicates
	// that the title-based pass above misses — e.g. LLM extraction producing
	// "User prefers dark mode" and diary synthesis producing "Dark mode preference".
	for cat, mems := range groups {
		if len(mems) < 2 {
			continue
		}
		// Rebuild group from DB in case title-merge removed some.
		fresh, err := db.ListMemories(200, cat)
		if err != nil || len(fresh) < 2 {
			continue
		}
		type fpEntry struct {
			row *storage.MemoryRow
			fp  map[string]bool
		}
		var entries []fpEntry
		for i := range fresh {
			entries = append(entries, fpEntry{row: &fresh[i], fp: contentFingerprint(fresh[i].Content)})
		}
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if jaccardSimilarity(entries[i].fp, entries[j].fp) < 0.80 {
					continue
				}
				keeper, discard := entries[i].row, entries[j].row
				if discard.Importance > keeper.Importance || (discard.Importance == keeper.Importance && len(discard.Content) > len(keeper.Content)) {
					keeper, discard = discard, keeper
				}
				keeper.IsUserConfirmed = keeper.IsUserConfirmed || discard.IsUserConfirmed
				if discard.Confidence > keeper.Confidence {
					keeper.Confidence = discard.Confidence
				}
				keeper.UpdatedAt = now
				db.UpdateMemory(*keeper) //nolint:errcheck
				db.DeleteMemory(discard.ID)
				merged++
				// Remove j from entries and re-check.
				entries = append(entries[:j], entries[j+1:]...)
				j--
			}
		}
	}

	if merged > 0 {
		logstore.Write("info", fmt.Sprintf("Dream: merged %d duplicate memories", merged), nil)
	}
	return merged
}

// contentFingerprint returns a word-set from the first 100 characters of
// normalized content, used for Jaccard similarity comparison.
func contentFingerprint(content string) map[string]bool {
	lower := strings.ToLower(content)
	runes := []rune(lower)
	if len(runes) > 100 {
		runes = runes[:100]
	}
	words := strings.Fields(string(runes))
	fp := make(map[string]bool, len(words))
	for _, w := range words {
		// Strip punctuation from edges.
		w = strings.Trim(w, ".,!?;:'\"()-")
		if len(w) > 1 {
			fp[w] = true
		}
	}
	return fp
}

// jaccardSimilarity returns |A∩B| / |A∪B| for two word sets.
func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	intersection := 0
	for w := range a {
		if b[w] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// ── Phase 3: Tool outcome synthesis ─────────────────────────────────────────

// phaseToolOutcomeSynthesis queries tool_learning memories from the last 30 days,
// groups them by skill, asks the AI to synthesize actionable lessons, and writes
// the result to the "## Synthesized Tool Notes" section of SKILLS.md.
// This is the automated replacement for the manual "Things That Don't Work" section.
func phaseToolOutcomeSynthesis(ctx context.Context, provider agent.ProviderConfig, supportDir string, db *storage.DB) {
	skillsPath := filepath.Join(supportDir, "SKILLS.md")
	skillsData, err := os.ReadFile(skillsPath)
	if err != nil {
		return
	}

	// Fetch tool_learning memories from the last 30 days.
	all, err := db.ListMemories(100, "tool_learning")
	if err != nil || len(all) == 0 {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	var recent []storage.MemoryRow
	for _, m := range all {
		if m.CreatedAt >= cutoff && m.Confidence >= 0.5 {
			recent = append(recent, m)
		}
	}
	if len(recent) == 0 {
		return
	}

	var memBlock strings.Builder
	for _, m := range recent {
		memBlock.WriteString(fmt.Sprintf("- [c:%.2f] %s: %s\n", m.Confidence, m.Title, m.Content))
	}

	system := `You are Atlas reviewing tool outcome memories to synthesize practical notes.

Return ONLY the updated content for the "## Synthesized Tool Notes" section of SKILLS.md.
Format each note as: "- [skill.name] lesson (confidence: high/medium)"
Rules:
- Only include lessons with enough evidence (confidence >= 0.6, or multiple occurrences)
- Group related notes by skill
- Be specific: "weather.current fails with ICAO airport codes — use IATA instead" not "weather can fail"
- Max 8 notes total
- Return empty string if no notes are warranted
- Return ONLY the note lines, no section header`

	userContent := fmt.Sprintf("Tool outcome memories (last 30 days):\n%s\n\nWrite the Synthesized Tool Notes content:",
		truncate(memBlock.String(), 2000))

	reply, err := callFast(ctx, provider, system, userContent)
	if err != nil {
		logstore.Write("warn", "Dream: tool outcome synthesis AI call failed: "+err.Error(), nil)
		return
	}

	notes := strings.TrimSpace(reply)
	if notes == "" {
		return
	}

	current := strings.TrimSpace(string(skillsData))
	updated := replaceSKILLSSection(current, "## Synthesized Tool Notes", notes)
	updated = updateSkillsDate(updated)

	if err := atomicWrite(skillsPath, []byte(updated+"\n"), 0o600); err != nil {
		logstore.Write("warn", "Dream: tool notes write failed: "+err.Error(), nil)
		return
	}
	logstore.Write("info", fmt.Sprintf("Dream: synthesized %d tool outcome notes into SKILLS.md", len(recent)), nil)
}

// replaceSKILLSSection splices new content into a named "## Header" section of SKILLS.md.
// If the section is missing, it is appended.
func replaceSKILLSSection(content, header, newBody string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != header {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				end = j
				break
			}
		}
		result := make([]string, 0, len(lines)+3)
		result = append(result, lines[:i+1]...)
		result = append(result, "")
		result = append(result, newBody)
		result = append(result, "")
		result = append(result, lines[end:]...)
		return strings.Join(result, "\n")
	}
	return strings.TrimRight(content, "\n") + "\n\n---\n\n" + header + "\n\n" + newBody + "\n"
}

// ── Phase 4: Diary synthesis ────────────────────────────────────────────────

// ── Phase 4: Diary write ────────────────────────────────────────────────────

// phaseDiaryWrite reviews today's conversations and writes 0–3 diary entries
// for moments that genuinely mattered. The dream cycle is the sole diary owner.
func phaseDiaryWrite(ctx context.Context, provider agent.ProviderConfig, supportDir string, db *storage.DB) int {
	convs, err := db.ListConversationSummaries(50)
	if err != nil || len(convs) == 0 {
		return 0
	}

	// Filter to conversations active today.
	today := time.Now().Format("2006-01-02")
	var todayConvs []storage.ConversationSummaryRow
	for _, c := range convs {
		if strings.HasPrefix(c.UpdatedAt, today) || strings.HasPrefix(c.CreatedAt, today) {
			todayConvs = append(todayConvs, c)
		}
	}
	if len(todayConvs) == 0 {
		return 0
	}

	var sb strings.Builder
	for i, c := range todayConvs {
		first, last := "", ""
		if c.FirstUserMessage != nil {
			first = *c.FirstUserMessage
		}
		if c.LastAssistantMessage != nil {
			last = *c.LastAssistantMessage
		}
		sb.WriteString(fmt.Sprintf("Conversation %d (%d messages):\nUser: %s\nAtlas: %s\n\n",
			i+1, c.MessageCount,
			truncate(first, 300),
			truncate(last, 300),
		))
	}

	system := `You are Atlas reviewing today's conversations to decide what, if anything, deserves a diary entry.

The diary is a permanent record of moments that genuinely mattered — not a log of activity.
Write an entry ONLY if a conversation contains at least one of the following:

- A milestone: something was shipped, built, fixed, or completed that represents real progress
- A first: a new capability, integration, or behavior that Atlas can now do for the first time
- A discovery: a non-obvious insight about the user, their work, their life, or how Atlas should operate
- A decision that changes direction: a pivot, a new commitment, a deliberate choice with lasting consequences
- A relationship moment: a deepening of trust, an explicit statement of how the user wants to work with Atlas,
  or a moment that defined the agent-user dynamic
- A significant life event: something meaningful happened in the user's world that Atlas witnessed

Hard exclusions — never write an entry for:
- Routine tasks, lookups, or questions with no lasting significance
- UI tweaks, config changes, or minor fixes
- Anything that will be irrelevant or forgotten within a week
- Conversations with no clear outcome

Return a JSON array of 0–3 strings. Each string is one diary entry: a single sharp sentence,
first person past tense, max 120 characters, capturing what happened and why it mattered.
Return [] if today held nothing that clears the bar above — most days won't. No markdown, no quotes around the entries.`

	messages := []agent.OAIMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: "Today's conversations:\n\n" + truncate(sb.String(), 4000)},
	}

	reply, _, _, err := agent.CallAINonStreamingExported(ctx, provider, messages, nil)
	if err != nil {
		logstore.Write("warn", "Dream: diary write AI call failed: "+err.Error(), nil)
		return 0
	}
	replyStr, ok := reply.Content.(string)
	if !ok {
		return 0
	}
	replyStr = strings.TrimSpace(replyStr)
	if strings.HasPrefix(replyStr, "```") {
		if idx := strings.Index(replyStr, "\n"); idx >= 0 {
			replyStr = replyStr[idx+1:]
		}
		if idx := strings.LastIndex(replyStr, "```"); idx >= 0 {
			replyStr = replyStr[:idx]
		}
		replyStr = strings.TrimSpace(replyStr)
	}

	var entries []string
	if err := json.Unmarshal([]byte(replyStr), &entries); err != nil {
		logstore.Write("debug", "Dream: diary write invalid JSON: "+err.Error(), nil)
		return 0
	}

	written := 0
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if e, err := features.AppendDiaryEntry(supportDir, entry); err == nil && e != "" {
			written++
		}
	}
	if written > 0 {
		logstore.Write("info", fmt.Sprintf("Dream: wrote %d diary entries", written), nil)
	}
	return written
}

// ── Phase 5: Diary synthesis ────────────────────────────────────────────────

func phaseDiarySynthesis(ctx context.Context, provider agent.ProviderConfig, supportDir string, db *storage.DB) int {
	diary := features.DiaryContext(supportDir, 14)
	if diary == "" {
		return 0
	}

	system := `You analyze Atlas diary entries and extract recurring patterns, preferences, or behaviors worth remembering long-term.

Return a JSON array of objects. Each object has:
- "category": one of "preference", "workflow", "episodic"
- "title": short descriptive title (max 6 words)
- "content": one sentence describing the pattern
- "importance": 0.5-1.0

Rules:
- Only extract patterns that appear across MULTIPLE diary entries
- Skip one-off events unless they represent a major milestone
- Max 3 items
- Return [] if no clear patterns emerge`

	messages := []agent.OAIMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: "Diary entries from the last 14 days:\n\n" + truncate(diary, 3000)},
	}

	reply, _, _, err := agent.CallAINonStreamingExported(ctx, provider, messages, nil)
	if err != nil {
		logstore.Write("warn", "Dream: diary synthesis AI call failed: "+err.Error(), nil)
		return 0
	}

	replyStr, ok := reply.Content.(string)
	if !ok {
		return 0
	}
	replyStr = strings.TrimSpace(replyStr)

	// Strip markdown code fences.
	if strings.HasPrefix(replyStr, "```") {
		if idx := strings.Index(replyStr, "\n"); idx >= 0 {
			replyStr = replyStr[idx+1:]
		}
		if idx := strings.LastIndex(replyStr, "```"); idx >= 0 {
			replyStr = replyStr[:idx]
		}
		replyStr = strings.TrimSpace(replyStr)
	}

	var candidates []struct {
		Category   string  `json:"category"`
		Title      string  `json:"title"`
		Content    string  `json:"content"`
		Importance float64 `json:"importance"`
	}
	if err := json.Unmarshal([]byte(replyStr), &candidates); err != nil {
		logstore.Write("debug", "Dream: diary synthesis invalid JSON: "+err.Error(), nil)
		return 0
	}

	if len(candidates) > 3 {
		candidates = candidates[:3]
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	saved := 0

	for _, c := range candidates {
		if c.Title == "" || c.Content == "" {
			continue
		}
		if c.Importance < 0.5 || c.Importance > 1.0 {
			c.Importance = 0.7
		}

		existing, err := db.FindDuplicateMemory(c.Category, c.Title)
		if err != nil {
			continue
		}
		if existing != nil {
			// Update existing with newer content if longer.
			if len(c.Content) > len(existing.Content) {
				upd := *existing
				upd.Content = c.Content
				upd.UpdatedAt = now
				db.UpdateMemory(upd) //nolint:errcheck
			}
			continue
		}

		convID := "dream-cycle"
		row := storage.MemoryRow{
			ID:                    dreamMemoryID(),
			Category:              c.Category,
			Title:                 c.Title,
			Content:               c.Content,
			Source:                "diary_synthesis",
			Confidence:            0.80,
			Importance:            c.Importance,
			CreatedAt:             now,
			UpdatedAt:             now,
			TagsJSON:              `["dream","diary_synthesis"]`,
			RelatedConversationID: &convID,
		}
		db.SaveMemory(row) //nolint:errcheck
		saved++
	}

	if saved > 0 {
		logstore.Write("info", fmt.Sprintf("Dream: synthesized %d memories from diary", saved), nil)
	}
	return saved
}

// dreamMemoryID generates a random 16-byte hex memory ID for dream-created memories.
func dreamMemoryID() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// ── Phase 4: MIND refresh ───────────────────────────────────────────────────

func phaseMindRefresh(ctx context.Context, provider agent.ProviderConfig, supportDir string, db *storage.DB) {
	mindPath := filepath.Join(supportDir, "MIND.md")
	data, err := os.ReadFile(mindPath)
	if err != nil {
		return
	}
	current := strings.TrimSpace(string(data))
	if current == "" {
		return
	}

	// Build context: current memories + recent diary.
	mems, _ := db.ListAllMemories()
	var memBlock strings.Builder
	for _, m := range mems {
		memBlock.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Category, m.Title, m.Content))
	}

	diary := features.DiaryContext(supportDir, 14)

	system := `You are Atlas reviewing your MIND.md during a nightly consolidation cycle.

Return ONLY the sections you want to update, each with its exact "## " header.
Updatable sections:
- ## You
- ## How You Work
- ## What's Active
- ## What I've Learned

Rules:
- Return NOTHING for sections that don't need changes
- Do NOT return ## Identity, ## Current Frame, ## Commitments, or ## Today's Read — these are protected
- Do NOT include "# Mind of Atlas" or the metadata line
- Use memories and diary entries as evidence for updates
- Abstraction only — compress episodic evidence into high-level insights; never write raw observations ("user asked about X"), only what they reveal ("user approaches X by…")
- Confidence encoding — prefix beliefs in ## You, ## How You Work, ## What I've Learned with: **Confirmed** / **High confidence** / **Working theory**
- Remove outdated content contradicted by recent evidence
- First person throughout`

	userContent := fmt.Sprintf(`Current MIND.md:
%s

All stored memories:
%s

Recent diary (14 days):
%s

Return only sections that need updating based on the current evidence:`,
		truncateSandwich(current, 6000),
		truncate(memBlock.String(), 2000),
		truncate(diary, 1500),
	)

	reply, err := callFast(ctx, provider, system, userContent)
	if err != nil {
		logstore.Write("warn", "Dream: MIND refresh AI call failed: "+err.Error(), nil)
		return
	}

	patch := strings.TrimSpace(reply)
	if patch == "" {
		return
	}

	merged := mergeMindSections(current, patch)
	merged = updateReflectionDate(merged)

	if err := validateMindContent(merged); err != nil {
		logstore.Write("warn", "Dream: MIND refresh validation failed: "+err.Error(), nil)
		return
	}

	if err := atomicWrite(mindPath, []byte(merged), 0o600); err != nil {
		logstore.Write("warn", "Dream: MIND refresh write failed: "+err.Error(), nil)
		return
	}

	logstore.Write("info", "Dream: MIND.md refreshed from consolidated evidence", nil)
}
