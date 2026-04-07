package chat

import "atlas-runtime-go/internal/logstore"

// Slot terminology mapping:
//   - Primary model (UI) => primary slot (ResolveProvider)
//   - Fast model (UI) => fast slot (ResolveFastProvider)
//   - Router slot (internal) => ResolveBackgroundProvider
//
// Per-turn fallback semantics:
//   - Sticky only for the current HandleMessage call.
//   - A single router failure marks the turn as fallback-only.
//   - Exactly one warn log is emitted per turn fallback event.
type turnContext struct {
	routerFallbackSticky bool
	routerFallbackLogged bool
}

func (t *turnContext) markRouterFallback(reason string) {
	t.routerFallbackSticky = true
	if t.routerFallbackLogged {
		return
	}
	t.routerFallbackLogged = true
	meta := map[string]string{"reason": reason}
	logstore.Write("warn", "router fallback: engine offline, using cloud fast model", meta)
}
