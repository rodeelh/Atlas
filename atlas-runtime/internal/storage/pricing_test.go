package storage

// pricing_test.go — tests for ComputeCost / lookupPrice.
//
// Coverage:
//   - Exact model matches
//   - Suffix stripping (-preview, -exp, -latest, etc.)
//   - Date suffix stripping (-YYYYMMDD)
//   - Family fallbacks (gemini, gpt-5.4, gpt-4o, claude, o1/o3/o4)
//   - Local providers always return $0, known=true
//   - Completely unknown model returns $0, known=false
//   - Cost arithmetic correctness

import (
	"math"
	"testing"
)

const epsilon = 1e-9

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}

// ── Local providers ───────────────────────────────────────────────────────────

func TestComputeCost_LocalProviders_ReturnZero(t *testing.T) {
	for _, provider := range []string{"lm_studio", "ollama", "atlas_engine"} {
		ic, oc, known := ComputeCost(provider, "any-model", 1_000_000, 1_000_000)
		if !known {
			t.Errorf("%s: known should be true", provider)
		}
		if ic != 0 || oc != 0 {
			t.Errorf("%s: want $0/$0, got $%.6f/$%.6f", provider, ic, oc)
		}
	}
}

// ── Exact matches ─────────────────────────────────────────────────────────────

func TestComputeCost_ExactMatch_GPT4o(t *testing.T) {
	// gpt-4o: $2.50/M input, $10.00/M output
	ic, oc, known := ComputeCost("openai", "gpt-4o", 1_000_000, 1_000_000)
	if !known {
		t.Fatal("gpt-4o should be a known model")
	}
	if !approxEqual(ic, 2.50) {
		t.Errorf("input cost: want 2.50, got %.6f", ic)
	}
	if !approxEqual(oc, 10.00) {
		t.Errorf("output cost: want 10.00, got %.6f", oc)
	}
}

func TestComputeCost_ExactMatch_ClaudeSonnet46(t *testing.T) {
	// claude-sonnet-4-6: $3.00/M input, $15.00/M output
	ic, oc, known := ComputeCost("anthropic", "claude-sonnet-4-6", 1_000_000, 1_000_000)
	if !known {
		t.Fatal("claude-sonnet-4-6 should be a known model")
	}
	if !approxEqual(ic, 3.00) {
		t.Errorf("input cost: want 3.00, got %.6f", ic)
	}
	if !approxEqual(oc, 15.00) {
		t.Errorf("output cost: want 15.00, got %.6f", oc)
	}
}

func TestComputeCost_ExactMatch_O1(t *testing.T) {
	// o1: $15/M input, $60/M output
	ic, oc, known := ComputeCost("openai", "o1", 1_000_000, 1_000_000)
	if !known {
		t.Fatal("o1 should be known")
	}
	if !approxEqual(ic, 15.00) {
		t.Errorf("o1 input cost: want 15.00, got %.6f", ic)
	}
	if !approxEqual(oc, 60.00) {
		t.Errorf("o1 output cost: want 60.00, got %.6f", oc)
	}
}

// ── Suffix stripping ──────────────────────────────────────────────────────────

func TestComputeCost_StripPreviewSuffix(t *testing.T) {
	// "gemini-2.5-pro-preview" → strip "-preview" → "gemini-2.5-pro" (exact match)
	_, _, known := ComputeCost("google", "gemini-2.5-pro-preview", 1_000_000, 1_000_000)
	if !known {
		t.Error("gemini-2.5-pro-preview should be known via suffix strip")
	}
}

func TestComputeCost_StripLatestSuffix(t *testing.T) {
	// "claude-3-5-sonnet-latest" → strip "-latest" → "claude-3-5-sonnet" (no match) → family fallback
	_, _, known := ComputeCost("anthropic", "claude-3-5-sonnet-latest", 1_000_000, 1_000_000)
	if !known {
		t.Error("claude-3-5-sonnet-latest should be known via suffix strip or family fallback")
	}
}

func TestComputeCost_StripExpSuffix(t *testing.T) {
	_, _, known := ComputeCost("google", "gemini-2.5-flash-exp", 1_000_000, 1_000_000)
	if !known {
		t.Error("gemini-2.5-flash-exp should be known via -exp suffix strip")
	}
}

// ── Date suffix stripping ─────────────────────────────────────────────────────

func TestComputeCost_StripDateSuffix(t *testing.T) {
	// "claude-3-5-sonnet-20241022" → strip last 9 chars → "claude-3-5-sonnet" → family fallback
	_, _, known := ComputeCost("anthropic", "claude-3-5-sonnet-20241022", 1_000_000, 1_000_000)
	if !known {
		t.Error("claude-3-5-sonnet-20241022 should be known via date suffix strip or exact match")
	}
}

// ── Family fallbacks ─────────────────────────────────────────────────────────

func TestComputeCost_FamilyFallback_GeminiFlash(t *testing.T) {
	_, _, known := ComputeCost("google", "gemini-3.0-flash-unknown-variant", 1_000_000, 1_000_000)
	if !known {
		t.Error("gemini flash variant should fall back to gemini-2.5-flash pricing")
	}
}

func TestComputeCost_FamilyFallback_GeminiFlashLite(t *testing.T) {
	_, _, known := ComputeCost("google", "gemini-future-flash-lite", 1_000_000, 1_000_000)
	if !known {
		t.Error("gemini flash-lite variant should fall back to gemini-2.5-flash-lite pricing")
	}
}

func TestComputeCost_FamilyFallback_GeminiPro(t *testing.T) {
	_, _, known := ComputeCost("google", "gemini-future-pro", 1_000_000, 1_000_000)
	if !known {
		t.Error("gemini pro variant should fall back to gemini-2.5-pro pricing")
	}
}

func TestComputeCost_FamilyFallback_ClaudeOpus(t *testing.T) {
	_, _, known := ComputeCost("anthropic", "claude-opus-future", 1_000_000, 1_000_000)
	if !known {
		t.Error("claude-opus- prefix should fall back to opus pricing")
	}
}

func TestComputeCost_FamilyFallback_ClaudeHaiku(t *testing.T) {
	_, _, known := ComputeCost("anthropic", "claude-haiku-future", 1_000_000, 1_000_000)
	if !known {
		t.Error("claude-haiku- prefix should fall back to haiku pricing")
	}
}

func TestComputeCost_FamilyFallback_GPT54(t *testing.T) {
	_, _, known := ComputeCost("openai", "gpt-5.4-pro-vision", 1_000_000, 1_000_000)
	if !known {
		t.Error("gpt-5.4 variant should be known via family fallback")
	}
}

func TestComputeCost_FamilyFallback_O3Series(t *testing.T) {
	_, _, known := ComputeCost("openai", "o3-ultra", 1_000_000, 1_000_000)
	if !known {
		t.Error("o3 variant should fall back to o3 pricing")
	}
}

func TestComputeCost_FamilyFallback_O4Series(t *testing.T) {
	_, _, known := ComputeCost("openai", "o4-pro", 1_000_000, 1_000_000)
	if !known {
		t.Error("o4 variant should fall back to o4-mini pricing")
	}
}

// ── Unknown model ─────────────────────────────────────────────────────────────

func TestComputeCost_UnknownModel_ReturnsZeroAndFalse(t *testing.T) {
	ic, oc, known := ComputeCost("unknown-corp", "totally-unknown-model-xyz-9999", 1_000_000, 1_000_000)
	if known {
		t.Error("completely unknown model should return known=false")
	}
	if ic != 0 || oc != 0 {
		t.Errorf("unknown model: want $0/$0, got $%.6f/$%.6f", ic, oc)
	}
}

// ── Cost arithmetic ───────────────────────────────────────────────────────────

func TestComputeCost_Arithmetic_SmallTokenCount(t *testing.T) {
	// gpt-4o-mini: $0.15/M input, $0.60/M output
	// 1000 input tokens = $0.15/1000 = $0.00015
	// 2000 output tokens = $0.60/500 = $0.0012
	ic, oc, known := ComputeCost("openai", "gpt-4o-mini", 1000, 2000)
	if !known {
		t.Fatal("gpt-4o-mini should be known")
	}
	wantIC := 1000.0 / 1_000_000.0 * 0.15
	wantOC := 2000.0 / 1_000_000.0 * 0.60
	if !approxEqual(ic, wantIC) {
		t.Errorf("input cost: want %.8f, got %.8f", wantIC, ic)
	}
	if !approxEqual(oc, wantOC) {
		t.Errorf("output cost: want %.8f, got %.8f", wantOC, oc)
	}
}

func TestComputeCost_Arithmetic_ZeroTokens(t *testing.T) {
	ic, oc, known := ComputeCost("openai", "gpt-4o", 0, 0)
	if !known {
		t.Fatal("gpt-4o should be known")
	}
	if ic != 0 || oc != 0 {
		t.Errorf("zero tokens: want $0/$0, got $%.6f/$%.6f", ic, oc)
	}
}
