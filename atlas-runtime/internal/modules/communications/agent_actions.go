package communications

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"atlas-runtime-go/internal/comms"
	"atlas-runtime-go/internal/skills"
)

type sendMessageArgs struct {
	DestinationID string `json:"destinationID"`
	Platform      string `json:"platform"`
	ChannelID     string `json:"channelID"`
	ThreadID      string `json:"threadID"`
	Message       string `json:"message"`
}

func (m *Module) registerAgentActions() {
	if m.skills == nil {
		return
	}
	m.skills.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "communication.list_channels",
			Description: "List authorized chat bridge channels Atlas can use to reach the user.",
			Properties: map[string]skills.ToolParam{
				"platform": {Type: "string", Description: "Optional platform filter, such as telegram, whatsapp, slack, or discord."},
			},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult:    m.agentListChannels,
	})
	m.skills.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "communication.send_message",
			Description: "Send a message to the user through an existing authorized chat bridge channel. Use communication.list_channels first unless the destination is already known.",
			Properties: map[string]skills.ToolParam{
				"destinationID": {Type: "string", Description: "Preferred channel ID from communication.list_channels, for example telegram:123: or whatsapp:me@server:."},
				"platform":      {Type: "string", Description: "Platform when destinationID is not provided: telegram, whatsapp, slack, or discord."},
				"channelID":     {Type: "string", Description: "Channel/chat ID when destinationID is not provided."},
				"threadID":      {Type: "string", Description: "Optional thread ID for Slack or Discord destinations."},
				"message":       {Type: "string", Description: "Message text to send to the user."},
			},
			Required: []string{"message"},
		},
		// This is intentionally auto-approved: it can only target channels the
		// user has already authorized by chatting with Atlas through a bridge.
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.agentSendMessage,
	})
}

func (m *Module) agentListChannels(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var params struct {
		Platform string `json:"platform"`
	}
	_ = json.Unmarshal(args, &params)
	platform := strings.ToLower(strings.TrimSpace(params.Platform))

	channels := m.service.Channels()
	out := make([]map[string]any, 0, len(channels))
	for _, ch := range channels {
		if platform != "" && strings.ToLower(ch.Platform) != platform {
			continue
		}
		record := map[string]any{
			"id":                      ch.ID,
			"platform":                ch.Platform,
			"channelID":               ch.ChannelID,
			"activeConversationID":    ch.ActiveConversationID,
			"updatedAt":               ch.UpdatedAt,
			"canReceiveNotifications": ch.CanReceiveNotifications,
		}
		if ch.ChannelName != nil {
			record["channelName"] = *ch.ChannelName
		}
		if ch.UserID != nil {
			record["userID"] = *ch.UserID
		}
		if ch.ThreadID != nil {
			record["threadID"] = *ch.ThreadID
		}
		out = append(out, record)
	}

	summary := fmt.Sprintf("Found %d authorized communication channel(s).", len(out))
	if len(out) == 0 {
		summary = "No authorized communication channels are available yet. Ask the user to message Atlas through Telegram, WhatsApp, Slack, or Discord first."
	}
	return skills.OKResult(summary, map[string]any{"channels": out}), nil
}

func (m *Module) agentSendMessage(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var params sendMessageArgs
	if err := json.Unmarshal(args, &params); err != nil {
		return skills.ErrResult("send bridge message", "argument parsing", false, err), nil
	}
	message := strings.TrimSpace(params.Message)
	if message == "" {
		return skills.ErrResult("send bridge message", "argument validation", false, fmt.Errorf("message is required")), nil
	}

	dest, label, err := m.resolveDestination(params)
	if err != nil {
		return skills.ErrResult("send bridge message", "destination validation", false, err), nil
	}
	if err := m.service.SendAutomationResult(ctx, dest, message); err != nil {
		return skills.ErrResult("send bridge message to "+label, "bridge delivery", false, err), nil
	}
	return skills.OKResult("Sent message to "+label+".", map[string]any{
		"platform":  dest.Platform,
		"channelID": dest.ChannelID,
		"threadID":  dest.ThreadID,
	}), nil
}

func (m *Module) resolveDestination(args sendMessageArgs) (comms.AutomationDestination, string, error) {
	channels := m.service.Channels()
	targetID := strings.TrimSpace(args.DestinationID)
	platform := strings.ToLower(strings.TrimSpace(args.Platform))
	channelID := strings.TrimSpace(args.ChannelID)
	threadID := strings.TrimSpace(args.ThreadID)

	for _, ch := range channels {
		chThread := ""
		if ch.ThreadID != nil {
			chThread = *ch.ThreadID
		}
		idMatches := targetID != "" && ch.ID == targetID
		tupleMatches := targetID == "" &&
			strings.ToLower(ch.Platform) == platform &&
			ch.ChannelID == channelID &&
			chThread == threadID
		if !idMatches && !tupleMatches {
			continue
		}
		label := ch.Platform + ":" + ch.ChannelID
		if ch.ChannelName != nil && strings.TrimSpace(*ch.ChannelName) != "" {
			label = ch.Platform + ":" + strings.TrimSpace(*ch.ChannelName)
		}
		return comms.AutomationDestination{
			Platform:  ch.Platform,
			ChannelID: ch.ChannelID,
			ThreadID:  chThread,
		}, label, nil
	}

	if targetID != "" {
		return comms.AutomationDestination{}, "", fmt.Errorf("destination %q is not an authorized communication channel", targetID)
	}
	return comms.AutomationDestination{}, "", fmt.Errorf("destination %s:%s is not an authorized communication channel", platform, channelID)
}
