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

// ── MLX-LM types ─────────────────────────────────────────────────────────────

// MLXInferenceStats holds per-turn performance metrics for the last completed
// inference. Populated by MLXManager.RecordInference after each agent turn.
type MLXInferenceStats struct {
	DecodeTPS        float64 `json:"decodeTPS"`               // completion tokens / decode seconds
	PromptTokens     int     `json:"promptTokens"`            // input tokens (from usage)
	CompletionTokens int     `json:"completionTokens"`        // output tokens (from usage)
	GenerationSec    float64 `json:"generationSec"`           // wall-clock seconds for the full turn
	FirstTokenSec    float64 `json:"firstTokenSec,omitempty"` // time-to-first-token when streaming
	StreamChunks     int     `json:"streamChunks,omitempty"`  // number of streamed assistant deltas
	StreamChars      int     `json:"streamChars,omitempty"`   // total streamed text chars
	AvgChunkChars    float64 `json:"avgChunkChars,omitempty"` // derived from stream chars / chunks
}

// MLXStatus describes the current state of the MLX-LM process.
// Mirrors EngineStatus for the llama.cpp subsystem.
type MLXStatus struct {
	Running        bool               `json:"running"`
	Loading        bool               `json:"loading,omitempty"`
	LoadedModel    string             `json:"loadedModel"`
	Port           int                `json:"port"`
	VenvReady      bool               `json:"venvReady"`                // Python venv + mlx-lm package present
	PackageVersion string             `json:"packageVersion,omitempty"` // mlx-lm version string (installed)
	LatestVersion  string             `json:"latestVersion,omitempty"`  // latest version on PyPI
	LastError      string             `json:"lastError,omitempty"`
	IsAppleSilicon bool               `json:"isAppleSilicon"`          // hardware capability gate
	LastInference  *MLXInferenceStats `json:"lastInference,omitempty"` // stats from last completed turn
	Scheduler      MLXSchedulerStats  `json:"scheduler"`
}

// MLXModelInfo describes one MLX model directory stored in mlx-models/.
// Unlike llama.cpp where each model is a single .gguf file, MLX models
// are directories containing safetensors shards + config.json.
type MLXModelInfo struct {
	Name      string `json:"name"`      // directory name, e.g. "Llama-3.2-3B-Instruct-4bit"
	SizeBytes int64  `json:"sizeBytes"` // total bytes of all files in the directory
	SizeHuman string `json:"sizeHuman"`
}

// MLXDownloadProgress tracks the state of an in-progress mlx_lm model download.
// The input is a HuggingFace repo ID (e.g. "mlx-community/Llama-3.2-3B-Instruct-4bit")
// rather than a direct URL, so we store repo instead of url.
type MLXDownloadProgress struct {
	Active     bool    `json:"active"`
	Repo       string  `json:"repo"`       // HuggingFace repo ID
	ModelName  string  `json:"modelName"`  // destination directory name (last segment of repo)
	Downloaded int64   `json:"downloaded"` // bytes downloaded so far (best-effort; subprocess-based)
	Total      int64   `json:"total"`      // total bytes (-1 when unknown)
	Percent    float64 `json:"percent"`
	Error      string  `json:"error,omitempty"`
}
