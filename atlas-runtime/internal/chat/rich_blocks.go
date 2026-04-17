package chat

import (
	"encoding/json"
	"strings"

	"atlas-runtime-go/internal/agent"
)

func messageBlocksJSON(blocks []map[string]any) *string {
	if len(blocks) == 0 {
		return nil
	}
	b, err := json.Marshal(blocks)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

func firstNonNilString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func HydrateStoredBlocks(raw string) []map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		return nil
	}
	for _, block := range blocks {
		if block["type"] != "file" {
			continue
		}
		file, _ := block["file"].(map[string]any)
		if file == nil {
			continue
		}
		path, _ := file["path"].(string)
		if path == "" {
			continue
		}
		token := agent.RegisterArtifact(path)
		if token == "" {
			continue
		}
		file["fileToken"] = token
	}
	return blocks
}
