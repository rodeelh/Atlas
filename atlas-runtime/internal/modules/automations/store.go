package automations

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/storage"
)

func (m *Module) importLegacyDefinitions(replace bool) error {
	if m.store == nil {
		return nil
	}
	existing, err := m.store.ListAutomations()
	if err != nil {
		return err
	}
	if !replace && len(existing) > 0 {
		return nil
	}
	items := features.ParseGremlins(m.supportDir)
	seen := map[string]bool{}
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			item.ID = automationID(item.Name)
		}
		if err := m.validateDestination(item.CommunicationDestination); err != nil {
			return fmt.Errorf("automation %q: %w", item.ID, err)
		}
		seen[item.ID] = true
		if err := m.store.SaveAutomation(automationRowFromItem(item)); err != nil {
			return err
		}
	}
	if replace {
		for _, row := range existing {
			if !seen[row.ID] {
				if _, err := m.store.DeleteAutomation(row.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (m *Module) listDefinitions() ([]features.GremlinItem, error) {
	if m.store == nil {
		return features.ParseGremlins(m.supportDir), nil
	}
	rows, err := m.store.ListAutomations()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if err := m.importLegacyDefinitions(false); err != nil {
			return nil, err
		}
		rows, err = m.store.ListAutomations()
		if err != nil {
			return nil, err
		}
	}
	items := make([]features.GremlinItem, 0, len(rows))
	for _, row := range rows {
		item, err := automationItemFromRow(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (m *Module) getDefinition(id string) (features.GremlinItem, bool, error) {
	if m.store == nil {
		for _, item := range features.ParseGremlins(m.supportDir) {
			if item.ID == id {
				return item, true, nil
			}
		}
		return features.GremlinItem{}, false, nil
	}
	row, err := m.store.GetAutomation(id)
	if err != nil {
		return features.GremlinItem{}, false, err
	}
	if row == nil {
		for _, item := range features.ParseGremlins(m.supportDir) {
			if item.ID == id {
				if err := m.store.SaveAutomation(automationRowFromItem(item)); err != nil {
					return features.GremlinItem{}, false, err
				}
				return item, true, nil
			}
		}
		return features.GremlinItem{}, false, nil
	}
	item, err := automationItemFromRow(*row)
	if err != nil {
		return features.GremlinItem{}, false, err
	}
	return item, true, nil
}

func (m *Module) saveDefinition(item features.GremlinItem) (features.GremlinItem, error) {
	if strings.TrimSpace(item.ID) == "" {
		item.ID = automationID(item.Name)
	}
	if strings.TrimSpace(item.CreatedAt) == "" {
		item.CreatedAt = time.Now().Format("2006-01-02")
	}
	if strings.TrimSpace(item.SourceType) == "" {
		item.SourceType = "manual"
	}
	if strings.TrimSpace(item.Emoji) == "" {
		item.Emoji = "⚡"
	}
	if item.Tags == nil {
		item.Tags = []string{}
	}
	if err := m.validateDestination(item.CommunicationDestination); err != nil {
		return features.GremlinItem{}, err
	}
	if m.store == nil {
		if err := features.AppendGremlin(m.supportDir, item); err != nil {
			return features.GremlinItem{}, err
		}
		return item, nil
	}
	if existing, ok, err := m.getDefinition(item.ID); err != nil {
		return features.GremlinItem{}, err
	} else if ok && !strings.EqualFold(existing.Name, item.Name) && automationID(item.Name) == item.ID {
		return features.GremlinItem{}, fmt.Errorf("automation id already exists: %s", item.ID)
	}
	if err := m.store.SaveAutomation(automationRowFromItem(item)); err != nil {
		return features.GremlinItem{}, err
	}
	if err := m.mirrorDefinitionsToGremlins(); err != nil {
		return features.GremlinItem{}, err
	}
	return item, nil
}

func (m *Module) validateDestination(dest *features.CommunicationDestination) error {
	if dest == nil {
		return nil
	}
	platform := strings.TrimSpace(dest.Platform)
	channelID := strings.TrimSpace(dest.ChannelID)
	threadID := strVal(dest.ThreadID)
	if platform == "" || channelID == "" {
		return fmt.Errorf("automation destination requires platform and channelID")
	}
	if m.commsStore == nil {
		return nil
	}
	session, err := m.commsStore.FetchCommSession(platform, channelID, threadID)
	if err != nil {
		return fmt.Errorf("validate automation destination: %w", err)
	}
	if session == nil {
		return fmt.Errorf("automation destination %s:%s is not an authorized communication channel", platform, channelID)
	}
	return nil
}

func (m *Module) createDefinition(item features.GremlinItem) (features.GremlinItem, error) {
	if strings.TrimSpace(item.ID) == "" {
		item.ID = automationID(item.Name)
	}
	if _, ok, err := m.getDefinition(item.ID); err != nil {
		return features.GremlinItem{}, err
	} else if ok {
		return features.GremlinItem{}, fmt.Errorf("automation id already exists: %s", item.ID)
	}
	return m.saveDefinition(item)
}

func (m *Module) deleteDefinition(id string) (bool, error) {
	if m.store == nil {
		return features.DeleteGremlin(m.supportDir, id)
	}
	found, err := m.store.DeleteAutomation(id)
	if err != nil {
		return false, err
	}
	if found {
		if err := m.mirrorDefinitionsToGremlins(); err != nil {
			return false, err
		}
	}
	return found, nil
}

func mergeAutomationPatch(existing features.GremlinItem, raw map[string]json.RawMessage) (features.GremlinItem, error) {
	out := existing
	if value, ok, err := patchString(raw, "name"); err != nil {
		return features.GremlinItem{}, err
	} else if ok && strings.TrimSpace(value) != "" {
		out.Name = value
	}
	if value, ok, err := patchString(raw, "emoji"); err != nil {
		return features.GremlinItem{}, err
	} else if ok && strings.TrimSpace(value) != "" {
		out.Emoji = value
	}
	if value, ok, err := patchString(raw, "prompt"); err != nil {
		return features.GremlinItem{}, err
	} else if ok {
		out.Prompt = value
	}
	if value, ok, err := patchString(raw, "scheduleRaw"); err != nil {
		return features.GremlinItem{}, err
	} else if ok && strings.TrimSpace(value) != "" {
		out.ScheduleRaw = value
		out.ScheduleJSON = nil
		out.NextRunAt = nil
	}
	if value, ok, err := patchString(raw, "sourceType"); err != nil {
		return features.GremlinItem{}, err
	} else if ok && strings.TrimSpace(value) != "" {
		out.SourceType = value
	}
	if value, ok, err := patchBool(raw, "isEnabled"); err != nil {
		return features.GremlinItem{}, err
	} else if ok {
		out.IsEnabled = value
		if !value {
			out.NextRunAt = nil
		}
	}
	if value, ok, err := patchOptionalString(raw, "workflowID"); err != nil {
		return features.GremlinItem{}, err
	} else if ok {
		out.WorkflowID = value
	}
	if _, ok := raw["target"]; ok {
		var target *features.ExecutableTarget
		if err := decodeNullable(raw["target"], &target); err != nil {
			return features.GremlinItem{}, fmt.Errorf("target: %w", err)
		}
		out.ExecutableTarget = target
	}
	if _, ok := raw["workflowInputValues"]; ok {
		var values map[string]string
		if err := decodeNullable(raw["workflowInputValues"], &values); err != nil {
			return features.GremlinItem{}, fmt.Errorf("workflowInputValues: %w", err)
		}
		out.WorkflowInputValues = values
	}
	if _, ok := raw["telegramChatID"]; ok {
		var value *int64
		if err := decodeNullable(raw["telegramChatID"], &value); err != nil {
			return features.GremlinItem{}, fmt.Errorf("telegramChatID: %w", err)
		}
		out.TelegramChatID = value
	}
	if _, ok := raw["communicationDestination"]; ok {
		var dest *features.CommunicationDestination
		if err := decodeNullable(raw["communicationDestination"], &dest); err != nil {
			return features.GremlinItem{}, fmt.Errorf("communicationDestination: %w", err)
		}
		out.CommunicationDestination = dest
	}
	if value, ok, err := patchOptionalString(raw, "gremlinDescription"); err != nil {
		return features.GremlinItem{}, err
	} else if ok {
		out.GremlinDescription = value
	}
	if _, ok := raw["tags"]; ok {
		var tags []string
		if err := decodeNullable(raw["tags"], &tags); err != nil {
			return features.GremlinItem{}, fmt.Errorf("tags: %w", err)
		}
		out.Tags = tags
	}
	out.LastModifiedAt = strPtr(time.Now().UTC().Format(time.RFC3339))
	return normalizeAutomationItem(out), nil
}

func patchString(raw map[string]json.RawMessage, key string) (string, bool, error) {
	data, ok := raw[key]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", true, fmt.Errorf("%s: %w", key, err)
	}
	return value, true, nil
}

func patchOptionalString(raw map[string]json.RawMessage, key string) (*string, bool, error) {
	data, ok := raw[key]
	if !ok {
		return nil, false, nil
	}
	var value *string
	if err := decodeNullable(data, &value); err != nil {
		return nil, true, fmt.Errorf("%s: %w", key, err)
	}
	if value != nil {
		trimmed := strings.TrimSpace(*value)
		if trimmed == "" {
			return nil, true, nil
		}
		value = &trimmed
	}
	return value, true, nil
}

func patchBool(raw map[string]json.RawMessage, key string) (bool, bool, error) {
	data, ok := raw[key]
	if !ok {
		return false, false, nil
	}
	var value bool
	if err := json.Unmarshal(data, &value); err != nil {
		return false, true, fmt.Errorf("%s: %w", key, err)
	}
	return value, true, nil
}

func decodeNullable[T any](data json.RawMessage, out *T) error {
	if string(data) == "null" {
		var zero T
		*out = zero
		return nil
	}
	return json.Unmarshal(data, out)
}

func (m *Module) mirrorDefinitionsToGremlins() error {
	// Query the DB directly rather than going through listDefinitions(), which
	// would re-import from GREMLINS.md when the DB is empty (e.g. after the
	// last automation is deleted), undoing the deletion.
	if m.store == nil {
		return nil
	}
	rows, err := m.store.ListAutomations()
	if err != nil {
		return err
	}
	items := make([]features.GremlinItem, 0, len(rows))
	for _, row := range rows {
		item, err := automationItemFromRow(row)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	return features.WriteGremlinItems(m.supportDir, items)
}

func automationRowFromItem(item features.GremlinItem) storage.AutomationRow {
	item = normalizeAutomationItem(item)
	now := time.Now().UTC().Format(time.RFC3339)
	updatedAt := now
	if item.LastModifiedAt != nil && strings.TrimSpace(*item.LastModifiedAt) != "" {
		updatedAt = strings.TrimSpace(*item.LastModifiedAt)
	}
	tags := item.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON := mustJSON(tags, "[]")
	scheduleJSON := item.ScheduleJSON
	nextRunAt := item.NextRunAt
	if !item.IsEnabled {
		nextRunAt = nil
	}
	if scheduleJSON == nil || nextRunAt == nil {
		if meta, next, ok := scheduleState(item.ScheduleRaw, time.Now()); ok {
			if scheduleJSON == nil {
				scheduleJSON = strPtr(mustJSON(meta, "{}"))
			}
			if nextRunAt == nil && item.IsEnabled {
				nextValue := next.UTC().Format(time.RFC3339)
				nextRunAt = &nextValue
			}
		}
	}
	workflowID, workflowInputsJSON := automationStorageTarget(item)
	return storage.AutomationRow{
		ID:                           item.ID,
		Name:                         item.Name,
		Emoji:                        defaultString(item.Emoji, "⚡"),
		Prompt:                       item.Prompt,
		ScheduleRaw:                  item.ScheduleRaw,
		ScheduleJSON:                 scheduleJSON,
		IsEnabled:                    item.IsEnabled,
		SourceType:                   defaultString(item.SourceType, "manual"),
		CreatedAt:                    defaultString(item.CreatedAt, time.Now().Format("2006-01-02")),
		UpdatedAt:                    updatedAt,
		NextRunAt:                    nextRunAt,
		WorkflowID:                   workflowID,
		WorkflowInputsJSON:           workflowInputsJSON,
		CommunicationDestinationJSON: optionalJSON(item.CommunicationDestination),
		GremlinDescription:           item.GremlinDescription,
		TagsJSON:                     tagsJSON,
	}
}

func automationItemFromRow(row storage.AutomationRow) (features.GremlinItem, error) {
	var workflowInputs map[string]string
	if row.WorkflowInputsJSON != nil && strings.TrimSpace(*row.WorkflowInputsJSON) != "" {
		if err := json.Unmarshal([]byte(*row.WorkflowInputsJSON), &workflowInputs); err != nil {
			return features.GremlinItem{}, err
		}
	}
	var dest *features.CommunicationDestination
	if row.CommunicationDestinationJSON != nil && strings.TrimSpace(*row.CommunicationDestinationJSON) != "" {
		var parsed features.CommunicationDestination
		if err := json.Unmarshal([]byte(*row.CommunicationDestinationJSON), &parsed); err != nil {
			return features.GremlinItem{}, err
		}
		dest = &parsed
	}
	var tags []string
	if strings.TrimSpace(row.TagsJSON) != "" {
		if err := json.Unmarshal([]byte(row.TagsJSON), &tags); err != nil {
			return features.GremlinItem{}, err
		}
	}
	if tags == nil {
		tags = []string{}
	}
	updatedAt := row.UpdatedAt
	item := features.GremlinItem{
		ID:                       row.ID,
		Name:                     row.Name,
		Emoji:                    row.Emoji,
		Prompt:                   row.Prompt,
		ScheduleRaw:              row.ScheduleRaw,
		ScheduleJSON:             row.ScheduleJSON,
		IsEnabled:                row.IsEnabled,
		SourceType:               row.SourceType,
		CreatedAt:                row.CreatedAt,
		WorkflowID:               row.WorkflowID,
		WorkflowInputValues:      workflowInputs,
		CommunicationDestination: dest,
		GremlinDescription:       row.GremlinDescription,
		Tags:                     tags,
		NextRunAt:                row.NextRunAt,
		LastModifiedAt:           &updatedAt,
	}
	item.ExecutableTarget = decodeAutomationTarget(row.WorkflowID)
	return normalizeAutomationItem(item), nil
}

func optionalJSON(value any) *string {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return nil
	}
	out := string(data)
	return &out
}

func mustJSON(value any, fallback string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(data)
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func automationID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
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

func normalizeAutomationItem(item features.GremlinItem) features.GremlinItem {
	if item.ExecutableTarget != nil {
		targetType := strings.TrimSpace(item.ExecutableTarget.Type)
		targetRef := strings.TrimSpace(item.ExecutableTarget.Ref)
		if targetType != "" && targetRef != "" {
			item.ExecutableTarget = &features.ExecutableTarget{Type: targetType, Ref: targetRef}
			if targetType == "workflow" {
				item.WorkflowID = strPtr(targetRef)
			} else {
				item.WorkflowID = nil
			}
			return item
		}
		item.ExecutableTarget = nil
	}
	if item.WorkflowID != nil && strings.TrimSpace(*item.WorkflowID) != "" {
		item.ExecutableTarget = &features.ExecutableTarget{Type: "workflow", Ref: strings.TrimSpace(*item.WorkflowID)}
	}
	return item
}

func automationStorageTarget(item features.GremlinItem) (*string, *string) {
	if item.ExecutableTarget == nil {
		return item.WorkflowID, optionalJSON(item.WorkflowInputValues)
	}
	targetType := strings.TrimSpace(item.ExecutableTarget.Type)
	targetRef := strings.TrimSpace(item.ExecutableTarget.Ref)
	if targetType == "" || targetRef == "" {
		return nil, optionalJSON(item.WorkflowInputValues)
	}
	switch targetType {
	case "workflow":
		return strPtr(targetRef), optionalJSON(item.WorkflowInputValues)
	case "skill":
		return strPtr("skill:" + targetRef), optionalJSON(item.WorkflowInputValues)
	case "command":
		return strPtr("command:" + targetRef), optionalJSON(item.WorkflowInputValues)
	default:
		return strPtr(targetType + ":" + targetRef), optionalJSON(item.WorkflowInputValues)
	}
}

func decodeAutomationTarget(raw *string) *features.ExecutableTarget {
	if raw == nil {
		return nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "skill:") {
		return &features.ExecutableTarget{Type: "skill", Ref: strings.TrimSpace(strings.TrimPrefix(value, "skill:"))}
	}
	if strings.HasPrefix(value, "agent:") {
		return &features.ExecutableTarget{Type: "agent", Ref: strings.TrimSpace(strings.TrimPrefix(value, "agent:"))}
	}
	if strings.HasPrefix(value, "command:") {
		return &features.ExecutableTarget{Type: "command", Ref: strings.TrimSpace(strings.TrimPrefix(value, "command:"))}
	}
	return &features.ExecutableTarget{Type: "workflow", Ref: value}
}
