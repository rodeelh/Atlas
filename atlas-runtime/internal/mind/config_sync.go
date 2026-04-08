package mind

// config_sync.go applies nap-related fields from RuntimeConfigSnapshot to
// the thoughts package variables. Separated from nap_scheduler.go so tests
// can exercise it without starting a real scheduler, and so the thoughts
// package itself never imports config.

import (
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/mind/thoughts"
)

func applyConfigToThoughtsImpl(cfg config.RuntimeConfigSnapshot) {
	if cfg.ThoughtAutoExecuteThreshold > 0 {
		thoughts.AutoExecuteThreshold = cfg.ThoughtAutoExecuteThreshold
	}
	if cfg.ThoughtProposeThreshold > 0 {
		thoughts.ProposeThreshold = cfg.ThoughtProposeThreshold
	}
	if cfg.ThoughtDiscardOnNegatives > 0 {
		thoughts.DiscardOnNegatives = cfg.ThoughtDiscardOnNegatives
	}
	if cfg.ThoughtDiscardOnIgnores > 0 {
		thoughts.DiscardOnIgnores = cfg.ThoughtDiscardOnIgnores
	}
}
