package auth

import (
	"crypto/rand"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Stub Keychain access so tests never trigger system dialogs.
	LoadOrCreateSigningKeyFn = func() []byte {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("test: rand.Read: " + err.Error())
		}
		return key
	}
	os.Exit(m.Run())
}
