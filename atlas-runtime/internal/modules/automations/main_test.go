package automations

import (
	"os"
	"testing"

	"atlas-runtime-go/internal/creds"
	"atlas-runtime-go/internal/preferences"
)

func TestMain(m *testing.M) {
	creds.Read = func() (creds.Bundle, error) { return creds.Bundle{}, nil }
	preferences.ExecSecurityFn = func(args ...string) (string, error) { return "", os.ErrNotExist }
	os.Exit(m.Run())
}
