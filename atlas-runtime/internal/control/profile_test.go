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

func TestValidateExternalPreviewURLRejectsPrivateHosts(t *testing.T) {
	tests := []string{
		"http://127.0.0.1:8080",
		"http://localhost:8080",
		"http://10.0.0.5",
		"http://169.254.169.254/latest/meta-data",
	}
	for _, rawURL := range tests {
		if err := validateExternalPreviewURL(rawURL); err == nil {
			t.Fatalf("expected %q to be rejected", rawURL)
		}
	}
}

func TestValidateExternalPreviewURLAllowsPublicHost(t *testing.T) {
	if err := validateExternalPreviewURL("https://example.com/path"); err != nil {
		t.Fatalf("expected public URL to pass validation: %v", err)
	}
}
