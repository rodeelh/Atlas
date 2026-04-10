package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectMLXModelCapabilities_DetectsToolsAndThinking(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "tokenizer_config.json"), `{
  "chat_template": "<tool_call>\n<function={{ tool_call.name }}>",
  "tool_parser_type": ""
}`)
	writeTestFile(t, filepath.Join(dir, "tokenizer.json"), `{
  "model": {
    "vocab": {
      "<think>": 1,
      "</think>": 2
    }
  }
}`)

	caps := InspectMLXModelCapabilities(dir)
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if !caps.HasChatTemplate {
		t.Fatal("expected chat template detection")
	}
	if !caps.HasToolCalling || caps.ToolParserType != "qwen3_coder" {
		t.Fatalf("expected qwen3_coder tool support, got %+v", caps)
	}
	if !caps.HasThinking {
		t.Fatalf("expected thinking support, got %+v", caps)
	}
}

func TestInspectMLXModelCapabilities_UsesExplicitParserType(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "tokenizer_config.json"), `{
  "chat_template_type": "deepseek_v32",
  "tool_parser_type": "json_tools"
}`)
	writeTestFile(t, filepath.Join(dir, "tokenizer.json"), `{"model":{"vocab":{}}}`)

	caps := InspectMLXModelCapabilities(dir)
	if caps.ToolParserType != "json_tools" {
		t.Fatalf("expected explicit tool parser type, got %+v", caps)
	}
	if caps.ChatTemplateType != "deepseek_v32" || !caps.HasChatTemplate {
		t.Fatalf("expected chat template type detection, got %+v", caps)
	}
}

func TestInspectMLXModelCapabilities_DetectsChannelThinking(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "tokenizer.json"), `{
  "model": {
    "vocab": {
      "<|channel>": 1,
      "<channel|>": 2
    }
  }
}`)

	caps := InspectMLXModelCapabilities(dir)
	if !caps.HasThinking {
		t.Fatalf("expected channel thinking support, got %+v", caps)
	}
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
