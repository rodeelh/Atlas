package storage

import "strings"

// ModelPrice holds the per-million-token prices in USD for one model.
type ModelPrice struct {
	InputPerM  float64 // USD per 1M input tokens
	OutputPerM float64 // USD per 1M output tokens
}

// modelPricing is the canonical pricing table keyed by exact model ID.
// Rates are $/1M tokens. Last verified: April 2026.
//
// Sources:
//   - OpenAI:    https://openai.com/api/pricing/          (via pricepertoken.com — openai.com returns 403)
//   - Anthropic: https://platform.claude.com/docs/en/about-claude/models/all-models
//   - Gemini:    https://ai.google.dev/gemini-api/docs/pricing
//
// TODO(monthly): Verify all prices against the three sources above on the first
// of each month and update this file if any rates have changed.
//
// For Gemini models with tiered pricing (≤200k / >200k context), the standard
// (≤200k) rate is used as it covers the vast majority of requests.
// Local/self-hosted providers (lm_studio, ollama, atlas_engine) are free — see ComputeCost.
var modelPricing = map[string]ModelPrice{

	// ── OpenAI — GPT-5 series ────────────────────────────────────────────────
	"gpt-5":        {InputPerM: 1.25, OutputPerM: 10.00},
	"gpt-5-mini":   {InputPerM: 0.125, OutputPerM: 1.00},
	"gpt-5-nano":   {InputPerM: 0.05, OutputPerM: 0.40},
	"gpt-5.4":      {InputPerM: 2.50, OutputPerM: 15.00},
	"gpt-5.4-mini": {InputPerM: 0.75, OutputPerM: 4.50},
	"gpt-5.4-nano": {InputPerM: 0.20, OutputPerM: 1.25},
	"gpt-5.4-pro":  {InputPerM: 30.00, OutputPerM: 180.00},

	// ── OpenAI — GPT-4 series ────────────────────────────────────────────────
	"gpt-4o":                 {InputPerM: 2.50, OutputPerM: 10.00},
	"gpt-4o-2024-11-20":      {InputPerM: 2.50, OutputPerM: 10.00},
	"gpt-4o-2024-08-06":      {InputPerM: 2.50, OutputPerM: 10.00},
	"gpt-4o-mini":            {InputPerM: 0.15, OutputPerM: 0.60},
	"gpt-4o-mini-2024-07-18": {InputPerM: 0.15, OutputPerM: 0.60},
	"chatgpt-4o-latest":      {InputPerM: 5.00, OutputPerM: 15.00},
	"gpt-4.1":                {InputPerM: 2.00, OutputPerM: 8.00},
	"gpt-4.1-mini":           {InputPerM: 0.20, OutputPerM: 0.80},
	"gpt-4.1-nano":           {InputPerM: 0.05, OutputPerM: 0.20},
	"gpt-4-turbo":            {InputPerM: 10.00, OutputPerM: 30.00},
	"gpt-4-turbo-preview":    {InputPerM: 10.00, OutputPerM: 30.00},
	"gpt-4":                  {InputPerM: 30.00, OutputPerM: 60.00},
	"gpt-3.5-turbo":          {InputPerM: 0.50, OutputPerM: 1.50},
	"gpt-3.5-turbo-0125":     {InputPerM: 0.50, OutputPerM: 1.50},
	"gpt-3.5-turbo-instruct": {InputPerM: 1.50, OutputPerM: 2.00},

	// ── OpenAI — o-series (reasoning) ────────────────────────────────────────
	"o1":                    {InputPerM: 15.00, OutputPerM: 60.00},
	"o1-mini":               {InputPerM: 0.55, OutputPerM: 2.20},
	"o1-pro":                {InputPerM: 150.00, OutputPerM: 600.00},
	"o1-preview":            {InputPerM: 15.00, OutputPerM: 60.00},
	"o3":                    {InputPerM: 2.00, OutputPerM: 8.00},
	"o3-mini":               {InputPerM: 1.10, OutputPerM: 4.40},
	"o3-mini-high":          {InputPerM: 1.10, OutputPerM: 4.40},
	"o3-pro":                {InputPerM: 20.00, OutputPerM: 80.00},
	"o3-deep-research":      {InputPerM: 10.00, OutputPerM: 40.00},
	"o4-mini":               {InputPerM: 0.55, OutputPerM: 2.20},
	"o4-mini-high":          {InputPerM: 1.10, OutputPerM: 4.40},
	"o4-mini-deep-research": {InputPerM: 2.00, OutputPerM: 8.00},

	// ── Anthropic — Claude 4.x ───────────────────────────────────────────────
	"claude-opus-4-6":           {InputPerM: 5.00, OutputPerM: 25.00},
	"claude-sonnet-4-6":         {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-haiku-4-5-20251001": {InputPerM: 1.00, OutputPerM: 5.00},

	// ── Anthropic — Claude 4.5 series ────────────────────────────────────────
	"claude-opus-4-5-20251101":   {InputPerM: 5.00, OutputPerM: 25.00},
	"claude-sonnet-4-5-20250929": {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-opus-4-5":            {InputPerM: 5.00, OutputPerM: 25.00},
	"claude-sonnet-4-5":          {InputPerM: 3.00, OutputPerM: 15.00},

	// ── Anthropic — Claude 4 series ──────────────────────────────────────────
	"claude-opus-4-1-20250805":   {InputPerM: 15.00, OutputPerM: 75.00},
	"claude-opus-4-20250514":     {InputPerM: 15.00, OutputPerM: 75.00},
	"claude-sonnet-4-20250514":   {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-sonnet-4-5-20250514": {InputPerM: 3.00, OutputPerM: 15.00},

	// ── Anthropic — Claude 3.x (legacy) ──────────────────────────────────────
	"claude-3-opus-20240229":     {InputPerM: 15.00, OutputPerM: 75.00},
	"claude-3-opus-latest":       {InputPerM: 15.00, OutputPerM: 75.00},
	"claude-3-5-sonnet-20241022": {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-sonnet-20240620": {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-sonnet-latest":   {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-haiku-20241022":  {InputPerM: 0.80, OutputPerM: 4.00},
	"claude-3-5-haiku-latest":    {InputPerM: 0.80, OutputPerM: 4.00},
	"claude-haiku-3-5":           {InputPerM: 0.80, OutputPerM: 4.00},
	"claude-3-sonnet-20240229":   {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-haiku-20240307":    {InputPerM: 0.25, OutputPerM: 1.25},

	// ── Gemini — 3.x series ──────────────────────────────────────────────────
	"gemini-3.1-pro-preview":        {InputPerM: 2.00, OutputPerM: 12.00},
	"gemini-3.1-flash-lite-preview": {InputPerM: 0.25, OutputPerM: 1.50},
	"gemini-3-flash-preview":        {InputPerM: 0.50, OutputPerM: 3.00},
	"gemini-3-pro-preview":          {InputPerM: 2.00, OutputPerM: 12.00},

	// ── Gemini — 2.5 series ──────────────────────────────────────────────────
	"gemini-2.5-pro":        {InputPerM: 1.25, OutputPerM: 10.00},
	"gemini-2.5-flash":      {InputPerM: 0.30, OutputPerM: 2.50},
	"gemini-2.5-flash-lite": {InputPerM: 0.10, OutputPerM: 0.40},

	// ── Gemini — 2.0 series ──────────────────────────────────────────────────
	"gemini-2.0-flash":              {InputPerM: 0.10, OutputPerM: 0.40},
	"gemini-2.0-flash-lite":         {InputPerM: 0.075, OutputPerM: 0.30},
	"gemini-2.0-flash-thinking-exp": {InputPerM: 0.10, OutputPerM: 0.40},

	// ── Gemini — 1.5 series (legacy) ─────────────────────────────────────────
	"gemini-1.5-pro":          {InputPerM: 1.25, OutputPerM: 5.00},
	"gemini-1.5-pro-latest":   {InputPerM: 1.25, OutputPerM: 5.00},
	"gemini-1.5-flash":        {InputPerM: 0.075, OutputPerM: 0.30},
	"gemini-1.5-flash-latest": {InputPerM: 0.075, OutputPerM: 0.30},
	"gemini-1.0-pro":          {InputPerM: 0.50, OutputPerM: 1.50},
}

// lookupPrice finds pricing for a model, applying fuzzy fallbacks for
// preview/experimental/dated variants that aren't in the exact table.
func lookupPrice(model string) (ModelPrice, bool) {
	// 1. Exact match.
	if p, ok := modelPricing[model]; ok {
		return p, true
	}

	// 2. Strip common preview/experimental suffixes and retry.
	for _, sfx := range []string{"-preview", "-exp", "-experimental", "-latest", "-turbo"} {
		if strings.HasSuffix(model, sfx) {
			if p, ok := modelPricing[strings.TrimSuffix(model, sfx)]; ok {
				return p, true
			}
		}
	}

	// 3. Strip trailing date suffix (-YYYYMMDD) and retry.
	if len(model) > 9 {
		if p, ok := modelPricing[model[:len(model)-9]]; ok {
			return p, true
		}
	}

	// 4. Family-level fallback by name fragments.
	lower := strings.ToLower(model)
	switch {
	// Gemini — match by generation and tier.
	case strings.HasPrefix(lower, "gemini-"):
		if strings.Contains(lower, "flash-lite") {
			if p, ok := modelPricing["gemini-2.5-flash-lite"]; ok {
				return p, true
			}
		}
		if strings.Contains(lower, "flash") {
			if p, ok := modelPricing["gemini-2.5-flash"]; ok {
				return p, true
			}
		}
		if strings.Contains(lower, "pro") {
			if p, ok := modelPricing["gemini-2.5-pro"]; ok {
				return p, true
			}
		}

	// OpenAI GPT — match by series.
	case strings.HasPrefix(lower, "gpt-5.4"):
		if p, ok := modelPricing["gpt-5.4"]; ok {
			return p, true
		}
	case strings.HasPrefix(lower, "gpt-5"):
		if p, ok := modelPricing["gpt-5"]; ok {
			return p, true
		}
	case strings.HasPrefix(lower, "gpt-4.1"):
		if p, ok := modelPricing["gpt-4.1"]; ok {
			return p, true
		}
	case strings.HasPrefix(lower, "gpt-4o"):
		if p, ok := modelPricing["gpt-4o"]; ok {
			return p, true
		}
	case strings.HasPrefix(lower, "gpt-4"):
		if p, ok := modelPricing["gpt-4-turbo"]; ok {
			return p, true
		}
	case strings.HasPrefix(lower, "gpt-"):
		// Any other GPT model — fall back to gpt-4o as a rough estimate.
		if p, ok := modelPricing["gpt-4o"]; ok {
			return p, true
		}

	// OpenAI o-series.
	case strings.HasPrefix(lower, "o1"):
		if p, ok := modelPricing["o1"]; ok {
			return p, true
		}
	case strings.HasPrefix(lower, "o3"):
		if p, ok := modelPricing["o3"]; ok {
			return p, true
		}
	case strings.HasPrefix(lower, "o4"):
		if p, ok := modelPricing["o4-mini"]; ok {
			return p, true
		}

	// Anthropic — match by model family.
	case strings.HasPrefix(lower, "claude-"):
		if strings.Contains(lower, "opus") {
			if p, ok := modelPricing["claude-opus-4-6"]; ok {
				return p, true
			}
		}
		if strings.Contains(lower, "haiku") {
			if p, ok := modelPricing["claude-haiku-4-5-20251001"]; ok {
				return p, true
			}
		}
		// Default claude → sonnet pricing.
		if p, ok := modelPricing["claude-sonnet-4-6"]; ok {
			return p, true
		}
	}

	return ModelPrice{}, false
}

// ImageQualityPrice holds per-image USD cost for each quality tier.
// Auto is treated the same as Medium (balanced default).
// Sources: https://openai.com/api/pricing/ · https://ai.google.dev/gemini-api/docs/pricing
// Last verified: April 2026.
type ImageQualityPrice struct {
	Low    float64
	Medium float64
	High   float64
}

// imageModelPricing is the per-image pricing table keyed by model ID.
// Gemini models are priced per image regardless of quality (flat rate).
var imageModelPricing = map[string]ImageQualityPrice{
	// OpenAI — gpt-image-1 series (1024×1024)
	"gpt-image-1":      {Low: 0.011, Medium: 0.042, High: 0.167},
	"gpt-image-1-mini": {Low: 0.005, Medium: 0.011, High: 0.036},
	"gpt-image-1.5":    {Low: 0.009, Medium: 0.034, High: 0.133},

	// Gemini — flat per-image rate regardless of quality
	"gemini-2.5-flash-image":          {Low: 0.039, Medium: 0.039, High: 0.039},
	"gemini-3.1-flash-image-preview":  {Low: 0.039, Medium: 0.039, High: 0.039},
}

// ComputeImageCost returns the estimated USD cost for one generated image.
// auto quality maps to Medium. Returns (0, false) for unknown models.
func ComputeImageCost(model, quality string) (costPerImage float64, known bool) {
	p, ok := imageModelPricing[model]
	if !ok {
		// Family fallback for gpt-image-1 variants.
		if strings.HasPrefix(model, "gpt-image-1-mini") {
			p = imageModelPricing["gpt-image-1-mini"]
		} else if strings.HasPrefix(model, "gpt-image-1.5") {
			p = imageModelPricing["gpt-image-1.5"]
		} else if strings.HasPrefix(model, "gpt-image-1") {
			p = imageModelPricing["gpt-image-1"]
		} else if strings.HasPrefix(model, "gemini-") {
			p = imageModelPricing["gemini-2.5-flash-image"]
		} else {
			return 0, false
		}
	}
	switch quality {
	case "low":
		return p.Low, true
	case "high":
		return p.High, true
	default: // medium, auto, ""
		return p.Medium, true
	}
}

// ComputeCost returns estimated USD costs for one turn.
// Local providers (lm_studio, ollama, atlas_engine) always return $0, known=true.
// Unknown cloud models return $0, known=false so the caller can log a warning.
func ComputeCost(provider, model string, inputTokens, outputTokens int) (inputCost, outputCost float64, known bool) {
	switch provider {
	case "lm_studio", "ollama", "atlas_engine", "atlas_mlx":
		return 0, 0, true
	}
	price, ok := lookupPrice(model)
	if !ok {
		return 0, 0, false
	}
	inputCost = float64(inputTokens) / 1_000_000.0 * price.InputPerM
	outputCost = float64(outputTokens) / 1_000_000.0 * price.OutputPerM
	return inputCost, outputCost, true
}
