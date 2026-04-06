package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArchitectureGuardrails_ExtractedDomainsStayOutOfLegacyLayer(t *testing.T) {
	runtimeRoot := repoRuntimeRoot(t)

	for _, rel := range []string{
		"internal/domain/engine.go",
		"internal/domain/usage.go",
		"internal/domain/features.go",
	} {
		if _, err := os.Stat(filepath.Join(runtimeRoot, rel)); !os.IsNotExist(err) {
			t.Fatalf("legacy domain file should remain removed: %s", rel)
		}
	}
}

func TestArchitectureGuardrails_RouterUsesModuleHostForEngineAndUsage(t *testing.T) {
	runtimeRoot := repoRuntimeRoot(t)

	for _, rel := range []string{
		"cmd/atlas-runtime/main.go",
		"internal/server/router.go",
	} {
		data, err := os.ReadFile(filepath.Join(runtimeRoot, rel))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", rel, err)
		}
		text := string(data)
		for _, forbidden := range []string{"engineDomain", "usageDomain", "NewEngineDomain", "NewUsageDomain"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s still references %q", rel, forbidden)
			}
		}
	}
}

func repoRuntimeRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
