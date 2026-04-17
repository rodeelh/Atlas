package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"atlas-runtime-go/internal/creds"
)

func TestWebFetchPageReturnsStructuredResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
			<html>
				<head>
					<title>Atlas Fetch Test</title>
					<meta name="description" content="Structured web fetch test content.">
					<link rel="canonical" href="/canonical">
				</head>
				<body>
					<h1>Atlas</h1>
					<h2>Overview</h2>
					<p>Structured web fetch test content with enough text to produce a useful preview chunk for downstream consumers.</p>
				</body>
			</html>`))
	}))
	defer srv.Close()

	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := webFetchPage(context.Background(), args)
	if err != nil {
		t.Fatalf("webFetchPage: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success result: %+v", res)
	}
	source, ok := res.Artifacts["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected structured source artifact, got %+v", res.Artifacts["source"])
	}
	if source["url"] != srv.URL {
		t.Fatalf("source.url = %v", source["url"])
	}
	if title, _ := res.Artifacts["title"].(string); title != "Atlas Fetch Test" {
		t.Fatalf("title = %q", title)
	}
	headings, _ := res.Artifacts["headings"].([]string)
	if len(headings) < 2 || headings[0] != "Atlas" {
		t.Fatalf("headings = %#v", headings)
	}
	if preview, _ := res.Artifacts["preview"].(string); !strings.Contains(preview, "Structured web fetch test content") {
		t.Fatalf("preview missing fetched content: %q", preview)
	}
}

func TestWebCheckURLReturnsStructuredResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := webCheckURL(context.Background(), args)
	if err != nil {
		t.Fatalf("webCheckURL: %v", err)
	}
	source, ok := res.Artifacts["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected structured source artifact, got %+v", res.Artifacts["source"])
	}
	if source["status"] != http.StatusNoContent {
		t.Fatalf("status = %v", source["status"])
	}
}

func TestSearchWebWithFallbackUsesOpenAIWhenBraveFails(t *testing.T) {
	origBraveURL := braveSearchURL
	origOpenAIURL := openAIWebSearchURL
	origReadCreds := readCredsBundle
	t.Cleanup(func() {
		braveSearchURL = origBraveURL
		openAIWebSearchURL = origOpenAIURL
		readCredsBundle = origReadCreds
		resetSearchCache()
	})

	braveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"upstream unavailable"}`, http.StatusBadGateway)
	}))
	defer braveSrv.Close()
	braveSearchURL = braveSrv.URL

	openAISrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer openai-key" {
			t.Fatalf("Authorization header = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		tools, _ := body["tools"].([]any)
		if len(tools) == 0 {
			t.Fatalf("expected web_search tool in request body")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output_text": `{"results":[{"title":"OpenAI Responses API","url":"https://platform.openai.com/docs/api-reference/responses","description":"Official API reference."}]}`,
		})
	}))
	defer openAISrv.Close()
	openAIWebSearchURL = openAISrv.URL

	readCredsBundle = func() (creds.Bundle, error) {
		return creds.Bundle{
			BraveSearchAPIKey: "brave-key",
			OpenAIAPIKey:      "openai-key",
		}, nil
	}

	resp, err := searchWebWithFallback(context.Background(), SearchOptions{Query: "openai responses api", Count: 3})
	if err != nil {
		t.Fatalf("searchWebWithFallback: %v", err)
	}
	if resp.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", resp.Provider)
	}
	if len(resp.Results) != 1 || resp.Results[0].URL != "https://platform.openai.com/docs/api-reference/responses" {
		t.Fatalf("unexpected results: %+v", resp.Results)
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("expected brave failure warning in fallback response")
	}
}

func TestSearchWebWithFallbackCachesResponses(t *testing.T) {
	origBraveURL := braveSearchURL
	origReadCreds := readCredsBundle
	t.Cleanup(func() {
		braveSearchURL = origBraveURL
		readCredsBundle = origReadCreds
		resetSearchCache()
	})

	hits := 0
	braveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{
				"results": []map[string]any{
					{"title": "Atlas", "url": "https://atlas.example", "description": "Atlas result"},
				},
			},
		})
	}))
	defer braveSrv.Close()
	braveSearchURL = braveSrv.URL
	readCredsBundle = func() (creds.Bundle, error) {
		return creds.Bundle{BraveSearchAPIKey: "brave-key"}, nil
	}

	opts := SearchOptions{Query: "atlas", Count: 3, SearchType: "web"}
	first, err := searchWebWithFallback(context.Background(), opts)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	second, err := searchWebWithFallback(context.Background(), opts)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	if first.Cached {
		t.Fatalf("first response should not be cached")
	}
	if !second.Cached {
		t.Fatalf("second response should be cached")
	}
	if hits != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", hits)
	}
}

func TestParseOpenAIWebSearchResultsHandlesCodeFence(t *testing.T) {
	results := parseOpenAIWebSearchResults("```json\n{\"results\":[{\"title\":\"Atlas\",\"url\":\"https://atlas.example\",\"description\":\"Search result.\"}]}\n```", 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Atlas" || results[0].URL != "https://atlas.example" {
		t.Fatalf("unexpected result: %+v", results[0])
	}
}

