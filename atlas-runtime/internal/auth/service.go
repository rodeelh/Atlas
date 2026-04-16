// Package auth implements the Atlas web authentication model:
// HMAC-SHA256 signed launch tokens + 7-day session cookies.
// The security model mirrors WebAuthService.swift exactly.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/storage"
)

// Sentinel errors returned by VerifyLaunchToken.
var (
	ErrInvalidToken = errors.New("invalid launch token")
	ErrExpiredToken = errors.New("launch token has expired")
	ErrAlreadyUsed  = errors.New("launch token has already been used")
)

const (
	SessionCookieName     = "atlas_session"
	tokenLifetime         = 60 * time.Second
	localSessionLifetime  = 8 * time.Hour      // local machine sessions (WebAuthn / PIN)
	sessionLifetime       = 7 * 24 * time.Hour // legacy — kept for compatibility
	remoteSessionLifetime = 24 * time.Hour     // remote LAN sessions
)

// Session is an active browser session.
type Session struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
	IsRemote  bool
}

// IsValid reports whether the session has not yet expired.
func (s *Session) IsValid() bool { return time.Now().Before(s.ExpiresAt) }

// Service implements token issuance and session lifecycle.
// It is safe for concurrent use.
type Service struct {
	mu         sync.Mutex
	signingKey []byte
	sessions   map[string]*Session
	usedNonces map[string]struct{}
	nonceOrder []string // insertion-order slice for FIFO pruning
	db         *storage.DB
	localAuth  *LocalAuthService // set via SetLocalAuth after construction
}

// SetLocalAuth wires the LocalAuthService so that HasLocalCredentials uses the
// atomic flag rather than a DB round-trip on every request.
func (s *Service) SetLocalAuth(l *LocalAuthService) {
	s.localAuth = l
}

// keychainService / keychainAccount identify the Keychain item that holds the
// persistent HMAC signing key. Using a dedicated item avoids touching the
// credential bundle and keeps the key isolated.
const (
	keychainService = "com.projectatlas.auth"
	keychainAccount = "signing-key"
)

// loadOrCreateSigningKey loads the HMAC signing key from the macOS Keychain.
// If no key exists yet it generates a fresh 32-byte key, persists it, and
// returns it. Falls back to an ephemeral key (with a warning) if Keychain
// access fails — this preserves startup availability on headless/CI builds.
//
// Security note (C-1): the key value is passed to `security add-generic-password`
// via the -w flag. On macOS this briefly appears in ps-visible process args.
// The window is sub-millisecond and only occurs once per install. A full fix
// requires the native Security.framework API via CGo — tracked as a future TODO.
func loadOrCreateSigningKey() []byte {
	// Try to load an existing key.
	out, err := exec.Command("security", "find-generic-password",
		"-s", keychainService, "-a", keychainAccount, "-w").Output()
	if err == nil {
		key, decErr := hex.DecodeString(strings.TrimSpace(string(out)))
		if decErr == nil && len(key) == 32 {
			return key
		}
	}

	// No key found (first run) or decode failed — generate and persist.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("auth: crypto/rand failure generating signing key: %v", err))
	}
	_, storeErr := exec.Command("security", "add-generic-password",
		"-U",
		"-s", keychainService,
		"-a", keychainAccount,
		"-w", hex.EncodeToString(key),
	).Output()
	if storeErr != nil {
		log.Printf("auth: warn: could not persist signing key to keychain — using ephemeral key")
	}
	return key
}

// NewService creates a Service. The HMAC signing key is loaded from (or
// created in) the macOS Keychain so it survives daemon restarts, keeping
// open browser tabs and pending launch tokens valid across restarts.
func NewService(db *storage.DB) *Service {
	return &Service{
		signingKey: loadOrCreateSigningKey(),
		sessions:   make(map[string]*Session),
		usedNonces: make(map[string]struct{}),
		nonceOrder: nil,
		db:         db,
	}
}

// IssueLaunchToken issues a short-lived signed launch token.
// Format: base64url(payloadJSON).base64url(HMAC-SHA256)
// This matches the Swift WebAuthService.issueLaunchToken() format exactly.
func (s *Service) IssueLaunchToken() string {
	type payload struct {
		Exp    float64 `json:"exp"`
		Nonce  string  `json:"nonce"`
		Source string  `json:"source"`
	}
	p := payload{
		Exp:    float64(time.Now().Add(tokenLifetime).UnixNano()) / 1e9,
		Nonce:  newUUID(),
		Source: "menubar",
	}
	payloadJSON, _ := json.Marshal(p)
	payloadB64 := b64url(payloadJSON)

	mac := hmac.New(sha256.New, s.signingKey)
	mac.Write([]byte(payloadB64))
	sig := mac.Sum(nil)

	return payloadB64 + "." + b64url(sig)
}

// VerifyLaunchToken verifies a raw launch token string.
// On success the nonce is consumed so it cannot be replayed.
func (s *Service) VerifyLaunchToken(raw string) error {
	parts := splitToken(raw)
	if len(parts) != 2 {
		return ErrInvalidToken
	}
	payloadB64, sigB64 := parts[0], parts[1]

	// Verify HMAC-SHA256 using constant-time comparison.
	sigBytes, err := b64urlDecode(sigB64)
	if err != nil {
		return ErrInvalidToken
	}
	mac := hmac.New(sha256.New, s.signingKey)
	mac.Write([]byte(payloadB64))
	expected := mac.Sum(nil)
	if !hmac.Equal(sigBytes, expected) {
		return ErrInvalidToken
	}

	// Decode payload.
	type payload struct {
		Exp   float64 `json:"exp"`
		Nonce string  `json:"nonce"`
	}
	payloadBytes, err := b64urlDecode(payloadB64)
	if err != nil {
		return ErrInvalidToken
	}
	var p payload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return ErrInvalidToken
	}

	// Check expiry.
	if float64(time.Now().UnixNano())/1e9 >= p.Exp {
		return ErrExpiredToken
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check one-time nonce.
	if _, used := s.usedNonces[p.Nonce]; used {
		return ErrAlreadyUsed
	}
	s.usedNonces[p.Nonce] = struct{}{}
	s.nonceOrder = append(s.nonceOrder, p.Nonce)
	s.pruneNonces()

	return nil
}

// CreateSession creates a new browser session and persists it to SQLite.
// Local sessions last 7 days; remote sessions last 24 hours.
func (s *Service) CreateSession(isRemote bool) *Session {
	now := time.Now()
	lifetime := sessionLifetime
	if isRemote {
		lifetime = remoteSessionLifetime
	}
	sess := &Session{
		ID:        randomHex(32),
		CreatedAt: now,
		ExpiresAt: now.Add(lifetime),
		IsRemote:  isRemote,
	}

	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.pruneExpiredSessions()
	s.mu.Unlock()

	go s.db.SaveWebSession(sess.ID, sess.CreatedAt, sess.ExpiresAt, isRemote)
	return sess
}

// CreateLocalSession creates an 8-hour local machine session (WebAuthn / PIN auth).
// Local sessions have IsRemote=false and a shorter lifetime than legacy sessions.
func (s *Service) CreateLocalSession() *Session {
	now := time.Now()
	sess := &Session{
		ID:        randomHex(32),
		CreatedAt: now,
		ExpiresAt: now.Add(localSessionLifetime),
		IsRemote:  false,
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.pruneExpiredSessions()
	s.mu.Unlock()
	go s.db.SaveWebSession(sess.ID, sess.CreatedAt, sess.ExpiresAt, false)
	return sess
}

// HasLocalCredentials returns true if at least one local auth credential (WebAuthn or PIN)
// has been registered. Used by the middleware to decide whether to enforce local auth.
// Uses the LocalAuthService atomic flag when available (no DB round-trip, no TOCTOU).
func (s *Service) HasLocalCredentials() bool {
	if s.localAuth != nil {
		return s.localAuth.HasCredentials()
	}
	return s.db.HasLocalCredentials()
}

// DeleteLocalSession immediately removes a local session, e.g. on explicit lock/logout.
func (s *Service) DeleteLocalSession(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
	go s.db.DeleteWebSession(id)
}

// ValidateLocalSession returns true if id is a valid, non-expired local session.
func (s *Service) ValidateLocalSession(id string) bool {
	if !s.ValidateSession(id) {
		return false
	}
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	return sess != nil && !sess.IsRemote
}

// ValidateSession returns true if the session ID is known and not expired.
// Consults the SQLite store on cache miss (after daemon restart).
func (s *Service) ValidateSession(id string) bool {
	if id == "" {
		return false
	}

	s.mu.Lock()
	if sess, ok := s.sessions[id]; ok {
		valid := sess.IsValid()
		if !valid {
			delete(s.sessions, id)
			go s.db.DeleteWebSession(id)
		}
		s.mu.Unlock()
		if valid {
			go s.db.RefreshWebSession(id)
		}
		return valid
	}
	s.mu.Unlock()

	// Cache miss — consult SQLite (happens once per restart per session).
	rec, err := s.db.FetchWebSession(id)
	if err != nil || rec == nil {
		return false
	}
	sess := &Session{
		ID:        rec.ID,
		CreatedAt: rec.CreatedAt,
		ExpiresAt: rec.ExpiresAt,
		IsRemote:  rec.IsRemote,
	}
	if !sess.IsValid() {
		return false
	}

	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	go s.db.RefreshWebSession(id)
	return true
}

// SessionDetail returns the full Session for id, or nil if invalid.
func (s *Service) SessionDetail(id string) *Session {
	if !s.ValidateSession(id) {
		return nil
	}
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	return sess
}

// InvalidateAllRemoteSessions removes all remote sessions (e.g. API key rotation).
func (s *Service) InvalidateAllRemoteSessions() {
	s.mu.Lock()
	for id, sess := range s.sessions {
		if sess.IsRemote {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
	go s.db.DeleteAllRemoteWebSessions()
}

// ValidateAPIKey performs a constant-time comparison of the presented key
// against the stored remote access API key.
func ValidateAPIKey(presented, stored string) bool {
	if presented == "" || stored == "" {
		return false
	}
	return hmac.Equal([]byte(presented), []byte(stored))
}

// SessionSetCookieValue returns the Set-Cookie header value for a session.
// Matches WebAuthService.sessionSetCookieValue(for:).
func SessionSetCookieValue(sess *Session) string {
	return sessionSetCookieValue(sess, false)
}

// SessionSetCookieValueForRequest returns the Set-Cookie header value for a
// session, enabling Secure when the request arrived over HTTPS directly or via
// a trusted TLS-terminating proxy that sets X-Forwarded-Proto=https.
func SessionSetCookieValueForRequest(sess *Session, r *http.Request) string {
	return sessionSetCookieValue(sess, IsSecureRequest(r))
}

func sessionSetCookieValue(sess *Session, secure bool) string {
	sameSite := "Strict"
	if sess.IsRemote {
		sameSite = "Lax"
	}
	maxAge := int(time.Until(sess.ExpiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	secureAttr := ""
	if secure {
		secureAttr = "; Secure"
	}
	return fmt.Sprintf(
		"%s=%s; HttpOnly; SameSite=%s; Path=/; Max-Age=%d%s",
		SessionCookieName, sess.ID, sameSite, maxAge, secureAttr,
	)
}

// CSRFToken returns the session-bound CSRF token.
// Token format: hex(HMAC_SHA256(signingKey, "csrf:"+sessionID)).
func (s *Service) CSRFToken(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	mac := hmac.New(sha256.New, s.signingKey)
	mac.Write([]byte("csrf:"))
	mac.Write([]byte(sessionID))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidateCSRF reports whether token matches the expected token for sessionID.
func (s *Service) ValidateCSRF(sessionID, token string) bool {
	if sessionID == "" || token == "" {
		return false
	}
	expected := s.CSRFToken(sessionID)
	return hmac.Equal([]byte(token), []byte(expected))
}

// SessionIDFromCookie extracts the atlas_session value from a Cookie header.
func SessionIDFromCookie(cookieHeader string) string {
	for len(cookieHeader) > 0 {
		var pair string
		pair, cookieHeader, _ = strings.Cut(cookieHeader, ";")
		pair = strings.TrimSpace(pair)
		k, v, _ := strings.Cut(pair, "=")
		if strings.TrimSpace(k) == SessionCookieName {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ── Private helpers ───────────────────────────────────────────────────────────

func (s *Service) pruneExpiredSessions() {
	for id, sess := range s.sessions {
		if !sess.IsValid() {
			delete(s.sessions, id)
		}
	}
}

// pruneNonces evicts the oldest 250 entries (FIFO via nonceOrder) when the
// set exceeds 500. Random map iteration was previously used, which could
// accidentally delete recently-issued nonces before they were redeemed.
func (s *Service) pruneNonces() {
	if len(s.usedNonces) <= 500 {
		return
	}
	evict := 250
	if evict > len(s.nonceOrder) {
		evict = len(s.nonceOrder)
	}
	for _, k := range s.nonceOrder[:evict] {
		delete(s.usedNonces, k)
	}
	s.nonceOrder = s.nonceOrder[evict:]
}

func b64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func b64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func splitToken(raw string) []string {
	// Split on last dot only (payload may contain dots via base64url padding)
	idx := -1
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] == '.' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	return []string{raw[:idx], raw[idx+1:]}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: crypto/rand failure: %v", err))
	}
	return hex.EncodeToString(b)
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: crypto/rand failure: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
