package chat

import (
	"testing"

	"atlas-runtime-go/internal/config"
)

func TestAutonomyToolPolicy_UnleashedDoesNotInjectTrustScope(t *testing.T) {
	cfg := config.RuntimeConfigSnapshot{AutonomyMode: config.AutonomyModeUnleashed}
	if policy := autonomyToolPolicy(cfg); policy != nil {
		t.Fatalf("expected no autonomy tool policy in unleashed mode, got %+v", policy)
	}
}

func TestAutonomyToolPolicy_SandboxedOnlyDeniesLockedSurfaces(t *testing.T) {
	cfg := config.RuntimeConfigSnapshot{AutonomyMode: config.AutonomyModeSandboxed}
	policy := autonomyToolPolicy(cfg)
	if policy == nil || !policy.Enabled {
		t.Fatalf("expected enabled sandbox policy, got %+v", policy)
	}
	if !policy.AllowsLiveWrite || !policy.AllowsSensitiveRead {
		t.Fatalf("expected sandbox autonomy policy to avoid hard-blocking normal tools, got %+v", policy)
	}
	if len(policy.DeniedToolPrefixes) == 0 {
		t.Fatalf("expected sandbox-denied tool prefixes, got %+v", policy)
	}
}
