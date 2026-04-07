package discord

import "testing"

func TestClassifyIncomingMessage_DMAllowed(t *testing.T) {
	ev := messageCreateEvent{
		GuildID:  "",
		Author:   discordUser{ID: "u1", Bot: false},
		Content:  "hello",
		Mentions: nil,
		Type:     0,
	}
	text, isDM, ok := classifyIncomingMessage(ev, "bot-id")
	if !ok {
		t.Fatal("expected DM to be allowed")
	}
	if !isDM {
		t.Fatal("expected DM classification")
	}
	if text != "hello" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestClassifyIncomingMessage_GuildRequiresMention(t *testing.T) {
	ev := messageCreateEvent{
		GuildID: "g1",
		Author:  discordUser{ID: "u1", Bot: false},
		Content: "hello",
		Type:    0,
	}
	_, _, ok := classifyIncomingMessage(ev, "bot-id")
	if ok {
		t.Fatal("expected guild message without mention to be ignored")
	}
}

func TestClassifyIncomingMessage_GuildMentionStripsTag(t *testing.T) {
	ev := messageCreateEvent{
		GuildID: "g1",
		Author:  discordUser{ID: "u1", Bot: false},
		Content: "<@bot-id>  test ping",
		Mentions: []discordUser{
			{ID: "bot-id", Bot: true},
		},
		Type: 0,
	}
	text, isDM, ok := classifyIncomingMessage(ev, "bot-id")
	if !ok {
		t.Fatal("expected mentioned guild message to be allowed")
	}
	if isDM {
		t.Fatal("expected guild classification")
	}
	if text != "test ping" {
		t.Fatalf("unexpected stripped text: %q", text)
	}
}

