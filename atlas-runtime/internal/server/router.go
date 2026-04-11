// Package server builds and configures the HTTP router.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"atlas-runtime-go/internal/auth"
	"atlas-runtime-go/internal/domain"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/runtime"
)

// BuildRouter constructs the chi router with CORS, auth middleware, and all
// registered domain handlers. The routing structure mirrors the Swift runtime's
// RuntimeHTTPHandler.route() auth-gate + handler-dispatch pattern.
func BuildRouter(
	authDomain *domain.AuthDomain,
	controlDomain *domain.ControlDomain,
	chatDomain *domain.ChatDomain,
	authSvc *auth.Service,
	runtimeSvc *runtime.Service,
	remoteEnabled func() bool,
	tailscaleEnabled func() bool,
	host *platform.RuntimeHost,
) http.Handler {
	r := chi.NewRouter()

	// Request logger (query strings redacted to avoid leaking auth tokens).
	r.Use(runtimeStatusMiddleware(runtimeSvc))
	r.Use(requestLogger)
	r.Use(chimw.Recoverer)

	// CORS — reflect the Origin back for trusted sources.
	// Using AllowedOrigins: ["*"] with AllowCredentials: true is spec-invalid
	// (browsers will reject it). Instead we use AllowOriginFunc to:
	//   • allow localhost origins only for localhost-hosted requests
	//   • require non-localhost origins to match the request host exactly
	//     (prevents credentialed cross-site reads from arbitrary origins)
	r.Use(cors.Handler(cors.Options{
		AllowOriginFunc: func(r *http.Request, origin string) bool {
			return auth.IsAllowedCORSOrigin(r, origin, remoteEnabled, tailscaleEnabled)
		},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Cookie", "X-Request-ID", "X-CSRF-Token"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// LAN gate — rejects non-localhost requests when remote access is disabled.
	// Tailscale connections bypass this gate when Tailscale is enabled.
	r.Use(auth.LanGate(remoteEnabled, tailscaleEnabled))

	// ── Auth-exempt routes ────────────────────────────────────────────────────
	// These must be registered BEFORE the RequireSession middleware group so
	// that the browser can reach /auth/bootstrap (token exchange) without a
	// session, and the menu bar app can reach /auth/token without one.
	authDomain.RegisterPublic(r)
	if host != nil {
		host.ApplyPublic(r)
	}

	// ── Session-protected routes ──────────────────────────────────────────────
	r.Group(func(protected chi.Router) {
		protected.Use(auth.RequireSession(authSvc, tailscaleEnabled))

		// Auth-required auth routes (/auth/remote-status, /auth/remote-key, DELETE /auth/remote-sessions).
		authDomain.Register(protected)

		// All other domain routes.
		controlDomain.Register(protected)
		chatDomain.Register(protected)
		if host != nil {
			host.ApplyProtected(protected)
		}
	})

	return r
}
