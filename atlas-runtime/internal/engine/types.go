// Package engine manages the Engine LM subprocess — a bundled llama-server
// binary that provides a local OpenAI-compatible inference endpoint.
package engine

// EngineStatus describes the current state of the Engine LM process.
type EngineStatus struct {
	Running        bool    `json:"running"`
	Loading        bool    `json:"loading,omitempty"`
	LoadedModel    string  `json:"loadedModel"`
	Port           int     `json:"port"`
	BinaryReady    bool    `json:"binaryReady"`
	BuildVersion   string  `json:"buildVersion,omitempty"`
	LastError      string  `json:"lastError,omitempty"`
	LastTPS        float64 `json:"lastTPS,omitempty"`        // decode tokens/sec from /metrics
	PromptTPS      float64 `json:"promptTPS,omitempty"`      // prompt eval tokens/sec from /metrics
	GenTimeSec     float64 `json:"genTimeSec,omitempty"`     // total generation time in seconds from /metrics
	ActiveRequests int     `json:"activeRequests,omitempty"` // in-flight slots from /metrics
	ContextTokens  int     `json:"contextTokens,omitempty"`  // tokens in KV cache from /slots
}

// ModelInfo describes a GGUF model file stored in the models directory.
type ModelInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	SizeHuman string `json:"sizeHuman"`
}

// DownloadProgress tracks the state of an in-progress or recently interrupted
// model download. Active is true while the download goroutine is running.
type DownloadProgress struct {
	Active     bool    `json:"active"`
	Filename   string  `json:"filename"`
	URL        string  `json:"url,omitempty"`
	Downloaded int64   `json:"downloaded"`
	Total      int64   `json:"total"`
	Percent    float64 `json:"percent"`
}
