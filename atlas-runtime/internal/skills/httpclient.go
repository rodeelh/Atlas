package skills

// httpclient.go — shared HTTP client for all web/search skill calls.
//
// The default Go TLS stack uses x509.SystemCertPool() which on macOS reads
// from the system trust store. In environments with a TLS-intercepting proxy
// (Zscaler, Cisco Umbrella, etc.) the proxy's root CA is often installed into
// the macOS Keychain but the Go runtime's CGo path can miss it, producing:
//
//   tls: failed to verify certificate: x509: certificate signed by unknown authority
//
// This package builds a one-time cert pool that combines Go's system pool
// with every certificate exported from the Login and System keychains via
// the macOS `security` CLI. The result is cached in a package-level sync.Once
// so the subprocess only runs once per process lifetime.

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

var (
	webTLSOnce      sync.Once
	webTLSTransport *http.Transport
)

// newWebClient returns an *http.Client with the given timeout that uses
// the expanded macOS TLS cert pool. Falls back to the default transport if
// the cert pool cannot be built.
func newWebClient(timeout time.Duration) *http.Client {
	webTLSOnce.Do(buildWebTransport)
	return &http.Client{Timeout: timeout, Transport: webTLSTransport}
}

// buildWebTransport populates webTLSTransport once. It loads the system cert
// pool and augments it with PEM blocks exported from the macOS Keychain so
// that MITM proxy CAs (Zscaler, etc.) are trusted without any user config.
func buildWebTransport() {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}

	// Export certs from every relevant macOS keychain. We run each command
	// separately so a failure on one keychain doesn't stop the others.
	keychains := [][]string{
		// All certs trusted by the current user (includes corporate root CAs
		// installed by MDM or Zscaler).
		{"security", "find-certificate", "-a", "-p"},
		// System-wide keychain — extra safety net.
		{"security", "find-certificate", "-a", "-p", "/Library/Keychains/System.keychain"},
		// Explicitly request trusted roots (macOS 10.13+).
		{"security", "find-certificate", "-a", "-p", "-c", ".", "/System/Library/Keychains/SystemRootCertificates.keychain"},
	}
	for _, args := range keychains {
		if out, err := exec.Command(args[0], args[1:]...).Output(); err == nil {
			pool.AppendCertsFromPEM(out)
		}
	}

	webTLSTransport = &http.Transport{
		TLSClientConfig:       &tls.Config{RootCAs: pool},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
