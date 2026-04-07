package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestIsAllowedSelfChat_AllowsOwnDirectThread(t *testing.T) {
	b := &Bridge{account: "16464257838"}
	ev := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   types.NewJID("16464257838", types.DefaultUserServer),
				Sender: types.NewJID("16464257838", types.DefaultUserServer),
			},
		},
	}
	if !b.isAllowedSelfChat(ev) {
		t.Fatal("expected own self-chat to be allowed")
	}
}

func TestIsAllowedSelfChat_RejectsOtherContact(t *testing.T) {
	b := &Bridge{account: "16464257838"}
	ev := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   types.NewJID("19998887777", types.DefaultUserServer),
				Sender: types.NewJID("19998887777", types.DefaultUserServer),
			},
		},
	}
	if b.isAllowedSelfChat(ev) {
		t.Fatal("expected other contact chat to be rejected")
	}
}

func TestIsAllowedSelfChat_RejectsGroup(t *testing.T) {
	b := &Bridge{account: "16464257838"}
	ev := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				IsGroup: true,
				Chat:    types.NewJID("12345-67890", types.GroupServer),
				Sender:  types.NewJID("16464257838", types.DefaultUserServer),
			},
		},
	}
	if b.isAllowedSelfChat(ev) {
		t.Fatal("expected group chat to be rejected")
	}
}

