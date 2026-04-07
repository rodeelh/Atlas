package features

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/storage"
)

const gremlinsFile = "GREMLINS.md"

var gremlinsMu sync.Mutex

// GremlinItem mirrors Swift GremlinItem (camelCase JSON tags).
type GremlinItem struct {
	ID                       string                    `json:"id"`
	Name                     string                    `json:"name"`
	Emoji                    string                    `json:"emoji"`
	Prompt                   string                    `json:"prompt"`
	ScheduleRaw              string                    `json:"scheduleRaw"`
	ScheduleJSON             *string                   `json:"scheduleJSON,omitempty"`
	IsEnabled                bool                      `json:"isEnabled"`
	SourceType               string                    `json:"sourceType"`
	CreatedAt                string                    `json:"createdAt"`
	WorkflowID               *string                   `json:"workflowID,omitempty"`
	WorkflowInputValues      map[string]string         `json:"workflowInputValues,omitempty"`
	TelegramChatID           *int64                    `json:"telegramChatID,omitempty"`
	CommunicationDestination *CommunicationDestination `json:"communicationDestination,omitempty"`
	GremlinDescription       *string                   `json:"gremlinDescription,omitempty"`
	Tags                     []string                  `json:"tags"`
	MaxRetries               int                       `json:"maxRetries"`
	TimeoutSeconds           *int                      `json:"timeoutSeconds,omitempty"`
	NextRunAt                *string                   `json:"nextRunAt,omitempty"`
	LastModifiedAt           *string                   `json:"lastModifiedAt,omitempty"`
}

type CommunicationDestination struct {
	ID          string  `json:"id"`
	Platform    string  `json:"platform"`
	ChannelID   string  `json:"channelID"`
	ChannelName *string `json:"channelName,omitempty"`
	UserID      *string `json:"userID,omitempty"`
	ThreadID    *string `json:"threadID,omitempty"`
}

// GremlinRunRecord mirrors the gremlin_runs SQLite schema for JSON output.
type GremlinRunRecord struct {
	RunID           string  `json:"id"`
	GremlinID       string  `json:"gremlinID"`
	StartedAt       string  `json:"startedAt"`
	FinishedAt      *string `json:"finishedAt,omitempty"`
	Status          string  `json:"status"`
	Output          *string `json:"output,omitempty"`
	ErrorMessage    *string `json:"errorMessage,omitempty"`
	ConversationID  *string `json:"conversationID,omitempty"`
	WorkflowRunID   *string `json:"workflowRunID,omitempty"`
	TriggerSource   string  `json:"triggerSource,omitempty"`
	ExecutionStatus string  `json:"executionStatus,omitempty"`
	DeliveryStatus  string  `json:"deliveryStatus,omitempty"`
	DeliveryError   *string `json:"deliveryError,omitempty"`
	DestinationJSON *string `json:"destinationJSON,omitempty"`
	DurationMs      int64   `json:"durationMs,omitempty"`
	RetryCount      int     `json:"retryCount,omitempty"`
	ArtifactsJSON   *string `json:"artifactsJSON,omitempty"`
}

// ReadGremlinsRaw returns the raw content of GREMLINS.md, or "" if not found.
func ReadGremlinsRaw(supportDir string) string {
	data, err := os.ReadFile(filepath.Join(supportDir, gremlinsFile))
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteGremlinsRaw atomically overwrites GREMLINS.md.
func WriteGremlinsRaw(supportDir, content string) error {
	gremlinsMu.Lock()
	defer gremlinsMu.Unlock()
	if err := validateGremlinContent(content); err != nil {
		return err
	}
	return writeGremlinsRawLocked(supportDir, content)
}

func writeGremlinsRawLocked(supportDir, content string) error {
	path := filepath.Join(supportDir, gremlinsFile)
	tmp, err := os.CreateTemp(filepath.Dir(path), "GREMLINS-*.md")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
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

func validateGremlinContent(content string) error {
	items := parseGremlinMarkdown(content)
	seen := map[string]bool{}
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		if seen[item.ID] {
			return fmt.Errorf("duplicate automation id %q", item.ID)
		}
		seen[item.ID] = true
	}
	return nil
}

// ParseGremlins parses GREMLINS.md and returns the list of GremlinItems.
func ParseGremlins(supportDir string) []GremlinItem {
	raw := ReadGremlinsRaw(supportDir)
	if raw == "" {
		return []GremlinItem{}
	}
	return parseGremlinMarkdown(raw)
}

// gremlinBlock accumulates fields for one gremlin while scanning the file.
type gremlinBlock struct {
	name           string
	emoji          string
	schedule       string
	isEnabled      bool
	sourceType     string
	createdAt      string
	workflowID     *string
	workflowInputs map[string]string
	telegramChatID *int64
	destination    *CommunicationDestination
	id             string
	desc           *string
	tags           []string
	promptLines    []string
	inMetadata     bool
	active         bool
}

func newGremlinBlock() gremlinBlock {
	return gremlinBlock{
		emoji:      "⚡",
		isEnabled:  true,
		sourceType: "manual",
		createdAt:  time.Now().Format("2006-01-02"),
		inMetadata: true,
	}
}

func (b *gremlinBlock) toItem() (GremlinItem, bool) {
	if !b.active || b.name == "" {
		return GremlinItem{}, false
	}
	prompt := strings.TrimSpace(strings.Join(b.promptLines, "\n"))
	tags := b.tags
	if tags == nil {
		tags = []string{}
	}
	return GremlinItem{
		ID:                       b.id,
		Name:                     b.name,
		Emoji:                    b.emoji,
		Prompt:                   prompt,
		ScheduleRaw:              b.schedule,
		IsEnabled:                b.isEnabled,
		SourceType:               b.sourceType,
		CreatedAt:                b.createdAt,
		WorkflowID:               b.workflowID,
		WorkflowInputValues:      b.workflowInputs,
		TelegramChatID:           b.telegramChatID,
		CommunicationDestination: b.destination,
		GremlinDescription:       b.desc,
		Tags:                     tags,
		MaxRetries:               0,
	}, true
}

// parseGremlinMarkdown parses the GREMLINS.md format:
//
//	## Name [emoji]
//	schedule: <schedule>
//	status: enabled|disabled
//	created: <date> via <source>
//	<optional metadata lines>
//
//	<prompt text>
//	---
func parseGremlinMarkdown(content string) []GremlinItem {
	var items []GremlinItem
	cur := newGremlinBlock()
	scanner := bufio.NewScanner(strings.NewReader(content))

	flush := func() {
		if item, ok := cur.toItem(); ok {
			items = append(items, item)
		}
		cur = newGremlinBlock()
	}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.TrimSpace(line) == "---" {
			flush()
			continue
		}

		if strings.HasPrefix(line, "## ") {
			if cur.active {
				flush()
			}
			rest := strings.TrimPrefix(line, "## ")
			// Extract trailing [emoji]
			if idx := strings.LastIndex(rest, "["); idx >= 0 && strings.HasSuffix(strings.TrimSpace(rest), "]") {
				emojiStr := strings.TrimSpace(rest[idx+1 : len(strings.TrimSpace(rest))-1])
				cur.emoji = emojiStr
				rest = strings.TrimSpace(rest[:idx])
			}
			cur.name = strings.TrimSpace(rest)
			cur.id = slugify(cur.name)
			cur.active = true
			cur.inMetadata = true
			continue
		}

		if !cur.active {
			continue
		}

		if cur.inMetadata {
			if colonIdx := strings.Index(line, ":"); colonIdx > 0 {
				key := strings.ToLower(strings.TrimSpace(line[:colonIdx]))
				val := strings.TrimSpace(line[colonIdx+1:])
				switch key {
				case "schedule":
					cur.schedule = val
					continue
				case "status":
					cur.isEnabled = strings.ToLower(val) == "enabled"
					continue
				case "created":
					parts := strings.SplitN(val, " via ", 2)
					cur.createdAt = strings.TrimSpace(parts[0])
					if len(parts) == 2 {
						cur.sourceType = strings.TrimSpace(parts[1])
					}
					continue
				case "description":
					d := val
					cur.desc = &d
					continue
				case "workflow_id":
					if strings.TrimSpace(val) == "" {
						cur.workflowID = nil
					} else {
						v := strings.TrimSpace(val)
						cur.workflowID = &v
					}
					continue
				case "workflow_inputs":
					if strings.TrimSpace(val) == "" {
						cur.workflowInputs = nil
						continue
					}
					var parsed map[string]string
					if err := json.Unmarshal([]byte(val), &parsed); err == nil {
						cur.workflowInputs = parsed
					}
					continue
				case "notify_telegram":
					if strings.TrimSpace(val) == "" {
						cur.telegramChatID = nil
						continue
					}
					if id, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err == nil {
						cur.telegramChatID = &id
						channelID := strconv.FormatInt(id, 10)
						cur.destination = &CommunicationDestination{
							ID:        "telegram:" + channelID,
							Platform:  "telegram",
							ChannelID: channelID,
						}
					}
					continue
				case "notify_destination":
					if strings.TrimSpace(val) == "" {
						cur.destination = nil
						continue
					}
					var dest CommunicationDestination
					if err := json.Unmarshal([]byte(val), &dest); err == nil && strings.TrimSpace(dest.Platform) != "" && strings.TrimSpace(dest.ChannelID) != "" {
						if strings.TrimSpace(dest.ID) == "" {
							if dest.ThreadID != nil && strings.TrimSpace(*dest.ThreadID) != "" {
								dest.ID = dest.Platform + ":" + dest.ChannelID + ":" + *dest.ThreadID
							} else {
								dest.ID = dest.Platform + ":" + dest.ChannelID
							}
						}
						cur.destination = &dest
					}
					continue
				case "tags":
					for _, t := range strings.Split(val, ",") {
						if trimmed := strings.TrimSpace(t); trimmed != "" {
							cur.tags = append(cur.tags, trimmed)
						}
					}
					continue
				case "modified", "max_retries", "timeout_seconds":
					continue // parse but ignore optional fields we don't need
				}
			}
			// Empty line after schedule is set → transition to prompt body
			if strings.TrimSpace(line) == "" && cur.schedule != "" {
				cur.inMetadata = false
				continue
			}
			// Non-metadata content while schedule is set → treat as prompt start
			if cur.schedule != "" {
				cur.inMetadata = false
				// fall through to add to promptLines
			}
		}

		if !cur.inMetadata {
			cur.promptLines = append(cur.promptLines, line)
		}
	}

	flush() // flush last block (no trailing ---)
	return items
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-")
}

// ListGremlinRuns returns run history from SQLite for the given gremlinID (or all runs).
func ListGremlinRuns(db *storage.DB, gremlinID string, limit int) []GremlinRunRecord {
	rows, err := db.ListGremlinRuns(gremlinID, limit)
	if err != nil {
		return []GremlinRunRecord{}
	}
	out := make([]GremlinRunRecord, 0, len(rows))
	for _, r := range rows {
		rec := GremlinRunRecord{
			RunID:           r.RunID,
			GremlinID:       r.GremlinID,
			StartedAt:       unixToISO(r.StartedAt),
			Status:          r.Status,
			Output:          r.Output,
			ErrorMessage:    r.ErrorMessage,
			ConversationID:  r.ConversationID,
			WorkflowRunID:   r.WorkflowRunID,
			TriggerSource:   r.TriggerSource,
			ExecutionStatus: r.ExecutionStatus,
			DeliveryStatus:  r.DeliveryStatus,
			DeliveryError:   r.DeliveryError,
			DestinationJSON: r.DestinationJSON,
			DurationMs:      r.DurationMs,
			RetryCount:      r.RetryCount,
			ArtifactsJSON:   r.ArtifactsJSON,
		}
		if r.FinishedAt != nil {
			s := unixToISO(*r.FinishedAt)
			rec.FinishedAt = &s
		}
		out = append(out, rec)
	}
	return out
}

// AppendGremlin adds a new GremlinItem block to GREMLINS.md.
func AppendGremlin(supportDir string, item GremlinItem) error {
	gremlinsMu.Lock()
	defer gremlinsMu.Unlock()
	if item.ID == "" {
		item.ID = slugify(item.Name)
	}
	for _, existing := range parseGremlinMarkdown(ReadGremlinsRaw(supportDir)) {
		if existing.ID == item.ID {
			return fmt.Errorf("automation id already exists: %s", item.ID)
		}
	}
	if item.CreatedAt == "" {
		item.CreatedAt = time.Now().Format("2006-01-02")
	}
	if item.SourceType == "" {
		item.SourceType = "manual"
	}
	if item.Emoji == "" {
		item.Emoji = "⚡"
	}
	block := formatGremlinBlock(item)
	raw := ReadGremlinsRaw(supportDir)
	var updated string
	if raw == "" {
		updated = block
	} else {
		updated = strings.TrimRight(raw, "\n") + "\n\n" + block
	}
	return writeGremlinsRawLocked(supportDir, updated)
}

// UpdateGremlin rewrites the block for the given gremlinID in GREMLINS.md.
// Returns the updated item, or nil if not found.
func UpdateGremlin(supportDir, gremlinID string, updates GremlinItem) (*GremlinItem, error) {
	gremlinsMu.Lock()
	defer gremlinsMu.Unlock()
	items := ParseGremlins(supportDir)
	var found *GremlinItem
	for i := range items {
		if items[i].ID == gremlinID {
			found = &items[i]
			break
		}
	}
	if found == nil {
		return nil, nil
	}

	// Apply updates onto the found item.
	if updates.Name != "" {
		found.Name = updates.Name
	}
	if updates.Emoji != "" {
		found.Emoji = updates.Emoji
	}
	found.Prompt = updates.Prompt
	if updates.ScheduleRaw != "" {
		found.ScheduleRaw = updates.ScheduleRaw
	}
	found.IsEnabled = updates.IsEnabled
	found.WorkflowID = updates.WorkflowID
	found.WorkflowInputValues = updates.WorkflowInputValues
	found.TelegramChatID = updates.TelegramChatID
	found.CommunicationDestination = updates.CommunicationDestination
	if updates.GremlinDescription != nil {
		found.GremlinDescription = updates.GremlinDescription
	}
	if updates.Tags != nil {
		found.Tags = updates.Tags
	}

	// Rebuild GREMLINS.md from the updated item list.
	var blocks []string
	for i := range items {
		if items[i].ID == gremlinID {
			blocks = append(blocks, formatGremlinBlock(*found))
		} else {
			blocks = append(blocks, formatGremlinBlock(items[i]))
		}
	}
	if err := writeGremlinsRawLocked(supportDir, strings.Join(blocks, "\n\n")); err != nil {
		return nil, err
	}
	return found, nil
}

// DeleteGremlin removes the block for the given gremlinID from GREMLINS.md.
func DeleteGremlin(supportDir, gremlinID string) (bool, error) {
	gremlinsMu.Lock()
	defer gremlinsMu.Unlock()
	items := ParseGremlins(supportDir)
	var remaining []GremlinItem
	found := false
	for _, item := range items {
		if item.ID == gremlinID {
			found = true
			continue
		}
		remaining = append(remaining, item)
	}
	if !found {
		return false, nil
	}
	var blocks []string
	for _, item := range remaining {
		blocks = append(blocks, formatGremlinBlock(item))
	}
	return true, writeGremlinsRawLocked(supportDir, strings.Join(blocks, "\n\n"))
}

// WriteGremlinItems rewrites GREMLINS.md from typed automation definitions.
// This keeps the legacy markdown file usable as import/export compatibility
// while SQLite-backed modules own canonical state.
func WriteGremlinItems(supportDir string, items []GremlinItem) error {
	gremlinsMu.Lock()
	defer gremlinsMu.Unlock()
	seen := map[string]bool{}
	blocks := make([]string, 0, len(items))
	for _, item := range items {
		id := item.ID
		if strings.TrimSpace(id) == "" {
			id = slugify(item.Name)
		}
		if seen[id] {
			return fmt.Errorf("duplicate automation id %q", id)
		}
		seen[id] = true
		blocks = append(blocks, formatGremlinBlock(item))
	}
	return writeGremlinsRawLocked(supportDir, strings.Join(blocks, "\n\n"))
}

// formatGremlinBlock serialises a GremlinItem as a GREMLINS.md section.
func formatGremlinBlock(item GremlinItem) string {
	status := "enabled"
	if !item.IsEnabled {
		status = "disabled"
	}
	source := item.SourceType
	if source == "" {
		source = "manual"
	}
	var sb strings.Builder
	sb.WriteString("## " + item.Name + " [" + item.Emoji + "]\n")
	sb.WriteString("schedule: " + item.ScheduleRaw + "\n")
	sb.WriteString("status: " + status + "\n")
	sb.WriteString("created: " + item.CreatedAt + " via " + source + "\n")
	if item.GremlinDescription != nil && *item.GremlinDescription != "" {
		sb.WriteString("description: " + *item.GremlinDescription + "\n")
	}
	if len(item.Tags) > 0 {
		sb.WriteString("tags: " + strings.Join(item.Tags, ", ") + "\n")
	}
	if item.WorkflowID != nil && strings.TrimSpace(*item.WorkflowID) != "" {
		sb.WriteString("workflow_id: " + strings.TrimSpace(*item.WorkflowID) + "\n")
	}
	if len(item.WorkflowInputValues) > 0 {
		if data, err := json.Marshal(item.WorkflowInputValues); err == nil {
			sb.WriteString("workflow_inputs: " + string(data) + "\n")
		}
	}
	if item.CommunicationDestination != nil && strings.TrimSpace(item.CommunicationDestination.Platform) != "" && strings.TrimSpace(item.CommunicationDestination.ChannelID) != "" {
		if data, err := json.Marshal(item.CommunicationDestination); err == nil {
			sb.WriteString("notify_destination: " + string(data) + "\n")
		}
	} else if item.TelegramChatID != nil {
		sb.WriteString("notify_telegram: " + strconv.FormatInt(*item.TelegramChatID, 10) + "\n")
	}
	sb.WriteString("\n")
	sb.WriteString(item.Prompt)
	sb.WriteString("\n---")
	return sb.String()
}

func unixToISO(ts float64) string {
	sec := int64(ts)
	nano := int64((ts - float64(sec)) * 1e9)
	return time.Unix(sec, nano).UTC().Format(time.RFC3339)
}

// SetAutomationEnabled toggles the status: field for the given automation in GREMLINS.md.
func SetAutomationEnabled(supportDir, gremlinID string, enabled bool) error {
	gremlinsMu.Lock()
	defer gremlinsMu.Unlock()
	raw := ReadGremlinsRaw(supportDir)
	if raw == "" {
		return nil
	}
	newStatus := "disabled"
	if enabled {
		newStatus = "enabled"
	}

	lines := strings.Split(raw, "\n")
	inTarget := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			inTarget = false
			continue
		}
		if strings.HasPrefix(line, "## ") {
			rest := strings.TrimPrefix(line, "## ")
			if idx := strings.LastIndex(rest, "["); idx >= 0 {
				rest = strings.TrimSpace(rest[:idx])
			}
			inTarget = slugify(strings.TrimSpace(rest)) == gremlinID
			continue
		}
		if inTarget && strings.HasPrefix(strings.ToLower(line), "status:") {
			lines[i] = "status: " + newStatus
		}
	}
	return writeGremlinsRawLocked(supportDir, strings.Join(lines, "\n"))
}
