package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// IsAllowedCORSOrigin validates whether origin may read credentialed responses.
// Policy:
//   - Empty Origin (non-browser/native clients) is allowed.
//   - localhost origins are allowed only when the request host is localhost.
//   - Non-localhost origins must match the request host (same canonical host).
//
// The remoteEnabled/tailscaleEnabled inputs are intentionally accepted for
// call-site compatibility with router wiring; origin trust is host-bound.
func IsAllowedCORSOrigin(
	r *http.Request,
	origin string,
	remoteEnabled func() bool,
	tailscaleEnabled func() bool,
) bool {
	_ = remoteEnabled
	_ = tailscaleEnabled

	if origin == "" {
		return true
	}

	if isLocalhostOrigin(origin) {
		return IsLocalhostHost(r.Host)
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	return strings.EqualFold(CanonicalHost(u.Host), CanonicalHost(r.Host))
}

