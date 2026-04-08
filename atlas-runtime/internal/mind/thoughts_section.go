package mind

// thoughts_section.go bridges the pure `thoughts` data package to MIND.md on
// disk. It owns reading the ## THOUGHTS section out of MIND.md and writing a
// new rendered section back in, while holding the mind write lock for the
// duration of the read-modify-write cycle.
//
// The ## THOUGHTS section is a NEW section added by the mind-thoughts
// feature. If MIND.md does not yet contain a ## THOUGHTS section, we create
// one with an empty placeholder the first time a write fires. We never touch
// ## ACTIVE THEORIES — that section is part of Atlas's personality and is
// off-limits to this system (see project_mind_thoughts.md memory).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/mind/thoughts"
)

// ReadThoughtsSection reads the current ## THOUGHTS section from MIND.md and
// returns the parsed thoughts. Does NOT acquire the mind lock — callers that
// are doing a read-modify-write should hold the lock explicitly via
// WithMindLock. Callers doing a read-only check (e.g. the chat service
// injecting thoughts into the system prompt) can call this without the lock.
//
// Returns (nil, nil) if MIND.md is missing or has no ## THOUGHTS section —
// both are normal states on a fresh install or before the first nap runs.
// Parse errors from individual bullets are logged as warnings and skipped;
// the surviving thoughts are still returned.
func ReadThoughtsSection(supportDir string) ([]thoughts.Thought, error) {
	mindPath := filepath.Join(supportDir, "MIND.md")
	data, err := os.ReadFile(mindPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read MIND.md: %w", err)
	}
	body := extractMindSection(string(data), thoughts.SectionHeader)
	if body == "" {
		return nil, nil
	}
	parsed, errs := thoughts.ParseSection(body)
	for _, e := range errs {
		logstore.Write("warn", "THOUGHTS section parse error: "+e.Error(), nil)
	}
	return parsed, nil
}

// WriteThoughtsSection replaces the ## THOUGHTS section in MIND.md with a
// fresh rendering of the given thoughts list. If MIND.md does not yet have a
// ## THOUGHTS section, one is appended at the end of the document.
//
// Callers MUST hold the mind lock (via WithMindLock or TryWithMindLock)
// because this function does its own read-write cycle on MIND.md and a
// concurrent writer could lose updates. The lock is NOT acquired here so
// callers can also bundle other edits in the same critical section.
func WriteThoughtsSection(supportDir string, list []thoughts.Thought) error {
	mindPath := filepath.Join(supportDir, "MIND.md")
	data, err := os.ReadFile(mindPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No MIND.md yet — nothing to update. Caller should re-seed first.
			return fmt.Errorf("write THOUGHTS section: MIND.md not found")
		}
		return fmt.Errorf("read MIND.md: %w", err)
	}
	content := string(data)
	rendered := thoughts.RenderSection(list)
	updated := replaceOrAppendMindSection(content, thoughts.SectionHeader, rendered)
	return atomicWrite(mindPath, []byte(updated), 0o600)
}

// UpdateThoughtsSection is a convenience for the common pattern: acquire
// the mind lock, read the current thoughts, run fn to produce the new list,
// write it back. Non-blocking (uses WithMindLock). Returns fn's error if
// any step fails.
//
// `now` is passed through to callers that need a consistent timestamp for
// their apply operation (injected instead of time.Now() for testability at
// higher levels).
func UpdateThoughtsSection(supportDir string, fn func(current []thoughts.Thought) ([]thoughts.Thought, error)) error {
	return WithMindLock(func() error {
		current, err := ReadThoughtsSection(supportDir)
		if err != nil {
			return err
		}
		next, err := fn(current)
		if err != nil {
			return err
		}
		return WriteThoughtsSection(supportDir, next)
	})
}

// UpdateThoughtsSectionWithTimeout is the bounded-wait version of
// UpdateThoughtsSection. Naps use this so a stuck dream cycle does not
// block nap firing forever.
func UpdateThoughtsSectionWithTimeout(ctx context.Context, supportDir string, timeout time.Duration, fn func(current []thoughts.Thought) ([]thoughts.Thought, error)) error {
	return WithMindLockTimeout(ctx, timeout, func() error {
		current, err := ReadThoughtsSection(supportDir)
		if err != nil {
			return err
		}
		next, err := fn(current)
		if err != nil {
			return err
		}
		return WriteThoughtsSection(supportDir, next)
	})
}

// extractMindSection returns the body text of a named "## " section, or "" if
// the section is not present. The header itself is not included. Body ends
// at the next "## " heading or EOF. Uses a line-anchored scan so a literal
// "## X" embedded in another section's body cannot cause a mis-splice.
func extractMindSection(content, header string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != header {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				end = j
				break
			}
		}
		return strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))
	}
	return ""
}

// replaceOrAppendMindSection replaces a named "## " section's body with newBody
// if the section exists, otherwise appends "## <header>\n\n<newBody>\n" at
// the end of the document. Preserves the rest of the document exactly.
func replaceOrAppendMindSection(content, header, newBody string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != header {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				end = j
				break
			}
		}
		result := make([]string, 0, len(lines)+3)
		result = append(result, lines[:i+1]...)
		result = append(result, "", newBody, "")
		result = append(result, lines[end:]...)
		return strings.Join(result, "\n")
	}
	// Section missing — append at the end.
	return strings.TrimRight(content, "\n") + "\n\n" + header + "\n\n" + newBody + "\n"
}
