package dashboards

import (
	"strings"
	"testing"
)

// ── preset mode ───────────────────────────────────────────────────────────────

func TestCompilePresetValidates(t *testing.T) {
	code := &WidgetCode{Mode: ModePreset, Preset: PresetMetric}
	if err := compileWidget(code); err != nil {
		t.Fatalf("preset should compile: %v", err)
	}
}

func TestCompileAllPresetKindsValidate(t *testing.T) {
	for _, preset := range []string{
		PresetMetric,
		PresetTable,
		PresetLineChart,
		PresetAreaChart,
		PresetBarChart,
		PresetPieChart,
		PresetDonutChart,
		PresetScatter,
		PresetStacked,
		PresetList,
		PresetMarkdown,
		PresetTimeline,
		PresetHeatmap,
		PresetProgress,
		PresetGauge,
		PresetStatusGrid,
		PresetKPIGroup,
	} {
		if err := compileWidget(&WidgetCode{Mode: ModePreset, Preset: preset}); err != nil {
			t.Fatalf("preset %q should compile: %v", preset, err)
		}
	}
}

func TestCompilePresetRejectsUnknown(t *testing.T) {
	code := &WidgetCode{Mode: ModePreset, Preset: "not-real"}
	if err := compileWidget(code); err == nil {
		t.Fatal("expected unknown-preset error")
	}
}

func TestCompilePresetClearsCodeFields(t *testing.T) {
	code := &WidgetCode{
		Mode:     ModePreset,
		Preset:   PresetTable,
		TSX:      "leftover",
		Compiled: "leftover",
		Hash:     "leftover",
	}
	if err := compileWidget(code); err != nil {
		t.Fatal(err)
	}
	if code.TSX != "" || code.Compiled != "" || code.Hash != "" {
		t.Fatalf("preset should clear code fields, got %+v", code)
	}
}

// ── code mode — esbuild happy path ────────────────────────────────────────────

func TestCompileCodeHappyPath(t *testing.T) {
	src := `import { h } from "preact";
export default function Widget({ data }: { data: { title: string } }) {
  return h("div", null, data.title);
}`
	code := &WidgetCode{Mode: ModeCode, TSX: src}
	if err := compileWidget(code); err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if code.Compiled == "" {
		t.Fatal("expected compiled output")
	}
	if code.Hash == "" {
		t.Fatal("expected hash")
	}
	// Sanity — compiled JS should have evaluated the TSX to a real expression.
	if !strings.Contains(code.Compiled, "Widget") {
		t.Fatalf("compiled output missing expected identifier: %s", code.Compiled)
	}
}

func TestCompileCodeCacheHit(t *testing.T) {
	src := `export default function W(){ return null; }`
	code := &WidgetCode{Mode: ModeCode, TSX: src}
	if err := compileWidget(code); err != nil {
		t.Fatal(err)
	}
	firstCompiled := code.Compiled
	firstHash := code.Hash

	// Second call with identical TSX should be a cache hit (we detect by
	// leaving Compiled/Hash unchanged; the fast path skips esbuild).
	if err := compileWidget(code); err != nil {
		t.Fatal(err)
	}
	if code.Hash != firstHash {
		t.Fatalf("hash changed on cache hit: %s vs %s", firstHash, code.Hash)
	}
	if code.Compiled != firstCompiled {
		t.Fatalf("compiled changed on cache hit")
	}
}

func TestCompileCodeCacheInvalidationOnChange(t *testing.T) {
	code := &WidgetCode{Mode: ModeCode, TSX: `export default function A(){}`}
	if err := compileWidget(code); err != nil {
		t.Fatal(err)
	}
	firstHash := code.Hash

	code.TSX = `export default function B(){}`
	if err := compileWidget(code); err != nil {
		t.Fatal(err)
	}
	if code.Hash == firstHash {
		t.Fatal("hash should change when tsx changes")
	}
}

// ── code mode — safety & import allowlist ─────────────────────────────────────

func TestCompileCodeRejectsForbiddenToken(t *testing.T) {
	code := &WidgetCode{Mode: ModeCode, TSX: `const x = fetch("https://evil.example");`}
	err := compileWidget(code)
	if err == nil {
		t.Fatal("expected rejection of fetch()")
	}
	if !strings.Contains(err.Error(), "forbidden token") {
		t.Fatalf("expected forbidden-token error, got %v", err)
	}
}

func TestCompileCodeRejectsDisallowedImport(t *testing.T) {
	src := `import foo from "lodash";
export default function W(){ return foo; }`
	code := &WidgetCode{Mode: ModeCode, TSX: src}
	err := compileWidget(code)
	if err == nil {
		t.Fatal("expected rejection of disallowed import")
	}
	if !strings.Contains(err.Error(), "lodash") && !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected import-allowlist error, got %v", err)
	}
}

func TestCompileCodeAllowsAtlasUIImport(t *testing.T) {
	src := `import { Card } from "@atlas/ui";
export default function W({ data }: { data: any }) {
  return <Card>{data.title}</Card>;
}`
	code := &WidgetCode{Mode: ModeCode, TSX: src}
	if err := compileWidget(code); err != nil {
		t.Fatalf("allowed import rejected: %v", err)
	}
	// Import should survive as-is (external) so the iframe runtime supplies it.
	if !strings.Contains(code.Compiled, "@atlas/ui") {
		t.Fatalf("expected @atlas/ui import preserved as external; got:\n%s", code.Compiled)
	}
}

func TestCompileCodeEmptyTSX(t *testing.T) {
	if err := compileWidget(&WidgetCode{Mode: ModeCode, TSX: ""}); err == nil {
		t.Fatal("empty tsx should fail")
	}
	if err := compileWidget(&WidgetCode{Mode: ModeCode, TSX: "   "}); err == nil {
		t.Fatal("whitespace tsx should fail")
	}
}

func TestCompileCodeSyntaxError(t *testing.T) {
	src := `this is not valid ::::: typescript @@@`
	code := &WidgetCode{Mode: ModeCode, TSX: src}
	err := compileWidget(code)
	if err == nil {
		t.Fatal("expected esbuild syntax error")
	}
	if !strings.Contains(err.Error(), "esbuild") {
		t.Fatalf("expected esbuild error prefix, got %v", err)
	}
}

// ── mode validation ───────────────────────────────────────────────────────────

func TestCompileRejectsUnknownMode(t *testing.T) {
	if err := compileWidget(&WidgetCode{Mode: "bogus"}); err == nil {
		t.Fatal("expected unknown-mode error")
	}
}

func TestCompileRejectsNilCode(t *testing.T) {
	if err := compileWidget(nil); err == nil {
		t.Fatal("expected nil-code error")
	}
}
