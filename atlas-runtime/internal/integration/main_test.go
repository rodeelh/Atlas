package integration

import (
	"crypto/rand"
	"os"
	"testing"

	"atlas-runtime-go/internal/auth"
	"atlas-runtime-go/internal/creds"
	"atlas-runtime-go/internal/preferences"
)

func TestMain(m *testing.M) {
	// Stub macOS Keychain calls so tests never trigger system dialogs.
	auth.LoadOrCreateSigningKeyFn = func() []byte {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("test: rand.Read: " + err.Error())
		}
		return key
	}
	creds.Read = func() (creds.Bundle, error) { return creds.Bundle{}, nil }
	preferences.ExecSecurityFn = func(args ...string) (string, error) { return "", os.ErrNotExist }
	os.Exit(m.Run())
}
