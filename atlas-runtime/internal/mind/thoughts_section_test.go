package mind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"atlas-runtime-go/internal/mind/thoughts"
)

// seedMind writes a minimal MIND.md with a few existing sections into dir
// so the thoughts section helpers have something realistic to splice into.
func seedMind(t *testing.T, dir string, includeThoughts bool) {
	t.Helper()
	body := `# Mind of Atlas
_Last updated: 2026-04-07_

## Identity

I am Atlas.

## Today's Read

Nothing yet.

## Active Theories

Working theory: the user cares about clean design.
`
	if includeThoughts {
		body += `
## THOUGHTS

_(no active thoughts)_
`
	}
	if err := os.WriteFile(filepath.Join(dir, "MIND.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed MIND.md: %v", err)
	}
}

func TestReadThoughtsSection_Missing(t *testing.T) {
	dir := t.TempDir()
	list, err := ReadThoughtsSection(dir)
	if err != nil {
		t.Fatalf("ReadThoughtsSection on missing MIND.md: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("got %d thoughts, want 0", len(list))
	}
}

func TestReadThoughtsSection_NoSection(t *testing.T) {
	dir := t.TempDir()
	seedMind(t, dir, false)
	list, err := ReadThoughtsSection(dir)
	if err != nil {
		t.Fatalf("ReadThoughtsSection: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("got %d thoughts, want 0", len(list))
	}
}

func TestWriteThoughtsSection_AppendsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	seedMind(t, dir, false)

	now := mustT(t, "2026-04-07T12:00:00Z")
	list := []thoughts.Thought{{
		ID: "T-01", Body: "first thought",
		Confidence: 80, Value: 70, Class: thoughts.ClassRead,
		Score: 56, Created: now, Reinforced: now,
		SurfacedMax: 2, Source: "conv-a",
	}}
	if err := WithMindLock(func() error {
		return WriteThoughtsSection(dir, list)
	}); err != nil {
		t.Fatalf("WriteThoughtsSection: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "MIND.md"))
	content := string(data)
	if !strings.Contains(content, "## THOUGHTS") {
		t.Errorf("MIND.md missing THOUGHTS section after write:\n%s", content)
	}
	if !strings.Contains(content, "first thought") {
		t.Errorf("MIND.md missing body text:\n%s", content)
	}
	// Other sections must be preserved.
	if !strings.Contains(content, "## Identity") {
		t.Errorf("MIND.md lost Identity section")
	}
	if !strings.Contains(content, "## Active Theories") {
		t.Errorf("MIND.md lost Active Theories section (this is the personality, must not be touched)")
	}
}

func TestWriteThoughtsSection_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	seedMind(t, dir, true)

	now := mustT(t, "2026-04-07T12:00:00Z")
	list := []thoughts.Thought{{
		ID: "T-01", Body: "brand new",
		Confidence: 80, Value: 70, Class: thoughts.ClassRead,
		Score: 56, Created: now, Reinforced: now,
		SurfacedMax: 2, Source: "conv-a",
	}}
	WithMindLock(func() error {
		return WriteThoughtsSection(dir, list)
	})

	data, _ := os.ReadFile(filepath.Join(dir, "MIND.md"))
	content := string(data)
	if strings.Contains(content, "_(no active thoughts)_") {
		t.Errorf("placeholder should have been replaced")
	}
	if !strings.Contains(content, "brand new") {
		t.Errorf("new body not in MIND.md:\n%s", content)
	}
	// Active Theories must still be present — bumping this in tests to catch
	// accidental touching of the personality section.
	if !strings.Contains(content, "## Active Theories") {
		t.Errorf("Active Theories section was clobbered by THOUGHTS write")
	}
}

func TestUpdateThoughtsSection_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	seedMind(t, dir, true)
	now := mustT(t, "2026-04-07T12:00:00Z")

	// Add a thought.
	err := UpdateThoughtsSection(dir, func(current []thoughts.Thought) ([]thoughts.Thought, error) {
		out, _, err := thoughts.Apply(current, []thoughts.Op{{
			Kind: thoughts.OpAdd, Body: "round-trip test",
			Confidence: 60, Value: 50, Class: thoughts.ClassRead,
			Source: "conv-a", Provenance: "p",
		}}, now)
		return out, err
	})
	if err != nil {
		t.Fatalf("UpdateThoughtsSection: %v", err)
	}

	// Read it back.
	list, err := ReadThoughtsSection(dir)
	if err != nil {
		t.Fatalf("ReadThoughtsSection: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("round-trip: got %d thoughts, want 1", len(list))
	}
	if list[0].Body != "round-trip test" {
		t.Errorf("body: got %q", list[0].Body)
	}
	if list[0].Score != 30 { // 60 * 50 * 1.00 / 100
		t.Errorf("score: got %d, want 30", list[0].Score)
	}
}

func TestMindLock_Exclusive(t *testing.T) {
	// Two goroutines both try to write — the lock should serialize them
	// so both writes succeed and no MIND.md read-modify-write is lost.
	dir := t.TempDir()
	seedMind(t, dir, true)

	var wg sync.WaitGroup
	wg.Add(2)

	write := func(body string) {
		defer wg.Done()
		err := UpdateThoughtsSection(dir, func(current []thoughts.Thought) ([]thoughts.Thought, error) {
			now := time.Now().UTC()
			out, _, err := thoughts.Apply(current, []thoughts.Op{{
				Kind: thoughts.OpAdd, Body: body,
				Confidence: 50, Value: 50, Class: thoughts.ClassRead,
				Source: "s",
			}}, now)
			return out, err
		})
		if err != nil {
			t.Errorf("write %q: %v", body, err)
		}
	}

	go write("from goroutine one")
	go write("from goroutine two")
	wg.Wait()

	list, err := ReadThoughtsSection(dir)
	if err != nil {
		t.Fatalf("ReadThoughtsSection: %v", err)
	}
	// If the lock worked, both thoughts should be present.
	if len(list) != 2 {
		t.Errorf("lock failed: got %d thoughts, want 2", len(list))
	}
}

func TestMindLockTimeout(t *testing.T) {
	// Hold the lock in one goroutine; the other should time out cleanly.
	blocker := make(chan struct{})
	done := make(chan struct{})
	go func() {
		WithMindLock(func() error {
			<-blocker
			return nil
		})
		close(done)
	}()

	// Give the blocker a moment to acquire the lock.
	time.Sleep(20 * time.Millisecond)

	ctx := context.Background()
	start := time.Now()
	err := WithMindLockTimeout(ctx, 100*time.Millisecond, func() error { return nil })
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected timeout error, got nil")
	}
	if elapsed < 100*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("timeout elapsed: %v, want ~100ms", elapsed)
	}
	close(blocker)
	<-done
}

func mustT(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("mustT: %v", err)
	}
	return ts
}
