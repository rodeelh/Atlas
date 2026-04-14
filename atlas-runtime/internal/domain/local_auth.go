package domain

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/auth"
)

// clientIP extracts the best-effort client IP for rate limiting.
// Uses the auth package helper so the logic stays consistent with the rest of
// the auth stack (trusts X-Forwarded-For only when behind a trusted proxy).
func clientIP(r *http.Request) string {
	if ip := auth.ClientIP(r); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// LocalAuthDomain handles local machine authentication routes.
//
// All routes are auth-exempt (registered before RequireSession) because they
// ARE the authentication mechanism for local access.
//
//	GET  /auth/local/status                    — configured/authenticated state
//	POST /auth/local/webauthn/register/begin   — start WebAuthn registration
//	POST /auth/local/webauthn/register/finish  — complete WebAuthn registration
//	POST /auth/local/webauthn/authenticate/begin  — start WebAuthn login
//	POST /auth/local/webauthn/authenticate/finish — complete WebAuthn login
//	POST /auth/local/pin/setup                 — set or replace PIN
//	POST /auth/local/pin/verify                — authenticate with PIN
//	GET  /auth/local/credentials               — list registered credentials
//	DELETE /auth/local/credentials/{id}        — remove a credential
type LocalAuthDomain struct {
	authSvc   *auth.Service
	localAuth *auth.LocalAuthService
}

// NewLocalAuthDomain creates a LocalAuthDomain.
func NewLocalAuthDomain(authSvc *auth.Service, localAuth *auth.LocalAuthService) *LocalAuthDomain {
	return &LocalAuthDomain{authSvc: authSvc, localAuth: localAuth}
}

// RegisterPublic mounts all local auth routes on r (no session required).
// Most routes handle their own auth checks internally:
//   - Setup/auth routes are intentionally unauthenticated (they ARE authentication).
//   - Credential management routes (list, delete, PIN replace) require a valid
//     local session enforced in-handler via requireLocalSession.
func (d *LocalAuthDomain) RegisterPublic(r chi.Router) {
	r.Get("/auth/local/status", d.handleStatus)
	r.Post("/auth/local/webauthn/register/begin", d.handleWebAuthnRegisterBegin)
	r.Post("/auth/local/webauthn/register/finish", d.handleWebAuthnRegisterFinish)
	r.Post("/auth/local/webauthn/authenticate/begin", d.handleWebAuthnAuthBegin)
	r.Post("/auth/local/webauthn/authenticate/finish", d.handleWebAuthnAuthFinish)
	r.Post("/auth/local/pin/setup", d.handlePINSetup)
	r.Post("/auth/local/pin/verify", d.handlePINVerify)
	r.Get("/auth/local/credentials", d.handleListCredentials)
	r.Delete("/auth/local/credentials/{id}", d.handleDeleteCredential)
	r.Post("/auth/local/logout", d.handleLogout)
}

// requireLocalSession checks for a valid local session on the request.
// Returns true (and writes nothing) if authenticated.
// Returns false after writing a 401 response if not authenticated.
func (d *LocalAuthDomain) requireLocalSession(w http.ResponseWriter, r *http.Request) bool {
	sessionID := auth.SessionIDFromCookie(r.Header.Get("Cookie"))
	if !d.authSvc.ValidateLocalSession(sessionID) {
		writeError(w, http.StatusUnauthorized, "Local authentication required.")
		return false
	}
	return true
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// handleStatus returns the configuration and authentication state for the local
// machine. The web UI calls this on every load to decide whether to show the
// setup flow, the auth gate, or the main app.
func (d *LocalAuthDomain) handleStatus(w http.ResponseWriter, r *http.Request) {
	configured := d.localAuth.HasCredentials()
	sessionID := auth.SessionIDFromCookie(r.Header.Get("Cookie"))
	authenticated := d.authSvc.ValidateLocalSession(sessionID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configured":    configured,
		"authenticated": authenticated,
		"hasWebAuthn":   d.localAuth.HasWebAuthnCredentials(),
		"hasPIN":        d.localAuth.HasPINCredential(),
	})
}

// handleWebAuthnRegisterBegin starts a WebAuthn credential registration ceremony.
func (d *LocalAuthDomain) handleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	if req.Name == "" {
		req.Name = "Security Key"
	}

	optJSON, sessionID, err := d.localAuth.BeginRegistration(req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-WebAuthn-Session", sessionID)
	w.WriteHeader(http.StatusOK)
	w.Write(optJSON) //nolint:errcheck
}

// handleWebAuthnRegisterFinish completes a WebAuthn credential registration.
func (d *LocalAuthDomain) handleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-WebAuthn-Session")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "Missing X-WebAuthn-Session header")
		return
	}
	credName := r.Header.Get("X-Credential-Name")
	if credName == "" {
		credName = "Security Key"
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	if err := d.localAuth.FinishRegistration(sessionID, credName, body); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Issue a local session immediately — registration implies authentication.
	// Without this, the frontend calls onAuthenticated() but has no session
	// cookie, causing every subsequent API call to return 401 and reload-loop.
	sess := d.authSvc.CreateLocalSession()
	http.SetCookie(w, localSessionCookie(sess, r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
}

// handleWebAuthnAuthBegin starts a WebAuthn authentication ceremony.
func (d *LocalAuthDomain) handleWebAuthnAuthBegin(w http.ResponseWriter, r *http.Request) {
	optJSON, sessionID, err := d.localAuth.BeginAuthentication()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-WebAuthn-Session", sessionID)
	w.WriteHeader(http.StatusOK)
	w.Write(optJSON) //nolint:errcheck
}

// handleWebAuthnAuthFinish completes a WebAuthn authentication ceremony and
// issues a local session cookie on success.
func (d *LocalAuthDomain) handleWebAuthnAuthFinish(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-WebAuthn-Session")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "Missing X-WebAuthn-Session header")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	if err := d.localAuth.FinishAuthentication(sessionID, body); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	sess := d.authSvc.CreateLocalSession()
	http.SetCookie(w, localSessionCookie(sess, r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "authenticated"})
}

// handlePINSetup stores a new PIN (replacing any existing one).
// First-time setup (no credentials configured) is allowed unauthenticated —
// that is the setup flow. Replacing an existing PIN requires a valid local
// session to prevent an attacker from silently overwriting credentials.
func (d *LocalAuthDomain) handlePINSetup(w http.ResponseWriter, r *http.Request) {
	if d.localAuth.HasCredentials() {
		if !d.requireLocalSession(w, r) {
			return
		}
	}

	var req struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PIN == "" {
		writeError(w, http.StatusBadRequest, "Missing or invalid PIN")
		return
	}

	if err := d.localAuth.SetupPIN(req.PIN); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Issue a local session immediately so the user is authenticated after setup.
	sess := d.authSvc.CreateLocalSession()
	http.SetCookie(w, localSessionCookie(sess, r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "pin_set"})
}

// handlePINVerify authenticates with a PIN and issues a local session cookie.
func (d *LocalAuthDomain) handlePINVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PIN == "" {
		writeError(w, http.StatusBadRequest, "Missing PIN")
		return
	}

	ok, err := d.localAuth.VerifyPIN(clientIP(r), req.PIN)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	sess := d.authSvc.CreateLocalSession()
	http.SetCookie(w, localSessionCookie(sess, r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "authenticated"})
}

// handleListCredentials returns all registered local credentials.
// Requires a valid local session — credential metadata is sensitive.
func (d *LocalAuthDomain) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	if !d.requireLocalSession(w, r) {
		return
	}
	creds, err := d.localAuth.ListCredentials()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if creds == nil {
		creds = []auth.CredentialInfo{}
	}
	writeJSON(w, http.StatusOK, creds)
}

// handleDeleteCredential removes a credential by ID.
// Requires a valid local session to prevent an attacker from deleting all
// credentials and triggering the unconfigured pass-through in the middleware.
func (d *LocalAuthDomain) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if !d.requireLocalSession(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Missing credential ID")
		return
	}
	if err := d.localAuth.DeleteCredential(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleLogout ends the current local session immediately and expires the cookie.
// The client reloads to the auth gate — no auth required (the session is cleared here).
func (d *LocalAuthDomain) handleLogout(w http.ResponseWriter, r *http.Request) {
	sessionID := auth.SessionIDFromCookie(r.Header.Get("Cookie"))
	if sessionID != "" {
		d.authSvc.DeleteLocalSession(sessionID)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   auth.IsSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

// localSessionCookie builds an HttpOnly session cookie for a local session.
// SameSite=Strict is safe for local access (same origin only).
// Secure is set when the request was made over HTTPS so the cookie is
// marked HTTPS-only on HTTPS deployments.
func localSessionCookie(sess *auth.Session, r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   auth.IsSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sess.ExpiresAt.Sub(sess.CreatedAt).Seconds()),
	}
}
