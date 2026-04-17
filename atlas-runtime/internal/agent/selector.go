package agent

// ToolSelector selects and upgrades the tool set for a single agent turn.
// The interface lives in the agent package so LoopConfig can reference it
// without an import cycle (concrete implementations live in internal/chat).
type ToolSelector interface {
	// Initial returns the tool list for the first model call.
	// Returns nil to use the full registry (identity / no selection).
	Initial() []map[string]any

	// Upgrade handles a request_tools tool call and returns the upgraded tool
	// list plus the summary text to send back as the tool result.
	Upgrade(tc OAIToolCall) (tools []map[string]any, summary string)
}

// RequestToolsArgs is the parsed payload of a request_tools tool call.
// Exported so selector implementations in internal/chat can parse it without
// re-declaring the same struct.
type RequestToolsArgs struct {
	Broad      bool     `json:"broad"`
	Categories []string `json:"categories"`
}

// IdentitySelector is the zero-value ToolSelector. It returns nil for Initial
// (full tool registry) and nil/empty for Upgrade (no upgrade). Used as the
// default when LoopConfig.Selector is nil.
type IdentitySelector struct{}

func (IdentitySelector) Initial() []map[string]any                               { return nil }
func (IdentitySelector) Upgrade(_ OAIToolCall) ([]map[string]any, string)        { return nil, "" }
