package thoughts

// parse.go round-trips thoughts between Go structs and the markdown bullet
// format used in MIND.md's ## THOUGHTS section.
//
// Format on disk:
//
//	## THOUGHTS
//
//	- **[T-01]** Body line one, can wrap across multiple lines because the
//	  metadata line always starts with "  · ".
//	  · score 92 · class read · created 2026-04-07T14:30:00Z · reinforced 2026-04-07T14:30:00Z
//	  · surfaced 0/2 · source conv-7f3a:nap-3
//	  · [provenance: user mentioned openclaw release rhythm]
//	  · action {"skill":"openclaw-latest-build-changes.check-latest-build-changes","args":{"repo":"openclaw/openclaw"}}
//
//	- **[T-02]** ...
//
// The parser is forgiving: unknown metadata keys are ignored, missing optional
// fields fall back to zero values, and a thought missing required fields is
// skipped with an error returned alongside the successfully-parsed thoughts.
// This means a partially-corrupted THOUGHTS section degrades gracefully.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SectionHeader is the markdown heading that marks the thoughts section in
// MIND.md. Exported so other packages locate it consistently.
const SectionHeader = "## THOUGHTS"

// thoughtIDRe matches a thought id anywhere in a line: T-01, T-123, etc.
var thoughtIDRe = regexp.MustCompile(`\[(T-\d+)\]`)

// bulletStartRe identifies the first line of a thought bullet. It captures the
// thought id and the body prefix on the same line.
var bulletStartRe = regexp.MustCompile(`^-\s+\*\*\[(T-\d+)\]\*\*\s+(.*)$`)

// metadataLineRe identifies a continuation line that carries metadata.
// These lines start with optional whitespace, then "·" or a bullet·, then
// one or more metadata fields. The leading whitespace of the bullet body is
// preserved so we can tell body lines from metadata lines without ambiguity.
var metadataLineRe = regexp.MustCompile(`^\s*·\s`)

// ParseSection parses a ## THOUGHTS section body (not including the heading)
// and returns the thoughts it contains. Malformed bullets are skipped and
// returned as errors in the second return value; callers can choose to log
// them without failing the whole parse. Returns an empty slice if section is
// empty or only whitespace.
func ParseSection(body string) ([]Thought, []error) {
	var thoughts []Thought
	var errs []error
	if strings.TrimSpace(body) == "" {
		return thoughts, errs
	}

	// Walk line by line, accumulating bullet blocks. A new bullet starts at a
	// line matching bulletStartRe; subsequent lines belong to the current
	// bullet until the next bullet start or a blank line followed by something
	// that isn't a metadata line.
	lines := strings.Split(body, "\n")
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		t, err := parseBullet(current)
		if err != nil {
			errs = append(errs, err)
		} else {
			thoughts = append(thoughts, t)
		}
		current = nil
	}

	for _, line := range lines {
		if bulletStartRe.MatchString(line) {
			flush()
			current = []string{line}
			continue
		}
		if len(current) == 0 {
			// Whitespace/junk before the first bullet. Ignore.
			continue
		}
		// Continuation line — body wrap, metadata, or blank. Blank lines
		// inside a bullet terminate the bullet only if they're followed by
		// another bullet start; otherwise they're harmless.
		if strings.TrimSpace(line) == "" {
			// A blank line inside a bullet is a soft terminator — we flush
			// on the next non-blank if it isn't a continuation. We detect
			// that by peeking at whether the accumulated lines already had
			// at least one metadata line. If so, the bullet is complete.
			hasMeta := false
			for _, l := range current {
				if metadataLineRe.MatchString(l) {
					hasMeta = true
					break
				}
			}
			if hasMeta {
				flush()
			}
			continue
		}
		current = append(current, line)
	}
	flush()

	return thoughts, errs
}

// parseBullet parses a single bullet's accumulated lines into a Thought.
func parseBullet(lines []string) (Thought, error) {
	if len(lines) == 0 {
		return Thought{}, fmt.Errorf("parseBullet: no lines")
	}
	m := bulletStartRe.FindStringSubmatch(lines[0])
	if m == nil {
		return Thought{}, fmt.Errorf("parseBullet: first line is not a bullet: %q", lines[0])
	}
	id := m[1]
	firstBodyChunk := strings.TrimSpace(m[2])

	// Walk continuation lines. Everything before the first metadata line is
	// appended to the body (with spaces normalizing the wrap). From the first
	// metadata line onward, everything is metadata.
	bodyParts := []string{firstBodyChunk}
	var metaLines []string
	inMeta := false
	for _, line := range lines[1:] {
		if metadataLineRe.MatchString(line) {
			inMeta = true
			metaLines = append(metaLines, strings.TrimSpace(line))
			continue
		}
		if inMeta {
			// A non-metadata line after metadata started — treat it as a
			// metadata continuation, because provenance can be long.
			metaLines = append(metaLines, strings.TrimSpace(line))
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			bodyParts = append(bodyParts, trimmed)
		}
	}

	t := Thought{
		ID:          id,
		Body:        strings.Join(bodyParts, " "),
		SurfacedMax: 2,
	}

	// Flatten metadata lines into one big string for key scanning. Each metadata
	// line is "· key value · key value · key value ..." — we split on the
	// bullet and trim. The leading "·" after normalization becomes an empty
	// field that we skip.
	meta := strings.Join(metaLines, " ")
	fields := splitMetadataFields(meta)
	if err := applyMetadata(&t, fields); err != nil {
		return Thought{}, fmt.Errorf("thought %s: %w", id, err)
	}

	if err := t.Validate(); err != nil {
		return Thought{}, err
	}
	return t, nil
}

// splitMetadataFields splits a metadata string on "·" separators and trims
// each field. Empty fields are discarded. A single "action {...}" field with
// an embedded JSON object is preserved as one unit even though the JSON may
// contain "·" characters (it doesn't in practice but we handle it).
func splitMetadataFields(meta string) []string {
	// Fast path: no embedded JSON. Just split on "·".
	if !strings.Contains(meta, "action ") {
		parts := strings.Split(meta, "·")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	// Careful path: find "action {" and peel off the JSON blob as a single
	// field, then split the rest normally.
	idx := strings.Index(meta, "action ")
	before := meta[:idx]
	fromAction := meta[idx:]

	// Peel the JSON object: find the matching closing brace starting from
	// the first "{" after "action ".
	jsonStart := strings.Index(fromAction, "{")
	if jsonStart == -1 {
		// Malformed — fall back to naive split.
		parts := strings.Split(meta, "·")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	depth := 0
	jsonEnd := -1
	for i := jsonStart; i < len(fromAction); i++ {
		switch fromAction[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				jsonEnd = i + 1
				break
			}
		}
		if jsonEnd != -1 {
			break
		}
	}
	if jsonEnd == -1 {
		jsonEnd = len(fromAction)
	}
	actionField := strings.TrimSpace(fromAction[:jsonEnd])
	after := fromAction[jsonEnd:]

	out := []string{}
	for _, p := range strings.Split(before, "·") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	out = append(out, actionField)
	for _, p := range strings.Split(after, "·") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// applyMetadata scans parsed metadata fields and fills the thought struct.
// Unknown keys are ignored for forward compatibility. Required fields are
// validated by Thought.Validate after this returns.
func applyMetadata(t *Thought, fields []string) error {
	for _, f := range fields {
		// "score 92"
		// "class read"
		// "created 2026-04-07T14:30:00Z"
		// "reinforced 2026-04-07T14:30:00Z"
		// "surfaced 0/2"
		// "source conv-7f3a:nap-3"
		// "confidence 80"
		// "value 70"
		// "[provenance: ...]"
		// "action {...}"
		if strings.HasPrefix(f, "[provenance:") {
			// "[provenance: ...]" — trim the brackets and prefix
			v := strings.TrimSuffix(strings.TrimPrefix(f, "[provenance:"), "]")
			t.Provenance = strings.TrimSpace(v)
			continue
		}
		if strings.HasPrefix(f, "action ") {
			blob := strings.TrimSpace(strings.TrimPrefix(f, "action "))
			var act ProposedAction
			if err := json.Unmarshal([]byte(blob), &act); err != nil {
				return fmt.Errorf("invalid action json: %w", err)
			}
			t.Action = &act
			continue
		}
		key, value, ok := splitKeyValue(f)
		if !ok {
			continue
		}
		switch key {
		case "score":
			if n, err := strconv.Atoi(value); err == nil {
				t.Score = n
			}
		case "confidence":
			if n, err := strconv.Atoi(value); err == nil {
				t.Confidence = n
			}
		case "value":
			if n, err := strconv.Atoi(value); err == nil {
				t.Value = n
			}
		case "class":
			t.Class = ActionClass(value)
		case "created":
			if ts, err := parseTime(value); err == nil {
				t.Created = ts
			}
		case "reinforced":
			if ts, err := parseTime(value); err == nil {
				t.Reinforced = ts
			}
		case "surfaced":
			// "N/M"
			if slash := strings.Index(value, "/"); slash != -1 {
				if n, err := strconv.Atoi(value[:slash]); err == nil {
					t.SurfacedN = n
				}
				if m, err := strconv.Atoi(value[slash+1:]); err == nil {
					t.SurfacedMax = m
				}
			} else if n, err := strconv.Atoi(value); err == nil {
				t.SurfacedN = n
			}
		case "source":
			t.Source = value
		}
	}
	return nil
}

// splitKeyValue splits "key value with spaces" into ("key", "value with spaces").
// Returns false if the field has no space separator.
func splitKeyValue(f string) (string, string, bool) {
	i := strings.IndexByte(f, ' ')
	if i == -1 {
		return "", "", false
	}
	return f[:i], strings.TrimSpace(f[i+1:]), true
}

// parseTime accepts either RFC3339 or a short "2026-04-07T14:30" form.
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time format: %q", s)
}

// RenderSection writes a slice of thoughts back into the markdown bullet
// format. The output does NOT include the "## THOUGHTS" heading — callers
// splice the rendered body into the containing MIND.md document.
//
// If thoughts is empty, the output is the single line "_(no active thoughts)_"
// so the section never appears empty in MIND.md (which would be confusing when
// reading the file by eye).
func RenderSection(thoughts []Thought) string {
	if len(thoughts) == 0 {
		return "_(no active thoughts)_"
	}
	var b strings.Builder
	for i, t := range thoughts {
		if i > 0 {
			b.WriteString("\n")
		}
		renderThought(&b, t)
	}
	return b.String()
}

func renderThought(b *strings.Builder, t Thought) {
	fmt.Fprintf(b, "- **[%s]** %s\n", t.ID, t.Body)

	// Metadata line — always in this order for consistency.
	fmt.Fprintf(b, "  · score %d · class %s · confidence %d · value %d",
		t.Score, t.Class, t.Confidence, t.Value)
	if !t.Created.IsZero() {
		fmt.Fprintf(b, " · created %s", t.Created.UTC().Format(time.RFC3339))
	}
	if !t.Reinforced.IsZero() {
		fmt.Fprintf(b, " · reinforced %s", t.Reinforced.UTC().Format(time.RFC3339))
	}
	max := t.SurfacedMax
	if max == 0 {
		max = 2
	}
	fmt.Fprintf(b, " · surfaced %d/%d", t.SurfacedN, max)
	if t.Source != "" {
		fmt.Fprintf(b, " · source %s", t.Source)
	}
	b.WriteString("\n")

	if t.Provenance != "" {
		fmt.Fprintf(b, "  · [provenance: %s]\n", t.Provenance)
	}
	if t.Action != nil {
		blob, err := json.Marshal(t.Action)
		if err == nil {
			fmt.Fprintf(b, "  · action %s\n", blob)
		}
	}
}
