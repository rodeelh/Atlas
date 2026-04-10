package chat

import (
	"testing"
	"time"
)

func TestBroadcasterSupportsMultipleSubscribersPerConversation(t *testing.T) {
	bc := NewBroadcaster()

	subA, chA := bc.Register("conv-1")
	subB, chB := bc.Register("conv-1")

	if got := bc.ActiveCount(); got != 2 {
		t.Fatalf("ActiveCount() = %d, want 2", got)
	}
	if got := bc.ConversationSubscriberCount("conv-1"); got != 2 {
		t.Fatalf("ConversationSubscriberCount() = %d, want 2", got)
	}

	event := SSEEvent{Type: "assistant_delta", Content: "hello"}
	bc.Emit("conv-1", event)

	assertRecv := func(name string, ch <-chan SSEEvent) {
		t.Helper()
		select {
		case got := <-ch:
			if got.Type != event.Type || got.Content != event.Content {
				t.Fatalf("%s received %+v, want %+v", name, got, event)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", name)
		}
	}

	assertRecv("subscriber A", chA)
	assertRecv("subscriber B", chB)

	bc.Remove("conv-1", subA)
	if got := bc.ActiveCount(); got != 1 {
		t.Fatalf("ActiveCount() after Remove = %d, want 1", got)
	}

	bc.Finish("conv-1")

	select {
	case _, ok := <-chB:
		if ok {
			t.Fatal("subscriber B channel should be closed after Finish")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscriber B close")
	}

	// Removing an already-finished subscription should be a no-op.
	bc.Remove("conv-1", subB)
}
