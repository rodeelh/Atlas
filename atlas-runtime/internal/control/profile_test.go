package control

import (
	"strings"
	"testing"
)

func TestProfileService_UpdatePreferencesNormalizesCurrency(t *testing.T) {
	svc := NewProfileService()
	resp := svc.UpdatePreferences("celsius", "usd", "metric")
	if resp.Currency != "USD" {
		t.Fatalf("expected currency to be uppercased, got %q", resp.Currency)
	}
	if resp.TemperatureUnit != "celsius" || resp.UnitSystem != "metric" {
		t.Fatalf("unexpected preferences response: %+v", resp)
	}
}

func TestExtractHTMLMetaSupportsPropertyAndName(t *testing.T) {
	html := `<html><head>
<meta property="og:title" content="Atlas Title">
<meta name="description" content="Atlas Description">
</head></html>`

	if got := extractHTMLMeta(html, "og:title"); got != "Atlas Title" {
		t.Fatalf("unexpected og:title: %q", got)
	}
	if got := extractHTMLMeta(html, "description"); got != "Atlas Description" {
		t.Fatalf("unexpected description: %q", got)
	}
}

func TestExtractHTMLTitleReadsTitleTag(t *testing.T) {
	html := `<html><head><title> Atlas Page </title></head></html>`
	if got := extractHTMLTitle(html); strings.TrimSpace(got) != "Atlas Page" {
		t.Fatalf("unexpected title: %q", got)
	}
}
