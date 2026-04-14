// Package main is a real-time evaluation harness for the Atlas Forge pipeline.
// It runs 20 ProposeRequests of increasing complexity, scores each proposal on
// seven quality dimensions, and emits a final report with actionable recommendations.
//
// Usage:
//
//	ANTHROPIC_API_KEY=sk-ant-... go run ./cmd/forge-eval
//
// Each proposal makes 2 AI calls (draft + formalize = 40 calls total).
// A short delay between cases avoids rate-limit bursts.
// Results are printed in real-time; the summary report follows at the end.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"atlas-runtime-go/internal/forge"
	"atlas-runtime-go/internal/forge/forgetypes"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	defaultModel  = "claude-sonnet-4-6"
	caseTimeout   = 4 * time.Minute
	betweenCases  = 900 * time.Millisecond
	passThreshold = 85
)

// ── ANSI helpers ──────────────────────────────────────────────────────────────

const (
	clrGreen  = "\033[32m"
	clrRed    = "\033[31m"
	clrYellow = "\033[33m"
	clrCyan   = "\033[36m"
	clrBold   = "\033[1m"
	clrReset  = "\033[0m"
)

func chk(ok bool) string {
	if ok {
		return clrGreen + "✓" + clrReset
	}
	return clrRed + "✗" + clrReset
}

func scoreColor(s int) string {
	if s >= passThreshold {
		return clrGreen
	}
	if s >= 60 {
		return clrYellow
	}
	return clrRed
}

// ── Anthropic provider ────────────────────────────────────────────────────────

type anthropicProvider struct {
	apiKey string
	model  string
	client *http.Client
	calls  atomic.Int64
}

func newAnthropicProvider(apiKey, model string) *anthropicProvider {
	return &anthropicProvider{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 90 * time.Second},
	}
}

func (p *anthropicProvider) CallNonStreaming(ctx context.Context, system, user string) (string, error) {
	p.calls.Add(1)
	body := map[string]any{
		"model":      p.model,
		"max_tokens": 4096,
		"system":     system,
		"messages":   []map[string]any{{"role": "user", "content": user}},
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d: %.300s", resp.StatusCode, string(raw))
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty content array")
	}
	return result.Content[0].Text, nil
}

// ── Logging provider wrapper ──────────────────────────────────────────────────
// Wraps forge.AIProvider and records per-stage timing.
// Each call increments an internal counter: odd = draft, even = formalize.

type stageRecord struct {
	name     string // "draft" | "formalize"
	duration time.Duration
	err      error
}

type loggingProvider struct {
	inner  forge.AIProvider
	stages []stageRecord
	call   int
}

func newLoggingProvider(inner forge.AIProvider) *loggingProvider {
	return &loggingProvider{inner: inner}
}

func (p *loggingProvider) CallNonStreaming(ctx context.Context, system, user string) (string, error) {
	p.call++
	name := "draft"
	if p.call%2 == 0 {
		name = "formalize"
	}
	start := time.Now()
	out, err := p.inner.CallNonStreaming(ctx, system, user)
	p.stages = append(p.stages, stageRecord{
		name:     name,
		duration: time.Since(start),
		err:      err,
	})
	return out, err
}

// ── Test cases — 20 in five tiers ────────────────────────────────────────────

type testCase struct {
	id          int
	name        string
	description string
	apiURL      string
	tier        string // simple | medium | complex | local | workflow
	kind        string // http | local | workflow
}

var cases = []testCase{
	// ── Tier 1: Simple HTTP, no auth, single action (1–5) ─────────────────
	{
		id:          1,
		name:        "ISS Position",
		description: "Get the current latitude and longitude of the International Space Station",
		apiURL:      "https://api.open-notify.org/iss-now.json",
		tier:        "simple",
		kind:        "http",
	},
	{
		id:          2,
		name:        "Random Cat Fact",
		description: "Fetch a random cat fact",
		apiURL:      "https://catfact.ninja/fact",
		tier:        "simple",
		kind:        "http",
	},
	{
		id:          3,
		name:        "Random Dog Image",
		description: "Get a random dog photo URL, optionally filtered by breed",
		apiURL:      "https://dog.ceo/api/breeds/image/random",
		tier:        "simple",
		kind:        "http",
	},
	{
		id:          4,
		name:        "Open Library Search",
		description: "Search for books by title, author, or subject across the Open Library catalog",
		apiURL:      "https://openlibrary.org/search.json",
		tier:        "simple",
		kind:        "http",
	},
	{
		id:          5,
		name:        "IP Info Lookup",
		description: "Return geolocation, hostname, and ASN data for any IP address",
		apiURL:      "https://ipinfo.io",
		tier:        "simple",
		kind:        "http",
	},

	// ── Tier 2: HTTP with API key, 2 actions (6–10) ───────────────────────
	{
		id:          6,
		name:        "GitHub Profile",
		description: "Look up a GitHub user's profile and list their public repositories",
		apiURL:      "https://api.github.com",
		tier:        "medium",
		kind:        "http",
	},
	{
		id:          7,
		name:        "OpenWeatherMap",
		description: "Get current weather conditions and a 5-day forecast for any city",
		apiURL:      "https://api.openweathermap.org/data/2.5",
		tier:        "medium",
		kind:        "http",
	},
	{
		id:          8,
		name:        "NewsAPI Headlines",
		description: "Fetch today's top news headlines and search articles by keyword",
		apiURL:      "https://newsapi.org/v2",
		tier:        "medium",
		kind:        "http",
	},
	{
		id:          9,
		name:        "Alpha Vantage Stocks",
		description: "Get a real-time stock quote and historical daily closing prices",
		apiURL:      "https://www.alphavantage.co/query",
		tier:        "medium",
		kind:        "http",
	},
	{
		id:          10,
		name:        "Airtable Records",
		description: "List records from an Airtable base table and create a new record",
		apiURL:      "https://api.airtable.com/v0",
		tier:        "medium",
		kind:        "http",
	},

	// ── Tier 3: Complex auth, multi-action, mutations (11–14) ────────────
	{
		id:          11,
		name:        "Notion Pages",
		description: "Search Notion pages by query and create new pages in a Notion database",
		apiURL:      "https://api.notion.com/v1",
		tier:        "complex",
		kind:        "http",
	},
	{
		id:          12,
		name:        "Linear Issues",
		description: "List open Linear issues, filter by label, and create a new issue in a team",
		apiURL:      "https://api.linear.app/graphql",
		tier:        "complex",
		kind:        "http",
	},
	{
		id:          13,
		name:        "Todoist Tasks",
		description: "List active Todoist tasks, add a new task with due date, and mark a task complete",
		apiURL:      "https://api.todoist.com/rest/v2",
		tier:        "complex",
		kind:        "http",
	},
	{
		id:          14,
		name:        "Stripe Billing",
		description: "Retrieve Stripe account balance, list recent charges, and create a new payment intent",
		apiURL:      "https://api.stripe.com/v1",
		tier:        "complex",
		kind:        "http",
	},

	// ── Tier 4: Local macOS skills — no external API (15–17) ─────────────
	{
		id:          15,
		name:        "Battery Status",
		description: "Report battery percentage, charge state, cycle count, and health from macOS system_profiler",
		apiURL:      "",
		tier:        "local",
		kind:        "local",
	},
	{
		id:          16,
		name:        "Finder Selection",
		description: "Return the full file paths of all items currently selected in the macOS Finder",
		apiURL:      "",
		tier:        "local",
		kind:        "local",
	},
	{
		id:          17,
		name:        "Calendar Events Today",
		description: "List all calendar events scheduled for today using macOS Calendar via AppleScript",
		apiURL:      "",
		tier:        "local",
		kind:        "local",
	},

	// ── Tier 5: Workflow / multi-step LLM reasoning (18–20) ──────────────
	{
		id:          18,
		name:        "Research Summarizer",
		description: "Search the web for a given topic and synthesize a structured summary with key findings and sources using LLM reasoning",
		apiURL:      "",
		tier:        "workflow",
		kind:        "workflow",
	},
	{
		id:          19,
		name:        "Daily Briefing",
		description: "Compile a personalized morning briefing by combining current weather, top news headlines, and today's calendar events via LLM synthesis",
		apiURL:      "",
		tier:        "workflow",
		kind:        "workflow",
	},
	{
		id:          20,
		name:        "Code Quality Reviewer",
		description: "Analyze a source code file for bugs, security vulnerabilities, and style violations using LLM reasoning and return structured feedback with line references",
		apiURL:      "",
		tier:        "workflow",
		kind:        "workflow",
	},
}

// ── Score card ────────────────────────────────────────────────────────────────

type scoreCard struct {
	ParseOK        bool // proposal returned without error
	ActionIDMatch  bool // every plans[].actionID is in spec.actions[].id
	NoPlaceholders bool // no placeholder domains in HTTP URLs
	AuthComplete   bool // all auth fields present for chosen authType
	SchemaComplete bool // all required top-level fields non-empty
	RiskJustified  bool // riskLevel consistent with HTTP methods used
	URLsValid      bool // all HTTP URLs are valid absolute HTTPS
	Issues         []string
}

func (sc scoreCard) Total() int {
	pts := 0
	if sc.ParseOK {
		pts += 20
	}
	if sc.ActionIDMatch {
		pts += 20
	}
	if sc.NoPlaceholders {
		pts += 15
	}
	if sc.AuthComplete {
		pts += 15
	}
	if sc.SchemaComplete {
		pts += 15
	}
	if sc.RiskJustified {
		pts += 10
	}
	if sc.URLsValid {
		pts += 5
	}
	return pts
}

func (sc scoreCard) Pass() bool { return sc.Total() >= passThreshold }

func scoreProposal(p forge.ForgeProposal, tc testCase) scoreCard {
	sc := scoreCard{ParseOK: true}

	var spec forge.ForgeSkillSpec
	var plans []forge.ForgeActionPlan
	if err := json.Unmarshal([]byte(p.SpecJSON), &spec); err != nil {
		sc.ParseOK = false
		sc.Issues = append(sc.Issues, "SpecJSON parse error: "+err.Error())
		return sc
	}
	if err := json.Unmarshal([]byte(p.PlansJSON), &plans); err != nil {
		sc.ParseOK = false
		sc.Issues = append(sc.Issues, "PlansJSON parse error: "+err.Error())
		return sc
	}

	// ── 1. ActionID consistency (20 pts) ─────────────────────────────────
	specIDs := map[string]bool{}
	for _, a := range spec.Actions {
		specIDs[a.ID] = true
	}
	allMatch := len(plans) > 0 && len(spec.Actions) > 0
	for _, plan := range plans {
		if !specIDs[plan.ActionID] {
			sc.Issues = append(sc.Issues,
				fmt.Sprintf("plan actionID %q not in spec.actions (have: %v)",
					plan.ActionID, specActionIDs(spec)))
			allMatch = false
		}
	}
	sc.ActionIDMatch = allMatch
	if len(plans) == 0 {
		sc.Issues = append(sc.Issues, "no plans generated")
	}
	if len(spec.Actions) == 0 {
		sc.Issues = append(sc.Issues, "no spec actions generated")
	}

	// ── 2. No placeholder URLs (15 pts) ──────────────────────────────────
	noPlaceholders := true
	httpPlanCount := 0
	for _, plan := range plans {
		if plan.HTTPRequest == nil {
			continue
		}
		httpPlanCount++
		u, err := url.Parse(plan.HTTPRequest.URL)
		if err == nil && forgetypes.PlaceholderDomains[strings.ToLower(u.Hostname())] {
			sc.Issues = append(sc.Issues, "placeholder domain in URL: "+u.Hostname())
			noPlaceholders = false
		}
	}
	if httpPlanCount == 0 {
		// Local/workflow with no HTTP plans — not applicable, give full credit
		noPlaceholders = true
	}
	sc.NoPlaceholders = noPlaceholders

	// ── 3. URL format validity (5 pts) ───────────────────────────────────
	// Requires absolute HTTPS/HTTP URLs. Catches local:// and other bogus schemes
	// that url.Parse accepts but Atlas's HTTP runner would reject.
	urlsValid := true
	for _, plan := range plans {
		if plan.HTTPRequest == nil || plan.HTTPRequest.URL == "" {
			continue
		}
		stripped := stripPlaceholders(plan.HTTPRequest.URL)
		u, err := url.Parse(stripped)
		if err != nil || u.Scheme == "" || u.Host == "" {
			sc.Issues = append(sc.Issues, "invalid URL: "+plan.HTTPRequest.URL)
			urlsValid = false
		} else if u.Scheme != "https" && u.Scheme != "http" {
			sc.Issues = append(sc.Issues, fmt.Sprintf("non-HTTP scheme %q in URL: %s", u.Scheme, plan.HTTPRequest.URL))
			urlsValid = false
		}
	}
	sc.URLsValid = urlsValid

	// ── 4. Auth field completeness (15 pts) ──────────────────────────────
	authComplete := true
	for _, plan := range plans {
		h := plan.HTTPRequest
		if h == nil {
			continue
		}
		switch h.AuthType {
		case "apiKeyHeader":
			if h.AuthSecretKey == "" || h.AuthHeaderName == "" {
				sc.Issues = append(sc.Issues,
					fmt.Sprintf("apiKeyHeader incomplete (secretKey=%q headerName=%q)",
						h.AuthSecretKey, h.AuthHeaderName))
				authComplete = false
			}
		case "apiKeyQuery":
			if h.AuthSecretKey == "" || h.AuthQueryParamName == "" {
				sc.Issues = append(sc.Issues,
					fmt.Sprintf("apiKeyQuery incomplete (secretKey=%q queryParam=%q)",
						h.AuthSecretKey, h.AuthQueryParamName))
				authComplete = false
			}
		case "bearerTokenStatic":
			if h.AuthSecretKey == "" {
				sc.Issues = append(sc.Issues, "bearerTokenStatic missing authSecretKey")
				authComplete = false
			}
		case "basicAuth":
			if h.AuthSecretKey == "" {
				sc.Issues = append(sc.Issues, "basicAuth missing authSecretKey")
				authComplete = false
			}
		case "oauth2ClientCredentials":
			if h.OAuth2ClientIDKey == "" || h.OAuth2ClientSecretKey == "" || h.OAuth2TokenURL == "" {
				sc.Issues = append(sc.Issues,
					fmt.Sprintf("oauth2ClientCredentials incomplete (clientIDKey=%q clientSecretKey=%q tokenURL=%q)",
						h.OAuth2ClientIDKey, h.OAuth2ClientSecretKey, h.OAuth2TokenURL))
				authComplete = false
			}
		}
	}
	sc.AuthComplete = authComplete

	// ── 5. Schema completeness (15 pts) ──────────────────────────────────
	missing := []string{}
	if strings.TrimSpace(p.Name) == "" {
		missing = append(missing, "name")
	}
	if strings.TrimSpace(p.Description) == "" {
		missing = append(missing, "description")
	}
	if strings.TrimSpace(p.Summary) == "" {
		missing = append(missing, "summary")
	}
	if strings.TrimSpace(spec.ID) == "" {
		missing = append(missing, "spec.id")
	}
	if strings.TrimSpace(spec.Category) == "" {
		missing = append(missing, "spec.category")
	}
	if len(spec.Actions) == 0 {
		missing = append(missing, "spec.actions")
	}
	if len(plans) == 0 {
		missing = append(missing, "plans")
	}

	// Kind-appropriate plan type check — penalises wrong execution model.
	switch tc.kind {
	case "local":
		hasLocalPlan := false
		for _, plan := range plans {
			if plan.Type == "local" {
				hasLocalPlan = true
				if plan.LocalPlan == nil {
					missing = append(missing, fmt.Sprintf("plan[%s]: type=local but localPlan is nil", plan.ActionID))
				} else {
					if plan.LocalPlan.Interpreter == "" {
						missing = append(missing, fmt.Sprintf("plan[%s]: localPlan missing interpreter", plan.ActionID))
					}
					if plan.LocalPlan.Script == "" {
						missing = append(missing, fmt.Sprintf("plan[%s]: localPlan missing script", plan.ActionID))
					}
				}
			}
		}
		if !hasLocalPlan && len(plans) > 0 {
			missing = append(missing, "no plans with type=local (got HTTP plans instead of macOS automation)")
		}
	case "workflow":
		hasWorkflowPlan := false
		for _, plan := range plans {
			switch plan.Type {
			case "llm.generate", "atlas.tool", "return":
				hasWorkflowPlan = true
				if plan.WorkflowStep == nil {
					missing = append(missing, fmt.Sprintf("plan[%s]: type=%s but workflowStep is nil", plan.ActionID, plan.Type))
				}
			}
		}
		if !hasWorkflowPlan && len(plans) > 0 {
			missing = append(missing, "no workflow plan types found (llm.generate/atlas.tool/return) — got HTTP plans instead")
		}
	}

	sc.SchemaComplete = len(missing) == 0
	if len(missing) > 0 {
		sc.Issues = append(sc.Issues, "schema issues: "+strings.Join(missing, "; "))
	}

	// ── 6. Risk level justification (10 pts) ─────────────────────────────
	hasDelete, hasMutation := false, false
	for _, plan := range plans {
		if plan.HTTPRequest == nil {
			continue
		}
		switch strings.ToUpper(plan.HTTPRequest.Method) {
		case "DELETE":
			hasDelete = true
		case "POST", "PUT", "PATCH":
			hasMutation = true
		}
	}
	if httpPlanCount == 0 {
		// Non-HTTP skill — any non-empty riskLevel is acceptable
		sc.RiskJustified = p.RiskLevel != ""
	} else {
		switch p.RiskLevel {
		case "high":
			sc.RiskJustified = hasDelete
			if !sc.RiskJustified {
				sc.Issues = append(sc.Issues, "riskLevel=high but no DELETE operations found")
			}
		case "medium":
			sc.RiskJustified = hasMutation || hasDelete
			if !sc.RiskJustified {
				sc.Issues = append(sc.Issues, "riskLevel=medium but only GET operations found")
			}
		case "low":
			sc.RiskJustified = !hasDelete && !hasMutation
			if !sc.RiskJustified {
				sc.Issues = append(sc.Issues, "riskLevel=low but mutation/delete operations present")
			}
		default:
			sc.RiskJustified = false
			sc.Issues = append(sc.Issues, fmt.Sprintf("unrecognised riskLevel %q", p.RiskLevel))
		}
	}

	return sc
}

func specActionIDs(spec forge.ForgeSkillSpec) []string {
	ids := make([]string, len(spec.Actions))
	for i, a := range spec.Actions {
		ids[i] = a.ID
	}
	return ids
}

// stripPlaceholders removes {param} tokens from a URL so url.Parse works cleanly.
func stripPlaceholders(raw string) string {
	var out strings.Builder
	inBrace := false
	for _, r := range raw {
		switch r {
		case '{':
			inBrace = true
		case '}':
			inBrace = false
		default:
			if !inBrace {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

// ── Test result ───────────────────────────────────────────────────────────────

type testResult struct {
	Case     testCase
	Total    time.Duration
	Stages   []stageRecord
	Proposal *forge.ForgeProposal
	Score    scoreCard
	Err      error // non-nil if Propose() itself failed
}

// ── Harness ───────────────────────────────────────────────────────────────────

func runHarness(provider forge.AIProvider) []testResult {
	tmpDir, err := os.MkdirTemp("", "forge-eval-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	svc := forge.NewService(tmpDir)
	results := make([]testResult, 0, len(cases))

	for i, tc := range cases {
		if i > 0 {
			time.Sleep(betweenCases)
		}
		fmt.Printf("\n%s[%02d/20]%s %s%-40s%s %s%s · %s%s\n",
			clrBold, tc.id, clrReset,
			clrBold, tc.name, clrReset,
			clrCyan, tc.tier, tc.kind, clrReset,
		)
		fmt.Printf("         running (2 AI calls)...\n")

		r := runCase(tc, svc, provider)
		results = append(results, r)
		printResult(r)
	}
	return results
}

func runCase(tc testCase, svc *forge.Service, provider forge.AIProvider) testResult {
	lp := newLoggingProvider(provider)
	ctx, cancel := context.WithTimeout(context.Background(), caseTimeout)
	defer cancel()

	start := time.Now()
	proposal, err := svc.Propose(ctx, forge.ProposeRequest{
		Name:        tc.name,
		Description: tc.description,
		APIURL:      tc.apiURL,
	}, lp)
	total := time.Since(start)

	r := testResult{
		Case:   tc,
		Total:  total,
		Stages: lp.stages,
		Err:    err,
	}
	if err == nil {
		r.Proposal = &proposal
		r.Score = scoreProposal(proposal, tc)
	} else {
		r.Score = scoreCard{
			ParseOK: false,
			Issues:  []string{"Propose() failed: " + err.Error()},
		}
	}
	return r
}

// ── Per-case output ───────────────────────────────────────────────────────────

func stageStr(s stageRecord) string {
	status := clrGreen + "✓" + clrReset
	if s.err != nil {
		status = clrRed + "✗" + clrReset
	}
	return fmt.Sprintf("%-10s %.1fs %s", s.name, s.duration.Seconds(), status)
}

func printResult(r testResult) {
	// Overwrite the "running..." line
	fmt.Printf("\033[1A\033[2K") // move up one line and clear it

	// Stage timing
	if len(r.Stages) >= 1 {
		fmt.Printf("         Stage 1 %s\n", stageStr(r.Stages[0]))
	}
	if len(r.Stages) >= 2 {
		fmt.Printf("         Stage 2 %s\n", stageStr(r.Stages[1]))
	}
	if r.Err != nil && len(r.Stages) == 0 {
		fmt.Printf("         %sPre-AI failure: %s%s\n", clrRed, r.Err.Error(), clrReset)
	}
	fmt.Printf("         Total %.1fs\n", r.Total.Seconds())

	sc := r.Score
	score := sc.Total()
	col := scoreColor(score)
	verdict := clrGreen + "PASS" + clrReset
	if !sc.Pass() {
		verdict = clrRed + "FAIL" + clrReset
	}

	fmt.Printf("         %sScore %d/100%s  %s\n", col, score, clrReset, verdict)
	fmt.Printf("         parse%s actionID%s placeholders%s auth%s schema%s risk%s urls%s\n",
		chk(sc.ParseOK), chk(sc.ActionIDMatch), chk(sc.NoPlaceholders),
		chk(sc.AuthComplete), chk(sc.SchemaComplete), chk(sc.RiskJustified), chk(sc.URLsValid),
	)

	if len(sc.Issues) > 0 {
		fmt.Printf("         %sIssues:%s\n", clrYellow, clrReset)
		for _, iss := range sc.Issues {
			fmt.Printf("           %s· %s%s\n", clrRed, iss, clrReset)
		}
	}

	// Proposal details when available
	if r.Proposal != nil {
		p := r.Proposal
		var spec forge.ForgeSkillSpec
		var plans []forge.ForgeActionPlan
		json.Unmarshal([]byte(p.SpecJSON), &spec)   //nolint:errcheck
		json.Unmarshal([]byte(p.PlansJSON), &plans) //nolint:errcheck

		fmt.Printf("         Skill: %s%s%s  category=%s  risk=%s  actions=%d\n",
			clrBold, p.SkillID, clrReset, spec.Category, p.RiskLevel, len(spec.Actions))
		for _, plan := range plans {
			switch {
			case plan.HTTPRequest != nil:
				fmt.Printf("           %-28s  %s  %s  auth=%s\n",
					plan.ActionID,
					plan.HTTPRequest.Method,
					truncate(plan.HTTPRequest.URL, 55),
					plan.HTTPRequest.AuthType,
				)
			case plan.LocalPlan != nil:
				fmt.Printf("           %-28s  local(%s)\n",
					plan.ActionID, plan.LocalPlan.Interpreter)
			default:
				fmt.Printf("           %-28s  type=%s\n", plan.ActionID, plan.Type)
			}
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// ── Summary ───────────────────────────────────────────────────────────────────

type dimStat struct {
	key   string
	label string
	fail  int
}

func printSummary(results []testResult) {
	fmt.Printf("\n%s%s╔═══════════════════════════════════════════════════╗%s\n", clrBold, clrCyan, clrReset)
	fmt.Printf("%s%s║             EVALUATION SUMMARY                    ║%s\n", clrBold, clrCyan, clrReset)
	fmt.Printf("%s%s╚═══════════════════════════════════════════════════╝%s\n\n", clrBold, clrCyan, clrReset)

	total := len(results)
	passed, scoreSum := 0, 0

	dims := []*dimStat{
		{key: "parse", label: "Parse success   "},
		{key: "actionID", label: "ActionID match  "},
		{key: "placeholder", label: "No placeholders "},
		{key: "auth", label: "Auth complete   "},
		{key: "schema", label: "Schema complete "},
		{key: "risk", label: "Risk justified  "},
		{key: "urls", label: "URLs valid      "},
	}
	dimIdx := map[string]*dimStat{}
	for _, d := range dims {
		dimIdx[d.key] = d
	}

	type tierStat struct{ pass, total int }
	byTier := map[string]*tierStat{}

	// Collect per-result failures
	allIssues := map[string][]string{} // caseID → issues

	for _, r := range results {
		sc := r.Score
		scoreSum += sc.Total()
		if sc.Pass() {
			passed++
		}
		if !sc.ParseOK {
			dimIdx["parse"].fail++
		}
		if !sc.ActionIDMatch {
			dimIdx["actionID"].fail++
		}
		if !sc.NoPlaceholders {
			dimIdx["placeholder"].fail++
		}
		if !sc.AuthComplete {
			dimIdx["auth"].fail++
		}
		if !sc.SchemaComplete {
			dimIdx["schema"].fail++
		}
		if !sc.RiskJustified {
			dimIdx["risk"].fail++
		}
		if !sc.URLsValid {
			dimIdx["urls"].fail++
		}

		tier := r.Case.tier
		if byTier[tier] == nil {
			byTier[tier] = &tierStat{}
		}
		byTier[tier].total++
		if sc.Pass() {
			byTier[tier].pass++
		}
		if len(sc.Issues) > 0 {
			caseLabel := fmt.Sprintf("[%02d] %s", r.Case.id, r.Case.name)
			allIssues[caseLabel] = sc.Issues
		}
	}

	avg := float64(scoreSum) / float64(total)
	fmt.Printf("  Overall: %s%d/%d passed%s  avg score %s%.0f/100%s\n\n",
		clrBold, passed, total, clrReset,
		scoreColor(int(avg)), avg, clrReset)

	// Per-tier breakdown
	fmt.Printf("  %sPer-tier pass rate:%s\n", clrBold, clrReset)
	tiers := []string{"simple", "medium", "complex", "local", "workflow"}
	for _, tier := range tiers {
		st := byTier[tier]
		if st == nil {
			continue
		}
		col := clrGreen
		if st.pass < st.total {
			col = clrYellow
		}
		if st.pass == 0 {
			col = clrRed
		}
		bar := miniBar(st.pass, st.total, 10)
		fmt.Printf("    %-10s  %s  %s%d/%d passed%s\n", tier, bar, col, st.pass, st.total, clrReset)
	}
	fmt.Println()

	// Dimension failure rates
	fmt.Printf("  %sDimension failure rates:%s\n", clrBold, clrReset)
	for _, d := range dims {
		pct := float64(d.fail) / float64(total) * 100
		col := clrGreen
		if pct >= 20 {
			col = clrYellow
		}
		if pct >= 45 {
			col = clrRed
		}
		bar := pctBar(d.fail, total, 15)
		fmt.Printf("    %s  %s  %s%d/20 fail (%.0f%%)%s\n",
			d.label, bar, col, d.fail, pct, clrReset)
	}
	fmt.Println()

	// All issues grouped by case
	if len(allIssues) > 0 {
		fmt.Printf("  %sIssue log:%s\n", clrBold, clrReset)
		for _, r := range results {
			label := fmt.Sprintf("[%02d] %s", r.Case.id, r.Case.name)
			issues := allIssues[label]
			if len(issues) == 0 {
				continue
			}
			fmt.Printf("    %s%s%s\n", clrYellow, label, clrReset)
			for _, iss := range issues {
				fmt.Printf("      %s· %s%s\n", clrRed, iss, clrReset)
			}
		}
		fmt.Println()
	}

	printRecommendations(results, dimIdx, total)
}

func miniBar(pass, total, width int) string {
	if total == 0 {
		return strings.Repeat("░", width)
	}
	filled := width * pass / total
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	col := clrGreen
	if pass < total {
		col = clrYellow
	}
	if pass == 0 {
		col = clrRed
	}
	return col + "[" + bar + "]" + clrReset
}

func pctBar(fail, total, width int) string {
	if total == 0 {
		return "[" + strings.Repeat("░", width) + "]"
	}
	pct := fail * 100 / total
	filled := width * pct / 100
	col := clrGreen
	if pct >= 20 {
		col = clrYellow
	}
	if pct >= 45 {
		col = clrRed
	}
	bar := col + strings.Repeat("█", filled) + clrReset + strings.Repeat("░", width-filled)
	return "[" + bar + "]"
}

// ── Recommendations ───────────────────────────────────────────────────────────

type rec struct {
	priority string // HIGH | MEDIUM | LOW
	text     string
}

func printRecommendations(results []testResult, dimIdx map[string]*dimStat, total int) {
	fmt.Printf("  %s%sRecommendations%s\n", clrBold, clrCyan, clrReset)
	fmt.Printf("  %s─────────────────────────────────────────────────%s\n\n", clrCyan, clrReset)

	recs := []rec{}

	// ActionID mismatch — most critical structural bug
	if n := dimIdx["actionID"].fail; n >= 3 {
		recs = append(recs, rec{"HIGH",
			fmt.Sprintf("ActionID mismatch in %d/20 proposals. The formalize prompt's checklist rule "+
				"(plans[].actionID must match spec.actions[].id) is being ignored in %.0f%% of cases. "+
				"Fix: add a concrete JSON counter-example to the formalize prompt showing a mismatch "+
				"and the corrected form. Also consider a post-parse repair pass in service_parse.go "+
				"that auto-aligns orphaned plan actionIDs to the nearest spec action by edit distance.",
				n, float64(n)*100/float64(total))})
	}

	// Placeholder domains leaking through
	if n := dimIdx["placeholder"].fail; n >= 2 {
		recs = append(recs, rec{"HIGH",
			fmt.Sprintf("Placeholder domains in %d/20 proposals. The two-stage pipeline is not "+
				"reliably preventing fabricated endpoints. Fix: strengthen the formalize prompt with "+
				"'If any URL hostname is a placeholder (example.com, test.com, etc.), replace it with "+
				"the real API hostname from the draft or leave the URL field empty — never fabricate.'",
				n)})
	}

	// Auth field gaps
	if n := dimIdx["auth"].fail; n >= 3 {
		recs = append(recs, rec{"HIGH",
			fmt.Sprintf("Auth field incompleteness in %d/20 proposals. Model selects an authType "+
				"but omits required sub-fields. Fix: append a per-authType field table to the formalize "+
				"prompt as a reference card: apiKeyHeader→{authSecretKey+authHeaderName}, "+
				"bearerTokenStatic→{authSecretKey}, basicAuth→{authSecretKey}, "+
				"oauth2ClientCredentials→{clientIDKey+clientSecretKey+tokenURL}.",
				n)})
	}

	// Risk level miscalibration
	if n := dimIdx["risk"].fail; n >= 3 {
		recs = append(recs, rec{"MEDIUM",
			fmt.Sprintf("Risk level wrong in %d/20 proposals. Fix: add a post-parse correction pass "+
				"in service_parse.go — after normalizeCanonicalProposal, scan plan HTTP methods and "+
				"auto-correct: any DELETE → high, any POST/PUT/PATCH → min(medium, declared), "+
				"all GET → low. This removes the risk dimension from the AI's responsibilities entirely "+
				"and makes it structurally correct by construction.",
				n)})
	}

	// Schema gaps
	if n := dimIdx["schema"].fail; n >= 2 {
		recs = append(recs, rec{"MEDIUM",
			fmt.Sprintf("Schema incompleteness in %d/20 proposals — required fields missing despite "+
				"normalizeCanonicalProposal fallbacks. Fix: add a pre-save assertion in research() that "+
				"checks name/description/summary/spec.id/category are non-empty and returns a structured "+
				"error before SaveProposal, rather than persisting a degraded record.",
				n)})
	}

	// URL format issues
	if n := dimIdx["urls"].fail; n >= 2 {
		recs = append(recs, rec{"LOW",
			fmt.Sprintf("URL format failures in %d/20 proposals. Likely causes: relative URLs, "+
				"missing scheme, or malformed path templates. Fix: add a URL pre-validation step in "+
				"normalizeCanonicalProposal that rejects any plan URL that doesn't parse as an absolute URI.",
				n)})
	}

	// Tier-specific failures
	localFails, workflowFails := 0, 0
	for _, r := range results {
		if !r.Score.Pass() {
			if r.Case.kind == "local" {
				localFails++
			}
			if r.Case.kind == "workflow" {
				workflowFails++
			}
		}
	}

	if localFails >= 2 {
		recs = append(recs, rec{"MEDIUM",
			fmt.Sprintf("%d/3 local macOS skills failed. The current buildDraftPrompt is optimised "+
				"for HTTP API research. Local skills (osascript, bash, system_profiler) need a different "+
				"mental model. Fix: detect kind=local from the absence of apiURL and switch to a "+
				"dedicated buildLocalDraftPrompt that primes the model for macOS automation primitives "+
				"rather than REST API patterns.",
				localFails)})
	}

	if workflowFails >= 2 {
		recs = append(recs, rec{"MEDIUM",
			fmt.Sprintf("%d/3 workflow skills failed. Plan types (llm.generate, atlas.tool, return) "+
				"required for workflow routing are not well documented in the current prompts — the model "+
				"defaults to http plans instead. Fix: add a workflow schema section to buildFormalizePrompt "+
				"that shows a complete example step for each plan type, and detect intent=workflow from "+
				"description keywords (synthesize, analyze, reasoning, LLM) to pre-prime the draft prompt.",
				workflowFails)})
	}

	if len(recs) == 0 {
		fmt.Printf("  %sAll dimensions performing well — no structural changes needed.%s\n\n", clrGreen, clrReset)
		return
	}

	priOrder := []string{"HIGH", "MEDIUM", "LOW"}
	priColor := map[string]string{"HIGH": clrRed, "MEDIUM": clrYellow, "LOW": clrCyan}
	idx := 1
	for _, pri := range priOrder {
		for _, r := range recs {
			if r.priority != pri {
				continue
			}
			col := priColor[pri]
			fmt.Printf("  %s[%s]%s Rec %d\n", col, pri, clrReset, idx)
			idx++
			wrapPrint("    ", r.text, 76)
			fmt.Println()
		}
	}
}

// wrapPrint word-wraps text at maxWidth and prints with the given indent prefix.
func wrapPrint(indent, text string, maxWidth int) {
	words := strings.Fields(text)
	line := indent
	for _, w := range words {
		if len(line)+len(w)+1 > maxWidth {
			fmt.Println(line)
			line = indent + w
		} else if line == indent {
			line += w
		} else {
			line += " " + w
		}
	}
	if line != indent {
		fmt.Println(line)
	}
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "error: ANTHROPIC_API_KEY not set")
		fmt.Fprintln(os.Stderr, "usage: ANTHROPIC_API_KEY=sk-ant-... go run ./cmd/forge-eval")
		os.Exit(1)
	}
	model := os.Getenv("FORGE_EVAL_MODEL")
	if model == "" {
		model = defaultModel
	}

	fmt.Printf("\n%s%s╔═══════════════════════════════════════════════════╗%s\n", clrBold, clrCyan, clrReset)
	fmt.Printf("%s%s║      ATLAS FORGE PIPELINE EVALUATION HARNESS      ║%s\n", clrBold, clrCyan, clrReset)
	fmt.Printf("%s%s╚═══════════════════════════════════════════════════╝%s\n", clrBold, clrCyan, clrReset)
	fmt.Printf("  Provider  anthropic\n")
	fmt.Printf("  Model     %s\n", model)
	fmt.Printf("  Cases     20 (5 simple · 5 medium · 4 complex · 3 local · 3 workflow)\n")
	fmt.Printf("  Pipeline  2 AI calls per proposal (draft → formalize)  =  40 calls total\n")
	fmt.Printf("  Scoring   7 dimensions · 100 pts · pass ≥ %d\n\n", passThreshold)

	provider := newAnthropicProvider(apiKey, model)
	results := runHarness(provider)
	printSummary(results)

	fmt.Printf("  Total AI calls made: %d\n\n", provider.calls.Load())
}
