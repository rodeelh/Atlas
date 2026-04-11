package features

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGremlinDestinationRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dest := &CommunicationDestination{
		ID:        "whatsapp:16464257838@s.whatsapp.net",
		Platform:  "whatsapp",
		ChannelID: "16464257838@s.whatsapp.net",
	}
	item := GremlinItem{
		Name:                     "Daily Check",
		Emoji:                    "⚡",
		Prompt:                   "Send digest",
		ScheduleRaw:              "daily 08:00",
		IsEnabled:                true,
		SourceType:               "web",
		CreatedAt:                "2026-04-07",
		CommunicationDestination: dest,
	}
	if err := AppendGremlin(dir, item); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one item, got %d", len(items))
	}
	if items[0].CommunicationDestination == nil {
		t.Fatal("expected communication destination to be persisted")
	}
	if items[0].CommunicationDestination.Platform != "whatsapp" {
		t.Fatalf("unexpected platform: %s", items[0].CommunicationDestination.Platform)
	}
	if items[0].CommunicationDestination.ChannelID != "16464257838@s.whatsapp.net" {
		t.Fatalf("unexpected channelID: %s", items[0].CommunicationDestination.ChannelID)
	}
}

func TestUpdateGremlinAllowsClearingDestination(t *testing.T) {
	dir := t.TempDir()
	item := GremlinItem{
		Name:        "Digest",
		Emoji:       "⚡",
		Prompt:      "Summarize",
		ScheduleRaw: "daily 08:00",
		IsEnabled:   true,
		SourceType:  "web",
		CreatedAt:   "2026-04-07",
		CommunicationDestination: &CommunicationDestination{
			ID:        "telegram:123",
			Platform:  "telegram",
			ChannelID: "123",
		},
	}
	if err := AppendGremlin(dir, item); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one item, got %d", len(items))
	}
	updated, err := UpdateGremlin(dir, items[0].ID, GremlinItem{
		Name:                     items[0].Name,
		Emoji:                    items[0].Emoji,
		Prompt:                   items[0].Prompt,
		ScheduleRaw:              items[0].ScheduleRaw,
		IsEnabled:                items[0].IsEnabled,
		SourceType:               items[0].SourceType,
		CreatedAt:                items[0].CreatedAt,
		CommunicationDestination: nil,
	})
	if err != nil {
		t.Fatalf("UpdateGremlin: %v", err)
	}
	if updated == nil {
		t.Fatal("expected updated item")
	}
	reloaded := ParseGremlins(dir)
	if len(reloaded) != 1 {
		t.Fatalf("expected one reloaded item, got %d", len(reloaded))
	}
	if reloaded[0].CommunicationDestination != nil {
		t.Fatal("expected destination to be cleared")
	}

	// Ensure file remains parseable.
	if raw := ReadGremlinsRaw(dir); raw == "" {
		t.Fatalf("expected non-empty %s", filepath.Join(dir, gremlinsFile))
	}
}

func TestAppendGremlinRejectsDuplicateID(t *testing.T) {
	dir := t.TempDir()
	item := GremlinItem{
		Name:        "Daily Check",
		Emoji:       "⚡",
		Prompt:      "Send digest",
		ScheduleRaw: "daily 08:00",
		IsEnabled:   true,
		SourceType:  "web",
		CreatedAt:   "2026-04-07",
	}
	if err := AppendGremlin(dir, item); err != nil {
		t.Fatalf("AppendGremlin first: %v", err)
	}
	err := AppendGremlin(dir, item)
	if err == nil || !strings.Contains(err.Error(), "automation id already exists") {
		t.Fatalf("expected duplicate ID error, got %v", err)
	}
}

func TestWriteGremlinsRawRejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	raw := `## Daily Check [⚡]
schedule: daily 08:00
status: enabled
created: 2026-04-07 via web

First
---

## Daily Check [⚡]
schedule: daily 09:00
status: enabled
created: 2026-04-07 via web

Second
---`
	err := WriteGremlinsRaw(dir, raw)
	if err == nil || !strings.Contains(err.Error(), "duplicate automation id") {
		t.Fatalf("expected duplicate raw ID error, got %v", err)
	}
}

func TestGremlinExecutableTargetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	item := GremlinItem{
		Name:        "Theme PDF",
		Emoji:       "⚡",
		Prompt:      "Create the weekly PDF",
		ScheduleRaw: "weekly Friday at 09:00",
		IsEnabled:   true,
		SourceType:  "web",
		CreatedAt:   "2026-04-07",
		ExecutableTarget: &ExecutableTarget{
			Type: "skill",
			Ref:  "theme-pdf.run",
		},
		WorkflowInputValues: map[string]string{
			"theme": "market recap",
		},
	}
	if err := AppendGremlin(dir, item); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one item, got %d", len(items))
	}
	if items[0].ExecutableTarget == nil {
		t.Fatal("expected executable target to be preserved")
	}
	if items[0].ExecutableTarget.Type != "skill" || items[0].ExecutableTarget.Ref != "theme-pdf.run" {
		t.Fatalf("unexpected target: %+v", items[0].ExecutableTarget)
	}
	if items[0].WorkflowID != nil {
		t.Fatalf("skill target should not populate workflowID, got %q", *items[0].WorkflowID)
	}
	if items[0].WorkflowInputValues["theme"] != "market recap" {
		t.Fatalf("expected target inputs to round-trip, got %+v", items[0].WorkflowInputValues)
	}
	raw := ReadGremlinsRaw(dir)
	if !strings.Contains(raw, "target_type: skill") || !strings.Contains(raw, "target_ref: theme-pdf.run") {
		t.Fatalf("expected target metadata in GREMLINS.md, got:\n%s", raw)
	}
}
