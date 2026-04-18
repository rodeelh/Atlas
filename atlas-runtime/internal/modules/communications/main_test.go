package communications

import (
	"os"
	"testing"

	"atlas-runtime-go/internal/creds"
)

func TestMain(m *testing.M) {
	creds.Read = func() (creds.Bundle, error) { return creds.Bundle{}, nil }
	os.Exit(m.Run())
}
