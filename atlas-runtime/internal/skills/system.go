package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (r *Registry) registerSystem() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.open_app",
			Description: "Opens a macOS application by name.",
			Properties: map[string]ToolParam{
				"appName": {Description: "Name of the application to open (e.g. 'Safari', 'Notes')", Type: "string"},
			},
			Required: []string{"appName"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemOpenApp,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.open_url",
			Description: "Opens a URL in the default browser.",
			Properties: map[string]ToolParam{
				"url": {Description: "URL to open", Type: "string"},
			},
			Required: []string{"url"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemOpenURL,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.open_file",
			Description: "Opens a file with its default application.",
			Properties: map[string]ToolParam{
				"path": {Description: "Absolute path to the file to open", Type: "string"},
			},
			Required: []string{"path"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemOpenFile,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.copy_to_clipboard",
			Description: "Copies text to the macOS clipboard.",
			Properties: map[string]ToolParam{
				"text": {Description: "Text to copy to clipboard", Type: "string"},
			},
			Required: []string{"text"},
		},
		PermLevel:   "draft",
		ActionClass: ActionClassLocalWrite,
		Fn:          systemCopyToClipboard,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.read_clipboard",
			Description: "Returns the current contents of the macOS clipboard.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel: "read",
		Fn:        systemReadClipboard,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.send_notification",
			Description: "Sends a macOS notification with a title and body.",
			Properties: map[string]ToolParam{
				"title": {Description: "Notification title", Type: "string"},
				"body":  {Description: "Notification body text", Type: "string"},
			},
			Required: []string{"title", "body"},
		},
		PermLevel:   "draft",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemSendNotification,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.running_apps",
			Description: "Returns a list of currently running (foreground) applications.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel: "read",
		Fn:        systemRunningApps,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.frontmost_app",
			Description: "Returns the name of the currently active (frontmost) application.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel: "read",
		Fn:        systemFrontmostApp,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.activate_app",
			Description: "Brings an application to the foreground.",
			Properties: map[string]ToolParam{
				"appName": {Description: "Name of the application to activate", Type: "string"},
			},
			Required: []string{"appName"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemActivateApp,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.quit_app",
			Description: "Quits a running application.",
			Properties: map[string]ToolParam{
				"appName": {Description: "Name of the application to quit", Type: "string"},
			},
			Required: []string{"appName"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassDestructiveLocal,
		Fn:          systemQuitApp,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.open_folder",
			Description: "Opens a folder in Finder.",
			Properties: map[string]ToolParam{
				"path": {Description: "Absolute path to the folder to open", Type: "string"},
			},
			Required: []string{"path"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemOpenFolder,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.reveal_in_finder",
			Description: "Reveals a file or folder in Finder (selects it without opening).",
			Properties: map[string]ToolParam{
				"path": {Description: "Absolute path to reveal in Finder", Type: "string"},
			},
			Required: []string{"path"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemRevealInFinder,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.open_file_with_app",
			Description: "Opens a file with a specific application.",
			Properties: map[string]ToolParam{
				"path":    {Description: "Absolute path to the file", Type: "string"},
				"appName": {Description: "Name of the application to use (e.g. 'TextEdit', 'Preview')", Type: "string"},
			},
			Required: []string{"path", "appName"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemOpenFileWithApp,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.schedule_notification",
			Description: "Schedules a macOS notification at a specific time (while the runtime is running).",
			Properties: map[string]ToolParam{
				"title":        {Description: "Notification title", Type: "string"},
				"body":         {Description: "Notification body text", Type: "string"},
				"delaySeconds": {Description: "Number of seconds from now to show the notification", Type: "integer"},
			},
			Required: []string{"title", "body", "delaySeconds"},
		},
		PermLevel:   "draft",
		ActionClass: ActionClassExternalSideEffect,
		Fn:          systemScheduleNotification,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.is_app_running",
			Description: "Checks whether a specific application is currently running.",
			Properties: map[string]ToolParam{
				"appName": {Description: "Name of the application to check", Type: "string"},
			},
			Required: []string{"appName"},
		},
		PermLevel: "read",
		Fn:        systemIsAppRunning,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "system.app_capabilities",
			Description: "Inspect whether macOS apps are installed or running, plus optional command availability, so Atlas can evaluate local routes with real facts.",
			Properties: map[string]ToolParam{
				"appNames": {
					Description: "Optional app names to inspect, for example ['Messages', 'Music', 'Safari']",
					Type:        "array",
					Items:       &ToolParam{Type: "string"},
				},
				"commands": {
					Description: "Optional command names to inspect, for example ['osascript', 'python3', 'ffmpeg']",
					Type:        "array",
					Items:       &ToolParam{Type: "string"},
				},
			},
			Required: []string{},
		},
		PermLevel: "read",
		FnResult:  systemAppCapabilities,
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

const shellTimeout = 30 * time.Second

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command %s %v failed: %w — %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func runCmdWithStdin(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewBufferString(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command %s failed: %w — %s", name, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// ── handlers ──────────────────────────────────────────────────────────────────

func systemOpenApp(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		AppName string `json:"appName"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.AppName == "" {
		return "", fmt.Errorf("appName is required")
	}
	if _, err := runCmd(ctx, "open", "-a", p.AppName); err != nil {
		return "", err
	}
	return "Opened " + p.AppName + ".", nil
}

func systemOpenURL(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if _, err := runCmd(ctx, "open", p.URL); err != nil {
		return "", err
	}
	return "Opened " + p.URL + ".", nil
}

func systemOpenFile(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if _, err := runCmd(ctx, "open", p.Path); err != nil {
		return "", err
	}
	return "Opened " + p.Path + ".", nil
}

func systemCopyToClipboard(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if _, err := runCmdWithStdin(ctx, p.Text, "pbcopy"); err != nil {
		return "", err
	}
	return "Copied to clipboard.", nil
}

func systemReadClipboard(ctx context.Context, _ json.RawMessage) (string, error) {
	out, err := runCmd(ctx, "pbpaste")
	if err != nil {
		return "", err
	}
	return out, nil
}

func systemSendNotification(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Title == "" || p.Body == "" {
		return "", fmt.Errorf("title and body are required")
	}
	script := fmt.Sprintf(`display notification %q with title %q`, p.Body, p.Title)
	if _, err := runCmd(ctx, "osascript", "-e", script); err != nil {
		return "", err
	}
	return "Notification sent.", nil
}

func systemRunningApps(ctx context.Context, _ json.RawMessage) (string, error) {
	script := `tell application "System Events" to get name of every process whose background only is false`
	out, err := runCmd(ctx, "osascript", "-e", script)
	if err != nil {
		return "", err
	}
	return out, nil
}

func systemFrontmostApp(ctx context.Context, _ json.RawMessage) (string, error) {
	script := `tell application "System Events" to get name of first application process whose frontmost is true`
	out, err := runCmd(ctx, "osascript", "-e", script)
	if err != nil {
		return "", err
	}
	return out, nil
}

func systemActivateApp(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		AppName string `json:"appName"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.AppName == "" {
		return "", fmt.Errorf("appName is required")
	}
	script := fmt.Sprintf(`tell application %q to activate`, p.AppName)
	out, err := runCmd(ctx, "osascript", "-e", script)
	if err != nil {
		return "", err
	}
	if out == "" {
		return "Activated " + p.AppName + ".", nil
	}
	return out, nil
}

func systemQuitApp(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		AppName string `json:"appName"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.AppName == "" {
		return "", fmt.Errorf("appName is required")
	}
	script := fmt.Sprintf(`tell application %q to quit`, p.AppName)
	out, err := runCmd(ctx, "osascript", "-e", script)
	if err != nil {
		return "", err
	}
	if out == "" {
		return "Quit " + p.AppName + ".", nil
	}
	return out, nil
}

func systemOpenFolder(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if _, err := runCmd(ctx, "open", p.Path); err != nil {
		return "", err
	}
	return "Opened folder: " + p.Path, nil
}

func systemRevealInFinder(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if _, err := runCmd(ctx, "open", "-R", p.Path); err != nil {
		return "", err
	}
	return "Revealed in Finder: " + p.Path, nil
}

func systemOpenFileWithApp(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path    string `json:"path"`
		AppName string `json:"appName"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || p.AppName == "" {
		return "", fmt.Errorf("path and appName are required")
	}
	if _, err := runCmd(ctx, "open", "-a", p.AppName, p.Path); err != nil {
		return "", err
	}
	return fmt.Sprintf("Opened %s with %s.", p.Path, p.AppName), nil
}

func systemScheduleNotification(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Title        string `json:"title"`
		Body         string `json:"body"`
		DelaySeconds int    `json:"delaySeconds"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Title == "" || p.Body == "" {
		return "", fmt.Errorf("title, body, and delaySeconds are required")
	}
	if p.DelaySeconds <= 0 {
		return "", fmt.Errorf("delaySeconds must be > 0")
	}
	if p.DelaySeconds > 86400 {
		return "", fmt.Errorf("delaySeconds must be ≤ 86400 (24 hours)")
	}

	title := p.Title
	body := p.Body
	delay := time.Duration(p.DelaySeconds) * time.Second

	go func() {
		time.Sleep(delay)
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		runCmd(ctx2, "osascript", "-e", script) //nolint:errcheck
	}()

	return fmt.Sprintf("Notification scheduled in %d seconds: %s", p.DelaySeconds, p.Title), nil
}

func systemIsAppRunning(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		AppName string `json:"appName"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.AppName == "" {
		return "", fmt.Errorf("appName is required")
	}
	script := fmt.Sprintf(
		`tell application "System Events" to (name of processes) contains %q`,
		p.AppName,
	)
	out, err := runCmd(ctx, "osascript", "-e", script)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "true" {
		return p.AppName + " is running.", nil
	}
	return p.AppName + " is not running.", nil
}

func systemAppCapabilities(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		AppNames []string `json:"appNames"`
		Commands []string `json:"commands"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}

	appNames := make([]string, 0, len(p.AppNames))
	for _, appName := range p.AppNames {
		if appName = strings.TrimSpace(appName); appName != "" {
			appNames = append(appNames, appName)
		}
	}
	commands := make([]string, 0, len(p.Commands))
	for _, command := range p.Commands {
		if command = strings.TrimSpace(command); command != "" {
			commands = append(commands, command)
		}
	}
	if len(appNames) == 0 && len(commands) == 0 {
		return ToolResult{}, fmt.Errorf("provide appNames, commands, or both")
	}

	appRecords := make([]map[string]any, 0, len(appNames))
	for _, appName := range appNames {
		record := map[string]any{
			"name":      appName,
			"installed": false,
			"running":   false,
		}
		if appPath, ok := lookupAppPath(ctx, appName); ok {
			record["installed"] = true
			record["path"] = appPath
		}
		if bundleID, ok := lookupBundleID(ctx, appName); ok {
			record["installed"] = true
			record["bundleID"] = bundleID
		}
		if running, ok := appIsRunning(ctx, appName); ok {
			record["running"] = running
		}
		appRecords = append(appRecords, record)
	}

	commandRecords := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		record := map[string]any{
			"name":      command,
			"installed": false,
		}
		if path, err := exec.LookPath(command); err == nil {
			record["installed"] = true
			record["path"] = path
		}
		commandRecords = append(commandRecords, record)
	}

	summaryParts := []string{}
	if len(appRecords) > 0 {
		installedCount := 0
		runningCount := 0
		for _, record := range appRecords {
			if record["installed"] == true {
				installedCount++
			}
			if record["running"] == true {
				runningCount++
			}
		}
		summaryParts = append(summaryParts, fmt.Sprintf("%d/%d app(s) installed, %d running", installedCount, len(appRecords), runningCount))
	}
	if len(commandRecords) > 0 {
		installedCount := 0
		for _, record := range commandRecords {
			if record["installed"] == true {
				installedCount++
			}
		}
		summaryParts = append(summaryParts, fmt.Sprintf("%d/%d command(s) on PATH", installedCount, len(commandRecords)))
	}

	return OKResult("App capability scan complete: "+strings.Join(summaryParts, "; "), map[string]any{
		"apps":     appRecords,
		"commands": commandRecords,
	}), nil
}

func lookupAppPath(ctx context.Context, appName string) (string, bool) {
	target := strings.TrimSpace(appName)
	if target == "" {
		return "", false
	}
	if !strings.HasSuffix(strings.ToLower(target), ".app") {
		target += ".app"
	}

	searchRoots := []string{"/Applications", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		searchRoots = append(searchRoots, filepath.Join(home, "Applications"))
	}
	for _, root := range searchRoots {
		candidate := filepath.Join(root, target)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}

	query := fmt.Sprintf(`kMDItemFSName == "%s"c && kMDItemContentType == "com.apple.application-bundle"`, target)
	out, err := runCmd(ctx, "mdfind", query)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return strings.TrimSpace(strings.Split(out, "\n")[0]), true
}

func lookupBundleID(ctx context.Context, appName string) (string, bool) {
	out, err := runCmd(ctx, "osascript", "-e", fmt.Sprintf(`id of application %q`, appName))
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return strings.TrimSpace(out), true
}

func appIsRunning(ctx context.Context, appName string) (bool, bool) {
	script := fmt.Sprintf(`tell application "System Events" to (name of processes) contains %q`, appName)
	out, err := runCmd(ctx, "osascript", "-e", script)
	if err != nil {
		return false, false
	}
	return strings.TrimSpace(out) == "true", true
}
