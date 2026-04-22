package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAtlasSessionCapabilitiesReportsUnleashedState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"autonomyMode":"unleashed","toolSelectionMode":"lazy","actionSafetyMode":"more_autonomous"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	r := NewRegistry(dir, nil, nil)
	result, err := r.Execute(context.Background(), "atlas.session_capabilities", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if got := result.Artifacts["autonomy_mode"]; got != "unleashed" {
		t.Fatalf("autonomy_mode = %v, want unleashed", got)
	}
	if got := result.Artifacts["normal_non_read_actions_auto_run"]; got != true {
		t.Fatalf("normal_non_read_actions_auto_run = %v, want true", got)
	}
	if got := result.Artifacts["app_capabilities_available"]; got != true {
		t.Fatalf("app_capabilities_available = %v, want true", got)
	}
	if got := result.Artifacts["command_check_available"]; got != true {
		t.Fatalf("command_check_available = %v, want true", got)
	}
	if got := result.Artifacts["workspace_roots_available"]; got != true {
		t.Fatalf("workspace_roots_available = %v, want true", got)
	}
}

func TestAtlasDiagnoseBlockerReportsMissingTool(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, nil)
	result, err := r.Execute(context.Background(), "atlas.diagnose_blocker", json.RawMessage(`{"action":"messages.read_recent"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if got := result.Artifacts["status"]; got != "missing_tool" {
		t.Fatalf("status = %v, want missing_tool", got)
	}
}

func TestAtlasDiagnoseBlockerReportsSandboxLockedSurface(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"autonomyMode":"sandboxed"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	r := NewRegistry(dir, nil, nil)
	result, err := r.Execute(context.Background(), "atlas.diagnose_blocker", json.RawMessage(`{"action":"atlas.update_operator_prompt"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if got := result.Artifacts["status"]; got != "sandbox_locked_surface" {
		t.Fatalf("status = %v, want sandbox_locked_surface", got)
	}
}

func TestSystemAppCapabilitiesReportsCommandPresence(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, nil)
	result, err := r.Execute(context.Background(), "system.app_capabilities", json.RawMessage(`{"commands":["osascript"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	commands, ok := result.Artifacts["commands"].([]map[string]any)
	if !ok || len(commands) != 1 {
		t.Fatalf("commands artifact = %#v, want one command record", result.Artifacts["commands"])
	}
	if commands[0]["installed"] != true {
		t.Fatalf("installed = %v, want true", commands[0]["installed"])
	}
}

func TestTerminalCheckCommandReportsInstalledCommand(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, nil)
	result, err := r.Execute(context.Background(), "terminal.check_command", json.RawMessage(`{"command":"osascript"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if got := result.Artifacts["installed"]; got != true {
		t.Fatalf("installed = %v, want true", got)
	}
	if got := result.Artifacts["runnable"]; got != true {
		t.Fatalf("runnable = %v, want true", got)
	}
	if got := result.Artifacts["path"]; got == "" {
		t.Fatalf("path = %v, want non-empty", got)
	}
}
