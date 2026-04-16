package dashboards

// compile.go — turns agent-authored TSX into JS for the sandboxed renderer.
//
// Pipeline:
//   1. safety.go's validateGeneratedTSX rejects forbidden tokens and caps
//      the source size.
//   2. esbuild transforms TSX with Preact automatic JSX runtime.
//   3. An OnResolve plugin enforces the import allowlist
//      (@atlas/ui, preact, preact/hooks) and marks every allowed import as
//      external — the iframe runtime supplies those at load time.
//   4. The compiled JS is stamped with sha256(tsx) so a follow-up commit
//      with identical source can skip recompile.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	esbuild "github.com/evanw/esbuild/pkg/api"
)

// compileWidget validates a WidgetCode and (when Mode == ModeCode) produces
// a JS bundle suitable for the sandboxed iframe renderer.
func compileWidget(code *WidgetCode) error {
	if code == nil {
		return errors.New("widget code is nil")
	}
	switch code.Mode {
	case ModePreset:
		if code.Preset == "" {
			return errors.New("widget code preset is required in preset mode")
		}
		switch code.Preset {
		case PresetMetric, PresetTable, PresetLineChart, PresetBarChart, PresetList, PresetMarkdown:
			// ok
		default:
			return errors.New("unknown widget preset: " + code.Preset)
		}
		// Clear code-mode fields to keep the record tidy.
		code.TSX = ""
		code.Compiled = ""
		code.Hash = ""
		return nil

	case ModeCode:
		tsx := strings.TrimSpace(code.TSX)
		if tsx == "" {
			return errors.New("widget code tsx is required in code mode")
		}
		if err := validateGeneratedTSX(tsx); err != nil {
			return err
		}
		hash := tsxHash(tsx)
		// Cache hit — TSX is unchanged since the last successful compile.
		if code.Compiled != "" && code.Hash == hash {
			return nil
		}
		compiled, err := compileTSX(tsx)
		if err != nil {
			return err
		}
		code.Compiled = compiled
		code.Hash = hash
		return nil

	default:
		return errors.New("widget code mode must be preset or code")
	}
}

// compileTSX transforms a single TSX source via esbuild and returns the JS.
// Imports outside the allowlist are rejected by the plugin before bundling.
func compileTSX(src string) (string, error) {
	result := esbuild.Build(esbuild.BuildOptions{
		Stdin: &esbuild.StdinOptions{
			Contents:   src,
			Loader:     esbuild.LoaderTSX,
			Sourcefile: "widget.tsx",
		},
		Bundle:          true,
		Write:           false,
		Format:          esbuild.FormatESModule,
		Target:          esbuild.ES2020,
		JSX:             esbuild.JSXAutomatic,
		JSXImportSource: "preact",
		Plugins:         []esbuild.Plugin{importAllowlistPlugin()},
		LogLevel:        esbuild.LogLevelSilent,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("esbuild: %s", result.Errors[0].Text)
	}
	if len(result.OutputFiles) == 0 {
		return "", errors.New("esbuild: no output produced")
	}
	// Hard cap on compiled size so a pathological expansion can't balloon
	// the JSON record (widgets are stored in-line in dashboards-v2.json).
	const maxCompiled = 256 * 1024
	out := string(result.OutputFiles[0].Contents)
	if len(out) > maxCompiled {
		return "", fmt.Errorf("compiled widget is too large (%d bytes, max %d)", len(out), maxCompiled)
	}
	return out, nil
}

// importAllowlistPlugin intercepts every import in a widget's TSX. Allowed
// specifiers are marked external (the iframe runtime supplies them); any
// other specifier produces a compile error with a clear remediation hint.
func importAllowlistPlugin() esbuild.Plugin {
	return esbuild.Plugin{
		Name: "dashboards-import-allowlist",
		Setup: func(build esbuild.PluginBuild) {
			build.OnResolve(esbuild.OnResolveOptions{Filter: ".*"}, func(args esbuild.OnResolveArgs) (esbuild.OnResolveResult, error) {
				if args.Kind == esbuild.ResolveEntryPoint {
					return esbuild.OnResolveResult{}, nil
				}
				if !IsImportAllowed(args.Path) {
					return esbuild.OnResolveResult{
						Errors: []esbuild.Message{{
							Text: fmt.Sprintf(
								"import %q is not allowed; widgets may only import from @atlas/ui, preact, preact/hooks",
								args.Path,
							),
						}},
					}, nil
				}
				return esbuild.OnResolveResult{
					Path:     args.Path,
					External: true,
				}, nil
			})
		},
	}
}

// tsxHash is sha256(TSX) hex-encoded — used as a cache key so an unchanged
// TSX source on re-commit skips the esbuild call.
func tsxHash(tsx string) string {
	sum := sha256.Sum256([]byte(tsx))
	return hex.EncodeToString(sum[:])
}
