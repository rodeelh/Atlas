package chat

// thought_surfacing.go implements Phase 7b: detect [T-NN] engagement
// markers the agent writes in its replies, persist a pending row to
// thought-engagement.jsonl, and emit telemetry. Runs at SaveMessage time
// for every assistant reply — the marker is already in the stream, so
// parsing is free and no extra model call is needed.
//
// The frontend stripper removes the marker before display, but the raw
// content in SQLite keeps it so the next turn's classifier can still
// read it if needed.
//
// Detection → engagement lifecycle:
//
//   1. Agent writes reply containing "...notes? [T-01]"
//   2. SaveMessage persists the raw content (marker intact)
//   3. DetectAndRecordSurfacings parses content → finds T-01
//   4. RecordSurfacing appends a pending row keyed by
//      (conv_id, message_id, thought_id)
//   5. Telemetry emits thought_surfaced
//   6. On the user's next turn, the classifier path (phase 7c) finds
//      this pending row and marks it terminal.

import (
	"regexp"
	"time"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/mind/telemetry"
	"atlas-runtime-go/internal/mind/thoughts"
)

// thoughtTagRe extracts thought ids like "[T-01]" or "[T-42]" from an
// assistant reply. Multiple ids in one reply each produce their own
// pending row. The regex accepts any digit count so "[T-100]" works.
var thoughtTagRe = regexp.MustCompile(`\[(T-\d+)\]`)

// detectSurfacedThoughts returns the distinct thought ids appearing as
// engagement markers in content. Preserves first-occurrence order so
// telemetry rows land in the order the agent wrote them. A thought id
// mentioned twice in the same reply counts as one surfacing.
func detectSurfacedThoughts(content string) []string {
	matches := thoughtTagRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// SurfacingRecorder is the narrow interface Service uses to persist
// pending surfacings. Real writes go to thoughts.RecordSurfacing; tests
// can swap in a stub.
type SurfacingRecorder interface {
	Record(convID, messageID, thoughtID string, surfacedAt time.Time) error
}

// realSurfacingRecorder is the production adapter backed by the
// thoughts package's append-only sidecar.
type realSurfacingRecorder struct {
	supportDir string
}

func (r *realSurfacingRecorder) Record(convID, messageID, thoughtID string, surfacedAt time.Time) error {
	return thoughts.RecordSurfacing(r.supportDir, convID, messageID, thoughtID, surfacedAt)
}

// detectAndRecordSurfacings is the Service-method seam called from every
// assistant SaveMessage path. It parses the raw assistant content for
// markers, writes a pending row per distinct id, and emits telemetry.
// Safe to call unconditionally — returns immediately on empty content.
//
// This runs synchronously because the sidecar writes are local disk and
// cheap, and we want the pending rows visible before the user can
// possibly send the next turn. A slow path here would risk a classifier
// race where the next turn arrives before the pending row exists.
func (s *Service) detectAndRecordSurfacings(convID, messageID, content string, surfacedAt time.Time) {
	if content == "" {
		return
	}
	// Master gate: if thoughts are disabled, don't even parse the marker.
	// Atlas isn't supposed to be raising thoughts in this mode anyway
	// (the system prompt won't contain any), but belt-and-braces: if a
	// stray marker shows up in a reply we don't record it.
	if !s.thoughtsEnabled() {
		return
	}
	ids := detectSurfacedThoughts(content)
	if len(ids) == 0 {
		return
	}
	if surfacedAt.IsZero() {
		surfacedAt = time.Now().UTC()
	}
	rec := s.surfacingRecorder()

	for _, id := range ids {
		if err := rec.Record(convID, messageID, id, surfacedAt); err != nil {
			logstore.Write("warn", "thought surfacing: record failed: "+err.Error(),
				map[string]string{"thought": id, "conv": shortConvID(convID)})
			continue
		}
		// Emit telemetry — lives alongside the rest of the mind-thoughts
		// lifecycle events so the Mind Health dashboard can see surfacing
		// activity next to naps, thought_added, and engagement_recorded.
		if s.greetingTelemetry != nil {
			s.greetingTelemetry.Emit(telemetry.KindThoughtSurfaced, id, convID, map[string]any{
				"message_id": messageID,
			})
		}
	}
}

// surfacingRecorder returns the configured recorder, falling back to the
// real disk-backed one when nothing has been injected. Tests set a stub
// via SetSurfacingRecorder.
func (s *Service) surfacingRecorder() SurfacingRecorder {
	if s.surfacingRec != nil {
		return s.surfacingRec
	}
	return &realSurfacingRecorder{supportDir: supportDirPath()}
}

// SetSurfacingRecorder lets tests inject a stub recorder without
// touching disk. Production wires nothing and gets the real recorder.
func (s *Service) SetSurfacingRecorder(rec SurfacingRecorder) {
	s.surfacingRec = rec
}

// supportDirPath returns the production support directory where the
// sidecar lives. Tests bypass this path entirely via SetSurfacingRecorder.
func supportDirPath() string {
	return config.SupportDir()
}

// shortConvID trims a conversation id for log metadata.
func shortConvID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
