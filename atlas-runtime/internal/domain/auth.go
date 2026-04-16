package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	keychain "github.com/keybase/go-keychain"

	"atlas-runtime-go/internal/auth"
	"atlas-runtime-go/internal/config"
)

// AuthDomain handles auth bootstrap, web static serving, and CORS preflight.
//
// Routes owned (all auth-exempt — registered outside the RequireSession group):
//
//	OPTIONS  *                     — CORS preflight
//	GET      /                     — redirect → /web
//	GET      /web                  — web UI static
//	GET      /web/*                — web UI static assets
//	GET      /auth/token           — issue launch token (native app)
//	GET      /auth/bootstrap       — exchange token → session cookie → /web
//	GET      /auth/ping            — diagnostic HTML ping
//	GET      /auth/remote-gate     — remote login page HTML
//	GET      /auth/https-required  — explains HTTPS requirement for remote LAN
//	GET      /auth/tailscale-disabled — explains that Tailscale access is disabled
//	POST     /auth/remote          — API key auth → session cookie → /web
//
// Auth-required (registered inside RequireSession group):
//
//	GET      /auth/remote-status   — LAN access info (lanIP, accessURL, port)
//	GET      /auth/csrf            — CSRF token for remote state-changing calls
//	GET      /auth/remote-key      — remote access key (authenticated)
//	POST     /auth/remote-key      — rotate remote access key (authenticated)
//	DELETE   /auth/remote-sessions — revoke all remote sessions + rotate key
type AuthDomain struct {
	svc       *auth.Service
	cfgStore  *config.Store
	webDir    string // path to atlas-web/dist for static serving
	limiter   *auth.RemoteAuthLimiter
	port      int // resolved listen port (from -port flag or config)
	localAuth *auth.LocalAuthService
}

// NewAuthDomain creates an AuthDomain.
// webDir is the path to the built web UI directory (e.g. atlas-web/dist).
// port is the resolved HTTP listen port (may differ from cfg.RuntimePort when -port flag is used).
func NewAuthDomain(svc *auth.Service, cfgStore *config.Store, webDir string, port int) *AuthDomain {
	return &AuthDomain{
		svc:      svc,
		cfgStore: cfgStore,
		webDir:   webDir,
		port:     port,
		limiter:  auth.NewRemoteAuthLimiter(),
	}
}

// SetLocalAuth wires the local auth service into AuthDomain so that remote
// LAN logins can also be validated against the shared PIN credential.
func (d *AuthDomain) SetLocalAuth(s *auth.LocalAuthService) {
	d.localAuth = s
}

// EnsureRemoteKey generates and stores a remote access key if none currently
// exists in the Keychain. Call once at startup.
func (d *AuthDomain) EnsureRemoteKey() {
	cfg := d.cfgStore.Load()
	if key := readRemoteAccessKey(cfg); key != "" {
		return // already present
	}
	newKey, err := generateAndStoreRemoteKey()
	if err != nil {
		log.Printf("Atlas: warning — could not generate remote access key: %v", err)
		return
	}
	log.Printf("Atlas: generated initial remote access key (len=%d)", len(newKey))
}

// RegisterPublic mounts auth-exempt routes on the root router.
// Call this BEFORE applying RequireSession middleware.
func (d *AuthDomain) RegisterPublic(r chi.Router) {
	r.Options("/*", d.preflight)
	r.Get("/", d.rootRedirect)
	r.Get("/web", d.serveWeb)
	r.Get("/web/*", d.serveWeb)
	r.Get("/auth/token", d.getToken)
	r.Get("/auth/bootstrap", d.bootstrap)
	r.Get("/auth/ping", d.ping)
	r.Get("/auth/remote-gate", d.remoteGate)
	r.Get("/auth/https-required", d.httpsRequired)
	r.Get("/auth/tailscale-disabled", d.tailscaleDisabled)
	// /auth/remote is rate-limited
	r.Post("/auth/remote", d.limiter.Middleware(http.HandlerFunc(d.remoteAuth)).ServeHTTP)
}

// Register mounts auth-required routes. Call inside a RequireSession group.
func (d *AuthDomain) Register(r chi.Router) {
	r.Get("/auth/remote-status", d.remoteStatus)
	r.Get("/auth/csrf", d.csrfToken)
	r.Get("/auth/remote-key", d.remoteKey)
	r.Post("/auth/remote-key", d.rotateRemoteKey)
	r.Delete("/auth/remote-sessions", d.revokeRemoteSessions)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (d *AuthDomain) preflight(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (d *AuthDomain) rootRedirect(w http.ResponseWriter, r *http.Request) {
	// Tailscale devices go straight to /web — no token required.
	// LAN devices without a valid remote session are sent to the auth gate.
	// Localhost always goes straight to /web.
	if isRemoteRequest(r) {
		cfg := d.cfgStore.Load()
		if auth.IsTailscaleAddr(r.RemoteAddr) {
			if !cfg.TailscaleEnabled {
				http.Redirect(w, r, "/auth/tailscale-disabled", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/web", http.StatusFound)
			return
		}
		if !auth.IsSecureRequest(r) {
			http.Redirect(w, r, "/auth/https-required", http.StatusFound)
			return
		}
		sessionID := auth.SessionIDFromCookie(r.Header.Get("Cookie"))
		sess := d.svc.SessionDetail(sessionID)
		if sess == nil || !sess.IsRemote {
			http.Redirect(w, r, "/auth/remote-gate", http.StatusFound)
			return
		}
	}
	http.Redirect(w, r, "/web", http.StatusFound)
}

func (d *AuthDomain) serveWeb(w http.ResponseWriter, r *http.Request) {
	if d.webDir == "" {
		http.Error(w, "Web UI not configured. Run: cd atlas-web && npm run build", http.StatusNotFound)
		return
	}

	urlPath := r.URL.Path

	if urlPath == "/web" || urlPath == "/web/" {
		urlPath = "/web/index.html"
	}

	// Keep lookups rooted under d.webDir. We normalize to a relative path after
	// cleaning so requests like /web/../../etc/passwd cannot escape the bundle.
	fsPath := strings.TrimPrefix(urlPath, "/web")
	relPath := strings.TrimPrefix(filepath.Clean("/"+fsPath), string(os.PathSeparator))
	if relPath == "" {
		relPath = "index.html"
	}

	filePath := filepath.Join(d.webDir, relPath)
	isSPAShell := relPath == "index.html"
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// SPA fallback — serve index.html for unrecognised paths.
		filePath = filepath.Join(d.webDir, "index.html")
		isSPAShell = true
	}

	// Gate all remote requests that resolve to the SPA shell, including fallback
	// routes (/web/foo that map to index.html). Static assets remain cacheable
	// and do not carry sensitive data.
	if isSPAShell && isRemoteRequest(r) {
		cfg := d.cfgStore.Load()
		if auth.IsTailscaleAddr(r.RemoteAddr) {
			if !cfg.TailscaleEnabled {
				http.Redirect(w, r, "/auth/tailscale-disabled", http.StatusFound)
				return
			}
			// Tailscale — serve SPA directly.
		} else {
			if !auth.IsSecureRequest(r) {
				http.Redirect(w, r, "/auth/https-required", http.StatusFound)
				return
			}
			sessionID := auth.SessionIDFromCookie(r.Header.Get("Cookie"))
			sess := d.svc.SessionDetail(sessionID)
			if sess == nil || !sess.IsRemote {
				http.Redirect(w, r, "/auth/remote-gate", http.StatusFound)
				return
			}
		}
	}

	http.ServeFile(w, r, filePath)
}

// isRemoteRequest returns true for non-loopback TCP peers.
func isRemoteRequest(r *http.Request) bool {
	return !auth.IsLocalRequest(r)
}

func (d *AuthDomain) getToken(w http.ResponseWriter, r *http.Request) {
	if !auth.IsLocalRequest(r) {
		writeError(w, http.StatusForbidden, "Launch token endpoint is local-only.")
		return
	}
	token := d.svc.IssueLaunchToken()
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (d *AuthDomain) bootstrap(w http.ResponseWriter, r *http.Request) {
	// Bootstrap must originate from the local machine. The launch token alone is
	// not sufficient protection — a LAN attacker who sniffs or guesses the token
	// within its 60-second window could otherwise establish a local session.
	if !auth.IsLocalRequest(r) {
		writeError(w, http.StatusForbidden, "Bootstrap is local-only.")
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "Missing 'token' query parameter.")
		return
	}
	if err := d.svc.VerifyLaunchToken(token); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	// CreateLocalSession produces an 8-hour local session (not the legacy
	// 7-day CreateSession which is remote-tier lifetime by mistake).
	sess := d.svc.CreateLocalSession()
	w.Header().Set("Set-Cookie", auth.SessionSetCookieValueForRequest(sess, r))
	http.Redirect(w, r, "/web", http.StatusFound)
}

func (d *AuthDomain) ping(w http.ResponseWriter, r *http.Request) {
	html := fmt.Sprintf(`<html><body style='font-family:monospace;padding:32px'>
<h2>✓ Atlas Go runtime is reachable</h2>
<p>Runtime: atlas-runtime</p>
<p>Time: %s</p>
<script>document.write('<p>JS works ✓</p><p>Origin: '+location.origin+'</p><p>Host: '+location.host+'</p>')</script>
</body></html>`, nowRFC3339())
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, html)
}

func (d *AuthDomain) remoteGate(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) && !auth.IsTailscaleAddr(r.RemoteAddr) && !auth.IsSecureRequest(r) {
		http.Redirect(w, r, "/auth/https-required", http.StatusFound)
		return
	}
	pinRequired := d.localAuth != nil && d.localAuth.HasPINCredential()
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, remoteGateHTML(pinRequired))
}

func (d *AuthDomain) httpsRequired(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUpgradeRequired)
	fmt.Fprint(w, auth.HTTPSRequiredHTML())
}

func (d *AuthDomain) tailscaleDisabled(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprint(w, auth.TailscaleDisabledHTML())
}

func (d *AuthDomain) remoteAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body.")
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		log.Printf("Atlas: remote login rejected — missing key (ip=%s)", remoteClientIP(r))
		writeError(w, http.StatusBadRequest, "Missing remote access key.")
		return
	}
	cfg := d.cfgStore.Load()
	if !cfg.RemoteAccessEnabled {
		log.Printf("Atlas: remote login rejected — remote access disabled (ip=%s)", remoteClientIP(r))
		writeError(w, http.StatusForbidden, "Remote access is not enabled.")
		return
	}
	// Prevent Tailscale IPs from obtaining a LAN session when Tailscale is
	// disabled. Without this check a Tailscale user could use the LAN key to
	// bypass the Tailscale toggle by creating a session that RequireSession
	// would otherwise accept.
	if auth.IsTailscaleAddr(r.RemoteAddr) && !cfg.TailscaleEnabled {
		log.Printf("Atlas: remote login rejected — Tailscale access disabled (ip=%s)", remoteClientIP(r))
		writeError(w, http.StatusForbidden, "Tailscale access is not enabled.")
		return
	}
	if isRemoteRequest(r) && !auth.IsTailscaleAddr(r.RemoteAddr) && !auth.IsSecureRequest(r) {
		writeError(w, http.StatusUpgradeRequired, "HTTPS is required for remote LAN authentication.")
		return
	}
	storedKey := readRemoteAccessKey(cfg)
	if !auth.ValidateAPIKey(key, storedKey) {
		log.Printf("Atlas: remote login rejected — invalid credentials (ip=%s)", remoteClientIP(r))
		writeError(w, http.StatusUnauthorized, "Invalid credentials.")
		return
	}
	// If a PIN credential is configured, the remote login must also include the
	// correct PIN. This unifies local and LAN auth under the same credential.
	if d.localAuth != nil && d.localAuth.HasPINCredential() {
		ok, rateLimitErr := d.localAuth.VerifyPIN(remoteClientIP(r), req.PIN)
		if rateLimitErr != nil {
			log.Printf("Atlas: remote login rate-limited (ip=%s)", remoteClientIP(r))
			writeError(w, http.StatusTooManyRequests, rateLimitErr.Error())
			return
		}
		if !ok {
			log.Printf("Atlas: remote login rejected — invalid credentials (ip=%s)", remoteClientIP(r))
			// Use the same error message as an invalid key to avoid leaking which
			// factor failed (prevents independent brute-force of each factor).
			writeError(w, http.StatusUnauthorized, "Invalid credentials.")
			return
		}
	}
	sess := d.svc.CreateSession(true)
	w.Header().Set("Set-Cookie", auth.SessionSetCookieValueForRequest(sess, r))
	log.Printf("Atlas: remote session created (ip=%s, expires=%s)", remoteClientIP(r), sess.ExpiresAt.Format("2006-01-02 15:04:05 UTC"))
	// Return 200 JSON so the gate page JS can navigate to /web after the cookie
	// is reliably applied. Using 302 with fetch redirect:'manual' is unreliable
	// across browsers (opaque redirect cookie-setting behaviour varies).
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (d *AuthDomain) remoteStatus(w http.ResponseWriter, r *http.Request) {
	cfg := d.cfgStore.Load()
	// Resolved port: prefer the port passed at construction (from -port flag),
	// fall back to config value. cfg.RuntimePort is 0 when the binary is run
	// with -port flag without updating the config file.
	port := d.port
	if port == 0 {
		port = cfg.RuntimePort
	}
	lanIP := detectLANIP()
	httpsReady := builtInHTTPSReady()
	var accessURL string
	if httpsReady && lanIP != "" && port > 0 {
		// LAN access requires HTTPS for non-Tailscale remote requests.
		accessURL = fmt.Sprintf("https://%s:%d", lanIP, port)
	}

	// Always detect the Tailscale IP so the UI can show "Tailscale detected —
	// enable it to use it" even when TailscaleEnabled is false. The endpoint is
	// auth-protected so this is intentional, not an accidental disclosure.
	tailscaleIP := detectTailscaleIP()
	var tailscaleURL string
	if tailscaleIP != "" && port > 0 && cfg.TailscaleEnabled {
		// Direct Tailscale traffic is already encrypted by the Tailnet, so we
		// keep the advertised URL on plain HTTP unless the user explicitly fronts
		// Atlas with a trusted Tailscale HTTPS endpoint such as tailscale serve.
		tailscaleURL = fmt.Sprintf("http://%s:%d", tailscaleIP, port)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"remoteAccessEnabled": cfg.RemoteAccessEnabled,
		"port":                port,
		"lanIP":               lanIP,
		"httpsReady":          httpsReady,
		"accessURL":           accessURL,
		"tailscaleEnabled":    cfg.TailscaleEnabled,
		"tailscaleIP":         tailscaleIP,
		"tailscaleURL":        tailscaleURL,
		"tailscaleConnected":  tailscaleIP != "",
	})
}

func builtInHTTPSReady() bool {
	if _, err := os.Stat(config.TLSCertPath()); err != nil {
		return false
	}
	if _, err := os.Stat(config.TLSKeyPath()); err != nil {
		return false
	}
	return true
}

func httpsRequiredHTML() string {
	return auth.HTTPSRequiredHTML()
}

func (d *AuthDomain) csrfToken(w http.ResponseWriter, r *http.Request) {
	sessionID := auth.SessionIDFromCookie(r.Header.Get("Cookie"))
	if sessionID == "" || d.svc.SessionDetail(sessionID) == nil {
		writeError(w, http.StatusUnauthorized, "No valid session.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": d.svc.CSRFToken(sessionID)})
}

func (d *AuthDomain) remoteKey(w http.ResponseWriter, r *http.Request) {
	cfg := d.cfgStore.Load()
	key := readRemoteAccessKey(cfg)
	if key == "" {
		// Auto-generate if missing (e.g. first run after enabling remote access).
		var err error
		key, err = generateAndStoreRemoteKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not generate remote access key.")
			return
		}
		log.Printf("Atlas: auto-generated missing remote access key")
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key})
}

func (d *AuthDomain) rotateRemoteKey(w http.ResponseWriter, r *http.Request) {
	newKey, err := generateAndStoreRemoteKey()
	if err != nil {
		log.Printf("Atlas: key rotation failed: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not rotate remote access key.")
		return
	}
	log.Printf("Atlas: remote access key rotated")
	writeJSON(w, http.StatusOK, map[string]string{"key": newKey})
}

func (d *AuthDomain) revokeRemoteSessions(w http.ResponseWriter, r *http.Request) {
	d.svc.InvalidateAllRemoteSessions()
	log.Printf("Atlas: all remote sessions revoked")

	// Also rotate the key so revoked sessions cannot re-authenticate with the old key.
	newKey, err := generateAndStoreRemoteKey()
	if err != nil {
		log.Printf("Atlas: warning — key rotation after revoke failed: %v", err)
		// Still return success — sessions are revoked even if key rotation failed.
	} else {
		log.Printf("Atlas: remote access key rotated after session revoke")
		_ = newKey
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// readRemoteAccessKey reads the remote access API key from the Keychain.
func readRemoteAccessKey(_ config.RuntimeConfigSnapshot) string {
	out, err := execSecurityInDomain("find-generic-password",
		"-s", "com.projectatlas.remotekey",
		"-a", "remoteAccessKey",
		"-w",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// generateAndStoreRemoteKey creates a cryptographically random 32-byte (64-char hex)
// key and stores it in the Keychain via the native Security.framework API.
// The key value never appears in process args (fixes C-1).
func generateAndStoreRemoteKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	key := hex.EncodeToString(b)

	const svc, acct = "com.projectatlas.remotekey", "remoteAccessKey"
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(svc)
	item.SetAccount(acct)
	item.SetData([]byte(key))
	item.SetSynchronizable(keychain.SynchronizableNo)
	item.SetAccessible(keychain.AccessibleWhenUnlocked)

	query := keychain.NewItem()
	query.SetSecClass(keychain.SecClassGenericPassword)
	query.SetService(svc)
	query.SetAccount(acct)
	query.SetMatchLimit(keychain.MatchLimitOne)

	err := keychain.UpdateItem(query, item)
	if err == keychain.ErrorItemNotFound {
		err = keychain.AddItem(item)
	}
	if err != nil {
		return "", fmt.Errorf("keychain write: %w", err)
	}
	return key, nil
}

// detectLANIP walks the host's network interfaces and returns the first
// private-range IPv4 address found on an active non-loopback interface.
func detectLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		// Skip loopback and down interfaces.
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			if isPrivateIPv4(ip4) {
				return ip4.String()
			}
		}
	}
	return ""
}

// detectTailscaleIP returns the Tailscale IP (100.64.0.0/10) of this machine,
// or empty string if Tailscale is not running or not connected.
// Uses auth.IsTailscaleIP to avoid duplicating the range definition.
func detectTailscaleIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		// Skip loopback and down interfaces — mirrors detectLANIP behaviour.
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			if auth.IsTailscaleIP(ip4.String()) {
				return ip4.String()
			}
		}
	}
	return ""
}

// isPrivateIPv4 returns true for addresses in RFC-1918 ranges:
// 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16.
func isPrivateIPv4(ip net.IP) bool {
	private := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	for _, cidr := range private {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteClientIP extracts the best-effort client IP from a request.
func remoteClientIP(r *http.Request) string {
	if ip := auth.ClientIP(r); ip != "" {
		return ip
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		return host[:idx]
	}
	return host
}

func remoteGateHTML(pinRequired bool) string {
	pinField := ""
	pinBody := ""
	if pinRequired {
		pinField = `
    <div class="field">
      <label for="p">PIN</label>
      <input id="p" type="password" placeholder="Enter your PIN" autocomplete="current-password">
    </div>`
		pinBody = `,pin:document.getElementById('p').value`
	}
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no,viewport-fit=cover">
<title>Atlas — Remote Access</title>
<style>
:root{
  --bg:#111111;
  --surface:#1a1a1a;
  --border:#333333;
  --text:#f5f5f5;
  --text-2:#cfcfcf;
  --accent:#f5f5f5;
  --input-bg:#0f0f0f;
}
*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%;overflow:hidden}
body{
  font-family:-apple-system,system-ui,sans-serif;
  background:var(--bg);
  color:var(--text);
  display:flex;align-items:center;justify-content:center;
  min-height:100vh;
  min-height:100dvh;
  padding:20px;
  overflow:hidden;
  touch-action:manipulation;
}
.card{
  background:var(--surface);
  border:1px solid var(--border);
  border-radius:12px;
  padding:24px;
  max-width:520px;
  width:100%;
  line-height:1.5;
  text-align:left;
}
h1{margin:0 0 8px;font-size:20px;color:var(--text)}
.subtitle{font-size:16px;color:var(--text-2);margin-bottom:18px;line-height:1.5}
.field{margin-bottom:12px;text-align:left}
label{display:block;font-size:13px;font-weight:500;color:var(--text-2);margin-bottom:6px}
input[type=password]{
  width:100%;
  padding:11px 14px;
  background:var(--input-bg);color:var(--text);
  border:1px solid var(--border);
  border-radius:8px;
  font-family:inherit;
  font-size:15px;
  outline:none;
}
input[type=password]::placeholder{color:var(--text-2);opacity:.75}
input[type=password]:focus{
  border-color:var(--accent);
  box-shadow:0 0 0 2px rgba(245,245,245,0.12);
}
button{
  width:100%;
  padding:11px 14px;
  margin-top:4px;
  background:var(--surface);
  color:var(--text);
  border:1px solid var(--border);
  border-radius:10px;
  font-family:inherit;
  font-size:15px;
  font-weight:500;
  cursor:pointer;
  transition:background .15s,opacity .15s,border-color .15s;
}
button:hover:not(:disabled){
  background:#202020;
  border-color:#4a4a4a;
}
button:disabled{opacity:.55;cursor:not-allowed}
.err{
  display:none;
  margin-top:14px;
  padding:10px 14px;
  background:#1f1212;
  border:1px solid rgba(255,59,48,.2);
  border-radius:8px;
  color:#ff8f87;
  font-size:13px;
  line-height:1.45;
  text-align:left;
}
@media (max-width: 640px){
  html,body{
    width:100%;
    height:100dvh;
    overflow:hidden;
  }
  body{
    min-height:100dvh;
    padding:
      max(18px, env(safe-area-inset-top, 0px))
      16px
      max(18px, env(safe-area-inset-bottom, 0px))
      16px;
    align-items:center;
    justify-content:center;
  }
  .card{
    width:100%;
    max-width:520px;
    border-radius:12px;
    padding:24px;
  }
  h1{
    font-size:20px;
  }
  .subtitle{
    font-size:16px;
    margin-bottom:18px;
  }
  input[type=password],button{
    min-height:48px;
    font-size:15px;
  }
}
</style>
</head>
<body>
<div class="card">
  <h1>Authorize Remote Access</h1>
  <p class="subtitle">Enter your credentials to connect to this Atlas runtime.</p>
  <form id="f" onsubmit="event.preventDefault();login()">
    <div class="field">
      <label for="k">Access Key</label>
      <input id="k" type="password" placeholder="Paste your remote access key" autocomplete="current-password" autofocus>
    </div>` + pinField + `
    <button type="submit" id="btn">Connect</button>
    <div class="err" id="err"></div>
  </form>
</div>
<script>
async function login(){
  var k=document.getElementById('k').value.trim();
  if(!k)return;
  var btn=document.getElementById('btn');
  var err=document.getElementById('err');
  btn.disabled=true;btn.textContent='Connecting\u2026';
  err.style.display='none';
  try{
    var body={key:k` + pinBody + `};
    var res=await fetch('/auth/remote',{method:'POST',credentials:'include',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
    if(res.ok){window.location='/web';return;}
    var j=await res.json().catch(function(){return{};});
    showErr(j.error||'Login failed ('+res.status+')');
  }catch(e){showErr('Network error. Check that Atlas is running on the host.');}
  btn.disabled=false;btn.textContent='Connect';
}
function showErr(msg){var e=document.getElementById('err');e.textContent=msg;e.style.display='block';}
</script>
</body>
</html>`
}
