package dashboards

// store.go — JSON-backed persistence for v2 dashboards.
//
// Format on disk: flat JSON array of Dashboard. Every saved record is stamped
// with SchemaVersion == 2. Records with any other schema version are refused
// at load time (they belong to an archived v1 file, see v1Archive).
//
// On first access we one-shot archive any legacy "dashboards.json" by
// renaming it to "dashboards.json.v1-archive" so the v2 loader starts clean.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	dashboardsFileV2 = "dashboards-v2.json"
	dashboardsFileV1 = "dashboards.json"
	v1ArchiveSuffix  = ".v1-archive"
)

// ErrNotFound is returned when a dashboard ID does not exist.
var ErrNotFound = errors.New("dashboard not found")

// ErrSchemaMismatch is returned if persisted data has an unexpected schema.
var ErrSchemaMismatch = errors.New("dashboard schema version mismatch")

// Store is the v2 dashboard store. Serialized access is enforced by the
// embedded mutex; file writes are atomic (temp + rename).
type Store struct {
	supportDir string
	mu         sync.Mutex
	archiveOnce sync.Once
}

// NewStore returns a Store rooted at supportDir.
func NewStore(supportDir string) *Store {
	return &Store{supportDir: supportDir}
}

// ArchiveV1IfPresent moves an existing v1 dashboards.json to
// dashboards.json.v1-archive so it no longer interferes with v2 loads.
// Safe to call multiple times; only the first call does any work.
func (s *Store) ArchiveV1IfPresent() error {
	var err error
	s.archiveOnce.Do(func() {
		oldPath := filepath.Join(s.supportDir, dashboardsFileV1)
		if _, statErr := os.Stat(oldPath); os.IsNotExist(statErr) {
			return
		} else if statErr != nil {
			err = statErr
			return
		}
		newPath := oldPath + v1ArchiveSuffix
		// If an archive already exists, leave it alone — but still remove the
		// live v1 file so it won't be reinterpreted by v2.
		if _, statErr := os.Stat(newPath); statErr == nil {
			err = os.Remove(oldPath)
			return
		}
		err = os.Rename(oldPath, newPath)
	})
	return err
}

// List returns every persisted dashboard.
func (s *Store) List() []Dashboard {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, _ := s.readAll()
	if all == nil {
		return []Dashboard{}
	}
	return all
}

// ListByStatus returns dashboards whose Status equals status. Pass "" for all.
func (s *Store) ListByStatus(status string) []Dashboard {
	all := s.List()
	if status == "" {
		return all
	}
	out := make([]Dashboard, 0, len(all))
	for _, d := range all {
		if d.Status == status {
			out = append(out, d)
		}
	}
	return out
}

// Get returns the dashboard with the given ID, or ErrNotFound.
func (s *Store) Get(id string) (Dashboard, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAll()
	if err != nil {
		return Dashboard{}, err
	}
	for _, d := range all {
		if d.ID == id {
			return d, nil
		}
	}
	return Dashboard{}, ErrNotFound
}

// Save inserts or replaces a dashboard. SchemaVersion is stamped automatically.
func (s *Store) Save(d Dashboard) (Dashboard, error) {
	if d.ID == "" {
		return Dashboard{}, fmt.Errorf("dashboard id is required")
	}
	if d.Name == "" {
		return Dashboard{}, fmt.Errorf("dashboard name is required")
	}
	if d.Status == "" {
		d.Status = StatusDraft
	}
	if d.Status != StatusDraft && d.Status != StatusLive {
		return Dashboard{}, fmt.Errorf("dashboard status must be draft or live, got %q", d.Status)
	}
	d.SchemaVersion = SchemaVersion

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.readAll()
	if err != nil {
		return Dashboard{}, err
	}
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
	if err := s.writeAll(existing); err != nil {
		return Dashboard{}, err
	}
	return d, nil
}

// Delete removes a dashboard by ID.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.readAll()
	if err != nil {
		return err
	}
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
		existing = []Dashboard{}
	}
	return s.writeAll(existing)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *Store) readAll() ([]Dashboard, error) {
	path := filepath.Join(s.supportDir, dashboardsFileV2)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Dashboard{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return []Dashboard{}, nil
	}
	var out []Dashboard
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", dashboardsFileV2, err)
	}
	// Enforce schema version. Any record without SchemaVersion==2 is refused
	// rather than silently upgraded; we don't want to guess at v1 field layout.
	for i := range out {
		if out[i].SchemaVersion != SchemaVersion {
			return nil, fmt.Errorf("%w: got %d, want %d (dashboard %q)",
				ErrSchemaMismatch, out[i].SchemaVersion, SchemaVersion, out[i].ID)
		}
	}
	return out, nil
}

func (s *Store) writeAll(list []Dashboard) error {
	if list == nil {
		list = []Dashboard{}
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.supportDir, dashboardsFileV2)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), dashboardsFileV2+"-*.tmp")
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
