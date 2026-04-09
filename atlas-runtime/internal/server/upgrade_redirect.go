package server

import (
	"net/http"

	"atlas-runtime-go/internal/auth"
)

// UpgradeRemotePlainHTTP redirects non-local, non-Tailscale LAN requests from
// plain HTTP to the built-in HTTPS listener. Localhost and direct Tailscale
// traffic remain plain HTTP because localhost is trusted and Tailnet transport
// encryption is already provided by Tailscale itself.
func UpgradeRemotePlainHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.IsLocalRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if auth.IsTailscaleAddr(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}
