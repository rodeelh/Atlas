package config

import "strings"

const (
	AutonomyModeSandboxed = "sandboxed"
	AutonomyModeUnleashed = "unleashed"
)

func NormalizeAutonomyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", AutonomyModeSandboxed:
		return AutonomyModeSandboxed
	case AutonomyModeUnleashed:
		return AutonomyModeUnleashed
	default:
		return AutonomyModeSandboxed
	}
}

func (c RuntimeConfigSnapshot) EffectiveAutonomyMode() string {
	return NormalizeAutonomyMode(c.AutonomyMode)
}

func (c RuntimeConfigSnapshot) IsUnleashed() bool {
	return c.EffectiveAutonomyMode() == AutonomyModeUnleashed
}
