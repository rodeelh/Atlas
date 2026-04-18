package skills

import (
	"os"
	"testing"

	"atlas-runtime-go/internal/creds"
)

func TestMain(m *testing.M) {
	// Stub macOS Keychain reads so tests never trigger system dialogs.
	creds.Read = func() (creds.Bundle, error) { return creds.Bundle{}, nil }
	os.Exit(m.Run())
}
