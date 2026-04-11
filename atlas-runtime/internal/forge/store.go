package forge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"atlas-runtime-go/internal/logstore"
)

const (
	proposalsFile = "forge-proposals.json"
	installedFile = "forge-installed.json"
)

var storeMu sync.Mutex

// ListProposals returns all forge proposals from disk.
func ListProposals(supportDir string) []ForgeProposal {
	var out []ForgeProposal
	readJSON(supportDir, proposalsFile, &out)
	if out == nil {
		return []ForgeProposal{}
	}
	return out
}

// GetProposal returns the proposal with the given ID, or nil.
func GetProposal(supportDir, id string) *ForgeProposal {
	proposals := ListProposals(supportDir)
	for i := range proposals {
		if proposals[i].ID == id {
			return &proposals[i]
		}
	}
	return nil
}

// SaveProposal appends (if new) or replaces (if ID matches) a proposal.
func SaveProposal(supportDir string, p ForgeProposal) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	var proposals []ForgeProposal
	readJSON(supportDir, proposalsFile, &proposals)

	found := false
	for i := range proposals {
		if proposals[i].ID == p.ID {
			proposals[i] = p
			found = true
			break
		}
	}
	if !found {
		proposals = append(proposals, p)
	}
	return writeJSON(supportDir, proposalsFile, proposals)
}

// UpdateProposalStatus sets the status field on a proposal and persists.
// Returns the updated proposal, or nil if not found.
func UpdateProposalStatus(supportDir, id, status string) (*ForgeProposal, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	var proposals []ForgeProposal
	readJSON(supportDir, proposalsFile, &proposals)

	var found *ForgeProposal
	for i := range proposals {
		if proposals[i].ID == id {
			proposals[i].Status = status
			found = &proposals[i]
			break
		}
	}
	if found == nil {
		return nil, nil
	}
	if err := writeJSON(supportDir, proposalsFile, proposals); err != nil {
		return nil, err
	}
	return found, nil
}

// ListInstalled returns all installed forge skill records.
func ListInstalled(supportDir string) []map[string]any {
	var out []map[string]any
	readJSON(supportDir, installedFile, &out)
	if out == nil {
		return []map[string]any{}
	}
	return out
}

// GetInstalled returns one installed forge record by skill ID.
func GetInstalled(supportDir, skillID string) map[string]any {
	for _, rec := range ListInstalled(supportDir) {
		if id, _ := rec["id"].(string); id == skillID {
			return rec
		}
	}
	return nil
}

// SaveInstalled adds or replaces an installed skill record (keyed by skillID).
func SaveInstalled(supportDir string, record map[string]any) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	var installed []map[string]any
	readJSON(supportDir, installedFile, &installed)

	skillID, _ := record["id"].(string)
	found := false
	for i := range installed {
		if id, _ := installed[i]["id"].(string); id == skillID {
			installed[i] = record
			found = true
			break
		}
	}
	if !found {
		installed = append(installed, record)
	}
	return writeJSON(supportDir, installedFile, installed)
}

// DeleteInstalled removes the installed skill record for skillID.
// Returns false if not found.
func DeleteInstalled(supportDir, skillID string) (bool, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	var installed []map[string]any
	readJSON(supportDir, installedFile, &installed)

	var remaining []map[string]any
	found := false
	for _, rec := range installed {
		if id, _ := rec["id"].(string); id == skillID {
			found = true
			continue
		}
		remaining = append(remaining, rec)
	}
	if !found {
		return false, nil
	}
	if remaining == nil {
		remaining = []map[string]any{}
	}
	return true, writeJSON(supportDir, installedFile, remaining)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func readJSON(supportDir, filename string, v any) {
	data, err := os.ReadFile(filepath.Join(supportDir, filename))
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, v); err != nil {
		logstore.Write("warn", "forge: corrupted JSON in "+filename+" — data discarded: "+err.Error(),
			map[string]string{"file": filepath.Join(supportDir, filename)})
	}
}

func writeJSON(supportDir, filename string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(supportDir, filename)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filename+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()
	return os.Rename(tmpPath, path)
}
