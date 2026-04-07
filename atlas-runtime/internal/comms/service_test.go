package comms

import (
	"testing"

	"atlas-runtime-go/internal/config"
)

func strPtr(v string) *string {
	return &v
}

func TestPlatformStatus_SlackRequiresBothTokens(t *testing.T) {
	svc := &Service{}
	cfg := config.RuntimeConfigSnapshot{SlackEnabled: true}

	onlyBot := credBundle{SlackBotToken: strPtr("xoxb-bot")}
	status := svc.platformStatus("slack", cfg, onlyBot, false, nil, nil)
	if status.CredentialConfigured {
		t.Fatalf("expected Slack credentials to be incomplete when app token is missing")
	}

	both := credBundle{SlackBotToken: strPtr("xoxb-bot"), SlackAppToken: strPtr("xapp-app")}
	status = svc.platformStatus("slack", cfg, both, false, nil, nil)
	if !status.CredentialConfigured {
		t.Fatalf("expected Slack credentials to be configured when bot + app tokens are present")
	}
}

func TestComputeHealthLabel(t *testing.T) {
	tests := []struct {
		name      string
		setup     string
		lastErr   *string
		wantLabel string
	}{
		{name: "ready", setup: "ready", wantLabel: "healthy"},
		{name: "partial", setup: "partial_setup", wantLabel: "degraded"},
		{name: "missing creds", setup: "missing_credentials", wantLabel: "idle"},
		{name: "not started", setup: "not_started", wantLabel: "idle"},
		{name: "error overrides", setup: "ready", lastErr: strPtr("boom"), wantLabel: "error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeHealthLabel(tc.setup, tc.lastErr)
			if got != tc.wantLabel {
				t.Fatalf("computeHealthLabel(%q) = %q, want %q", tc.setup, got, tc.wantLabel)
			}
		})
	}
}
