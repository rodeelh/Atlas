package forge

// store_test.go — tests for the Forge proposal / installed-skill store.
//
// Coverage:
//   - SaveProposal: new proposal appended; same ID replaces (idempotent)
//   - ListProposals: returns empty slice (not nil) when file absent
//   - GetProposal: returns nil for unknown ID
//   - UpdateProposalStatus: updates in-place; unknown ID returns nil; write failures surface
//   - SaveInstalled / ListInstalled / DeleteInstalled: full lifecycle
//   - Concurrent writes: race detector must pass
//   - writeJSON atomic rename: partial write never leaves corrupt state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ── ListProposals / GetProposal ───────────────────────────────────────────────

func TestListProposals_EmptyDir_ReturnsEmptySlice(t *testing.T) {
	supportDir := t.TempDir()
	proposals := ListProposals(supportDir)
	if proposals == nil {
		t.Error("ListProposals should return empty slice (not nil) when file absent")
	}
	if len(proposals) != 0 {
		t.Errorf("want 0 proposals, got %d", len(proposals))
	}
}

func TestGetProposal_UnknownID_ReturnsNil(t *testing.T) {
	supportDir := t.TempDir()
	if p := GetProposal(supportDir, "does-not-exist"); p != nil {
		t.Errorf("GetProposal for unknown ID should return nil, got %+v", p)
	}
}

// ── SaveProposal idempotency ──────────────────────────────────────────────────

func TestSaveProposal_NewProposal_Appended(t *testing.T) {
	supportDir := t.TempDir()
	p := ForgeProposal{ID: "p1", SkillID: "skill-a", Name: "Skill A", Status: "pending"}
	if err := SaveProposal(supportDir, p); err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}
	list := ListProposals(supportDir)
	if len(list) != 1 {
		t.Fatalf("want 1 proposal, got %d", len(list))
	}
	if list[0].ID != "p1" {
		t.Errorf("proposal ID: want p1, got %s", list[0].ID)
	}
}

func TestSaveProposal_SameID_Replaces(t *testing.T) {
	supportDir := t.TempDir()
	p := ForgeProposal{ID: "p1", SkillID: "skill-a", Status: "pending"}
	SaveProposal(supportDir, p) //nolint:errcheck

	p.Status = "installed"
	if err := SaveProposal(supportDir, p); err != nil {
		t.Fatalf("second SaveProposal: %v", err)
	}

	list := ListProposals(supportDir)
	if len(list) != 1 {
		t.Fatalf("same-ID save must replace, not append: got %d proposals", len(list))
	}
	if list[0].Status != "installed" {
		t.Errorf("status after replace: want installed, got %s", list[0].Status)
	}
}

func TestSaveProposal_MultipleProposals_AllPersist(t *testing.T) {
	supportDir := t.TempDir()
	for i := 0; i < 5; i++ {
		p := ForgeProposal{
			ID:      fmt.Sprintf("p%d", i),
			SkillID: fmt.Sprintf("skill-%d", i),
			Status:  "pending",
		}
		if err := SaveProposal(supportDir, p); err != nil {
			t.Fatalf("SaveProposal %d: %v", i, err)
		}
	}
	list := ListProposals(supportDir)
	if len(list) != 5 {
		t.Errorf("want 5 proposals, got %d", len(list))
	}
}

// ── UpdateProposalStatus ──────────────────────────────────────────────────────

func TestUpdateProposalStatus_Updates(t *testing.T) {
	supportDir := t.TempDir()
	p := ForgeProposal{ID: "p1", Status: "pending"}
	SaveProposal(supportDir, p) //nolint:errcheck

	updated, err := UpdateProposalStatus(supportDir, "p1", "installed")
	if err != nil {
		t.Fatalf("UpdateProposalStatus: %v", err)
	}
	if updated == nil {
		t.Fatal("UpdateProposalStatus should return updated proposal")
	}
	if updated.Status != "installed" {
		t.Errorf("status: want installed, got %s", updated.Status)
	}

	// Verify persisted.
	reloaded := GetProposal(supportDir, "p1")
	if reloaded == nil || reloaded.Status != "installed" {
		t.Errorf("persisted status: want installed, got %v", reloaded)
	}
}

func TestUpdateProposalStatus_UnknownID_ReturnsNil(t *testing.T) {
	supportDir := t.TempDir()
	result, err := UpdateProposalStatus(supportDir, "ghost-id", "installed")
	if err != nil {
		t.Fatalf("UpdateProposalStatus returned unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("UpdateProposalStatus with unknown ID should return nil, got %+v", result)
	}
}

func TestUpdateProposalStatus_WriteFailureReturnsError(t *testing.T) {
	supportDir := t.TempDir()
	p := ForgeProposal{ID: "p1", Status: "pending"}
	if err := SaveProposal(supportDir, p); err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}

	if err := os.Chmod(supportDir, 0o500); err != nil {
		t.Fatalf("Chmod supportDir: %v", err)
	}
	defer os.Chmod(supportDir, 0o700)

	updated, err := UpdateProposalStatus(supportDir, "p1", "installed")
	if err == nil {
		t.Fatal("expected write failure error, got nil")
	}
	if updated != nil {
		t.Fatalf("expected nil proposal on write failure, got %+v", updated)
	}

	reloaded := GetProposal(supportDir, "p1")
	if reloaded == nil || reloaded.Status != "pending" {
		t.Fatalf("proposal status should remain pending after failed write, got %+v", reloaded)
	}
}

// ── SaveInstalled / ListInstalled / DeleteInstalled ───────────────────────────

func TestInstalled_FullLifecycle(t *testing.T) {
	supportDir := t.TempDir()

	// Start empty.
	if list := ListInstalled(supportDir); len(list) != 0 {
		t.Fatalf("want empty installed list initially, got %d", len(list))
	}

	// Save.
	record := map[string]any{"id": "my-skill", "name": "My Skill"}
	if err := SaveInstalled(supportDir, record); err != nil {
		t.Fatalf("SaveInstalled: %v", err)
	}
	list := ListInstalled(supportDir)
	if len(list) != 1 {
		t.Fatalf("want 1 installed skill, got %d", len(list))
	}

	// Replace (same id).
	updated := map[string]any{"id": "my-skill", "name": "My Skill Updated"}
	if err := SaveInstalled(supportDir, updated); err != nil {
		t.Fatalf("SaveInstalled replace: %v", err)
	}
	list = ListInstalled(supportDir)
	if len(list) != 1 {
		t.Fatalf("replace should keep count at 1, got %d", len(list))
	}
	if list[0]["name"] != "My Skill Updated" {
		t.Errorf("name after replace: want %q, got %v", "My Skill Updated", list[0]["name"])
	}

	// Delete.
	found, err := DeleteInstalled(supportDir, "my-skill")
	if err != nil {
		t.Fatalf("DeleteInstalled: %v", err)
	}
	if !found {
		t.Error("DeleteInstalled should return true for existing record")
	}
	if list := ListInstalled(supportDir); len(list) != 0 {
		t.Errorf("want empty list after delete, got %d", len(list))
	}
}

func TestDeleteInstalled_UnknownID_ReturnsFalse(t *testing.T) {
	supportDir := t.TempDir()
	found, err := DeleteInstalled(supportDir, "not-there")
	if err != nil {
		t.Fatalf("DeleteInstalled for unknown ID should not error: %v", err)
	}
	if found {
		t.Error("DeleteInstalled for unknown ID should return false")
	}
}

func TestListInstalled_EmptyDir_ReturnsEmptySlice(t *testing.T) {
	supportDir := t.TempDir()
	list := ListInstalled(supportDir)
	if list == nil {
		t.Error("ListInstalled should return empty slice (not nil) when file absent")
	}
}

// ── Concurrency / race detector ───────────────────────────────────────────────

func TestSaveProposal_ConcurrentWrites_NoRace(t *testing.T) {
	supportDir := t.TempDir()
	var wg sync.WaitGroup
	const n = 40

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := ForgeProposal{
				ID:      fmt.Sprintf("proposal-%d", idx),
				SkillID: "skill-concurrent",
				Status:  "pending",
			}
			if err := SaveProposal(supportDir, p); err != nil {
				t.Errorf("concurrent SaveProposal %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// After all writes, file must be valid JSON.
	data, err := os.ReadFile(filepath.Join(supportDir, proposalsFile))
	if err != nil {
		t.Fatalf("proposals file not readable after concurrent writes: %v", err)
	}
	var proposals []ForgeProposal
	if err := json.Unmarshal(data, &proposals); err != nil {
		t.Fatalf("proposals file corrupted after concurrent writes: %v\nContent: %s", err, data)
	}
}

func TestSaveInstalled_ConcurrentWrites_NoRace(t *testing.T) {
	supportDir := t.TempDir()
	var wg sync.WaitGroup
	const n = 40

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rec := map[string]any{"id": fmt.Sprintf("skill-%d", idx%26)}
			if err := SaveInstalled(supportDir, rec); err != nil {
				t.Errorf("concurrent SaveInstalled %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(supportDir, installedFile))
	if err != nil {
		t.Fatalf("installed file not readable: %v", err)
	}
	var installed []map[string]any
	if err := json.Unmarshal(data, &installed); err != nil {
		t.Fatalf("installed file corrupted after concurrent writes: %v", err)
	}
}

// ── writeJSON atomic write ────────────────────────────────────────────────────

func TestWriteJSON_AtomicWrite_NoPartialFile(t *testing.T) {
	// writeJSON uses temp-file + rename. The destination must always be valid JSON
	// (never a partially-written file) because os.Rename is atomic on the same FS.
	supportDir := t.TempDir()

	// Write a large payload to make partial writes more likely if non-atomic.
	type entry struct {
		ID   string `json:"id"`
		Data string `json:"data"`
	}
	entries := make([]entry, 500)
	for i := range entries {
		entries[i] = entry{ID: "id", Data: strings.Repeat("x", 200)}
	}
	if err := writeJSON(supportDir, "test.json", entries); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(supportDir, "test.json"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	var out []entry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if len(out) != 500 {
		t.Errorf("want 500 entries, got %d", len(out))
	}
}
