package platform

import (
	"context"

	"atlas-runtime-go/internal/chat"
)

// AgentRuntime is the minimal core contract exposed to internal modules that
// need to submit or resume agent work. It intentionally mirrors the current
// chat service shape without exposing the rest of the runtime internals.
type AgentRuntime interface {
	HandleMessage(ctx context.Context, req chat.MessageRequest) (chat.MessageResponse, error)
	Resume(toolCallID string, approved bool)
}

// ChatAgentRuntime adapts chat.Service to the AgentRuntime contract.
type ChatAgentRuntime struct {
	service *chat.Service
}

func NewChatAgentRuntime(service *chat.Service) *ChatAgentRuntime {
	return &ChatAgentRuntime{service: service}
}

func (r *ChatAgentRuntime) HandleMessage(ctx context.Context, req chat.MessageRequest) (chat.MessageResponse, error) {
	return r.service.HandleMessage(ctx, req)
}

func (r *ChatAgentRuntime) Resume(toolCallID string, approved bool) {
	r.service.Resume(toolCallID, approved)
}
