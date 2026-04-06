package main

import (
	"context"
	"fmt"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/comms"
)

type bridgeMessageHandler interface {
	HandleMessage(ctx context.Context, req chat.MessageRequest) (chat.MessageResponse, error)
}

func newBridgeChatHandler(handler bridgeMessageHandler) comms.ChatHandler {
	return func(ctx context.Context, req comms.BridgeRequest) (string, string, error) {
		chatAttachments := make([]chat.MessageAttachment, len(req.Attachments))
		for i, a := range req.Attachments {
			chatAttachments[i] = chat.MessageAttachment{Filename: a.Filename, MimeType: a.MimeType, Data: a.Data}
		}
		resp, err := handler.HandleMessage(ctx, chat.MessageRequest{
			Message:        req.Text,
			ConversationID: req.ConvID,
			Platform:       req.Platform,
			Attachments:    chatAttachments,
		})
		if err != nil {
			return "", "", err
		}
		if resp.Response.ErrorMessage != "" {
			return "", "", fmt.Errorf("%s", resp.Response.ErrorMessage)
		}
		return resp.Response.AssistantMessage, resp.Conversation.ID, nil
	}
}
