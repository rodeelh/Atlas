package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/features"
	forgesvc "atlas-runtime-go/internal/forge"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
)

// aiProviderAdapter keeps forge isolated from the agent package while allowing
// the module to reuse the existing provider resolution path.
type aiProviderAdapter struct {
	cfg agent.ProviderConfig
}

func (a aiProviderAdapter) CallNonStreaming(ctx context.Context, system, user string) (string, error) {
	messages := []agent.OAIMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	reply, _, _, err := agent.CallAINonStreamingExported(ctx, a.cfg, messages, nil)
	if err != nil {
		return "", err
	}
	if s, ok := reply.Content.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", reply.Content), nil
}

type Module struct {
	supportDir string
	service    *forgesvc.Service
	chatSvc    *chat.Service
	skillsReg  *skills.Registry
}

func New(supportDir string, service *forgesvc.Service, chatSvc *chat.Service, skillsReg *skills.Registry) *Module {
	return &Module{
		supportDir: supportDir,
		service:    service,
		chatSvc:    chatSvc,
		skillsReg:  skillsReg,
	}
}

func (m *Module) ID() string { return "forge" }

func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{Version: "v1"}
}

func (m *Module) Register(host platform.Host) error {
	m.registerInstalledWorkflowActions()
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/forge/researching", m.getResearching)
	r.Get("/forge/proposals", m.listProposals)
	r.Post("/forge/proposals", m.propose)
	r.Get("/forge/installed", m.listInstalled)
	r.Post("/forge/installed/{skillID}/uninstall", m.uninstall)
	r.Post("/forge/proposals/{id}/install", m.install)
	r.Post("/forge/proposals/{id}/install-enable", m.installEnable)
	r.Post("/forge/proposals/{id}/reject", m.reject)
}

func (m *Module) getResearching(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.service.GetResearching())
}

func (m *Module) listProposals(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, forgesvc.ListProposals(m.supportDir))
}

func (m *Module) propose(w http.ResponseWriter, r *http.Request) {
	var req forgesvc.ProposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" || req.Description == "" {
		writeError(w, http.StatusBadRequest, "name and description are required")
		return
	}
	if m.chatSvc == nil {
		writeError(w, http.StatusNotImplemented, "agent loop not available")
		return
	}

	fastProvider, err := m.chatSvc.ResolveFastProvider()
	if err != nil {
		fastProvider, err = m.chatSvc.ResolveProvider()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "AI provider not configured: "+err.Error())
			return
		}
	}

	researchID := newID()
	now := time.Now().UTC().Format(time.RFC3339)
	item := forgesvc.ResearchingItem{
		ID:        researchID,
		Title:     req.Name,
		Message:   "Researching \"" + req.Name + "\"…",
		StartedAt: now,
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = m.service.Propose(ctx, req, aiProviderAdapter{cfg: fastProvider})
	}()

	writeJSON(w, http.StatusAccepted, item)
}

func (m *Module) listInstalled(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, forgesvc.ListInstalled(m.supportDir))
}

func (m *Module) install(w http.ResponseWriter, r *http.Request) {
	m.installProposal(w, chi.URLParam(r, "id"), false)
}

func (m *Module) installEnable(w http.ResponseWriter, r *http.Request) {
	m.installProposal(w, chi.URLParam(r, "id"), true)
}

func (m *Module) installProposal(w http.ResponseWriter, id string, enable bool) {
	proposal := forgesvc.GetProposal(m.supportDir, id)
	if proposal == nil {
		writeError(w, http.StatusNotFound, "proposal not found: "+id)
		return
	}

	target, err := forgesvc.InstallProposalArtifacts(m.supportDir, *proposal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to install proposal artifacts: "+err.Error())
		return
	}
	if target != nil && target.Type == "custom_skill" && m.skillsReg != nil {
		m.skillsReg.ReloadCustomSkill(m.supportDir, proposal.SkillID)
	} else if target != nil && target.Type == "workflow" {
		m.registerInstalledWorkflowActions()
	}

	status := "installed"
	if enable {
		status = "enabled"
	}

	record := forgesvc.BuildInstalledRecord(*proposal, status, target)
	if err := forgesvc.SaveInstalled(m.supportDir, record); err != nil {
		if target != nil && target.Type == "custom_skill" {
			_ = forgesvc.RemoveCustomSkillDir(m.supportDir, proposal.SkillID)
		}
		if target != nil && target.Type == "workflow" {
			_ = forgesvc.RemoveWorkflowInstall(m.supportDir, target.Ref)
		}
		writeError(w, http.StatusInternalServerError, "failed to save installed skill: "+err.Error())
		return
	}

	updatedProposal, err := forgesvc.UpdateProposalStatus(m.supportDir, id, status)
	if err != nil {
		_, _ = forgesvc.DeleteInstalled(m.supportDir, proposal.SkillID)
		if target != nil && target.Type == "custom_skill" {
			_ = forgesvc.RemoveCustomSkillDir(m.supportDir, proposal.SkillID)
		}
		if target != nil && target.Type == "workflow" {
			_ = forgesvc.RemoveWorkflowInstall(m.supportDir, target.Ref)
		}
		writeError(w, http.StatusInternalServerError, "failed to update proposal status: "+err.Error())
		return
	}
	if updatedProposal == nil {
		_, _ = forgesvc.DeleteInstalled(m.supportDir, proposal.SkillID)
		if target != nil && target.Type == "custom_skill" {
			_ = forgesvc.RemoveCustomSkillDir(m.supportDir, proposal.SkillID)
		}
		if target != nil && target.Type == "workflow" {
			_ = forgesvc.RemoveWorkflowInstall(m.supportDir, target.Ref)
		}
		writeError(w, http.StatusNotFound, "proposal not found: "+id)
		return
	}

	features.SetForgeSkillState(m.supportDir, proposal.SkillID, status)

	writeJSON(w, http.StatusOK, updatedProposal)
}

func (m *Module) reject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	proposal, err := forgesvc.UpdateProposalStatus(m.supportDir, id, "rejected")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update proposal status: "+err.Error())
		return
	}
	if proposal == nil {
		writeError(w, http.StatusNotFound, "proposal not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (m *Module) uninstall(w http.ResponseWriter, r *http.Request) {
	skillID := chi.URLParam(r, "skillID")
	record := forgesvc.GetInstalled(m.supportDir, skillID)

	found, err := forgesvc.DeleteInstalled(m.supportDir, skillID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to uninstall: "+err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "installed skill not found: "+skillID)
		return
	}

	targetType, targetRef := installedTarget(record)
	if targetType == "workflow" {
		m.unregisterInstalledActions(record)
		if err := forgesvc.RemoveWorkflowInstall(m.supportDir, targetRef); err != nil {
			logstore.Write("warn", "forge/uninstall: could not remove workflow install for "+skillID+": "+err.Error(), nil)
		}
	} else if err := forgesvc.RemoveCustomSkillDir(m.supportDir, skillID); err != nil {
		logstore.Write("warn", "forge/uninstall: could not remove custom skill dir for "+skillID+": "+err.Error(), nil)
	}

	features.SetForgeSkillState(m.supportDir, skillID, "uninstalled")

	writeJSON(w, http.StatusOK, map[string]any{
		"skillID":     skillID,
		"uninstalled": true,
	})
}

func installedTarget(record map[string]any) (string, string) {
	if record == nil {
		return "", ""
	}
	target, _ := record["target"].(map[string]any)
	targetType, _ := target["type"].(string)
	targetRef, _ := target["ref"].(string)
	return strings.TrimSpace(targetType), strings.TrimSpace(targetRef)
}

func (m *Module) registerInstalledWorkflowActions() {
	if m.skillsReg == nil {
		return
	}
	for _, record := range forgesvc.ListInstalled(m.supportDir) {
		targetType, targetRef := installedTarget(record)
		if targetType != "workflow" || targetRef == "" {
			continue
		}
		for _, action := range installedActions(record) {
			action := action
			if action.ID == "" {
				continue
			}
			m.skillsReg.RegisterExternal(skills.SkillEntry{
				Def: skills.ToolDef{
					Name:        action.ID,
					Description: defaultForgeActionDescription(action.Description, targetRef),
					Properties: map[string]skills.ToolParam{
						"inputValuesJSON": {
							Description: "Optional JSON object string with workflow input values, for example {\"theme\":\"Atlas productivity\",\"path\":\"/tmp/out.pdf\"}.",
							Type:        "string",
						},
					},
				},
				PermLevel: action.PermissionLevel,
				FnResult: func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
					var payload struct {
						InputValuesJSON string `json:"inputValuesJSON"`
					}
					if len(args) > 0 {
						if err := json.Unmarshal(args, &payload); err != nil {
							return skills.ToolResult{}, fmt.Errorf("invalid workflow-backed skill args: %w", err)
						}
					}
					workflowArgs := map[string]any{"id": targetRef}
					if strings.TrimSpace(payload.InputValuesJSON) != "" {
						workflowArgs["inputValuesJSON"] = payload.InputValuesJSON
					}
					runArgs, err := json.Marshal(workflowArgs)
					if err != nil {
						return skills.ToolResult{}, fmt.Errorf("encode workflow run args: %w", err)
					}
					return m.skillsReg.Execute(ctx, "workflow.run", runArgs)
				},
			})
		}
	}
}

func (m *Module) unregisterInstalledActions(record map[string]any) {
	if m.skillsReg == nil {
		return
	}
	for _, action := range installedActions(record) {
		if action.ID != "" {
			m.skillsReg.Unregister(action.ID)
		}
	}
}

type forgeInstalledAction struct {
	ID              string
	Description     string
	PermissionLevel string
}

func installedActions(record map[string]any) []forgeInstalledAction {
	raw, _ := record["actions"].([]any)
	out := make([]forgeInstalledAction, 0, len(raw))
	for _, item := range raw {
		action, _ := item.(map[string]any)
		if action == nil {
			continue
		}
		out = append(out, forgeInstalledAction{
			ID:              strings.TrimSpace(stringValueAny(action["id"])),
			Description:     strings.TrimSpace(stringValueAny(action["description"])),
			PermissionLevel: strings.TrimSpace(stringValueAny(action["permissionLevel"])),
		})
	}
	return out
}

func defaultForgeActionDescription(description, workflowID string) string {
	description = strings.TrimSpace(description)
	if description != "" {
		return description
	}
	return "Run workflow-backed Forge capability " + workflowID + "."
}

func stringValueAny(v any) string {
	s, _ := v.(string)
	return s
}

func newID() string {
	return "forge-" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
