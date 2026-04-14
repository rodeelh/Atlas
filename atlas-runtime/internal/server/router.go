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
	localAuthDomain *domain.LocalAuthDomain,
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
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders: []string{
			"Accept", "Authorization", "Content-Type", "Cookie",
			"X-Request-ID", "X-CSRF-Token",
			// WebAuthn session token — passed as a request header on the finish
			// call and returned as a response header from the begin call.
			// Must be in both AllowedHeaders and ExposedHeaders so cross-origin
			// callers (e.g. Vite dev server on a different port) can send and
			// read it.
			"X-WebAuthn-Session",
		},
		// ExposedHeaders lists response headers the browser JS may read in a
		// cross-origin context. By default only "simple" headers are accessible.
		ExposedHeaders:   []string{"X-WebAuthn-Session"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// LAN gate — rejects non-localhost requests when remote access is disabled.
	// Tailscale connections bypass this gate when Tailscale is enabled.
	r.Use(auth.LanGate(remoteEnabled, tailscaleEnabled))

	// ── Auth-exempt routes ────────────────────────────────────────────────────
	// These must be registered BEFORE the RequireSession middleware group so
	// that the browser can reach the auth gate and setup flow without a session.
	authDomain.RegisterPublic(r)
	localAuthDomain.RegisterPublic(r)
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
