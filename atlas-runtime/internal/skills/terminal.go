package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"atlas-runtime-go/internal/logstore"
)

func (r *Registry) registerTerminal() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.run_command",
			Description: "Run any command by executable name and argument list. Executed directly (no shell), so each arg is a separate element — no quoting or escaping needed. Returns combined stdout+stderr and exit code. In sandboxed mode this requires approval; in unleashed mode Atlas may execute it directly.",
			Properties: map[string]ToolParam{
				"command": {
					Description: "The executable to run (e.g. 'brew', 'git', 'python3', 'curl')",
					Type:        "string",
				},
				"args": {
					Description: "Arguments — each element is a separate argument (e.g. [\"install\", \"pandoc\"])",
					Type:        "array",
					Items:       &ToolParam{Type: "string"},
				},
				"workingDir": {
					Description: "Absolute working directory (optional, defaults to user home)",
					Type:        "string",
				},
			},
			Required: []string{"command"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassDestructiveLocal,
		FnResult:    terminalRunCommand,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.run_script",
			Description: "Execute a multi-line shell script via /bin/zsh. Supports pipes, redirects, loops, and zsh syntax. For tools that need shell initialization (nvm, rbenv, pyenv, conda), prefix the script with 'source ~/.zshrc'. In sandboxed mode this requires approval; in unleashed mode Atlas may execute it directly.",
			Properties: map[string]ToolParam{
				"script": {
					Description: "Shell script to execute (passed to /bin/sh -c)",
					Type:        "string",
				},
				"workingDir": {
					Description: "Absolute path to run the script from (optional, defaults to user home directory)",
					Type:        "string",
				},
			},
			Required: []string{"script"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassDestructiveLocal, // full shell — highest local risk
		FnResult:    terminalRunScript,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.run_as_admin",
			Description: "Run a shell command with administrator privileges. Triggers a macOS password prompt for the user to authenticate. Use for commands that require root/sudo (e.g. system-level installs, writing to protected paths). Always requires user approval.",
			Properties: map[string]ToolParam{
				"script": {
					Description: "Shell command or script to run as administrator",
					Type:        "string",
				},
			},
			Required: []string{"script"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassDestructiveLocal,
		Fn:          terminalRunAsAdmin,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.read_env",
			Description: "Read one or more environment variable values. Pass an empty array to list all non-sensitive variable names (values are not returned in list mode).",
			Properties: map[string]ToolParam{
				"keys": {
					Description: "Environment variable names to read (e.g. ['HOME', 'PATH']). Pass empty array to list all available non-sensitive variable names.",
					Type:        "array",
					Items:       &ToolParam{Type: "string"},
				},
			},
			Required: []string{"keys"},
		},
		PermLevel: "read",
		Fn:        terminalReadEnv,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.list_processes",
			Description: "List running processes, sorted by CPU usage. Optionally filter by process name substring.",
			Properties: map[string]ToolParam{
				"filter": {
					Description: "Optional name substring to filter results (case-insensitive)",
					Type:        "string",
				},
			},
			Required: []string{},
		},
		PermLevel: "read",
		Fn:        terminalListProcesses,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.kill_process",
			Description: "Send a signal to a process by PID. Defaults to SIGTERM. Use SIGKILL only if SIGTERM fails.",
			Properties: map[string]ToolParam{
				"pid": {
					Description: "Process ID to signal",
					Type:        "integer",
				},
				"signal": {
					Description: "Signal to send (default: TERM)",
					Type:        "string",
					Enum:        []string{"TERM", "KILL", "HUP", "INT"},
				},
			},
			Required: []string{"pid"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassDestructiveLocal,
		Fn:          terminalKillProcess,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.run_background",
			Description: "Start a command in the background and return immediately. Atlas will send a follow-up message in this conversation when the command finishes, including exit code and output. Use for long-running operations (builds, downloads, package installs) so the user isn't left waiting. In sandboxed mode this requires approval; in unleashed mode Atlas may execute it directly.",
			Properties: map[string]ToolParam{
				"command": {
					Description: "The executable to run (e.g. 'brew', 'npm', 'git')",
					Type:        "string",
				},
				"args": {
					Description: "Arguments — each element is a separate argument",
					Type:        "array",
					Items:       &ToolParam{Type: "string"},
				},
				"workingDir": {
					Description: "Absolute working directory (optional, defaults to user home)",
					Type:        "string",
				},
			},
			Required: []string{"command"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassDestructiveLocal,
		FnResult:    terminalRunBackground,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.get_working_directory",
			Description: "Returns the current working directory of the Atlas runtime process.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel: "read",
		Fn:        terminalGetWorkingDirectory,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.which",
			Description: "Locate the full path of a command on PATH, or report that it is not installed.",
			Properties: map[string]ToolParam{
				"command": {
					Description: "Command name to locate (e.g. 'git', 'python3', 'ffmpeg')",
					Type:        "string",
				},
			},
			Required: []string{"command"},
		},
		PermLevel: "read",
		Fn:        terminalWhich,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "terminal.check_command",
			Description: "Check whether a command exists, where it resolves on PATH, whether it is runnable, and what version string it reports.",
			Properties: map[string]ToolParam{
				"command": {
					Description: "Command name to inspect, for example 'python3', 'osascript', or 'brew'",
					Type:        "string",
				},
				"versionArgs": {
					Description: "Optional explicit version arguments, for example ['--version'] or ['-v']",
					Type:        "array",
					Items:       &ToolParam{Type: "string"},
				},
			},
			Required: []string{"command"},
		},
		PermLevel: "read",
		FnResult:  terminalCheckCommand,
	})
}

// ── constants & helpers ───────────────────────────────────────────────────────

const terminalMaxOutput = 32 * 1024 // 32 KB

// terminalBlocklist contains bare binary names that are blocked in run_command.
// run_script requires explicit user approval on every call, so it is not blocked here.
// Includes download and interpreter tools that could be used to fetch and execute
// arbitrary remote code — those remain available via run_script (which always asks).
var terminalBlocklist = []string{
	"rm", "rmdir", "mkfs", "dd", "shred", "fdisk",
	"sudo", "su", "chmod", "chown", "visudo",
	"curl", "wget", "python", "python3", "ruby", "node", "perl", "php",
}

// terminalSecretPatterns are substrings that mark an env var name as sensitive.
var terminalSecretPatterns = []string{
	"TOKEN", "KEY", "SECRET", "PASSWORD", "PASS", "AUTH", "CREDENTIAL", "CERT", "PRIVATE",
}

func terminalTruncate(s string) string {
	if len(s) <= terminalMaxOutput {
		return s
	}
	omitted := len(s) - terminalMaxOutput
	return s[:terminalMaxOutput] + fmt.Sprintf("\n[truncated — %d bytes omitted]", omitted)
}

func terminalCheckBlocklist(name string) error {
	base := strings.ToLower(filepath.Base(name))
	for _, blocked := range terminalBlocklist {
		if base == blocked {
			return fmt.Errorf("command %q is blocked for safety — use terminal.run_script (which requires explicit approval) if you truly need this", name)
		}
	}
	return nil
}

func terminalValidateWorkDir(dir string) error {
	if dir == "" {
		return nil
	}
	if !strings.HasPrefix(dir, "/") {
		return fmt.Errorf("workingDir must be an absolute path, got: %q", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("workingDir does not exist: %q", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("workingDir is not a directory: %q", dir)
	}
	return nil
}

// terminalDefaultDir returns the user's home directory as the default working dir.
func terminalDefaultDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

// terminalIsSensitiveEnvKey returns true if the variable name looks like a secret.
func terminalIsSensitiveEnvKey(name string) bool {
	upper := strings.ToUpper(name)
	for _, pat := range terminalSecretPatterns {
		if strings.Contains(upper, pat) {
			return true
		}
	}
	return false
}

// ── helpers ───────────────────────────────────────────────────────────────────

// terminalBuildResult constructs a structured ToolResult for terminal commands.
// label is the human-readable command string (e.g. "brew install pandoc").
// Success is derived from exitCode — non-zero means failure.
// Summary contains the full output for the AI model; LogOutcome is a clean
// one-liner for the activity log that avoids collapsing output into noise.
func terminalBuildResult(label string, exitCode int, output string) ToolResult {
	success := exitCode == 0

	// Full output for the model.
	var summary string
	if output != "" {
		summary = fmt.Sprintf("%s\n[exit %d]\n%s", label, exitCode, terminalTruncate(output))
	} else {
		summary = fmt.Sprintf("%s — exit %d", label, exitCode)
	}

	// Clean one-liner for the activity log.
	status := "ok"
	if !success {
		status = fmt.Sprintf("exit %d", exitCode)
	}
	logOutcome := fmt.Sprintf("%s → %s", label, status)

	return ToolResult{
		Success:    success,
		Summary:    summary,
		LogOutcome: logOutcome,
		Artifacts:  map[string]any{"exit_code": exitCode},
	}
}

// ── handlers ──────────────────────────────────────────────────────────────────

func terminalRunCommand(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Command    string   `json:"command"`
		Args       []string `json:"args"`
		WorkingDir string   `json:"workingDir"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Command == "" {
		return ToolResult{}, fmt.Errorf("command is required")
	}

	if err := terminalCheckBlocklist(p.Command); err != nil {
		return ToolResult{}, err
	}

	cwd := p.WorkingDir
	if cwd == "" {
		cwd = terminalDefaultDir()
	}
	if err := terminalValidateWorkDir(cwd); err != nil {
		return ToolResult{}, err
	}

	// Use context.Background() as the parent — the agent loop's toolCtx has a
	// 30-second deadline which would kill long-running installs/builds. Terminal
	// commands manage their own timeout independently.
	runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(runCtx, p.Command, p.Args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return ToolResult{}, fmt.Errorf("failed to run %q: %w", p.Command, err)
		}
	}

	label := p.Command
	if len(p.Args) > 0 {
		label += " " + strings.Join(p.Args, " ")
	}
	return terminalBuildResult(label, exitCode, strings.TrimSpace(string(out))), nil
}

func terminalRunScript(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Script     string `json:"script"`
		WorkingDir string `json:"workingDir"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Script == "" {
		return ToolResult{}, fmt.Errorf("script is required")
	}

	cwd := p.WorkingDir
	if cwd == "" {
		cwd = terminalDefaultDir()
	}
	if err := terminalValidateWorkDir(cwd); err != nil {
		return ToolResult{}, err
	}

	// Use context.Background() — same reasoning as run_command (30s toolCtx cap).
	runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Use /bin/zsh so the user's PATH extensions and shell functions (nvm, rbenv,
	// pyenv, conda etc.) are available. Source ~/.zshrc explicitly in the script
	// if those tools need initialization.
	cmd := exec.CommandContext(runCtx, "/bin/zsh", "-c", p.Script)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return ToolResult{}, fmt.Errorf("failed to run script: %w", err)
		}
	}

	// Use the first non-empty line of the script as the log label.
	label := "script"
	for _, line := range strings.Split(p.Script, "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			if len(t) > 80 {
				t = t[:80] + "…"
			}
			label = t
			break
		}
	}
	return terminalBuildResult(label, exitCode, strings.TrimSpace(string(out))), nil
}

func terminalReadEnv(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("keys is required")
	}

	// Value lookup mode.
	if len(p.Keys) > 0 {
		result := make(map[string]interface{}, len(p.Keys))
		for _, k := range p.Keys {
			if terminalIsSensitiveEnvKey(k) {
				result[k] = "[REDACTED — sensitive key]"
				continue
			}
			v, ok := os.LookupEnv(k)
			if ok {
				result[k] = v
			} else {
				result[k] = nil
			}
		}
		b, _ := json.MarshalIndent(result, "", "  ")
		return string(b), nil
	}

	// List-all mode — names only, secrets scrubbed.
	all := os.Environ()
	var names []string
	for _, pair := range all {
		name := pair
		if idx := strings.IndexByte(pair, '='); idx >= 0 {
			name = pair[:idx]
		}
		if !terminalIsSensitiveEnvKey(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

func terminalListProcesses(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Filter string `json:"filter"`
	}
	// Ignore unmarshal errors — filter is optional.
	json.Unmarshal(args, &p) //nolint:errcheck

	out, err := runCmd(ctx, "ps", "aux")
	if err != nil {
		return "", fmt.Errorf("failed to list processes: %w", err)
	}

	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		return "No processes found.", nil
	}

	// Keep the header.
	var result []string
	result = append(result, lines[0])

	filter := strings.ToLower(p.Filter)
	count := 0
	for _, line := range lines[1:] {
		if count >= 50 {
			break
		}
		if filter != "" && !strings.Contains(strings.ToLower(line), filter) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		result = append(result, line)
		count++
	}

	return strings.Join(result, "\n"), nil
}

func terminalKillProcess(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		PID    int    `json:"pid"`
		Signal string `json:"signal"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.PID == 0 {
		return "", fmt.Errorf("pid is required")
	}

	// Safety guards.
	if p.PID <= 0 {
		return "", fmt.Errorf("invalid PID: %d", p.PID)
	}
	if p.PID < 100 {
		return "", fmt.Errorf("refusing to signal PID %d — system/kernel processes (PID < 100) are protected", p.PID)
	}
	if p.PID == os.Getpid() {
		return "", fmt.Errorf("refusing to signal the Atlas runtime process itself (PID %d)", p.PID)
	}

	sig := p.Signal
	if sig == "" {
		sig = "TERM"
	}
	validSignals := map[string]bool{"TERM": true, "KILL": true, "HUP": true, "INT": true}
	if !validSignals[sig] {
		return "", fmt.Errorf("invalid signal %q — allowed: TERM, KILL, HUP, INT", sig)
	}

	ctx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()

	if _, err := runCmd(ctx, "kill", "-"+sig, strconv.Itoa(p.PID)); err != nil {
		return "", fmt.Errorf("failed to signal PID %d: %w", p.PID, err)
	}

	return fmt.Sprintf("Sent SIG%s to PID %d.", sig, p.PID), nil
}

func terminalGetWorkingDirectory(_ context.Context, _ json.RawMessage) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not determine working directory: %w", err)
	}
	return cwd, nil
}

func terminalWhich(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	out, err := runCmd(ctx, "which", p.Command)
	if err != nil {
		return "command not found: " + p.Command, nil
	}
	return strings.TrimSpace(out), nil
}

func terminalCheckCommand(_ context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Command     string   `json:"command"`
		VersionArgs []string `json:"versionArgs"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Command) == "" {
		return ToolResult{}, fmt.Errorf("command is required")
	}

	command := strings.TrimSpace(p.Command)
	path, err := exec.LookPath(command)
	if err != nil {
		return OKResult("Command is not installed on PATH: "+command, map[string]any{
			"command":   command,
			"installed": false,
			"runnable":  false,
		}), nil
	}

	artifacts := map[string]any{
		"command":   command,
		"installed": true,
		"runnable":  true,
		"path":      path,
	}

	versionArgSets := [][]string{}
	if len(p.VersionArgs) > 0 {
		versionArgSets = append(versionArgSets, p.VersionArgs)
	} else {
		versionArgSets = append(versionArgSets, []string{"--version"}, []string{"-version"}, []string{"version"}, []string{"-v"})
	}

	for _, versionArgs := range versionArgSets {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, path, versionArgs...)
		out, runErr := cmd.CombinedOutput()
		cancel()
		if runErr != nil {
			continue
		}
		if line := firstOutputLine(strings.TrimSpace(string(out))); line != "" {
			artifacts["version"] = line
			artifacts["versionArgsUsed"] = versionArgs
			break
		}
	}

	return OKResult("Command is installed and runnable: "+command, artifacts), nil
}

func firstOutputLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(s, "\n")[0])
}

func terminalRunAsAdmin(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Script string `json:"script"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Script) == "" {
		return "", fmt.Errorf("script is required")
	}

	// Escape the script for embedding in an AppleScript string literal.
	escaped := strings.ReplaceAll(p.Script, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)

	appleScript := fmt.Sprintf(`do shell script "%s" with administrator privileges`, escaped)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", appleScript)
	out, err := cmd.CombinedOutput()
	result := terminalTruncate(strings.TrimSpace(string(out)))
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Sprintf("[exit %d]\n%s", exitErr.ExitCode(), result), nil
		}
		return "", fmt.Errorf("run_as_admin failed: %w", err)
	}
	return result, nil
}

func terminalRunBackground(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Command    string   `json:"command"`
		Args       []string `json:"args"`
		WorkingDir string   `json:"workingDir"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Command == "" {
		return ToolResult{}, fmt.Errorf("command is required")
	}

	cwd := p.WorkingDir
	if cwd == "" {
		cwd = terminalDefaultDir()
	}
	if err := terminalValidateWorkDir(cwd); err != nil {
		return ToolResult{}, err
	}

	label := p.Command
	if len(p.Args) > 0 {
		label += " " + strings.Join(p.Args, " ")
	}

	// Capture the proactive sender before spawning the goroutine so it is
	// bound to this conversation even if the context is cancelled later.
	sender, hasSender := ProactiveSenderFromContext(ctx)

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(bgCtx, p.Command, p.Args...)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()

		exitCode := 0
		var execFailed string
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				execFailed = err.Error()
			}
		}

		success := exitCode == 0 && execFailed == ""

		// Log the completion to the activity log.
		outcome := fmt.Sprintf("background: %s → exit %d", label, exitCode)
		if execFailed != "" {
			outcome = fmt.Sprintf("background: %s — failed to start: %s", label, execFailed)
		}
		logstore.WriteAction(logstore.ActionLogEntry{
			ToolName:     "terminal.run_background",
			ActionClass:  string(ActionClassDestructiveLocal),
			InputSummary: label,
			Success:      success,
			Outcome:      outcome,
			Errors: func() []string {
				if execFailed != "" {
					return []string{execFailed}
				}
				if !success {
					return []string{fmt.Sprintf("exit %d", exitCode)}
				}
				return nil
			}(),
		})

		// Build the proactive chat message.
		output := strings.TrimSpace(string(out))
		var msg string
		if execFailed != "" {
			msg = fmt.Sprintf("Background task `%s` failed to start: %s", label, execFailed)
		} else if output != "" {
			msg = fmt.Sprintf("Background task `%s` finished.\n\n```\n[exit %d]\n%s\n```", label, exitCode, terminalTruncate(output))
		} else {
			msg = fmt.Sprintf("Background task `%s` finished with exit code %d.", label, exitCode)
		}

		if hasSender {
			sender(msg)
		}
	}()

	return ToolResult{
		Success:   true,
		Summary:   fmt.Sprintf("Started in background: %s", label),
		Artifacts: map[string]any{"status": "running", "command": label},
	}, nil
}
