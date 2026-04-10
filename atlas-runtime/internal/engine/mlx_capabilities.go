package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type mlxTokenizerConfig struct {
	ChatTemplate     string `json:"chat_template"`
	ChatTemplateType string `json:"chat_template_type"`
	ToolParserType   string `json:"tool_parser_type"`
}

type mlxTokenizerJSON struct {
	Model struct {
		Vocab map[string]int `json:"vocab"`
	} `json:"model"`
	AddedTokens []struct {
		Content string `json:"content"`
	} `json:"added_tokens"`
}

func InspectMLXModelCapabilities(modelPath string) *MLXModelCapabilities {
	caps := &MLXModelCapabilities{}

	var tokCfg mlxTokenizerConfig
	if readJSONFile(filepath.Join(modelPath, "tokenizer_config.json"), &tokCfg) {
		caps.ChatTemplateType = strings.TrimSpace(tokCfg.ChatTemplateType)
		caps.ToolParserType = strings.TrimSpace(tokCfg.ToolParserType)
		if strings.TrimSpace(tokCfg.ChatTemplate) != "" || caps.ChatTemplateType != "" {
			caps.HasChatTemplate = true
		}
		if caps.ToolParserType == "" {
			caps.ToolParserType = inferMLXToolParser(tokCfg.ChatTemplate)
		}
	}

	if !caps.HasChatTemplate {
		if raw, ok := readTextFile(filepath.Join(modelPath, "chat_template.jinja")); ok && strings.TrimSpace(raw) != "" {
			caps.HasChatTemplate = true
			if caps.ToolParserType == "" {
				caps.ToolParserType = inferMLXToolParser(raw)
			}
		}
	}

	caps.HasToolCalling = caps.ToolParserType != ""

	var tokJSON mlxTokenizerJSON
	if readJSONFile(filepath.Join(modelPath, "tokenizer.json"), &tokJSON) {
		vocab := map[string]struct{}{}
		for token := range tokJSON.Model.Vocab {
			vocab[token] = struct{}{}
		}
		for _, tok := range tokJSON.AddedTokens {
			if tok.Content != "" {
				vocab[tok.Content] = struct{}{}
			}
		}
		caps.HasThinking = hasMLXThinkingTokens(vocab)
	}

	return caps
}

func readJSONFile(path string, out any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, out) == nil
}

func readTextFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func hasMLXThinkingTokens(vocab map[string]struct{}) bool {
	thinkPairs := [][2]string{
		{"<think>", "</think>"},
		{"<longcat_think>", "</longcat_think>"},
	}
	for _, pair := range thinkPairs {
		if _, ok := vocab[pair[0]]; ok {
			if _, ok := vocab[pair[1]]; ok {
				return true
			}
		}
	}
	_, hasChannelStart := vocab["<|channel>"]
	_, hasChannelEnd := vocab["<channel|>"]
	return hasChannelStart && hasChannelEnd
}

func inferMLXToolParser(chatTemplate string) string {
	if chatTemplate == "" {
		return ""
	}
	switch {
	case strings.Contains(chatTemplate, "<minimax:tool_call>"):
		return "minimax_m2"
	case strings.Contains(chatTemplate, "<|tool_call>") && strings.Contains(chatTemplate, "<tool_call|>"):
		return "gemma4"
	case strings.Contains(chatTemplate, "<start_function_call>"):
		return "function_gemma"
	case strings.Contains(chatTemplate, "<longcat_tool_call>"):
		return "longcat"
	case strings.Contains(chatTemplate, "<arg_key>"):
		return "glm47"
	case strings.Contains(chatTemplate, "<|tool_list_start|>"):
		return "pythonic"
	case strings.Contains(chatTemplate, "<tool_call>\\n<function=") || strings.Contains(chatTemplate, "<tool_call>\n<function="):
		return "qwen3_coder"
	case strings.Contains(chatTemplate, "<|tool_calls_section_begin|>"):
		return "kimi_k2"
	case strings.Contains(chatTemplate, "[TOOL_CALLS]"):
		return "mistral"
	case strings.Contains(chatTemplate, "<tool_call>") && strings.Contains(chatTemplate, "tool_call.name"):
		return "json_tools"
	default:
		return ""
	}
}
