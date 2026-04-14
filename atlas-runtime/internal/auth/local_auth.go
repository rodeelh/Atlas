// Package auth — local authentication service.
// Implements a tiered local auth gate:
//
//	Tier 1: WebAuthn platform authenticator (Touch ID / Windows Hello)
//	Tier 2: WebAuthn roaming authenticator  (FIDO2 hardware key)
//	Tier 3: PIN / passphrase fallback        (bcrypt, for Linux / no authenticator)
//
// All tiers produce the same local session cookie via Service.CreateLocalSession.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/crypto/bcrypt"

	"atlas-runtime-go/internal/storage"
)

// ceremonyTTL is how long a pending WebAuthn ceremony session lives in memory.
const ceremonyTTL = 5 * time.Minute

// PIN brute-force protection constants.
const (
	pinMaxAttempts  = 5               // max failed attempts before lockout
	pinLockDuration = 15 * time.Minute // lockout duration after exceeding limit
	pinBcryptCost   = 12              // bcrypt cost factor (NIST SP 800-63B recommends ≥12)
)

// LocalAuthService manages WebAuthn and PIN credentials for local access.
// It is safe for concurrent use.
type LocalAuthService struct {
	mu         sync.Mutex
	wauth      *webauthn.WebAuthn
	db         *storage.DB
	ceremonies map[string]*ceremonyEntry // pending WebAuthn ceremony sessions

	// activeRegCeremony and activeAuthCeremony hold the current in-flight ceremony
	// session ID for each type. Starting a new ceremony invalidates the prior one,
	// preventing concurrent ceremony accumulation.
	activeRegCeremony  string
	activeAuthCeremony string

	// hasCredsFlag is an atomic bool that mirrors whether any local credential
	// exists. Updated on every write/delete so the middleware hot-path never
	// needs a DB round-trip (eliminates the TOCTOU window).
	hasCredsFlag atomic.Bool

	// pinAttempts tracks failed PIN attempts per client IP for rate limiting.
	pinAttempts map[string]*pinAttemptEntry
}

// ceremonyEntry holds the pending WebAuthn session data for a begin→finish round-trip.
type ceremonyEntry struct {
	data      webauthn.SessionData
	expiresAt time.Time
}

// pinAttemptEntry records failed PIN attempts for a given client IP.
type pinAttemptEntry struct {
	count       int
	lockedUntil time.Time
}

// CredentialInfo is the public view of a stored local credential.
type CredentialInfo struct {
	ID         string `json:"id"`
	Type       string `json:"type"` // "webauthn" | "pin"
	Name       string `json:"name"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt"`
}

// NewLocalAuthService creates a LocalAuthService for the given runtime port.
// Origins are validated dynamically at ceremony-finish time (see wauthForOrigin),
// so the port argument is retained only for API compatibility and is no longer
// used to build a static allowlist.
func NewLocalAuthService(db *storage.DB, _ int) (*LocalAuthService, error) {
	// s.wauth is used only for Begin* ceremony generation (challenge creation).
	// Origin validation happens in FinishRegistration / FinishAuthentication via
	// wauthForOrigin, so the origin listed here is never checked against real
	// requests — it just needs to satisfy the library's config validator.
	wauth, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Atlas",
		RPID:          "localhost",
		RPOrigins:     []string{"http://localhost"},
	})
	if err != nil {
		return nil, fmt.Errorf("local auth: webauthn init: %w", err)
	}

	svc := &LocalAuthService{
		wauth:       wauth,
		db:          db,
		ceremonies:  make(map[string]*ceremonyEntry),
		pinAttempts: make(map[string]*pinAttemptEntry),
	}

	// Initialise the atomic flag from the DB so the middleware hot-path is
	// accurate from the first request after a daemon restart.
	svc.hasCredsFlag.Store(db.HasLocalCredentials())

	return svc, nil
}

// HasCredentials returns true if at least one local credential is registered.
// Uses an atomic flag — no DB round-trip on the hot path.
func (s *LocalAuthService) HasCredentials() bool {
	return s.hasCredsFlag.Load()
}

// HasWebAuthnCredentials returns true if any WebAuthn credential is registered.
func (s *LocalAuthService) HasWebAuthnCredentials() bool {
	rows, err := s.db.LoadLocalCredentials()
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.Type == "webauthn" {
			return true
		}
	}
	return false
}

// HasPINCredential returns true if a PIN credential is registered.
func (s *LocalAuthService) HasPINCredential() bool {
	rows, err := s.db.LoadLocalCredentials()
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.Type == "pin" {
			return true
		}
	}
	return false
}

// ListCredentials returns public info about all registered credentials.
func (s *LocalAuthService) ListCredentials() ([]CredentialInfo, error) {
	rows, err := s.db.LoadLocalCredentials()
	if err != nil {
		return nil, err
	}
	out := make([]CredentialInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, CredentialInfo{
			ID:         r.ID,
			Type:       r.Type,
			Name:       r.Name,
			CreatedAt:  r.CreatedAt,
			LastUsedAt: r.LastUsedAt,
		})
	}
	return out, nil
}

// DeleteCredential removes a credential by ID and refreshes the atomic flag.
func (s *LocalAuthService) DeleteCredential(id string) error {
	if err := s.db.DeleteLocalCredential(id); err != nil {
		return err
	}
	// Refresh atomic flag — it may now be false if this was the last credential.
	s.hasCredsFlag.Store(s.db.HasLocalCredentials())
	return nil
}

// ── WebAuthn ─────────────────────────────────────────────────────────────────

// BeginRegistration starts a WebAuthn credential registration ceremony.
// Returns the CredentialCreation options JSON and a ceremony session ID to
// pass back when calling FinishRegistration.
//
// Any prior in-flight registration ceremony is invalidated so only one is
// active at a time, preventing ceremony accumulation.
func (s *LocalAuthService) BeginRegistration(_ string) ([]byte, string, error) {
	// Build user with existing credentials so the authenticator can populate
	// excludeCredentials, preventing re-registration of the same device.
	user, err := s.loadWebAuthnUser()
	if err != nil {
		return nil, "", err
	}

	// Collect existing credential descriptors to pass as excludeCredentials.
	var excludeList []protocol.CredentialDescriptor
	for _, c := range user.creds {
		excludeList = append(excludeList, protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: c.ID,
		})
	}

	opts := []webauthn.RegistrationOption{
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			// No attachment restriction — browser picks best available:
			// Touch ID on macOS, Windows Hello on Windows, key/PIN on Linux.
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	}
	if len(excludeList) > 0 {
		opts = append(opts, webauthn.WithExclusions(excludeList))
	}

	options, sessionData, err := s.wauth.BeginRegistration(user, opts...)
	if err != nil {
		return nil, "", fmt.Errorf("begin registration: %w", err)
	}

	sessionID := newLocalID()
	s.mu.Lock()
	// Invalidate any prior in-flight registration ceremony.
	if s.activeRegCeremony != "" {
		delete(s.ceremonies, s.activeRegCeremony)
	}
	s.evictExpiredCeremonies()
	s.ceremonies[sessionID] = &ceremonyEntry{
		data:      *sessionData,
		expiresAt: time.Now().Add(ceremonyTTL),
	}
	s.activeRegCeremony = sessionID
	s.mu.Unlock()

	optJSON, err := json.Marshal(options)
	if err != nil {
		return nil, "", fmt.Errorf("marshal registration options: %w", err)
	}
	return optJSON, sessionID, nil
}

// FinishRegistration completes a WebAuthn registration ceremony.
// body is the raw JSON bytes of the browser's PublicKeyCredential response.
func (s *LocalAuthService) FinishRegistration(sessionID, credName string, body []byte) error {
	sessionData, ok := s.popCeremony(sessionID, &s.activeRegCeremony)
	if !ok {
		return fmt.Errorf("ceremony session not found or expired")
	}

	user := &localUser{id: []byte("atlas-local"), name: "atlas", displayName: "Atlas"}

	parsedResponse, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return fmt.Errorf("parse credential creation response: %w", err)
	}

	// Build a ceremony-scoped webauthn instance from the origin the browser
	// actually used. This makes registration work on any localhost port without
	// a static allowlist — port changes and dev servers are handled transparently.
	origin, err := originFromWebAuthnBody(body)
	if err != nil {
		return fmt.Errorf("extract origin: %w", err)
	}
	if !isLocalhostWebAuthnOrigin(origin) {
		return fmt.Errorf("WebAuthn registration rejected: non-localhost origin %q", origin)
	}
	wa, err := wauthForOrigin(origin)
	if err != nil {
		return fmt.Errorf("webauthn init for origin: %w", err)
	}

	cred, err := wa.CreateCredential(user, sessionData, parsedResponse)
	if err != nil {
		return fmt.Errorf("create credential: %w", err)
	}

	credJSON, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("marshal credential: %w", err)
	}

	name := credName
	if name == "" {
		name = "Security Key"
	}
	id := hex.EncodeToString(cred.ID)
	if err := s.db.SaveLocalCredential(id, "webauthn", name, string(credJSON), ""); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}

	// Credential is committed — flip the atomic flag immediately so subsequent
	// middleware checks see the credential without a DB round-trip.
	s.hasCredsFlag.Store(true)
	return nil
}

// BeginAuthentication starts a WebAuthn authentication ceremony.
// Returns the CredentialAssertion options JSON and a ceremony session ID.
//
// Any prior in-flight authentication ceremony is invalidated so only one is
// active at a time.
func (s *LocalAuthService) BeginAuthentication() ([]byte, string, error) {
	user, err := s.loadWebAuthnUser()
	if err != nil {
		return nil, "", err
	}
	if len(user.creds) == 0 {
		return nil, "", fmt.Errorf("no WebAuthn credentials registered")
	}

	options, sessionData, err := s.wauth.BeginLogin(user,
		webauthn.WithUserVerification(protocol.VerificationPreferred),
	)
	if err != nil {
		return nil, "", fmt.Errorf("begin authentication: %w", err)
	}

	sessionID := newLocalID()
	s.mu.Lock()
	// Invalidate any prior in-flight authentication ceremony.
	if s.activeAuthCeremony != "" {
		delete(s.ceremonies, s.activeAuthCeremony)
	}
	s.evictExpiredCeremonies()
	s.ceremonies[sessionID] = &ceremonyEntry{
		data:      *sessionData,
		expiresAt: time.Now().Add(ceremonyTTL),
	}
	s.activeAuthCeremony = sessionID
	s.mu.Unlock()

	optJSON, err := json.Marshal(options)
	if err != nil {
		return nil, "", fmt.Errorf("marshal authentication options: %w", err)
	}
	return optJSON, sessionID, nil
}

// FinishAuthentication completes a WebAuthn authentication ceremony.
// body is the raw JSON bytes of the browser's PublicKeyCredential response.
func (s *LocalAuthService) FinishAuthentication(sessionID string, body []byte) error {
	sessionData, ok := s.popCeremony(sessionID, &s.activeAuthCeremony)
	if !ok {
		return fmt.Errorf("ceremony session not found or expired")
	}

	user, err := s.loadWebAuthnUser()
	if err != nil {
		return err
	}

	parsedResponse, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return fmt.Errorf("parse credential request response: %w", err)
	}

	// Build a ceremony-scoped webauthn instance from the origin the browser
	// actually used. This makes authentication work regardless of which port
	// the daemon or dev server is running on — no static allowlist needed.
	origin, err := originFromWebAuthnBody(body)
	if err != nil {
		return fmt.Errorf("extract origin: %w", err)
	}
	if !isLocalhostWebAuthnOrigin(origin) {
		return fmt.Errorf("WebAuthn authentication rejected: non-localhost origin %q", origin)
	}
	wa, err := wauthForOrigin(origin)
	if err != nil {
		return fmt.Errorf("webauthn init for origin: %w", err)
	}

	updatedCred, err := wa.ValidateLogin(user, sessionData, parsedResponse)
	if err != nil {
		return fmt.Errorf("validate login: %w", err)
	}

	// Persist updated sign count. Log errors — a stale sign count disables
	// credential-clone detection.
	if credJSON, merr := json.Marshal(updatedCred); merr == nil {
		if uerr := s.db.UpdateLocalCredentialSignCount(hex.EncodeToString(updatedCred.ID), string(credJSON)); uerr != nil {
			log.Printf("local auth: failed to update sign count for credential %s: %v", hex.EncodeToString(updatedCred.ID), uerr)
		}
	}
	return nil
}

// ── PIN ───────────────────────────────────────────────────────────────────────

// SetupPIN hashes and stores a new PIN, replacing any existing PIN credential.
// The PIN must be at least 6 characters.
func (s *LocalAuthService) SetupPIN(pin string) error {
	if len(pin) < 6 {
		return fmt.Errorf("PIN must be at least 6 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), pinBcryptCost)
	if err != nil {
		return fmt.Errorf("hash PIN: %w", err)
	}

	// Remove any existing PIN credential (one PIN at a time).
	if rows, _ := s.db.LoadLocalCredentials(); rows != nil {
		for _, r := range rows {
			if r.Type == "pin" {
				_ = s.db.DeleteLocalCredential(r.ID)
			}
		}
	}

	if err := s.db.SaveLocalCredential(newLocalID(), "pin", "PIN", "", string(hash)); err != nil {
		return err
	}

	// Credential is committed — flip the atomic flag immediately.
	s.hasCredsFlag.Store(true)
	return nil
}

// VerifyPIN checks the presented PIN against the stored hash.
// clientIP is used to enforce per-IP rate limiting.
// Returns (true, nil) on success, (false, nil) on wrong PIN,
// (false, non-nil) when the IP is rate-limited.
func (s *LocalAuthService) VerifyPIN(clientIP, pin string) (bool, error) {
	// Rate-limit check under the mutex.
	s.mu.Lock()
	entry := s.pinAttempts[clientIP]
	now := time.Now()
	if entry != nil && now.Before(entry.lockedUntil) {
		remaining := entry.lockedUntil.Sub(now).Round(time.Second)
		s.mu.Unlock()
		return false, fmt.Errorf("too many failed attempts — try again in %s", remaining)
	}
	s.mu.Unlock()

	rows, err := s.db.LoadLocalCredentials()
	if err != nil {
		return false, nil
	}
	for _, r := range rows {
		if r.Type != "pin" || r.PINHash == "" {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(r.PINHash), []byte(pin)) == nil {
			// Success — reset attempt counter.
			s.mu.Lock()
			delete(s.pinAttempts, clientIP)
			s.mu.Unlock()
			go s.db.TouchLocalCredential(r.ID)
			return true, nil
		}
	}

	// Wrong PIN — increment failure counter.
	s.mu.Lock()
	if s.pinAttempts[clientIP] == nil {
		s.pinAttempts[clientIP] = &pinAttemptEntry{}
	}
	s.pinAttempts[clientIP].count++
	if s.pinAttempts[clientIP].count >= pinMaxAttempts {
		s.pinAttempts[clientIP].lockedUntil = time.Now().Add(pinLockDuration)
	}
	s.mu.Unlock()
	return false, nil
}

// ── WebAuthn dynamic-origin helpers ──────────────────────────────────────────

// originFromWebAuthnBody extracts the origin from a WebAuthn credential
// response body (authentication or registration). The browser embeds the origin
// in clientDataJSON, which is base64url-encoded inside the response JSON.
func originFromWebAuthnBody(body []byte) (string, error) {
	var resp struct {
		Response struct {
			ClientDataJSON string `json:"clientDataJSON"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse credential response: %w", err)
	}
	if resp.Response.ClientDataJSON == "" {
		return "", fmt.Errorf("missing clientDataJSON in credential response")
	}
	// clientDataJSON is base64url without padding (RFC 4648 §5).
	clientData, err := base64.RawURLEncoding.DecodeString(resp.Response.ClientDataJSON)
	if err != nil {
		// Fall back to standard base64 in case the client adds padding.
		clientData, err = base64.URLEncoding.DecodeString(resp.Response.ClientDataJSON)
		if err != nil {
			return "", fmt.Errorf("decode clientDataJSON: %w", err)
		}
	}
	var cd struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(clientData, &cd); err != nil {
		return "", fmt.Errorf("parse clientDataJSON: %w", err)
	}
	if cd.Origin == "" {
		return "", fmt.Errorf("empty origin in clientDataJSON")
	}
	return cd.Origin, nil
}

// isLocalhostWebAuthnOrigin returns true if origin is a localhost origin that
// is safe to accept for WebAuthn. The RPID is "localhost" so any
// http://localhost:* or http://127.0.0.1:* origin is spec-valid.
func isLocalhostWebAuthnOrigin(origin string) bool {
	return strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1")
}

// wauthForOrigin returns a WebAuthn instance configured to accept exactly the
// given origin. Used for Finish* ceremonies so origin validation is dynamic
// rather than relying on a static allowlist built at startup.
func wauthForOrigin(origin string) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPDisplayName: "Atlas",
		RPID:          "localhost",
		RPOrigins:     []string{origin},
	})
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// localUser implements webauthn.User for the single Atlas local user.
type localUser struct {
	id          []byte
	name        string
	displayName string
	creds       []webauthn.Credential
}

func (u *localUser) WebAuthnID() []byte                         { return u.id }
func (u *localUser) WebAuthnName() string                       { return u.name }
func (u *localUser) WebAuthnDisplayName() string                { return u.displayName }
func (u *localUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// loadWebAuthnUser loads all WebAuthn credentials from the DB into a localUser.
func (s *LocalAuthService) loadWebAuthnUser() (*localUser, error) {
	rows, err := s.db.LoadLocalCredentials()
	if err != nil {
		return nil, fmt.Errorf("load credentials: %w", err)
	}
	user := &localUser{
		id:          []byte("atlas-local"),
		name:        "atlas",
		displayName: "Atlas",
	}
	for _, r := range rows {
		if r.Type != "webauthn" || r.Credential == "" {
			continue
		}
		var cred webauthn.Credential
		if err := json.Unmarshal([]byte(r.Credential), &cred); err != nil {
			continue // skip malformed entries
		}
		user.creds = append(user.creds, cred)
	}
	return user, nil
}

// popCeremony retrieves and removes a pending ceremony session.
// activePtr points to either activeRegCeremony or activeAuthCeremony and is
// cleared when the matching session ID is popped.
func (s *LocalAuthService) popCeremony(id string, activePtr *string) (webauthn.SessionData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredCeremonies()
	entry, ok := s.ceremonies[id]
	if !ok {
		return webauthn.SessionData{}, false
	}
	delete(s.ceremonies, id)
	if *activePtr == id {
		*activePtr = ""
	}
	return entry.data, true
}

// evictExpiredCeremonies removes expired ceremony entries. Must be called with mu held.
func (s *LocalAuthService) evictExpiredCeremonies() {
	now := time.Now()
	for id, entry := range s.ceremonies {
		if now.After(entry.expiresAt) {
			delete(s.ceremonies, id)
		}
	}
}

func newLocalID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("local auth: crypto/rand failure: %v", err))
	}
	return hex.EncodeToString(b)
}
