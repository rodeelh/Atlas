package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectiveToolDefsRecurringDeliveryIncludesAutomationAndCommunication(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)
	registry.RegisterExternal(SkillEntry{
		Def: ToolDef{
			Name:        "automation.create",
			Description: "Create a new Atlas automation.",
			Properties:  map[string]ToolParam{},
		},
		ActionClass: ActionClassLocalWrite,
		FnResult: func(context.Context, json.RawMessage) (ToolResult, error) {
			return OKResult("ok", nil), nil
		},
	})
	registry.RegisterExternal(SkillEntry{
		Def: ToolDef{
			Name:        "communication.list_channels",
			Description: "List authorized communication channels.",
			Properties:  map[string]ToolParam{},
		},
		ActionClass: ActionClassRead,
		FnResult: func(context.Context, json.RawMessage) (ToolResult, error) {
			return OKResult("ok", nil), nil
		},
	})

	tools := registry.SelectiveToolDefs("Send me a friendly detailed Orlando weather forecast every day at 8 AM on Telegram.")
	if !hasToolName(tools, "automation__create") {
		t.Fatalf("expected automation.create to be selected, got %v", toolNames(tools))
	}
	if !hasToolName(tools, "communication__list_channels") {
		t.Fatalf("expected communication.list_channels to be selected, got %v", toolNames(tools))
	}
}

func TestToolDefsForGroupsForMessageNarrowsWeatherGroup(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)
	tools := registry.ToolDefsForGroupsForMessage([]string{"weather"}, "What will the weather be in Orlando tomorrow?")
	names := toolNames(tools)
	if len(names) >= 9 {
		t.Fatalf("expected narrowed weather tool set, got %d tools: %v", len(names), names)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "weather__forecast") {
		t.Fatalf("expected forecast-style weather tool in narrowed set, got %v", names)
	}
}

func TestSelectiveToolDefsIncludesFilesystemForCreateAndSaveFiles(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)

	tools := registry.SelectiveToolDefs("I want Atlas to create and save files for me.")
	names := toolNames(tools)

	if !hasToolName(tools, "fs__write_file") {
		t.Fatalf("expected fs.write_file to be selected, got %v", names)
	}
	if !hasToolName(tools, "fs__create_directory") {
		t.Fatalf("expected fs.create_directory to be selected, got %v", names)
	}
}

func TestSelectiveToolDefsIncludesFilesystemForCommonTypoFileds(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)

	tools := registry.SelectiveToolDefs("Time to super charge atlas productivity... I want atlas to be able to create and save fileds")
	names := toolNames(tools)

	if !hasToolName(tools, "fs__write_file") {
		t.Fatalf("expected fs.write_file to be selected for typo input, got %v", names)
	}
}

func TestSelectiveToolDefsIncludesPDFToolForCreatePDFRequest(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)

	tools := registry.SelectiveToolDefs("Create a PDF with today's project summary.")
	names := toolNames(tools)

	if !hasToolName(tools, "fs__create_pdf") {
		t.Fatalf("expected fs.create_pdf to be selected, got %v", names)
	}
}

func TestSelectiveToolDefsIncludesTeamToolsForAgentManagementRequest(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)
	registry.RegisterExternal(SkillEntry{
		Def: ToolDef{
			Name:        "team.list",
			Description: "List Atlas agents.",
			Properties:  map[string]ToolParam{},
		},
		ActionClass: ActionClassRead,
		FnResult: func(context.Context, json.RawMessage) (ToolResult, error) {
			return OKResult("ok", nil), nil
		},
	})

	tools := registry.SelectiveToolDefs("Delete all agents.")
	names := toolNames(tools)

	if !hasToolName(tools, "agent__list") {
		t.Fatalf("expected agent.list public tool to be selected, got %v", names)
	}
}

func TestRegistryNormaliseResolvesAgentAliasToTeamAction(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)
	registry.RegisterExternal(SkillEntry{
		Def: ToolDef{
			Name:        "team.create",
			Description: "Create Atlas agents.",
			Properties:  map[string]ToolParam{},
		},
		ActionClass: ActionClassLocalWrite,
		FnResult: func(context.Context, json.RawMessage) (ToolResult, error) {
			return OKResult("ok", nil), nil
		},
	})
	registry.registerActionAlias("team.create", "agent.create")

	if got := registry.Normalise("agent.create"); got != "team.create" {
		t.Fatalf("Normalise(agent.create)=%q want team.create", got)
	}

	tools := registry.ToolDefinitions()
	if !hasToolName(tools, "agent__create") {
		t.Fatalf("expected public alias tool name agent__create, got %v", toolNames(tools))
	}
}

func TestSelectiveToolDefsIncludesCustomSkillViaDeclaredRoutingContract(t *testing.T) {
	supportDir := t.TempDir()
	skillDir := filepath.Join(supportDir, "skills", "ticket-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifest := `{
		"id":"ticket-helper",
		"name":"Ticket Helper",
		"description":"Manage support tickets.",
		"category":"productivity",
		"routing":{
			"capability_group":"tickets",
			"description":"Support ticket management and cleanup.",
			"phrases":["close stale tickets"],
			"words":["tickets"],
			"pairs":[["close","tickets"]],
			"threshold":1
		},
		"actions":[
			{
				"name":"close_stale",
				"description":"Close stale tickets.",
				"permission_level":"execute"
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(skillDir, "skill.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("Write skill.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "run"), []byte("#!/bin/sh\nprintf '{\"success\":true,\"output\":\"ok\"}\\n'\n"), 0o755); err != nil {
		t.Fatalf("Write run: %v", err)
	}

	registry := NewRegistry(supportDir, nil, nil)
	registry.LoadCustomSkills(supportDir)

	tools := registry.SelectiveToolDefs("Please close stale tickets.")
	names := toolNames(tools)

	if !hasToolName(tools, "ticket-helper__close_stale") {
		t.Fatalf("expected custom routed skill to be selected, got %v", names)
	}
}

func TestCustomSkillDeclarativeCommandRunnerLoadsAndExecutes(t *testing.T) {
	supportDir := t.TempDir()
	skillDir := filepath.Join(supportDir, "skills", "music-player")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifest := `{
		"id":"music-player",
		"name":"Music Player",
		"description":"Play catalog music.",
		"actions":[
			{
				"id":"play-song",
				"name":"Play Song",
				"description":"Play a song.",
				"permission_level":"execute",
				"input":{
					"type":"object",
					"properties":{"query":{"type":"string"}},
					"required":["query"]
				},
				"runner":{
					"type":"command",
					"command":"/bin/echo",
					"args":["playing {query}"]
				}
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(skillDir, "skill.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("Write skill.json: %v", err)
	}

	registry := NewRegistry(supportDir, nil, nil)
	registry.LoadCustomSkills(supportDir)

	if !hasToolName(registry.ToolDefinitions(), "music-player__play-song") {
		t.Fatalf("expected declarative runner skill to be registered, got %v", toolNames(registry.ToolDefinitions()))
	}
	result, err := registry.Execute(context.Background(), "music-player.play-song", json.RawMessage(`{"query":"Diamonds"}`))
	if err != nil {
		t.Fatalf("Execute declarative runner: %v", err)
	}
	if !result.Success || result.Summary != "playing Diamonds" {
		t.Fatalf("unexpected runner result: success=%v summary=%q", result.Success, result.Summary)
	}
}

// ─── Phase B1: team work-routing heuristic ───────────────────────────────────

// TestSelectiveToolDefs_TeamWorkRoutingPhrases verifies that work-routing
// phrases ("use a specialist", "ask scout", "have the team", etc.) activate
// the "team" group and expose team tools (Phase B1).
//
// Note: team.delegate is registered with canonical name "team.delegate" but
// the registry creates a public alias "agent.delegate" for it (OAI wire:
// "agent__delegate"). That is the name to check in SelectiveToolDefs output.
func TestSelectiveToolDefs_TeamWorkRoutingPhrases(t *testing.T) {
	phrases := []string{
		"use a specialist for this",
		"have a specialist handle it",
		"ask a specialist to do this",
		"use the team",
		"have the team review this",
		"delegate this",
		"send this to my team",
		"let scout handle the research",
		"ask scout to find pricing data",
		"have scout look into this",
		"use the reviewer",
		"ask the reviewer to check",
		"let a teammate take this",
	}
	for _, phrase := range phrases {
		t.Run(phrase, func(t *testing.T) {
			reg := NewRegistry(t.TempDir(), nil, nil)
			reg.RegisterExternal(SkillEntry{
				Def: ToolDef{
					Name:        "team.delegate",
					Description: "Delegate work to a team specialist.",
					Properties:  map[string]ToolParam{},
				},
				ActionClass: ActionClassLocalWrite,
				FnResult: func(_ context.Context, _ json.RawMessage) (ToolResult, error) {
					return OKResult("ok", nil), nil
				},
			})
			tools := reg.SelectiveToolDefs(phrase)
			// team.delegate's public OAI name is agent__delegate (alias registered by registry.register).
			if !hasToolName(tools, "agent__delegate") {
				t.Errorf("phrase %q: expected team.delegate (agent__delegate) in tool set, got %v", phrase, toolNames(tools))
			}
		})
	}
}

// TestSelectiveToolDefs_TeamControlPhrasesStillWork verifies that existing
// team-control phrases (e.g. "show my team", "list agents") still activate
// the "team" group (regression guard for B1 — must not break existing signals).
// Uses team.list (team.* namespace) which the routing contract maps to "team".
func TestSelectiveToolDefs_TeamControlPhrasesStillWork(t *testing.T) {
	phrases := []string{
		"list my agents",
		"list all agents",
		"manage team members",
	}
	for _, phrase := range phrases {
		t.Run(phrase, func(t *testing.T) {
			reg := NewRegistry(t.TempDir(), nil, nil)
			reg.RegisterExternal(SkillEntry{
				Def: ToolDef{
					Name:        "team.list",
					Description: "List all team members.",
					Properties:  map[string]ToolParam{},
				},
				ActionClass: ActionClassRead,
				FnResult: func(_ context.Context, _ json.RawMessage) (ToolResult, error) {
					return OKResult("ok", nil), nil
				},
			})
			tools := reg.SelectiveToolDefs(phrase)
			// team.list's public OAI alias is agent__list.
			if !hasToolName(tools, "agent__list") {
				t.Errorf("phrase %q: expected team.list (agent__list) in tool set, got %v", phrase, toolNames(tools))
			}
		})
	}
}

// TestSelectiveToolDefs_TeamFalsePositivesControlled verifies that generic
// work vocabulary that should NOT activate team tools does not do so.
// The B1 signals require explicit specialist/team/delegate framing.
func TestSelectiveToolDefs_TeamFalsePositivesControlled(t *testing.T) {
	phrases := []string{
		"what is the weather today",
		"search the web for golang tutorials",
		"read my files",
		"help me write an email",
		"what time is it",
	}
	for _, phrase := range phrases {
		t.Run(phrase, func(t *testing.T) {
			reg := NewRegistry(t.TempDir(), nil, nil)
			reg.RegisterExternal(SkillEntry{
				Def: ToolDef{
					Name:        "team.delegate",
					Description: "Delegate work to a team specialist.",
					Properties:  map[string]ToolParam{},
				},
				ActionClass: ActionClassLocalWrite,
				FnResult: func(_ context.Context, _ json.RawMessage) (ToolResult, error) {
					return OKResult("ok", nil), nil
				},
			})
			tools := reg.SelectiveToolDefs(phrase)
			// team.delegate's public OAI name is agent__delegate — check that too.
			if hasToolName(tools, "agent__delegate") {
				t.Errorf("phrase %q: team.delegate (agent__delegate) should NOT be in tool set for generic phrase, got %v", phrase, toolNames(tools))
			}
		})
	}
}

func hasToolName(tools []map[string]any, want string) bool {
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == want {
			return true
		}
	}
	return false
}

func toolNames(tools []map[string]any) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}
