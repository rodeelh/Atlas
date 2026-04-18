package control

import (
	"os"
	"testing"

	"atlas-runtime-go/internal/preferences"
)

func TestMain(m *testing.M) {
	// Stub macOS Keychain calls so tests never trigger system dialogs.
	preferences.ExecSecurityFn = func(args ...string) (string, error) {
		return "", os.ErrNotExist
	}
	os.Exit(m.Run())
}
