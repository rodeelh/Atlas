// Package skills provides the built-in skill registry for the Go runtime agent loop.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/browser"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/storage"
	"atlas-runtime-go/internal/voice"
)

// ToolParam describes a single JSON schema parameter.
type ToolParam struct {
	Description string
	Type        string     // "string", "number", "integer", "boolean", "array"
	Enum        []string   // optional
	Items       *ToolParam // required when Type == "array"
}

// ToolDef is an OpenAI function definition.
type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]ToolParam
	Required    []string
	// RawSchema, when set, is used as the "parameters" object in MarshalOpenAI
	// instead of the Properties/Required fields. Custom skills use this to pass
	// arbitrary JSON Schema objects defined in their skill.json manifest.
	RawSchema map[string]any
}

// oaiName converts an internal action ID (e.g. "weather.current") to a name
// that satisfies the OpenAI function-name pattern ^[a-zA-Z0-9_-]+$.
// Dots are replaced with double-underscores so the namespace is still readable.
func oaiName(name string) string {
	return strings.ReplaceAll(name, ".", "__")
}

// fromOAIName is the inverse of oaiName — converts back for registry lookup.
func fromOAIName(name string) string {
	return strings.ReplaceAll(name, "__", ".")
}

// MarshalOpenAI returns the tool as an OpenAI "tool" object.
// When RawSchema is set it is used directly as the "parameters" object,
// allowing custom skills to declare arbitrary JSON Schema. Otherwise the
// parameters object is built from Properties and Required.
func (d ToolDef) MarshalOpenAI() map[string]any {
	var parameters map[string]any
	if d.RawSchema != nil {
		parameters = d.RawSchema
	} else {
		props := map[string]any{}
		for name, p := range d.Properties {
			prop := map[string]any{
				"type":        p.Type,
				"description": p.Description,
			}
			if len(p.Enum) > 0 {
				prop["enum"] = p.Enum
			}
			if p.Type == "array" && p.Items != nil {
				prop["items"] = map[string]any{"type": p.Items.Type}
			}
			props[name] = prop
		}
		required := d.Required
		if required == nil {
			required = []string{}
		}
		parameters = map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		}
	}

	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        oaiName(d.Name),
			"description": d.Description,
			"parameters":  parameters,
		},
	}
}

// SkillEntry is one callable action in the registry.
//
// ActionClass is the canonical impact classification. It drives confirmation
// policy via DefaultNeedsConfirmation. PermLevel is preserved for
// backward compatibility and for policy-file overrides.
//
// If FnResult is set it is preferred over Fn. Skills that need to return
// structured artifacts, support dry-run simulation, or provide idempotency
// checks should implement FnResult. Simple skills may use Fn; the registry
// wraps their string output into a ToolResult automatically.
type SkillEntry struct {
	Def         ToolDef
	PermLevel   string      // "read", "draft", "execute" — legacy; still used for policy overrides
	ActionClass ActionClass // canonical impact class; drives confirmation policy

	// Fn is the legacy skill function. Returns a plain string result.
	// Exactly one of Fn or FnResult must be set.
	Fn func(ctx context.Context, args json.RawMessage) (string, error)

	// FnResult is the preferred skill function. Returns a structured ToolResult.
	// Set this for skills that support dry-run, idempotency checks, or rich artifacts.
	FnResult func(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// VisionFn is the function signature for making a single vision inference call.
// imageB64 is a raw base64-encoded PNG; prompt is the instruction. The function
// is injected at startup via SetVisionFn so the skill layer never imports agent.
type VisionFn func(ctx context.Context, imageB64, prompt string) (string, error)

// ForgePersistFn creates and persists a Forge proposal from pre-researched agent
// data. Injected at startup via SetForgePersistFn so the skills package never
// imports the forge package (which would create an import cycle through agent).
//
// Returns the proposal ID, display name, skill ID, risk level, action names,
// and external domains on success.
type ForgePersistFn func(specJSON, plansJSON, summary, rationale, contractJSON string) (
	id, name, skillID, riskLevel string,
	actionNames, domains []string,
	err error,
)

// Registry maps action IDs to SkillEntry.
type Registry struct {
	entries        map[string]SkillEntry
	supportDir     string
	db             *storage.DB
	browserMgr     *browser.Manager
	voiceMgr       *voice.Manager
	visionFn       VisionFn
	forgePersistFn ForgePersistFn

	// policyCache avoids a per-tool-call disk read of action-policies.json.
	// Refreshed when the cached value is older than policyCacheTTL.
	policyMu      sync.Mutex
	policyCache   map[string]string
	policyCacheAt time.Time
}

const policyCacheTTL = 5 * time.Second

// NewRegistry creates a Registry with all built-in skills registered.
// Pass a non-nil browserMgr to enable browser control and session skills.
func NewRegistry(supportDir string, db *storage.DB, browserMgr *browser.Manager) *Registry {
	r := &Registry{
		entries:    make(map[string]SkillEntry),
		supportDir: supportDir,
		db:         db,
		browserMgr: browserMgr,
	}
	r.registerInfo()
	r.registerInfoSkill()
	r.registerWeather()
	r.registerWeb()
	r.registerFilesystem()
	r.registerSystem()
	r.registerTerminal()
	r.registerAppleScript()
	r.registerFinance()
	r.registerImage()
	r.registerGremlin()
	r.registerWebSearch()
	r.registerForge()
	r.registerVault()
	r.registerBrowser()
	r.registerMemory()
	r.registerVoice()
	return r
}

// register adds a skill entry to the registry.
func (r *Registry) register(entry SkillEntry) {
	// Validate that exactly one of Fn or FnResult is set.
	if entry.Fn == nil && entry.FnResult == nil {
		panic(fmt.Sprintf("skills: %s registered with neither Fn nor FnResult", entry.Def.Name))
	}
	if entry.Fn != nil && entry.FnResult != nil {
		panic(fmt.Sprintf("skills: %s registered with both Fn and FnResult — pick one", entry.Def.Name))
	}
	// Default ActionClass from PermLevel when not explicitly set.
	if entry.ActionClass == "" {
		entry.ActionClass = defaultActionClass(entry.PermLevel)
	}
	r.entries[entry.Def.Name] = entry
}

// RegisterExternal adds a module-owned action to the skill registry.
// Runtime modules use this to expose canonical agent controls without moving
// their implementation back into the legacy skills package.
func (r *Registry) RegisterExternal(entry SkillEntry) {
	r.register(entry)
}

// defaultActionClass derives a reasonable ActionClass from the legacy PermLevel.
// Callers should set ActionClass explicitly for accurate classification.
func defaultActionClass(permLevel string) ActionClass {
	switch permLevel {
	case "read":
		return ActionClassRead
	case "draft":
		return ActionClassLocalWrite
	case "execute":
		return ActionClassExternalSideEffect
	}
	return ActionClassExternalSideEffect // safe default
}

// ToolCount returns the total number of registered tools.
func (r *Registry) ToolCount() int { return len(r.entries) }

// ToolDefinitions returns the OpenAI tools array (all registered actions).
func (r *Registry) ToolDefinitions() []map[string]any {
	out := make([]map[string]any, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.Def.MarshalOpenAI())
	}
	return out
}

// ToolDefsForGroups returns tools whose capability group is in groups. Unknown
// group names are ignored. Core tools are always included as context helpers.
func (r *Registry) ToolDefsForGroups(groups []string) []map[string]any {
	wanted := map[string]bool{"core": true}
	for _, group := range groups {
		group = strings.ToLower(strings.TrimSpace(group))
		if group != "" {
			wanted[group] = true
		}
	}
	out := make([]map[string]any, 0, len(r.entries))
	for _, e := range r.entries {
		if wanted[toolCapabilityGroup(e.Def.Name)] {
			out = append(out, e.Def.MarshalOpenAI())
		}
	}
	return out
}

// toolCapabilityGroup returns which capability group a tool name belongs to.
// Groups drive selective injection in SelectiveToolDefs.
//
// "core" is always-on. All other groups are scored against the user message
// via scoreGroups() in heuristic.go and activated when they meet their threshold.
func toolCapabilityGroup(name string) string {
	switch {
	// Always-on: time/date utilities only
	case name == "info.current_time",
		name == "info.current_date",
		name == "info.timezone_convert":
		return "core"

	// Atlas runtime status — only when user asks about Atlas itself
	case strings.HasPrefix(name, "atlas."):
		return "meta"

	// Scored groups
	case strings.HasPrefix(name, "weather."):
		return "weather"
	case strings.HasPrefix(name, "web."), strings.HasPrefix(name, "websearch."):
		return "web"
	case strings.HasPrefix(name, "finance."),
		name == "info.currency_for_location",
		name == "info.currency_convert":
		return "finance"
	case strings.HasPrefix(name, "applescript.calendar_"),
		strings.HasPrefix(name, "applescript.reminders_"),
		strings.HasPrefix(name, "applescript.mail_"),
		strings.HasPrefix(name, "applescript.contacts_"),
		strings.HasPrefix(name, "applescript.notes_"):
		return "office"
	case strings.HasPrefix(name, "applescript.music_"),
		strings.HasPrefix(name, "applescript.safari_"),
		name == "applescript.system_info":
		return "media"
	case strings.HasPrefix(name, "system."):
		return "mac"
	case strings.HasPrefix(name, "terminal."),
		name == "applescript.run_custom":
		return "shell"
	case strings.HasPrefix(name, "fs."):
		return "files"
	case strings.HasPrefix(name, "vault."):
		return "vault"
	case strings.HasPrefix(name, "browser."):
		return "browser"
	case strings.HasPrefix(name, "voice."):
		return "voice"
	case strings.HasPrefix(name, "communication."):
		return "communication"
	case strings.HasPrefix(name, "image."):
		return "creative"
	case strings.HasPrefix(name, "workflow."):
		return "workflow"
	case strings.HasPrefix(name, "automation."), strings.HasPrefix(name, "gremlin."):
		return "automation"
	case strings.HasPrefix(name, "forge."):
		return "forge"
	case strings.HasPrefix(name, "dashboard."):
		return "dashboards"
	default:
		return "custom"
	}
}

// groupThresholds defines the minimum score a group must reach to be included.
// Higher thresholds require stronger, more explicit intent signals.
//
//	≥ 1  most groups — any clear signal is enough
//	≥ 2  files, browser — privacy-sensitive or large; reduce false positives
//	≥ 3  shell — destructive; require unambiguous explicit intent
//
// "core" has no entry — it is always-on (no threshold needed).
// "meta" covers atlas.* runtime-status tools — only injected when explicitly asked.
var groupThresholds = map[string]int{
	"meta":          1,
	"weather":       1,
	"web":           1,
	"finance":       1,
	"office":        1,
	"media":         1,
	"mac":           1,
	"shell":         3,
	"files":         2,
	"vault":         1,
	"browser":       2,
	"voice":         1,
	"communication": 1,
	"creative":      1,
	"workflow":      1,
	"automation":    1,
	"forge":         1,
	"dashboards":    1,
	"custom":        1,
}

// SelectiveToolDefs returns a bounded tool set for the given user message.
//
// Always-on (every turn):
//   - "core" — info.current_time, info.current_date, info.timezone_convert
//
// Score-triggered (added when group score meets its threshold):
//   - meta ≥1 (atlas.info — only when user asks about Atlas/runtime status)
//   - weather ≥1, web ≥1, finance ≥1, office ≥1, media ≥1, mac ≥1
//   - vault ≥1, creative ≥1, automation ≥1, forge ≥1, dashboards ≥1
//   - files ≥2, browser ≥2
//   - shell ≥3
//
// Custom skills are scored individually: included when their name or
// description shares a meaningful token with the user message.
//
// The hard cap in agent/loop.go (capToolsForProvider) is the final safety net.
func (r *Registry) SelectiveToolDefs(userMessage string) []map[string]any {
	// core is always-on.
	triggered := map[string]bool{"core": true}

	// Score all groups and activate those meeting their threshold.
	scores := scoreGroups(userMessage)
	for group, score := range scores {
		threshold, ok := groupThresholds[group]
		if ok && score >= threshold {
			triggered[group] = true
		}
	}

	// Build message token set once for custom skill scoring.
	msgTokens := make(map[string]bool)
	for _, t := range tokenize(userMessage) {
		msgTokens[t] = true
	}

	// Custom skills behave like any other group: included only when the
	// "custom" group fired (the user explicitly mentioned custom/installed
	// skills — see intentSignals["custom"] in heuristic.go). msgTokens is no
	// longer used for per-skill matching.
	_ = msgTokens

	out := make([]map[string]any, 0, len(r.entries))
	var customIncluded, customTotal int
	for _, e := range r.entries {
		group := toolCapabilityGroup(e.Def.Name)
		if group == "custom" {
			customTotal++
		}
		if triggered[group] {
			out = append(out, e.Def.MarshalOpenAI())
			if group == "custom" {
				customIncluded++
			}
		}
	}

	// Log which groups fired, their scores, and custom match rate.
	activeGroups := make([]string, 0, len(triggered))
	for g := range triggered {
		activeGroups = append(activeGroups, g)
	}
	logstore.Write("debug",
		fmt.Sprintf("Tool selection: %d tools | groups: %v | scores: %v | custom: %d/%d",
			len(out), activeGroups, scores, customIncluded, customTotal),
		map[string]string{"mode": "heuristic"})

	return out
}

// customSkillMatches returns true when at least one meaningful word token from
// the skill's name or description appears in the message token set.
// Single-character tokens and very common stop words are skipped.
func customSkillMatches(def ToolDef, msgTokens map[string]bool) bool {
	// Stop words that appear in almost every skill description and carry no signal.
	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true,
		"to": true, "of": true, "in": true, "for": true, "with": true,
		"on": true, "at": true, "by": true, "it": true, "is": true,
		"be": true, "as": true, "do": true, "get": true, "set": true,
		"use": true, "can": true, "has": true, "run": true, "new": true,
	}
	check := func(s string) bool {
		for _, t := range tokenize(s) {
			if len(t) <= 1 || stopWords[t] {
				continue
			}
			if msgTokens[t] {
				return true
			}
		}
		return false
	}
	return check(def.Name) || check(def.Description)
}

// Canonicalize converts an AI-facing action name (may use __ encoding) to the
// internal dot-separated form. Safe to call with already-canonical IDs.
func (r *Registry) Canonicalize(actionID string) string {
	return r.normalise(actionID)
}

// normalise converts an actionID arriving from the AI (which uses oaiName encoding)
// back to the internal dot-separated form used as registry keys.
func (r *Registry) normalise(actionID string) string {
	// If it's already in the registry as-is, use it directly.
	if _, ok := r.entries[actionID]; ok {
		return actionID
	}
	// Try converting __ → . (AI sent the OAI-safe name back).
	canonical := fromOAIName(actionID)
	if _, ok := r.entries[canonical]; ok {
		return canonical
	}
	return actionID
}

// NeedsApproval checks whether actionID requires user confirmation before
// execution. The decision is made in two layers:
//
//  1. ActionClass → DefaultNeedsConfirmation() provides the base policy.
//  2. action-policies.json overrides (keyed by action ID) can force
//     "auto_approve" or "always_ask" for individual actions.
//
// Unknown actions default to requiring approval (safe fallback).
func (r *Registry) NeedsApproval(actionID string) bool {
	actionID = r.normalise(actionID)
	e, ok := r.entries[actionID]
	if !ok {
		return true // unknown action — require approval
	}

	// Layer 1: ActionClass-driven default.
	base := DefaultNeedsConfirmation(e.ActionClass)

	// Layer 2: per-action policy override.
	policy := r.loadPolicy(actionID)
	switch policy {
	case "auto_approve":
		return false
	case "always_ask":
		return true
	}

	return base
}

// IsStateful returns true for tools that share process-level state and must
// not run concurrently with other calls in the same batch. Currently covers
// all browser.* tools, which share a single go-rod Chrome session.
// Add new entries here whenever a skill holds exclusive locks or shared sessions.
func (r *Registry) IsStateful(actionID string) bool {
	actionID = r.normalise(actionID)
	return strings.HasPrefix(actionID, "browser.")
}

// GetActionClass returns the ActionClass for actionID.
// Returns ActionClassExternalSideEffect for unknown actions.
func (r *Registry) GetActionClass(actionID string) ActionClass {
	actionID = r.normalise(actionID)
	e, ok := r.entries[actionID]
	if !ok {
		return ActionClassExternalSideEffect
	}
	return e.ActionClass
}

// PermissionLevel returns the PermLevel for actionID, defaults to "execute".
func (r *Registry) PermissionLevel(actionID string) string {
	actionID = r.normalise(actionID)
	e, ok := r.entries[actionID]
	if !ok {
		return "execute"
	}
	return e.PermLevel
}

// Execute runs actionID with the given args and returns a structured ToolResult.
//
// In dry-run mode (IsDryRun(ctx) == true):
//   - Read-class actions execute normally (they have no side effects).
//   - All other action classes return a synthetic DryRunResult without invoking
//     the underlying skill function. Skills with FnResult may also intrinsect
//     IsDryRun(ctx) to return a richer simulation — the registry will call them
//     and use their result if they return DryRun==true.
func (r *Registry) Execute(ctx context.Context, actionID string, args json.RawMessage) (ToolResult, error) {
	actionID = r.normalise(actionID)
	e, ok := r.entries[actionID]
	if !ok {
		return ToolResult{}, fmt.Errorf("unknown action: %s", actionID)
	}

	// Dry-run gate for non-read actions.
	if IsDryRun(ctx) && e.ActionClass != ActionClassRead {
		// Let FnResult skills provide their own simulation first.
		if e.FnResult != nil {
			res, err := e.FnResult(ctx, args)
			if err == nil && res.DryRun {
				return res, nil
			}
			// If skill didn't handle dry-run, fall through to synthetic result.
		}
		return DryRunResult(
			fmt.Sprintf("would execute %s", actionID),
			fmt.Sprintf("call %s with args %s", actionID, RedactArgs(args)),
			actionID,
		), nil
	}

	if e.FnResult != nil {
		return e.FnResult(ctx, args)
	}

	// Legacy Fn path — wrap string result in ToolResult.
	s, err := e.Fn(ctx, args)
	return wrapStringResult(actionID, s, err), err
}

// SetVisionFn wires in a vision inference callback used by browser.solve_captcha.
// Must be called after the skills registry is constructed.
func (r *Registry) SetVisionFn(fn VisionFn) {
	r.visionFn = fn
}

// SetForgePersistFn wires in the Forge persistence callback used by
// forge.orchestration.propose. Must be called after both the skills registry
// and forge service are constructed.
func (r *Registry) SetForgePersistFn(fn ForgePersistFn) {
	r.forgePersistFn = fn
}

// loadPolicy returns the approval policy for actionID from a short-lived in-memory
// cache backed by action-policies.json. The cache refreshes every policyCacheTTL
// (5 s) so UI policy changes take effect quickly without a disk read per call.
func (r *Registry) loadPolicy(actionID string) string {
	r.policyMu.Lock()
	defer r.policyMu.Unlock()
	if r.policyCache == nil || time.Since(r.policyCacheAt) > policyCacheTTL {
		data, err := os.ReadFile(filepath.Join(r.supportDir, "action-policies.json"))
		if err == nil {
			var policies map[string]string
			if json.Unmarshal(data, &policies) == nil {
				r.policyCache = policies
			} else {
				r.policyCache = map[string]string{}
			}
		} else {
			r.policyCache = map[string]string{}
		}
		r.policyCacheAt = time.Now()
	}
	return r.policyCache[actionID]
}
