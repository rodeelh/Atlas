package dashboards

// store.go — JSON-backed persistence for dashboard definitions.
//
// Modeled after internal/forge/store.go: single file in the support directory,
// guarded by a package-level mutex, atomic write via temp-file + rename.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const dashboardsFile = "dashboards.json"

// ErrNotFound is returned when a dashboard ID does not exist.
var ErrNotFound = errors.New("dashboard not found")

var storeMu sync.Mutex

// Store is a thin handle over the support directory. The on-disk format is a
// flat JSON array of DashboardDefinition.
type Store struct {
	supportDir string
}

// NewStore returns a Store rooted at supportDir.
func NewStore(supportDir string) *Store {
	return &Store{supportDir: supportDir}
}

// List returns all persisted dashboards. Returns an empty (non-nil) slice when
// the file is absent or empty.
func (s *Store) List() []DashboardDefinition {
	var out []DashboardDefinition
	readDashboardsJSON(s.supportDir, &out)
	if out == nil {
		return []DashboardDefinition{}
	}
	return out
}

// Get returns the dashboard with the given ID, or ErrNotFound.
func (s *Store) Get(id string) (DashboardDefinition, error) {
	for _, d := range s.List() {
		if d.ID == id {
			return d, nil
		}
	}
	return DashboardDefinition{}, ErrNotFound
}

// Save inserts a new dashboard or replaces one with the same ID. Stamps
// CreatedAt on insert and always refreshes UpdatedAt.
func (s *Store) Save(d DashboardDefinition) (DashboardDefinition, error) {
	if d.ID == "" {
		return DashboardDefinition{}, fmt.Errorf("dashboard id is required")
	}
	if d.Name == "" {
		return DashboardDefinition{}, fmt.Errorf("dashboard name is required")
	}

	storeMu.Lock()
	defer storeMu.Unlock()

	var existing []DashboardDefinition
	readDashboardsJSON(s.supportDir, &existing)

	now := time.Now().UTC()
	d.UpdatedAt = now

	replaced := false
	for i := range existing {
		if existing[i].ID == d.ID {
			if d.CreatedAt.IsZero() {
				d.CreatedAt = existing[i].CreatedAt
			}
			existing[i] = d
			replaced = true
			break
		}
	}
	if !replaced {
		if d.CreatedAt.IsZero() {
			d.CreatedAt = now
		}
		existing = append(existing, d)
	}

	if err := writeDashboardsJSON(s.supportDir, existing); err != nil {
		return DashboardDefinition{}, err
	}
	return d, nil
}

// Delete removes a dashboard by ID. Returns ErrNotFound if absent.
func (s *Store) Delete(id string) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	var existing []DashboardDefinition
	readDashboardsJSON(s.supportDir, &existing)

	idx := -1
	for i := range existing {
		if existing[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrNotFound
	}
	existing = append(existing[:idx], existing[idx+1:]...)
	if existing == nil {
		existing = []DashboardDefinition{}
	}
	return writeDashboardsJSON(s.supportDir, existing)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func readDashboardsJSON(supportDir string, v any) {
	data, err := os.ReadFile(filepath.Join(supportDir, dashboardsFile))
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, v)
}

func writeDashboardsJSON(supportDir string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(supportDir, dashboardsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), dashboardsFile+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
