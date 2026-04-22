package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"atlas-runtime-go/internal/config"
)

var sandboxLockedActionPatterns = []string{
	"forge.",
	"atlas.update_operator_prompt",
}

func (r *Registry) registerInfo() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "atlas.info",
			Description: "Returns information about the Atlas runtime status, version, and active configuration.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel: "read",
		FnResult: func(_ context.Context, _ json.RawMessage) (ToolResult, error) {
			snap := loadRuntimeSnapshot(r.supportDir)
			mode := snap.EffectiveAutonomyMode()
			return OKResult(
				fmt.Sprintf("Atlas Go Runtime is running on %s/%s with %s autonomy.", runtime.GOOS, runtime.GOARCH, mode),
				map[string]any{
					"runtime":            "Atlas Go Runtime",
					"go_version":         runtime.Version(),
					"os":                 runtime.GOOS,
					"arch":               runtime.GOARCH,
					"autonomy_mode":      mode,
					"tool_selection":     normalizedToolSelectionMode(snap),
					"action_safety_mode": strings.TrimSpace(snap.ActionSafetyMode),
				},
			), nil
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "atlas.session_capabilities",
			Description: "Inspect what Atlas can do in the current session: autonomy mode, tool selection posture, approval posture, and the high-level capabilities currently installed.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel: "read",
		FnResult: func(_ context.Context, _ json.RawMessage) (ToolResult, error) {
			snap := loadRuntimeSnapshot(r.supportDir)
			mode := snap.EffectiveAutonomyMode()
			groups := capabilityGroupNames(r.ToolCapabilityManifest())
			summary := fmt.Sprintf(
				"Session is %s with %s tool selection. Web research=%t, shell=%t, files=%t, browser=%t, forge=%t, iMessage=%t.",
				mode,
				normalizedToolSelectionMode(snap),
				r.HasAction("web.research") || r.HasAction("websearch.query"),
				r.HasAction("terminal.run_command"),
				r.HasAction("fs.read_file"),
				r.HasAction("browser.navigate"),
				r.HasAction("forge.orchestration.propose"),
				r.HasAction("applescript.messages_send"),
			)
			return OKResult(summary, map[string]any{
				"autonomy_mode":                       mode,
				"tool_selection_mode":                 normalizedToolSelectionMode(snap),
				"action_safety_mode":                  strings.TrimSpace(snap.ActionSafetyMode),
				"normal_non_read_actions_auto_run":    mode == config.AutonomyModeUnleashed,
				"explicit_approval_actions":           []string{"send_publish_delete", "terminal.run_as_admin"},
				"available_capability_groups":         groups,
				"web_research_available":              r.HasAction("web.research") || r.HasAction("websearch.query"),
				"shell_available":                     r.HasAction("terminal.run_command"),
				"command_check_available":             r.HasAction("terminal.check_command"),
				"filesystem_available":                r.HasAction("fs.read_file"),
				"workspace_roots_available":           r.HasAction("fs.workspace_roots"),
				"browser_available":                   r.HasAction("browser.navigate"),
				"forge_available":                     r.HasAction("forge.orchestration.propose"),
				"app_capabilities_available":          r.HasAction("system.app_capabilities"),
				"operator_prompt_management":          r.HasAction("atlas.get_operator_prompt") && r.HasAction("atlas.update_operator_prompt"),
				"imessage_available":                  r.HasAction("applescript.messages_send"),
				"authorized_channel_bridge_available": r.HasAction("communication.send_message"),
			}), nil
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "atlas.diagnose_blocker",
			Description: "Diagnose why a specific Atlas action may be blocked, unavailable, approval-gated, or missing prerequisites in the current session.",
			Properties: map[string]ToolParam{
				"action": {Description: "Action ID to inspect, e.g. 'applescript.messages_send' or 'terminal.run_command'.", Type: "string"},
			},
			Required: []string{"action"},
		},
		PermLevel: "read",
		FnResult: func(_ context.Context, args json.RawMessage) (ToolResult, error) {
			var req struct {
				Action string `json:"action"`
			}
			if err := json.Unmarshal(args, &req); err != nil || strings.TrimSpace(req.Action) == "" {
				return ToolResult{}, fmt.Errorf("action is required")
			}

			actionID := r.Canonicalize(req.Action)
			snap := loadRuntimeSnapshot(r.supportDir)
			mode := snap.EffectiveAutonomyMode()
			class := r.GetActionClass(actionID)
			exists := r.HasAction(actionID)
			approvalRequired := r.NeedsApproval(actionID)
			reasons := []string{}
			nextSteps := []string{}
			status := "available"

			if !exists {
				status = "missing_tool"
				reasons = append(reasons, "This action is not currently installed in Atlas.")
				nextSteps = append(nextSteps, "Use an existing tool, add the capability, or Forge a new reusable skill if the gap is real.")
			} else {
				if mode == config.AutonomyModeSandboxed && matchesBlockedActionPattern(actionID, sandboxLockedActionPatterns) {
					status = "sandbox_locked_surface"
					reasons = append(reasons, "Sandboxed mode intentionally locks this self-modification surface.")
					nextSteps = append(nextSteps, "Switch Atlas to unleashed mode if you want this self-modification surface available.")
				}
				if strings.HasPrefix(actionID, "fs.") {
					if roots, err := LoadFsRoots(r.supportDir); err == nil && len(roots) == 0 {
						status = "missing_prerequisite"
						reasons = append(reasons, "No approved filesystem roots are configured yet.")
						nextSteps = append(nextSteps, "Add or approve a filesystem root before using file tools.")
					}
				}
				if approvalRequired {
					if status == "available" {
						status = "approval_required"
					}
					reasons = append(reasons, "This action currently requires explicit approval before Atlas should execute it.")
					nextSteps = append(nextSteps, "Choose a lower-risk path or request approval for this exact action.")
				}
			}

			if len(reasons) == 0 {
				reasons = append(reasons, "No hard blocker is visible from the current runtime metadata for this action.")
			}
			if len(nextSteps) == 0 {
				nextSteps = append(nextSteps, "Try the action directly and treat any runtime execution error as the next debugging signal.")
			}

			summary := fmt.Sprintf("Action %s is %s in %s mode.", actionID, status, mode)
			return OKResult(summary, map[string]any{
				"action":                 actionID,
				"exists":                 exists,
				"status":                 status,
				"autonomy_mode":          mode,
				"action_class":           string(class),
				"permission_level":       r.PermissionLevel(actionID),
				"approval_required":      approvalRequired,
				"reasons":                reasons,
				"recommended_next_steps": nextSteps,
			}), nil
		},
	})

}

func (r *Registry) registerInfoSkill() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "info.current_time",
			Description: "Returns the current time for a given timezone or location.",
			Properties: map[string]ToolParam{
				"timezone": {Description: "IANA timezone name, e.g. 'America/New_York' or 'Europe/London' (optional)", Type: "string"},
				"location": {Description: "City or country name — used to infer timezone if timezone not provided (optional)", Type: "string"},
			},
			Required: []string{},
		},
		PermLevel: "read",
		Fn:        infoCurrentTime,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "info.current_date",
			Description: "Returns the current date for a given timezone or location.",
			Properties: map[string]ToolParam{
				"timezone": {Description: "IANA timezone name (optional)", Type: "string"},
				"location": {Description: "City or country name (optional)", Type: "string"},
			},
			Required: []string{},
		},
		PermLevel: "read",
		Fn:        infoCurrentDate,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "info.timezone_convert",
			Description: "Converts a time from one timezone to another.",
			Properties: map[string]ToolParam{
				"time":          {Description: "Time to convert, e.g. '14:30' or '2024-01-15 14:30'", Type: "string"},
				"from_timezone": {Description: "Source IANA timezone, e.g. 'America/New_York'", Type: "string"},
				"to_timezone":   {Description: "Target IANA timezone, e.g. 'Asia/Tokyo'", Type: "string"},
			},
			Required: []string{"time", "from_timezone", "to_timezone"},
		},
		PermLevel: "read",
		Fn:        infoTimezoneConvert,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "info.currency_for_location",
			Description: "Returns the currency used in a given country or city.",
			Properties: map[string]ToolParam{
				"location": {Description: "Country or city name", Type: "string"},
			},
			Required: []string{"location"},
		},
		PermLevel: "read",
		Fn:        infoCurrencyForLocation,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "info.currency_convert",
			Description: "Converts an amount from one currency to another using live exchange rates.",
			Properties: map[string]ToolParam{
				"amount": {Description: "Amount to convert", Type: "number"},
				"from":   {Description: "Source currency ISO code, e.g. 'USD'", Type: "string"},
				"to":     {Description: "Target currency ISO code, e.g. 'EUR'", Type: "string"},
			},
			Required: []string{"amount", "from", "to"},
		},
		PermLevel: "read",
		Fn:        infoCurrencyConvert,
	})
}

// ── atlas.info ────────────────────────────────────────────────────────────────

func loadRuntimeSnapshot(supportDir string) config.RuntimeConfigSnapshot {
	snap := config.Defaults()
	if strings.TrimSpace(supportDir) == "" {
		return snap
	}
	data, err := os.ReadFile(filepath.Join(supportDir, "config.json"))
	if err != nil {
		return snap
	}
	_ = json.Unmarshal(data, &snap)
	return snap
}

func normalizedToolSelectionMode(snap config.RuntimeConfigSnapshot) string {
	mode := strings.TrimSpace(snap.ToolSelectionMode)
	if mode == "" {
		if snap.EnableSmartToolSelection {
			return "lazy"
		}
		return "off"
	}
	return mode
}

func capabilityGroupNames(manifest []ToolCapabilityGroupManifest) []string {
	if len(manifest) == 0 {
		return nil
	}
	names := make([]string, 0, len(manifest))
	for _, item := range manifest {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func matchesBlockedActionPattern(actionID string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		switch {
		case strings.HasSuffix(pattern, "."):
			if strings.HasPrefix(actionID, pattern) {
				return true
			}
		case strings.HasSuffix(pattern, "*"):
			if strings.HasPrefix(actionID, strings.TrimSuffix(pattern, "*")) {
				return true
			}
		default:
			if actionID == pattern {
				return true
			}
		}
	}
	return false
}

// ── info.current_time ─────────────────────────────────────────────────────────

func resolveTimezone(timezone, location string) (*time.Location, string, error) {
	if timezone != "" {
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			return nil, "", fmt.Errorf("unknown timezone %q: %w", timezone, err)
		}
		return loc, timezone, nil
	}
	if location != "" {
		tz := locationToTimezone(location)
		if tz != "" {
			loc, err := time.LoadLocation(tz)
			if err == nil {
				return loc, tz, nil
			}
		}
	}
	return time.Local, "local", nil
}

func infoCurrentTime(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Timezone string `json:"timezone"`
		Location string `json:"location"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	loc, tzName, err := resolveTimezone(p.Timezone, p.Location)
	if err != nil {
		return "", err
	}
	now := time.Now().In(loc)
	return fmt.Sprintf("Current time in %s: %s", tzName, now.Format("15:04:05 MST (Mon 2 Jan 2006)")), nil
}

// ── info.current_date ─────────────────────────────────────────────────────────

func infoCurrentDate(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Timezone string `json:"timezone"`
		Location string `json:"location"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	loc, tzName, err := resolveTimezone(p.Timezone, p.Location)
	if err != nil {
		return "", err
	}
	now := time.Now().In(loc)
	return fmt.Sprintf("Current date in %s: %s", tzName, now.Format("Monday, 2 January 2006")), nil
}

// ── info.timezone_convert ─────────────────────────────────────────────────────

func infoTimezoneConvert(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Time         string `json:"time"`
		FromTimezone string `json:"from_timezone"`
		ToTimezone   string `json:"to_timezone"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Time == "" || p.FromTimezone == "" || p.ToTimezone == "" {
		return "", fmt.Errorf("time, from_timezone, and to_timezone are required")
	}

	fromLoc, err := time.LoadLocation(p.FromTimezone)
	if err != nil {
		return "", fmt.Errorf("unknown from_timezone %q: %w", p.FromTimezone, err)
	}
	toLoc, err := time.LoadLocation(p.ToTimezone)
	if err != nil {
		return "", fmt.Errorf("unknown to_timezone %q: %w", p.ToTimezone, err)
	}

	// Try parsing as full datetime first, then just time
	var t time.Time
	layouts := []string{
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		"15:04",
		"15:04:05",
	}
	for _, layout := range layouts {
		if tt, err := time.ParseInLocation(layout, p.Time, fromLoc); err == nil {
			t = tt
			break
		}
	}
	if t.IsZero() {
		return "", fmt.Errorf("could not parse time %q — use formats like '14:30' or '2024-01-15 14:30'", p.Time)
	}

	converted := t.In(toLoc)
	return fmt.Sprintf("%s %s = %s %s",
		t.Format("15:04 MST"), p.FromTimezone,
		converted.Format("15:04 MST"), p.ToTimezone,
	), nil
}

// ── info.currency_for_location ────────────────────────────────────────────────

func infoCurrencyForLocation(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Location string `json:"location"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Location == "" {
		return "", fmt.Errorf("location is required")
	}

	code := locationToCurrency(strings.ToLower(p.Location))
	if code == "" {
		return fmt.Sprintf("Unknown currency for location: %s", p.Location), nil
	}
	return fmt.Sprintf("Currency in %s: %s", p.Location, code), nil
}

// ── info.currency_convert ─────────────────────────────────────────────────────

func infoCurrencyConvert(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Amount float64 `json:"amount"`
		From   string  `json:"from"`
		To     string  `json:"to"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.From == "" || p.To == "" {
		return "", fmt.Errorf("amount, from, and to are required")
	}

	from := strings.ToUpper(p.From)
	to := strings.ToUpper(p.To)

	if from == to {
		return fmt.Sprintf("%.2f %s = %.2f %s", p.Amount, from, p.Amount, to), nil
	}

	rate, err := fetchExchangeRate(ctx, from, to)
	if err != nil {
		return "", err
	}
	converted := p.Amount * rate
	return fmt.Sprintf("%.2f %s = %.2f %s (rate: %.6f)", p.Amount, from, converted, to, rate), nil
}

func fetchExchangeRate(ctx context.Context, from, to string) (float64, error) {
	u := fmt.Sprintf("https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/%s.json",
		strings.ToLower(from))

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Atlas/1.0 currency")

	resp, err := newWebClient(8 * time.Second).Do(req)
	if err != nil {
		return 0, fmt.Errorf("exchange rate fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, fmt.Errorf("exchange rate parse failed: %w", err)
	}

	// API format: { "date": "...", "<from>": { "<to>": <rate> } }
	inner, ok := data[strings.ToLower(from)].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("no rates found for %s", from)
	}
	rate, ok := inner[strings.ToLower(to)].(float64)
	if !ok {
		return 0, fmt.Errorf("no rate found for %s→%s", from, to)
	}
	return rate, nil
}

// ── location helpers ──────────────────────────────────────────────────────────

// locationToTimezone maps common city/country names to IANA timezone IDs.
// Lowercase keys.
func locationToTimezone(loc string) string {
	loc = strings.ToLower(strings.TrimSpace(loc))
	m := map[string]string{
		// North America
		"new york": "America/New_York", "nyc": "America/New_York",
		"los angeles": "America/Los_Angeles", "la": "America/Los_Angeles",
		"chicago": "America/Chicago", "houston": "America/Chicago",
		"toronto": "America/Toronto", "montreal": "America/Toronto",
		"vancouver":     "America/Vancouver",
		"denver":        "America/Denver",
		"phoenix":       "America/Phoenix",
		"miami":         "America/New_York",
		"san francisco": "America/Los_Angeles", "sf": "America/Los_Angeles",
		"seattle":     "America/Los_Angeles",
		"mexico city": "America/Mexico_City",
		// Europe
		"london": "Europe/London", "uk": "Europe/London", "england": "Europe/London",
		"paris": "Europe/Paris", "france": "Europe/Paris",
		"berlin": "Europe/Berlin", "germany": "Europe/Berlin",
		"rome": "Europe/Rome", "italy": "Europe/Rome",
		"madrid": "Europe/Madrid", "spain": "Europe/Madrid",
		"amsterdam": "Europe/Amsterdam",
		"zurich":    "Europe/Zurich", "switzerland": "Europe/Zurich",
		"stockholm": "Europe/Stockholm", "sweden": "Europe/Stockholm",
		"oslo": "Europe/Oslo", "norway": "Europe/Oslo",
		"moscow": "Europe/Moscow", "russia": "Europe/Moscow",
		"istanbul": "Europe/Istanbul", "turkey": "Europe/Istanbul",
		"athens": "Europe/Athens", "greece": "Europe/Athens",
		"warsaw": "Europe/Warsaw", "poland": "Europe/Warsaw",
		"dubai": "Asia/Dubai", "uae": "Asia/Dubai",
		// Asia
		"tokyo": "Asia/Tokyo", "japan": "Asia/Tokyo",
		"beijing": "Asia/Shanghai", "shanghai": "Asia/Shanghai", "china": "Asia/Shanghai",
		"hong kong": "Asia/Hong_Kong",
		"singapore": "Asia/Singapore",
		"seoul":     "Asia/Seoul", "korea": "Asia/Seoul",
		"mumbai": "Asia/Kolkata", "delhi": "Asia/Kolkata", "india": "Asia/Kolkata",
		"bangkok": "Asia/Bangkok", "thailand": "Asia/Bangkok",
		"jakarta": "Asia/Jakarta", "indonesia": "Asia/Jakarta",
		"karachi": "Asia/Karachi", "pakistan": "Asia/Karachi",
		"riyadh": "Asia/Riyadh", "saudi arabia": "Asia/Riyadh",
		"tel aviv": "Asia/Jerusalem", "israel": "Asia/Jerusalem",
		// Oceania
		"sydney": "Australia/Sydney", "melbourne": "Australia/Melbourne",
		"australia": "Australia/Sydney",
		"auckland":  "Pacific/Auckland", "new zealand": "Pacific/Auckland",
		// Africa
		"cairo": "Africa/Cairo", "egypt": "Africa/Cairo",
		"johannesburg": "Africa/Johannesburg", "south africa": "Africa/Johannesburg",
		"nairobi": "Africa/Nairobi", "kenya": "Africa/Nairobi",
		"lagos": "Africa/Lagos", "nigeria": "Africa/Lagos",
		// South America
		"sao paulo": "America/Sao_Paulo", "brazil": "America/Sao_Paulo",
		"buenos aires": "America/Argentina/Buenos_Aires", "argentina": "America/Argentina/Buenos_Aires",
		"bogota": "America/Bogota", "colombia": "America/Bogota",
		"lima": "America/Lima", "peru": "America/Lima",
		"santiago": "America/Santiago", "chile": "America/Santiago",
	}
	return m[loc]
}

// locationToCurrency maps country/city names to ISO 4217 currency codes.
func locationToCurrency(loc string) string {
	m := map[string]string{
		"usa": "USD", "united states": "USD", "us": "USD",
		"new york": "USD", "los angeles": "USD", "chicago": "USD",
		"san francisco": "USD", "seattle": "USD", "miami": "USD",
		"canada": "CAD", "toronto": "CAD", "vancouver": "CAD",
		"uk": "GBP", "united kingdom": "GBP", "england": "GBP",
		"london": "GBP", "britain": "GBP",
		"eurozone": "EUR", "europe": "EUR",
		"germany": "EUR", "berlin": "EUR",
		"france": "EUR", "paris": "EUR",
		"italy": "EUR", "rome": "EUR",
		"spain": "EUR", "madrid": "EUR",
		"netherlands": "EUR", "amsterdam": "EUR",
		"portugal":    "EUR",
		"switzerland": "CHF", "zurich": "CHF",
		"sweden": "SEK", "stockholm": "SEK",
		"norway": "NOK", "oslo": "NOK",
		"denmark": "DKK",
		"russia":  "RUB", "moscow": "RUB",
		"japan": "JPY", "tokyo": "JPY",
		"china": "CNY", "beijing": "CNY", "shanghai": "CNY",
		"hong kong":   "HKD",
		"singapore":   "SGD",
		"south korea": "KRW", "korea": "KRW", "seoul": "KRW",
		"india": "INR", "mumbai": "INR", "delhi": "INR",
		"australia": "AUD", "sydney": "AUD", "melbourne": "AUD",
		"new zealand": "NZD", "auckland": "NZD",
		"brazil": "BRL", "sao paulo": "BRL",
		"mexico": "MXN", "mexico city": "MXN",
		"south africa": "ZAR", "johannesburg": "ZAR",
		"uae": "AED", "dubai": "AED",
		"saudi arabia": "SAR", "riyadh": "SAR",
		"turkey": "TRY", "istanbul": "TRY",
		"egypt": "EGP", "cairo": "EGP",
		"argentina": "ARS", "buenos aires": "ARS",
		"thailand": "THB", "bangkok": "THB",
		"indonesia": "IDR", "jakarta": "IDR",
		"malaysia": "MYR", "kuala lumpur": "MYR",
		"philippines": "PHP", "manila": "PHP",
		"israel": "ILS", "tel aviv": "ILS",
		"pakistan": "PKR", "karachi": "PKR",
		"nigeria": "NGN", "lagos": "NGN",
		"kenya": "KES", "nairobi": "KES",
		"poland": "PLN", "warsaw": "PLN",
		"czech republic": "CZK", "prague": "CZK",
		"hungary": "HUF", "budapest": "HUF",
		"colombia": "COP", "bogota": "COP",
		"chile": "CLP", "santiago": "CLP",
		"peru": "PEN", "lima": "PEN",
	}
	return m[loc]
}
