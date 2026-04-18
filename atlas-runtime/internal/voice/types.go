// Package voice manages the Whisper STT and Kokoro TTS subprocesses.
//
// A voice session owns both servers. They are started on demand when the
// user begins using voice features and torn down together when the session
// ends or idles out. This keeps zero voice-related RAM resident when voice
// is off.
package voice

// VoiceStatus is the snapshot returned by GET /voice/status.
type VoiceStatus struct {
	SessionActive  bool   `json:"sessionActive"`
	SessionID      string `json:"sessionID,omitempty"`
	SessionStarted int64  `json:"sessionStartedUnix,omitempty"`

	WhisperRunning  bool   `json:"whisperRunning"`
	WhisperReady    bool   `json:"whisperReady"`
	WhisperPort     int    `json:"whisperPort"`
	WhisperModel    string `json:"whisperModel,omitempty"`
	WhisperBuildTag string `json:"whisperBuildTag,omitempty"`

	KokoroRunning  bool   `json:"kokoroRunning"`
	KokoroReady    bool   `json:"kokoroReady"`
	KokoroPort     int    `json:"kokoroPort"`
	KokoroVersion  string `json:"kokoroVersion,omitempty"`

	LastError string `json:"lastError,omitempty"`
}

// TranscribeResult is the response body of POST /voice/transcribe.
type TranscribeResult struct {
	Text      string  `json:"text"`
	Language  string  `json:"language,omitempty"`
	Duration  float64 `json:"duration,omitempty"`
	SessionID string  `json:"sessionID,omitempty"`
}

// SynthesizeResult holds a fully-synthesized WAV blob (used by voice.synthesize
// skill which writes to disk).
type SynthesizeResult struct {
	WAV       []byte `json:"-"`
	SizeBytes int    `json:"sizeBytes"`
}

// SynthesizeChunk is one chunk of raw PCM audio emitted during streaming
// synthesis. PCM carries signed 16-bit little-endian mono samples at
// SampleRate. The browser stitches chunks into a continuous stream via the
// ring-buffer ScriptProcessor in voicePlayback.ts.
type SynthesizeChunk struct {
	Index      int    `json:"index"`
	PCM        []byte `json:"-"`
	SampleRate int    `json:"sampleRate"`
	Text       string `json:"text"`
	Final      bool   `json:"final"`
}

// VoiceModelInfo describes a model file on disk for the GET /voice/models/{component} route.
type VoiceModelInfo struct {
	Name      string `json:"name"`
	Component string `json:"component"` // "whisper" | "kokoro"
	SizeBytes int64  `json:"sizeBytes"`
	SizeHuman string `json:"sizeHuman"`
}

// DownloadProgress mirrors engine.DownloadProgress for voice model downloads.
type DownloadProgress struct {
	Active     bool    `json:"active"`
	Component  string  `json:"component"`
	Filename   string  `json:"filename"`
	URL        string  `json:"url,omitempty"`
	Downloaded int64   `json:"downloaded"`
	Total      int64   `json:"total"`
	Percent    float64 `json:"percent"`
}
