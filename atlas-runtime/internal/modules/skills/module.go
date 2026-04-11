package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/capabilities"
	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/creds"
	"atlas-runtime-go/internal/customskills"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/forge"
	"atlas-runtime-go/internal/platform"
	runtimeskills "atlas-runtime-go/internal/skills"
)

type Module struct {
	supportDir string
	fsRootsMu  sync.Mutex
	storage    platform.Storage
}

func New(supportDir string) *Module {
	return &Module{supportDir: supportDir}
}

func (m *Module) ID() string { return "skills" }

func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{Version: "v1"}
}

func (m *Module) Register(host platform.Host) error {
	m.storage = host.Storage()
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/capabilities", m.listCapabilities)
	r.Get("/skills", m.listSkills)
	r.Get("/skills/custom", m.listCustomSkills)
	r.Post("/skills/install", m.installCustomSkill)
	r.Get("/skills/file-system/roots", m.listFsRoots)
	r.Post("/skills/file-system/roots", m.addFsRoot)
	r.Post("/skills/file-system/roots/{id}/remove", m.removeFsRoot)
	r.Post("/skills/file-system/pick-folder", m.pickFsFolder)
	r.Post("/skills/{id}/enable", m.enableSkill)
	r.Post("/skills/{id}/disable", m.disableSkill)
	r.Post("/skills/{id}/validate", m.validateSkill)
	r.Delete("/skills/{id}", m.removeCustomSkill)
}

func (m *Module) listCapabilities(w http.ResponseWriter, _ *http.Request) {
	var workflows capabilities.WorkflowLister
	var automations capabilities.AutomationLister
	if m.storage != nil {
		workflows = m.storage.Workflows()
		automations = m.storage.Automations()
	}
	records, err := capabilities.List(m.supportDir, workflows, automations)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list capabilities: "+err.Error())
		return
	}
	if records == nil {
		records = []capabilities.Record{}
	}
	writeJSON(w, http.StatusOK, records)
}

func (m *Module) listSkills(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.listVisibleSkills())
}

func (m *Module) listCustomSkills(w http.ResponseWriter, _ *http.Request) {
	manifests := customskills.ListManifests(m.supportDir)
	if manifests == nil {
		manifests = []customskills.CustomSkillManifest{}
	}
	writeJSON(w, http.StatusOK, manifests)
}

func (m *Module) listFsRoots(w http.ResponseWriter, _ *http.Request) {
	roots, err := runtimeskills.LoadFsRoots(m.supportDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read fs roots: "+err.Error())
		return
	}
	if roots == nil {
		roots = []runtimeskills.FsRoot{}
	}
	writeJSON(w, http.StatusOK, roots)
}

func (m *Module) addFsRoot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	m.fsRootsMu.Lock()
	defer m.fsRootsMu.Unlock()

	roots, err := runtimeskills.LoadFsRoots(m.supportDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read fs roots: "+err.Error())
		return
	}
	newRoot := runtimeskills.FsRoot{ID: runtimeskills.NewFsRootID(), Path: body.Path}
	roots = append(roots, newRoot)
	if err := runtimeskills.SaveFsRoots(m.supportDir, roots); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save fs roots: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newRoot)
}

func (m *Module) removeFsRoot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m.fsRootsMu.Lock()
	defer m.fsRootsMu.Unlock()

	roots, err := runtimeskills.LoadFsRoots(m.supportDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read fs roots: "+err.Error())
		return
	}

	filtered := make([]runtimeskills.FsRoot, 0, len(roots))
	found := false
	for _, root := range roots {
		if root.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, root)
	}
	if !found {
		writeError(w, http.StatusNotFound, "root not found: "+id)
		return
	}
	if err := runtimeskills.SaveFsRoots(m.supportDir, filtered); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save fs roots: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (m *Module) pickFsFolder(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e",
		`POSIX path of (choose folder with prompt "Select a folder to grant Atlas access to:")`)
	out, err := cmd.Output()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
		return
	}
	path := strings.TrimRight(strings.TrimSpace(string(out)), "/")
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (m *Module) enableSkill(w http.ResponseWriter, r *http.Request) {
	m.setSkillState(w, r, "enabled")
}

func (m *Module) disableSkill(w http.ResponseWriter, r *http.Request) {
	m.setSkillState(w, r, "disabled")
}

func (m *Module) setSkillState(w http.ResponseWriter, r *http.Request, state string) {
	id := chi.URLParam(r, "id")
	rec := features.SetSkillState(m.supportDir, id, state)
	if rec == nil {
		if forgeRec := updateForgeInstalledSkillState(m.supportDir, id, state); forgeRec != nil {
			writeJSON(w, http.StatusOK, forgeRec)
			return
		}
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "skill not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (m *Module) validateSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	rec := features.ValidateSkill(m.supportDir, id, credentialCheckForSkill)
	if rec != nil {
		writeJSON(w, http.StatusOK, rec)
		return
	}
	if result := validateForgeSkill(m.supportDir, id); result != nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	writeError(w, http.StatusNotFound, "skill not found: "+id)
}

func (m *Module) installCustomSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if _, err := os.Stat(filepath.Join(body.Path, "skill.json")); err != nil {
		writeError(w, http.StatusBadRequest, "source path does not contain skill.json")
		return
	}
	if _, err := os.Stat(filepath.Join(body.Path, "run")); err != nil {
		writeError(w, http.StatusBadRequest, "source path does not contain a run executable")
		return
	}

	data, err := os.ReadFile(filepath.Join(body.Path, "skill.json"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read skill.json: "+err.Error())
		return
	}
	var manifest struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid skill.json: "+err.Error())
		return
	}
	if manifest.ID == "" {
		writeError(w, http.StatusBadRequest, "skill.json must contain an id field")
		return
	}

	targetDir := filepath.Join(customskills.SkillsDir(m.supportDir), manifest.ID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create skill directory: "+err.Error())
		return
	}
	if err := copySkillFiles(body.Path, targetDir); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to install skill: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      manifest.ID,
		"path":    targetDir,
		"message": "Skill installed. Restart Atlas for the skill to become active.",
	})
}

func (m *Module) removeCustomSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	skillDir := filepath.Join(customskills.SkillsDir(m.supportDir), id)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "custom skill not found: "+id)
		return
	}
	if err := os.RemoveAll(skillDir); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove skill: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      id,
		"removed": true,
	})
}

func validateForgeSkill(supportDir, skillID string) map[string]any {
	installed := forge.ListInstalled(supportDir)
	var rec map[string]any
	for _, record := range installed {
		if id, _ := record["id"].(string); id == skillID {
			rec = record
			break
		}
	}
	if rec == nil {
		return nil
	}

	bundle, _ := creds.Read()
	var missing []string
	if secrets, ok := rec["requiredSecrets"].([]any); ok {
		for _, secret := range secrets {
			name, _ := secret.(string)
			if name != "" && bundle.CustomSecret(name) == "" {
				missing = append(missing, name)
			}
		}
	}

	valid := len(missing) == 0
	status := "passed"
	summary := "Skill is ready."
	issues := []string{}
	if !valid {
		status = "failed"
		summary = "Missing custom API keys: " + strings.Join(missing, ", ") + ". Add them in Settings → Credentials."
		issues = missing
	}

	return map[string]any{
		"id":     skillID,
		"source": "forge",
		"validation": map[string]any{
			"skillID": skillID,
			"status":  status,
			"summary": summary,
			"isValid": valid,
			"issues":  issues,
		},
	}
}

func (m *Module) listVisibleSkills() []features.SkillRecord {
	base := features.ListSkills(m.supportDir)
	seen := make(map[string]bool, len(base))
	for _, rec := range base {
		seen[rec.Manifest.ID] = true
	}
	for _, installed := range forge.ListInstalled(m.supportDir) {
		rec, ok := installedForgeSkillRecord(installed)
		if !ok || seen[rec.Manifest.ID] {
			continue
		}
		base = append(base, rec)
		seen[rec.Manifest.ID] = true
	}
	return base
}

func installedForgeSkillRecord(installed map[string]any) (features.SkillRecord, bool) {
	manifest, _ := installed["manifest"].(map[string]any)
	skillID := strings.TrimSpace(stringValueFromAny(installed["id"]))
	if skillID == "" || manifest == nil {
		return features.SkillRecord{}, false
	}
	actionsRaw, _ := installed["actions"].([]any)
	actions := make([]features.SkillAction, 0, len(actionsRaw))
	for _, item := range actionsRaw {
		action, _ := item.(map[string]any)
		if action == nil {
			continue
		}
		actions = append(actions, features.SkillAction{
			ID:              strings.TrimSpace(stringValueFromAny(action["id"])),
			Name:            strings.TrimSpace(stringValueFromAny(action["name"])),
			Description:     strings.TrimSpace(stringValueFromAny(action["description"])),
			PermissionLevel: strings.TrimSpace(stringValueFromAny(action["permissionLevel"])),
			ApprovalPolicy:  strings.TrimSpace(stringValueFromAny(action["approvalPolicy"])),
			IsEnabled:       boolFromAny(action["isEnabled"]),
		})
	}
	return features.SkillRecord{
		Manifest: features.SkillManifestInfo{
			ID:             skillID,
			Name:           defaultStringValue(stringValueFromAny(manifest["name"]), skillID),
			Version:        defaultStringValue(stringValueFromAny(manifest["version"]), "1.0"),
			Description:    defaultStringValue(stringValueFromAny(manifest["description"]), strings.TrimSpace(stringValueFromAny(installed["description"]))),
			LifecycleState: defaultStringValue(stringValueFromAny(manifest["lifecycleState"]), "installed"),
			RiskLevel:      defaultStringValue(stringValueFromAny(manifest["riskLevel"]), "medium"),
			IsUserVisible:  true,
			Category:       defaultStringValue(stringValueFromAny(manifest["category"]), "productivity"),
			Source:         defaultStringValue(stringValueFromAny(manifest["source"]), "forge"),
			Capabilities:   []string{},
			Tags:           stringSliceFromAny(manifest["tags"]),
		},
		Actions: actions,
	}, true
}

func updateForgeInstalledSkillState(supportDir, skillID, state string) *features.SkillRecord {
	record := forge.GetInstalled(supportDir, skillID)
	if record == nil {
		return nil
	}
	manifest, _ := record["manifest"].(map[string]any)
	if manifest == nil {
		manifest = map[string]any{}
		record["manifest"] = manifest
	}
	manifest["lifecycleState"] = state
	actionsRaw, _ := record["actions"].([]any)
	isEnabled := state == "enabled"
	for _, item := range actionsRaw {
		action, _ := item.(map[string]any)
		if action != nil {
			action["isEnabled"] = isEnabled
		}
	}
	if err := forge.SaveInstalled(supportDir, record); err != nil {
		return nil
	}
	features.SetForgeSkillState(supportDir, skillID, state)
	rec, ok := installedForgeSkillRecord(record)
	if !ok {
		return nil
	}
	return &rec
}

func stringValueFromAny(v any) string {
	s, _ := v.(string)
	return s
}

func boolFromAny(v any) bool {
	b, _ := v.(bool)
	return b
}

func stringSliceFromAny(v any) []string {
	switch raw := v.(type) {
	case []string:
		return append([]string(nil), raw...)
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return []string{}
	}
}

func defaultStringValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func credentialCheckForSkill(skillID string) (bool, string) {
	bundle := readCredentialBundle()
	switch skillID {
	case "web-research", "web-search":
		if bundle.BraveSearchAPIKey == "" {
			return false, "Brave Search API key is not configured."
		}
	case "image-generation", "vision":
		if bundle.OpenAIAPIKey == "" {
			return false, "OpenAI API key is not configured."
		}
	}
	return true, "Skill is ready."
}

func readCredentialBundle() creds.Bundle {
	bundle, _ := creds.Read()
	return bundle
}

func copySkillFiles(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read source dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", entry.Name(), err)
		}
		if err := os.WriteFile(dst, data, info.Mode()); err != nil {
			return fmt.Errorf("write %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
