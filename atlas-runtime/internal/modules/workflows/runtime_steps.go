package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/workflowexec"
)

type workflowPromptStep struct {
	ID     string
	Title  string
	Kind   string
	Prompt string
}

func (m *Module) executePreparedWorkflow(ctx context.Context, prepared workflowexec.PreparedRun) (status, summary, errorMessage string, stepRuns []map[string]any) {
	steps := promptStepDefinitions(prepared.Definition)
	if len(steps) == 0 {
		req := chat.MessageRequest{
			Message:        prepared.Prompt,
			ConversationID: prepared.ConversationID,
			ToolPolicy:     toolPolicyForDefinition(prepared.Definition),
		}
		resp, execErr := m.agent.HandleMessage(ctx, req)
		if execErr != nil {
			return "failed", "", execErr.Error(), workflowexec.InitialStepRuns(prepared.Definition)
		}
		if resp.Response.Status == "error" {
			return "failed", "", strings.TrimSpace(resp.Response.AssistantMessage), workflowexec.InitialStepRuns(prepared.Definition)
		}
		return "completed", resp.Response.AssistantMessage, "", workflowexec.InitialStepRuns(prepared.Definition)
	}

	stepRuns = workflowexec.InitialStepRuns(prepared.Definition)
	summaries := make([]string, 0, len(steps))
	policy := toolPolicyForDefinition(prepared.Definition)
	objective := strings.TrimSpace(workflowexec.ComposePrompt(prepared.Definition, nil, ""))
	for idx, step := range steps {
		stepIndex := stepRunIndex(stepRuns, step.ID)
		if stepIndex < 0 {
			continue
		}
		markStepRun(stepRuns[stepIndex], "running", "", "")
		_ = m.persistStepRuns(prepared.RunID, stepRuns)

		message := composeStepPrompt(objective, step, idx+1, len(steps))
		req := chat.MessageRequest{Message: message, ConversationID: prepared.ConversationID, ToolPolicy: policy}
		resp, execErr := m.agent.HandleMessage(ctx, req)
		if execErr != nil {
			markStepRun(stepRuns[stepIndex], "failed", "", execErr.Error())
			_ = m.persistStepRuns(prepared.RunID, stepRuns)
			return "failed", strings.Join(summaries, "\n\n"), execErr.Error(), stepRuns
		}
		if resp.Response.Status == "error" {
			errMsg := strings.TrimSpace(resp.Response.AssistantMessage)
			if errMsg == "" {
				errMsg = strings.TrimSpace(resp.Response.ErrorMessage)
			}
			markStepRun(stepRuns[stepIndex], "failed", "", errMsg)
			_ = m.persistStepRuns(prepared.RunID, stepRuns)
			return "failed", strings.Join(summaries, "\n\n"), errMsg, stepRuns
		}
		out := strings.TrimSpace(resp.Response.AssistantMessage)
		markStepRun(stepRuns[stepIndex], "completed", out, "")
		if out != "" {
			summaries = append(summaries, fmt.Sprintf("%s: %s", step.Title, out))
		}
		_ = m.persistStepRuns(prepared.RunID, stepRuns)
	}
	return "completed", strings.Join(summaries, "\n\n"), "", stepRuns
}

func promptStepDefinitions(def map[string]any) []workflowPromptStep {
	raw, ok := def["steps"].([]any)
	if !ok {
		return nil
	}
	steps := make([]workflowPromptStep, 0, len(raw))
	for idx, item := range raw {
		step, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind := stringValue(step, "kind")
		if kind == "" {
			kind = "prompt"
		}
		if kind != "prompt" {
			continue
		}
		prompt := strings.TrimSpace(stringValue(step, "prompt"))
		if prompt == "" {
			continue
		}
		id := stringValue(step, "id")
		if id == "" {
			id = fmt.Sprintf("step-%d", idx+1)
		}
		title := stringValue(step, "title")
		if title == "" {
			title = fmt.Sprintf("Step %d", idx+1)
		}
		steps = append(steps, workflowPromptStep{ID: id, Title: title, Kind: kind, Prompt: prompt})
	}
	return steps
}

func composeStepPrompt(objective string, step workflowPromptStep, index, total int) string {
	parts := []string{
		fmt.Sprintf("Workflow step %d of %d: %s", index, total, step.Title),
		step.Prompt,
	}
	if strings.TrimSpace(objective) != "" {
		parts = append(parts, "Workflow context:\n"+strings.TrimSpace(objective))
	}
	return strings.Join(parts, "\n\n")
}

func stepRunIndex(stepRuns []map[string]any, stepID string) int {
	for i, run := range stepRuns {
		if stringValue(run, "stepID") == stepID {
			return i
		}
	}
	return -1
}

func markStepRun(run map[string]any, status, output, errorMessage string) {
	now := time.Now().UTC().Format(time.RFC3339)
	run["status"] = status
	switch status {
	case "running":
		run["startedAt"] = now
	case "completed", "failed", "skipped":
		run["finishedAt"] = now
	}
	if strings.TrimSpace(output) != "" {
		run["output"] = strings.TrimSpace(output)
	}
	if strings.TrimSpace(errorMessage) != "" {
		run["errorMessage"] = strings.TrimSpace(errorMessage)
	}
}

func (m *Module) persistStepRuns(runID string, stepRuns []map[string]any) error {
	if m.store == nil {
		return nil
	}
	data, err := json.Marshal(stepRuns)
	if err != nil {
		return err
	}
	return m.store.UpdateWorkflowRunStepRuns(runID, string(data))
}

func toolPolicyForDefinition(def map[string]any) *agent.ToolPolicy {
	scope, _ := def["trustScope"].(map[string]any)
	policy := &agent.ToolPolicy{
		Enabled:             true,
		AllowsSensitiveRead: boolValue(scope, "allowsSensitiveRead", false),
		AllowsLiveWrite:     boolValue(scope, "allowsLiveWrite", false),
		ApprovedRootPaths:   stringListValue(scope, "approvedRootPaths"),
		AllowedToolPrefixes: allowedToolPrefixes(stringListValue(scope, "allowedApps")),
	}
	return policy
}

func stringListValue(obj map[string]any, key string) []string {
	if obj == nil {
		return nil
	}
	switch raw := obj[key].(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func allowedToolPrefixes(apps []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(prefix string) {
		if prefix == "" || seen[prefix] {
			return
		}
		seen[prefix] = true
		out = append(out, prefix)
	}
	for _, app := range apps {
		switch strings.ToLower(strings.TrimSpace(app)) {
		case "filesystem", "files", "file system", "fs":
			add("fs.")
		case "calendar":
			add("applescript.calendar_")
		case "reminders":
			add("applescript.reminders_")
		case "mail":
			add("applescript.mail_")
		case "contacts":
			add("applescript.contacts_")
		case "notes":
			add("applescript.notes_")
		case "safari":
			add("applescript.safari_")
		case "music":
			add("applescript.music_")
		case "web", "websearch", "web search":
			add("web.")
			add("websearch.")
		case "browser":
			add("browser.")
		case "weather":
			add("weather.")
		case "finance":
			add("finance.")
		case "memory":
			add("memory.")
		case "terminal", "shell":
			add("terminal.")
		case "vault":
			add("vault.")
		case "image", "creative":
			add("image.")
		case "voice":
			add("voice.")
		case "communications", "communication", "chat", "chat bridge", "bridge":
			add("communication.")
		}
	}
	return out
}
