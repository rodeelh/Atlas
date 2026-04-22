package skills

import "testing"

func TestDedicatedAppleScriptToolHint_ForMusic(t *testing.T) {
	hint := dedicatedAppleScriptToolHint(`tell application "Music" to play`)
	if hint == "" {
		t.Fatal("expected dedicated tool hint for Music AppleScript")
	}
}

func TestDedicatedAppleScriptToolHint_EmptyForUnknownScript(t *testing.T) {
	hint := dedicatedAppleScriptToolHint(`display dialog "hello"`)
	if hint != "" {
		t.Fatalf("expected no hint for generic script, got %q", hint)
	}
}
