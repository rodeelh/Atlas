package chat

import (
	"sync"
	"testing"
	"time"
)

func TestDetectSurfacedThoughts(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "empty",
			content: "",
			want:    nil,
		},
		{
			name:    "no marker",
			content: "Just a clean reply with nothing special going on.",
			want:    nil,
		},
		{
			name:    "single trailing marker",
			content: "By the way, I noticed the openclaw release rhythm — want notes? [T-01]",
			want:    []string{"T-01"},
		},
		{
			name:    "two distinct ids in one reply",
			content: "First observation [T-01] and something else at the end [T-02]",
			want:    []string{"T-01", "T-02"},
		},
		{
			name:    "duplicate id counts once",
			content: "Hey [T-01] and then [T-01] again",
			want:    []string{"T-01"},
		},
		{
			name:    "larger id numbers",
			content: "I checked this earlier [T-100]",
			want:    []string{"T-100"},
		},
		{
			name:    "marker embedded in prose is still caught",
			content: "thinking about [T-01] and more",
			want:    []string{"T-01"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectSurfacedThoughts(tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// stubSurfacingRecorder captures every Record call for assertion.
type stubSurfacingRecorder struct {
	mu    sync.Mutex
	calls []stubSurfacingCall
	err   error
}

type stubSurfacingCall struct {
	ConvID     string
	MessageID  string
	ThoughtID  string
	SurfacedAt time.Time
}

func (s *stubSurfacingRecorder) Record(convID, messageID, thoughtID string, surfacedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, stubSurfacingCall{
		ConvID: convID, MessageID: messageID, ThoughtID: thoughtID, SurfacedAt: surfacedAt,
	})
	return s.err
}

func TestDetectAndRecordSurfacings_WritesPendingRows(t *testing.T) {
	svc := &Service{}
	stub := &stubSurfacingRecorder{}
	svc.SetSurfacingRecorder(stub)

	svc.detectAndRecordSurfacings(
		"conv-1",
		"msg-1",
		"I spotted the openclaw release rhythm — want notes? [T-01]",
		time.Now().UTC(),
	)

	if len(stub.calls) != 1 {
		t.Fatalf("calls: got %d, want 1", len(stub.calls))
	}
	if stub.calls[0].ThoughtID != "T-01" {
		t.Errorf("thought id: got %q", stub.calls[0].ThoughtID)
	}
	if stub.calls[0].ConvID != "conv-1" {
		t.Errorf("conv id: got %q", stub.calls[0].ConvID)
	}
	if stub.calls[0].MessageID != "msg-1" {
		t.Errorf("message id: got %q", stub.calls[0].MessageID)
	}
}

func TestDetectAndRecordSurfacings_NoMarkers_NoCalls(t *testing.T) {
	svc := &Service{}
	stub := &stubSurfacingRecorder{}
	svc.SetSurfacingRecorder(stub)

	svc.detectAndRecordSurfacings("conv-1", "msg-1", "clean reply", time.Now().UTC())

	if len(stub.calls) != 0 {
		t.Errorf("expected 0 calls, got %d", len(stub.calls))
	}
}

func TestDetectAndRecordSurfacings_MultipleDistinctIDs(t *testing.T) {
	svc := &Service{}
	stub := &stubSurfacingRecorder{}
	svc.SetSurfacingRecorder(stub)

	svc.detectAndRecordSurfacings(
		"conv-1", "msg-1",
		"First thing. [T-01] Second thing. [T-02]",
		time.Now().UTC(),
	)

	if len(stub.calls) != 2 {
		t.Fatalf("calls: got %d, want 2", len(stub.calls))
	}
	ids := map[string]bool{}
	for _, c := range stub.calls {
		ids[c.ThoughtID] = true
	}
	if !ids["T-01"] || !ids["T-02"] {
		t.Errorf("missing ids: %+v", ids)
	}
}

func TestDetectAndRecordSurfacings_EmptyContent_NoCalls(t *testing.T) {
	svc := &Service{}
	stub := &stubSurfacingRecorder{}
	svc.SetSurfacingRecorder(stub)

	svc.detectAndRecordSurfacings("conv-1", "msg-1", "", time.Now().UTC())

	if len(stub.calls) != 0 {
		t.Errorf("expected 0 calls for empty content, got %d", len(stub.calls))
	}
}
