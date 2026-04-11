// Package chat implements the SSE message streaming infrastructure.
package chat

import (
	"encoding/json"
	"fmt"
	"sync"

	"atlas-runtime-go/internal/logstore"
)

// SSEEvent is a single server-sent event.
// Field names match the Swift StreamBroadcaster event format exactly.
type SSEEvent struct {
	Type           string `json:"type"`
	Content        string `json:"content,omitempty"`
	Role           string `json:"role,omitempty"`
	ConversationID string `json:"conversationID,omitempty"`
	Error          string `json:"error,omitempty"`
	Status         string `json:"status,omitempty"`
	ToolName       string `json:"toolName,omitempty"`
	ApprovalID     string `json:"approvalID,omitempty"`
	ToolCallID     string `json:"toolCallID,omitempty"`
	Arguments      string `json:"arguments,omitempty"`

	// file_generated — emitted when a tool produces a local file artifact.
	// FileToken is a short-lived random token redeemable at GET /artifacts/{token}.
	Filename  string `json:"filename,omitempty"`
	MimeType  string `json:"mimeType,omitempty"`
	FileSize  int64  `json:"fileSize,omitempty"`
	FileToken string `json:"fileToken,omitempty"`

	// tool_finished — JSON-encoded tool artifacts for frontend rich rendering.
	Result string `json:"result,omitempty"`
}

// Encoded returns the event serialised as an SSE data line.
func (e SSEEvent) Encoded() []byte {
	b, _ := json.Marshal(e)
	return append([]byte("data: "), append(b, '\n', '\n')...)
}

// Broadcaster multiplexes SSE events to registered per-conversation channels.
// Safe for concurrent use.
type Broadcaster struct {
	mu      sync.Mutex
	streams map[string]map[string]chan SSEEvent
	nextID  uint64
}

// NewBroadcaster returns a ready Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{streams: make(map[string]map[string]chan SSEEvent)}
}

// Register creates a buffered channel for conversationID and returns a
// subscription id plus the channel.
// The caller must call Remove when the SSE connection closes.
func (b *Broadcaster) Register(convID string) (string, <-chan SSEEvent) {
	ch := make(chan SSEEvent, 256)
	b.mu.Lock()
	b.nextID++
	subID := fmt.Sprintf("sub-%d", b.nextID)
	if b.streams[convID] == nil {
		b.streams[convID] = make(map[string]chan SSEEvent)
	}
	b.streams[convID][subID] = ch
	b.mu.Unlock()
	return subID, ch
}

// Emit sends an event to all registered channels for convID.
// It is a no-op if no listeners are registered (e.g. clients disconnected early).
func (b *Broadcaster) Emit(convID string, event SSEEvent) {
	b.mu.Lock()
	listeners := b.streams[convID]
	type listener struct {
		subID string
		ch    chan SSEEvent
	}
	snapshot := make([]listener, 0, len(listeners))
	for subID, ch := range listeners {
		snapshot = append(snapshot, listener{subID: subID, ch: ch})
	}
	b.mu.Unlock()
	if len(snapshot) == 0 {
		return
	}

	for _, listener := range snapshot {
		select {
		case listener.ch <- event:
		default:
			// Channel full — drop rather than block. Log so the operator knows.
			logstore.Write("warn", "SSE channel full — event dropped",
				map[string]string{"conv": convID, "type": event.Type, "subscriber": listener.subID})
		}
	}
}

// Finish closes all channels for convID and removes them from the registry.
func (b *Broadcaster) Finish(convID string) {
	b.mu.Lock()
	listeners, ok := b.streams[convID]
	if len(listeners) > 0 {
		delete(b.streams, convID)
	}
	b.mu.Unlock()
	if ok {
		for _, ch := range listeners {
			close(ch)
		}
	}
}

// Remove removes the channel for convID without closing it.
// Use this when the SSE handler exits before Finish is called.
func (b *Broadcaster) Remove(convID, subID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	listeners := b.streams[convID]
	if len(listeners) == 0 {
		return
	}
	delete(listeners, subID)
	if len(listeners) == 0 {
		delete(b.streams, convID)
	}
}

// ActiveCount returns the total number of currently registered SSE listeners.
func (b *Broadcaster) ActiveCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, listeners := range b.streams {
		n += len(listeners)
	}
	return n
}

// ConversationSubscriberCount returns the active listener count for a conversation.
func (b *Broadcaster) ConversationSubscriberCount(convID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.streams[convID])
}
