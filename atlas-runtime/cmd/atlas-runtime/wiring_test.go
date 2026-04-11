package main

import (
	"context"
	"errors"
	"testing"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/comms"
)

type stubBridgeHandler struct {
	lastReq  chat.MessageRequest
	response chat.MessageResponse
	err      error
}

func (s *stubBridgeHandler) HandleMessage(_ context.Context, req chat.MessageRequest) (chat.MessageResponse, error) {
	s.lastReq = req
	return s.response, s.err
}

func TestNewBridgeChatHandler_MapsBridgeRequest(t *testing.T) {
	stub := &stubBridgeHandler{}
	stub.response.Conversation.ID = "conv-123"
	stub.response.Response.AssistantMessage = "done"

	handler := newBridgeChatHandler(stub)
	msg, _, convID, err := handler(context.Background(), comms.BridgeRequest{
		Text:     "hello",
		ConvID:   "conv-input",
		Platform: "telegram",
		Attachments: []comms.BridgeAttachment{
			{Filename: "clip.wav", MimeType: "audio/wav", Data: "abc123"},
		},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if msg != "done" || convID != "conv-123" {
		t.Fatalf("unexpected outputs msg=%q convID=%q", msg, convID)
	}
	if stub.lastReq.Message != "hello" || stub.lastReq.ConversationID != "conv-input" || stub.lastReq.Platform != "telegram" {
		t.Fatalf("request mapping mismatch: %+v", stub.lastReq)
	}
	if len(stub.lastReq.Attachments) != 1 || stub.lastReq.Attachments[0].Filename != "clip.wav" {
		t.Fatalf("attachment mapping mismatch: %+v", stub.lastReq.Attachments)
	}
}

func TestNewBridgeChatHandler_PropagatesErrors(t *testing.T) {
	stub := &stubBridgeHandler{err: errors.New("boom")}
	handler := newBridgeChatHandler(stub)

	if _, _, _, err := handler(context.Background(), comms.BridgeRequest{Text: "hello"}); err == nil {
		t.Fatal("expected error")
	}
}
