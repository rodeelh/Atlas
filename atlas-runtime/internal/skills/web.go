package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"atlas-runtime-go/internal/creds"
)

func (r *Registry) registerWeb() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.fetch_page",
			Description: "Fetches a web page and returns the text content (first 3000 characters).",
			Properties: map[string]ToolParam{
				"url": {Description: "URL of the page to fetch", Type: "string"},
			},
			Required: []string{"url"},
		},
		PermLevel: "read",
		FnResult:  webFetchPage,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.research",
			Description: "Searches the web and fetches the top N pages, returning a combined summary.",
			Properties: map[string]ToolParam{
				"query":   {Description: "Research query", Type: "string"},
				"sources": {Description: "Number of pages to fetch and summarize (default 3)", Type: "integer"},
			},
			Required: []string{"query"},
		},
		PermLevel: "read",
		FnResult:  webResearch,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.news",
			Description: "Searches for recent news articles on a topic and returns structured results.",
			Properties: map[string]ToolParam{
				"query": {Description: "News topic to search for", Type: "string"},
				"count": {Description: "Number of results to return (default 5, max 10)", Type: "integer"},
			},
			Required: []string{"query"},
		},
		PermLevel: "read",
		FnResult:  webNews,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.check_url",
			Description: "Checks whether a URL is reachable and returns the HTTP status code.",
			Properties: map[string]ToolParam{
				"url": {Description: "URL to check", Type: "string"},
			},
			Required: []string{"url"},
		},
		PermLevel: "read",
		FnResult:  webCheckURL,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.multi_search",
			Description: "Runs 2–5 search queries in parallel and returns combined structured results.",
			Properties: map[string]ToolParam{
				"queries": {Description: "Comma-separated list of search queries (2–5)", Type: "string"},
				"count":   {Description: "Results per query (default 3)", Type: "integer"},
			},
			Required: []string{"queries"},
		},
		PermLevel: "read",
		FnResult:  webMultiSearch,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.extract_links",
			Description: "Fetches a web page and extracts all outbound hyperlinks.",
			Properties: map[string]ToolParam{
				"url":   {Description: "URL to extract links from", Type: "string"},
				"limit": {Description: "Maximum number of links to return (default 20)", Type: "integer"},
			},
			Required: []string{"url"},
		},
		PermLevel: "read",
		Fn:        webExtractLinks,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.summarize_url",
			Description: "Fetches a web page and returns a structured summary with metadata and key points.",
			Properties: map[string]ToolParam{
				"url": {Description: "URL to summarize", Type: "string"},
			},
			Required: []string{"url"},
		},
		PermLevel: "read",
		FnResult:  webSummarizeURL,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.search_latest",
			Description: "Search the web for the latest information on a topic.",
			Properties: map[string]ToolParam{
				"query": {Description: "Topic or question to search", Type: "string"},
				"count": {Description: "Number of results to return (default 5, max 10)", Type: "integer"},
			},
			Required: []string{"query"},
		},
		PermLevel: "read",
		FnResult:  webSearchLatest,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.search_docs",
			Description: "Search documentation and official sources for a topic.",
			Properties: map[string]ToolParam{
				"query":   {Description: "Documentation topic to search", Type: "string"},
				"site":    {Description: "Optional site or docs hostname to focus on", Type: "string"},
				"domains": {Description: "Optional domains to prefer, comma-separated", Type: "string"},
				"count":   {Description: "Number of results to return (default 5, max 10)", Type: "integer"},
			},
			Required: []string{"query"},
		},
		PermLevel: "read",
		FnResult:  webSearchDocs,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "web.search_entities",
			Description: "Search for entities such as companies, people, products, or places and return structured sources.",
			Properties: map[string]ToolParam{
				"query": {Description: "Entity name or lookup phrase", Type: "string"},
				"count": {Description: "Number of results to return (default 5, max 10)", Type: "integer"},
			},
			Required: []string{"query"},
		},
		PermLevel: "read",
		FnResult:  webSearchEntities,
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
	whitespaceRe = regexp.MustCompile(`\s+`)
	titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	metaTagRe = regexp.MustCompile(`(?is)<meta\s+[^>]*(?:name|property)\s*=\s*["']([^"']+)["'][^>]*content\s*=\s*["']([^"']*)["'][^>]*>`)
	metaTagReAlt = regexp.MustCompile(`(?is)<meta\s+[^>]*content\s*=\s*["']([^"']*)["'][^>]*(?:name|property)\s*=\s*["']([^"']+)["'][^>]*>`)
	linkCanonicalRe = regexp.MustCompile(`(?is)<link\s+[^>]*rel\s*=\s*["'][^"']*canonical[^"']*["'][^>]*href\s*=\s*["']([^"']+)["'][^>]*>`)
	headingRe = regexp.MustCompile(`(?is)<h([12])[^>]*>(.*?)</h[12]>`)
	braveSearchURL = "https://api.search.brave.com/res/v1/web/search"
	openAIWebSearchURL = "https://api.openai.com/v1/responses"
	readCredsBundle = creds.Read
	searchCacheTTLDefault = 45 * time.Minute
	searchCacheTTLLatest = 10 * time.Minute
	searchCacheTTLDocs = 6 * time.Hour
	searchCache = struct {
		sync.Mutex
		items map[string]cachedSearchResponse
	}{items: map[string]cachedSearchResponse{}}
)

type SearchOptions struct {
	Query     string
	Count     int
	Freshness string
	Site      string
	Domains   []string
	Language  string
	SearchType string
}

type SearchResult struct {
	Rank        int    `json:"rank"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Domain      string `json:"domain"`
	Snippet     string `json:"snippet"`
	Provider    string `json:"provider"`
	SourceType  string `json:"source_type"`
	Confidence  string `json:"confidence"`
	PublishedAt string `json:"published_at,omitempty"`
	CanonicalURL string `json:"canonical_url,omitempty"`
}

type SearchResponse struct {
	Query     string         `json:"query"`
	Provider  string         `json:"provider"`
	Filters   map[string]any `json:"filters,omitempty"`
	Results   []SearchResult `json:"results"`
	FetchedAt string         `json:"fetched_at"`
	Cached    bool           `json:"cached,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

type cachedSearchResponse struct {
	expiresAt time.Time
	response  SearchResponse
}

// stripHTML removes HTML tags and condenses whitespace.
func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = whitespaceRe.ReplaceAllString(s, " ")
	// Remove non-printable chars.
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) || r == '\n' {
			return r
		}
		return -1
	}, s)
	return strings.TrimSpace(s)
}

type braveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func normaliseSearchOptions(opts SearchOptions) SearchOptions {
	opts.Query = strings.TrimSpace(opts.Query)
	opts.Site = strings.TrimSpace(opts.Site)
	opts.Language = strings.TrimSpace(opts.Language)
	opts.Freshness = strings.TrimSpace(strings.ToLower(opts.Freshness))
	opts.SearchType = strings.TrimSpace(strings.ToLower(opts.SearchType))
	if opts.Count <= 0 {
		opts.Count = 5
	}
	if opts.Count > 10 {
		opts.Count = 10
	}
	if opts.SearchType == "" {
		opts.SearchType = "web"
	}
	domains := make([]string, 0, len(opts.Domains))
	seen := map[string]bool{}
	for _, d := range opts.Domains {
		d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(d, "www.")))
		d = strings.TrimPrefix(d, "https://")
		d = strings.TrimPrefix(d, "http://")
		d = strings.TrimSuffix(d, "/")
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		domains = append(domains, d)
	}
	sort.Strings(domains)
	opts.Domains = domains
	return opts
}

func searchFiltersArtifact(opts SearchOptions) map[string]any {
	filters := map[string]any{
		"count": opts.Count,
		"type":  opts.SearchType,
	}
	if opts.Freshness != "" {
		filters["freshness"] = opts.Freshness
	}
	if opts.Site != "" {
		filters["site"] = opts.Site
	}
	if len(opts.Domains) > 0 {
		filters["domains"] = opts.Domains
	}
	if opts.Language != "" {
		filters["language"] = opts.Language
	}
	return filters
}

func searchCacheKey(opts SearchOptions) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(opts.Query)),
		fmt.Sprintf("count=%d", opts.Count),
		"type=" + opts.SearchType,
		"freshness=" + opts.Freshness,
		"site=" + strings.ToLower(opts.Site),
		"lang=" + strings.ToLower(opts.Language),
	}
	if len(opts.Domains) > 0 {
		parts = append(parts, "domains="+strings.Join(opts.Domains, ","))
	}
	return strings.Join(parts, "|")
}

func searchCacheTTL(opts SearchOptions) time.Duration {
	switch opts.SearchType {
	case "news", "latest":
		return searchCacheTTLLatest
	case "docs":
		return searchCacheTTLDocs
	default:
		return searchCacheTTLDefault
	}
}

func loadSearchCache(key string) (SearchResponse, bool) {
	searchCache.Lock()
	defer searchCache.Unlock()
	entry, ok := searchCache.items[key]
	if !ok {
		return SearchResponse{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(searchCache.items, key)
		return SearchResponse{}, false
	}
	resp := entry.response
	resp.Cached = true
	return resp, true
}

func storeSearchCache(key string, ttl time.Duration, resp SearchResponse) {
	searchCache.Lock()
	defer searchCache.Unlock()
	searchCache.items[key] = cachedSearchResponse{
		expiresAt: time.Now().Add(ttl),
		response:  resp,
	}
}

func resetSearchCache() {
	searchCache.Lock()
	defer searchCache.Unlock()
	searchCache.items = map[string]cachedSearchResponse{}
}

func searchSummary(noun, query string, count int, provider string, cached bool) string {
	summary := fmt.Sprintf("%s for %q returned %d result(s).", noun, query, count)
	if provider != "" {
		summary += " Provider: " + provider + "."
	}
	if cached {
		summary += " Served from cache."
	}
	return summary
}

func searchResultArtifact(result SearchResult) map[string]any {
	out := map[string]any{
		"rank":       result.Rank,
		"title":      result.Title,
		"url":        result.URL,
		"domain":     result.Domain,
		"snippet":    result.Snippet,
		"provider":   result.Provider,
		"source_type": result.SourceType,
		"confidence": result.Confidence,
	}
	if result.PublishedAt != "" {
		out["published_at"] = result.PublishedAt
	}
	if result.CanonicalURL != "" {
		out["canonical_url"] = result.CanonicalURL
	}
	return out
}

func webDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
}

func webSourceKind(rawURL string) string {
	host := webDomain(rawURL)
	switch {
	case host == "":
		return "unknown"
	case strings.Contains(host, "wikipedia.org"), strings.Contains(host, "reddit.com"), strings.Contains(host, "medium.com"), strings.Contains(host, "substack.com"):
		return "secondary"
	case strings.Contains(host, "google."), strings.Contains(host, "bing.com"), strings.Contains(host, "search.brave.com"):
		return "aggregator"
	default:
		return "official_candidate"
	}
}

func webConfidence(status int, rawURL, preview string) string {
	if status >= 200 && status < 300 && webSourceKind(rawURL) == "official_candidate" && len(strings.TrimSpace(preview)) >= 120 {
		return "high"
	}
	if status >= 200 && status < 400 {
		return "medium"
	}
	return "low"
}

func compactPreview(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return text
}

func webSourceArtifact(rawURL, title, preview string, status int) map[string]any {
	return map[string]any{
		"url":         rawURL,
		"domain":      webDomain(rawURL),
		"title":       compactPreview(title, 80),
		"source_type": webSourceKind(rawURL),
		"confidence":  webConfidence(status, rawURL, preview),
		"status":      status,
	}
}

func searchQueryForProvider(opts SearchOptions) string {
	query := strings.TrimSpace(opts.Query)
	switch opts.SearchType {
	case "docs":
		query = query + " documentation OR docs OR reference OR api"
	case "entities":
		query = query + " official site facts"
	}
	if opts.Site != "" {
		query += " site:" + opts.Site
	}
	for _, domain := range opts.Domains {
		query += " site:" + domain
	}
	return strings.TrimSpace(query)
}

func braveFreshnessParam(freshness string) string {
	switch strings.ToLower(strings.TrimSpace(freshness)) {
	case "day", "24h", "latest":
		return "pd"
	case "week", "7d":
		return "pw"
	case "month", "30d":
		return "pm"
	case "year":
		return "py"
	default:
		return ""
	}
}

// braveSearch calls the Brave Search API.
func braveSearch(ctx context.Context, apiKey string, opts SearchOptions) ([]SearchResult, error) {
	u := fmt.Sprintf("%s?q=%s&count=%d",
		braveSearchURL,
		url.QueryEscape(searchQueryForProvider(opts)), opts.Count)
	if freshness := braveFreshnessParam(opts.Freshness); freshness != "" {
		u += "&freshness=" + url.QueryEscape(freshness)
	}
	if opts.Language != "" {
		u += "&search_lang=" + url.QueryEscape(opts.Language)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := newWebClient(15 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("brave search error %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("brave search parse failed: %w", err)
	}

	results := make([]SearchResult, 0, len(data.Web.Results))
	for i, r := range data.Web.Results {
		result := SearchResult{
			Rank:       i + 1,
			Title:      compactPreview(strings.TrimSpace(r.Title), 120),
			URL:        strings.TrimSpace(r.URL),
			Domain:     webDomain(r.URL),
			Snippet:    compactPreview(strings.TrimSpace(r.Description), 220),
			Provider:   "brave",
			SourceType: webSourceKind(r.URL),
			Confidence: webConfidence(http.StatusOK, r.URL, r.Description),
		}
		if result.URL == "" {
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func searchWebWithFallback(ctx context.Context, opts SearchOptions) (SearchResponse, error) {
	opts = normaliseSearchOptions(opts)
	if opts.Query == "" {
		return SearchResponse{}, fmt.Errorf("query is required")
	}
	cacheKey := searchCacheKey(opts)
	if cached, ok := loadSearchCache(cacheKey); ok {
		return cached, nil
	}

	bundle, _ := readCredsBundle()
	var failures []string
	resp := SearchResponse{
		Query:     opts.Query,
		Filters:   searchFiltersArtifact(opts),
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if strings.TrimSpace(bundle.BraveSearchAPIKey) != "" {
		results, err := braveSearch(ctx, bundle.BraveSearchAPIKey, opts)
		if err == nil {
			resp.Provider = "brave"
			resp.Results = results
			storeSearchCache(cacheKey, searchCacheTTL(opts), resp)
			return resp, nil
		}
		failures = append(failures, "Brave Search failed: "+err.Error())
	}

	if strings.TrimSpace(bundle.OpenAIAPIKey) != "" {
		results, err := openAIWebSearch(ctx, bundle.OpenAIAPIKey, opts)
		if err == nil {
			resp.Provider = "openai"
			resp.Results = results
			if len(failures) > 0 {
				resp.Warnings = append(resp.Warnings, failures...)
			}
			storeSearchCache(cacheKey, searchCacheTTL(opts), resp)
			return resp, nil
		}
		failures = append(failures, "OpenAI web search fallback failed: "+err.Error())
	}

	if len(failures) == 0 {
		return SearchResponse{}, fmt.Errorf("web search is unavailable: configure a Brave Search or OpenAI API key in Settings → Credentials")
	}
	return SearchResponse{}, fmt.Errorf("web search is unavailable: %s", strings.Join(failures, "; "))
}

func openAIWebSearch(ctx context.Context, apiKey string, opts SearchOptions) ([]SearchResult, error) {
	prompt := fmt.Sprintf(
		"Search the web for %q and return only JSON in the form {\"results\":[{\"title\":\"...\",\"url\":\"...\",\"description\":\"...\",\"published_at\":\"...\"}]}. Return up to %d results and keep each description under 160 characters.",
		searchQueryForProvider(opts), opts.Count,
	)
	if opts.Freshness != "" {
		prompt += " Prefer results matching freshness=" + opts.Freshness + "."
	}
	if opts.SearchType == "docs" {
		prompt += " Prefer official documentation, API references, and vendor docs over blogs."
	}
	if opts.SearchType == "news" || opts.SearchType == "latest" {
		prompt += " Prefer current reporting and recent updates."
	}
	body := map[string]any{
		"model": "gpt-5-mini",
		"reasoning": map[string]any{
			"effort": "low",
		},
		"tools": []map[string]any{
			{"type": "web_search"},
		},
		"include": []string{"web_search_call.action.sources"},
		"input": prompt,
		"max_output_tokens": 900,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai fallback marshal failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openAIWebSearchURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("openai fallback request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := newWebClient(20 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai fallback request failed: %w", err)
	}
	defer resp.Body.Close()

	var data struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Type   string `json:"type"`
			Action struct {
				Sources []struct {
					Title string `json:"title"`
					URL   string `json:"url"`
				} `json:"sources"`
			} `json:"action"`
		} `json:"output"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("openai fallback parse failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := ""
		if data.Error != nil {
			msg = strings.TrimSpace(data.Error.Message)
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("openai fallback error %d: %s", resp.StatusCode, msg)
	}
	if data.Error != nil && strings.TrimSpace(data.Error.Message) != "" {
		return nil, fmt.Errorf("openai fallback error: %s", strings.TrimSpace(data.Error.Message))
	}

	results := parseOpenAIWebSearchResults(data.OutputText, opts.Count)
	if len(results) > 0 {
		return results, nil
	}

	seen := map[string]bool{}
	for _, item := range data.Output {
		for _, src := range item.Action.Sources {
			rawURL := strings.TrimSpace(src.URL)
			if rawURL == "" || seen[rawURL] {
				continue
			}
			seen[rawURL] = true
			results = append(results, SearchResult{
				Rank:       len(results) + 1,
				Title:      compactPreview(strings.TrimSpace(src.Title), 120),
				URL:        rawURL,
				Domain:     webDomain(rawURL),
				Snippet:    "",
				Provider:   "openai",
				SourceType: webSourceKind(rawURL),
				Confidence: "medium",
			})
			if len(results) >= opts.Count {
				return results, nil
			}
		}
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("openai fallback returned no results")
	}
	return results, nil
}

func parseOpenAIWebSearchResults(output string, limit int) []SearchResult {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	if strings.HasPrefix(output, "```") {
		output = strings.TrimPrefix(output, "```json")
		output = strings.TrimPrefix(output, "```")
		output = strings.TrimSuffix(output, "```")
		output = strings.TrimSpace(output)
	}

	var parsed struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			PublishedAt string `json:"published_at"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		start := strings.IndexByte(output, '{')
		end := strings.LastIndexByte(output, '}')
		if start < 0 || end <= start {
			return nil
		}
		if err := json.Unmarshal([]byte(output[start:end+1]), &parsed); err != nil {
			return nil
		}
	}

	results := make([]SearchResult, 0, min(len(parsed.Results), limit))
	for i, item := range parsed.Results {
		rawURL := strings.TrimSpace(item.URL)
		if rawURL == "" {
			continue
		}
		results = append(results, SearchResult{
			Rank:        i + 1,
			Title:       compactPreview(strings.TrimSpace(item.Title), 120),
			URL:         rawURL,
			Domain:      webDomain(rawURL),
			Snippet:     compactPreview(strings.TrimSpace(item.Description), 220),
			Provider:    "openai",
			SourceType:  webSourceKind(rawURL),
			Confidence:  "medium",
			PublishedAt: strings.TrimSpace(item.PublishedAt),
		})
		if len(results) >= limit {
			break
		}
	}
	return results
}

func extractMetaTags(html string) map[string]string {
	out := map[string]string{}
	for _, m := range metaTagRe.FindAllStringSubmatch(html, -1) {
		if len(m) < 3 {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(m[1]))] = strings.TrimSpace(m[2])
	}
	for _, m := range metaTagReAlt.FindAllStringSubmatch(html, -1) {
		if len(m) < 3 {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(m[2]))] = strings.TrimSpace(m[1])
	}
	return out
}

func extractTitleFromHTML(html string, meta map[string]string) string {
	for _, key := range []string{"og:title", "twitter:title"} {
		if v := strings.TrimSpace(meta[key]); v != "" {
			return compactPreview(v, 160)
		}
	}
	if m := titleRe.FindStringSubmatch(html); len(m) > 1 {
		return compactPreview(stripHTML(m[1]), 160)
	}
	return ""
}

func extractCanonicalURL(html, pageURL string) string {
	if m := linkCanonicalRe.FindStringSubmatch(html); len(m) > 1 {
		if canon := strings.TrimSpace(m[1]); canon != "" {
			if base, err := url.Parse(pageURL); err == nil {
				if ref, err := url.Parse(canon); err == nil {
					return base.ResolveReference(ref).String()
				}
			}
			return canon
		}
	}
	return ""
}

func extractHeadingsFromHTML(html string, limit int) []string {
	matches := headingRe.FindAllStringSubmatch(html, -1)
	out := make([]string, 0, min(len(matches), limit))
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		h := compactPreview(stripHTML(m[2]), 120)
		if h == "" {
			continue
		}
		out = append(out, h)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func splitPreviewChunks(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	const chunkSize = 340
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return nil
	}
	var chunks []string
	var current strings.Builder
	for _, part := range parts {
		if current.Len() > 0 && current.Len()+1+len(part) > chunkSize {
			chunks = append(chunks, current.String())
			current.Reset()
			if len(chunks) >= limit {
				break
			}
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(part)
	}
	if current.Len() > 0 && len(chunks) < limit {
		chunks = append(chunks, current.String())
	}
	return chunks
}

// ── web.fetch_page ────────────────────────────────────────────────────────────

func webFetchPage(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.URL == "" {
		return ToolResult{}, fmt.Errorf("url is required")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if err != nil {
		return ToolResult{}, fmt.Errorf("invalid URL: %w", err)
	}
	req.Header.Set("User-Agent", "Atlas/1.0 (compatible; Go HTTP client)")

	resp, err := newWebClient(10 * time.Second).Do(req)
	if err != nil {
		return ToolResult{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 200*1024)) // 200KB limit
	if err != nil {
		return ToolResult{}, fmt.Errorf("read failed: %w", err)
	}

	html := string(bodyBytes)
	meta := extractMetaTags(html)
	title := extractTitleFromHTML(html, meta)
	description := compactPreview(strings.TrimSpace(firstNonEmpty(
		meta["description"],
		meta["og:description"],
		meta["twitter:description"],
	)), 220)
	canonicalURL := extractCanonicalURL(html, p.URL)
	publishedAt := strings.TrimSpace(firstNonEmpty(
		meta["article:published_time"],
		meta["published_time"],
		meta["date"],
		meta["datepublished"],
		meta["og:published_time"],
	))
	byline := compactPreview(strings.TrimSpace(firstNonEmpty(
		meta["author"],
		meta["article:author"],
	)), 120)
	headings := extractHeadingsFromHTML(html, 6)
	text := stripHTML(html)
	if len(text) > 3000 {
		text = text[:3000] + "... [truncated]"
	}
	preview := compactPreview(text, 320)
	chunks := splitPreviewChunks(text, 3)
	source := webSourceArtifact(p.URL, title, preview, resp.StatusCode)
	if canonicalURL != "" {
		source["canonical_url"] = canonicalURL
	}
	if publishedAt != "" {
		source["published_at"] = publishedAt
	}
	return OKResult(
		fmt.Sprintf("Fetched %s (HTTP %d, confidence %s).", p.URL, resp.StatusCode, webConfidence(resp.StatusCode, p.URL, preview)),
		map[string]any{
			"source":       source,
			"preview":      preview,
			"title":        title,
			"description":  description,
			"headings":     headings,
			"chunks":       chunks,
			"byline":       byline,
			"published_at": publishedAt,
			"canonical_url": canonicalURL,
			"fetched_at":   time.Now().UTC().Format(time.RFC3339),
		},
	), nil
}

// ── web.research ──────────────────────────────────────────────────────────────

func webResearch(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Query   string `json:"query"`
		Sources int    `json:"sources"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Query == "" {
		return ToolResult{}, fmt.Errorf("query is required")
	}
	if p.Sources <= 0 {
		p.Sources = 3
	}

	searchResp, err := searchWebWithFallback(ctx, SearchOptions{
		Query: p.Query,
		Count: p.Sources,
		SearchType: "web",
	})
	if err != nil {
		return ToolResult{}, err
	}

	if len(searchResp.Results) == 0 {
		return OKResult("No results found for: "+p.Query, map[string]any{
			"query":      p.Query,
			"provider":   searchResp.Provider,
			"filters":    searchResp.Filters,
			"results":    []map[string]any{},
			"sources":    []map[string]any{},
			"fetched_at": searchResp.FetchedAt,
		}), nil
	}

	sources := make([]map[string]any, 0, min(len(searchResp.Results), p.Sources))
	keyPoints := make([]string, 0, min(len(searchResp.Results), p.Sources))
	enrichedResults := make([]map[string]any, 0, min(len(searchResp.Results), p.Sources))
	highConfidence := false

	for i, r := range searchResp.Results {
		if i >= p.Sources {
			break
		}
		pageArgs, _ := json.Marshal(map[string]string{"url": r.URL})
		pageResult, err := webFetchPage(ctx, pageArgs)
		if err != nil {
			sources = append(sources, map[string]any{
				"url":         r.URL,
				"title":       compactPreview(r.Title, 80),
				"source_type": webSourceKind(r.URL),
				"confidence":  "low",
				"error":       compactPreview(err.Error(), 120),
			})
			enrichedResults = append(enrichedResults, searchResultArtifact(r))
			continue
		}
		source := webSourceArtifact(r.URL, r.Title, r.Snippet, 200)
		if data, ok := pageResult.Artifacts["source"].(map[string]any); ok {
			source = data
			if title := compactPreview(r.Title, 80); title != "" {
				source["title"] = title
			}
			if snippet := compactPreview(r.Snippet, 220); snippet != "" {
				source["snippet"] = snippet
			}
		}
		if preview, ok := pageResult.Artifacts["preview"].(string); ok && preview != "" {
			keyPoints = append(keyPoints, preview)
		} else if r.Snippet != "" {
			keyPoints = append(keyPoints, compactPreview(r.Snippet, 160))
		}
		if source["confidence"] == "high" {
			highConfidence = true
		}
		enriched := searchResultArtifact(r)
		if title, ok := pageResult.Artifacts["title"].(string); ok && title != "" {
			enriched["resolved_title"] = title
		}
		if publishedAt, ok := pageResult.Artifacts["published_at"].(string); ok && publishedAt != "" {
			enriched["published_at"] = publishedAt
		}
		if preview, ok := pageResult.Artifacts["preview"].(string); ok && preview != "" {
			enriched["content_preview"] = preview
		}
		enrichedResults = append(enrichedResults, enriched)
		sources = append(sources, source)
	}
	summary := fmt.Sprintf("Researched %q using %d source(s).", p.Query, len(sources))
	if searchResp.Provider == "openai" {
		summary += " Used the OpenAI web-search fallback."
	}
	if highConfidence {
		summary += " Includes at least one high-confidence source."
	}
	return OKResult(summary, map[string]any{
		"query":      p.Query,
		"provider":   searchResp.Provider,
		"filters":    searchResp.Filters,
		"confidence": map[bool]string{true: "high", false: "medium"}[highConfidence],
		"results":    enrichedResults,
		"sources":    sources,
		"key_points": compactToolList(keyPoints, 3, 160),
		"fetched_at": searchResp.FetchedAt,
	}), nil
}

// ── web.news ──────────────────────────────────────────────────────────────────

func webNews(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Query == "" {
		return ToolResult{}, fmt.Errorf("query is required")
	}
	searchResp, err := searchWebWithFallback(ctx, SearchOptions{
		Query: p.Query,
		Count: p.Count,
		Freshness: "day",
		SearchType: "news",
	})
	if err != nil {
		return ToolResult{}, err
	}

	if len(searchResp.Results) == 0 {
		return OKResult("No recent news found for: "+p.Query, map[string]any{
			"query":      p.Query,
			"provider":   searchResp.Provider,
			"filters":    searchResp.Filters,
			"results":    []map[string]any{},
			"fetched_at": searchResp.FetchedAt,
		}), nil
	}
	results := make([]map[string]any, 0, len(searchResp.Results))
	for _, r := range searchResp.Results {
		results = append(results, searchResultArtifact(r))
	}
	return OKResult(
		searchSummary("News search", p.Query, len(searchResp.Results), searchResp.Provider, searchResp.Cached),
		map[string]any{
			"query":      p.Query,
			"provider":   searchResp.Provider,
			"filters":    searchResp.Filters,
			"results":    results,
			"fetched_at": searchResp.FetchedAt,
		},
	), nil
}

// ── web.check_url ─────────────────────────────────────────────────────────────

func webCheckURL(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.URL == "" {
		return ToolResult{}, fmt.Errorf("url is required")
	}

	req, err := http.NewRequestWithContext(ctx, "HEAD", p.URL, nil)
	if err != nil {
		return ToolResult{}, fmt.Errorf("invalid URL: %w", err)
	}
	req.Header.Set("User-Agent", "Atlas/1.0 (url-checker)")

	resp, err := newWebClient(10 * time.Second).Do(req)
	if err != nil {
		return OKResult(fmt.Sprintf("%s is unreachable.", p.URL), map[string]any{
			"source": map[string]any{
				"url":         p.URL,
				"domain":      webDomain(p.URL),
				"source_type": webSourceKind(p.URL),
				"confidence":  "low",
				"status":      0,
			},
			"error": compactPreview(err.Error(), 140),
		}), nil
	}
	defer resp.Body.Close()

	return OKResult(
		fmt.Sprintf("%s responded with HTTP %d.", p.URL, resp.StatusCode),
		map[string]any{
			"source": webSourceArtifact(p.URL, "", "", resp.StatusCode),
		},
	), nil
}

// ── web.multi_search ──────────────────────────────────────────────────────────

func webMultiSearch(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Queries string `json:"queries"`
		Count   int    `json:"count"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Queries == "" {
		return ToolResult{}, fmt.Errorf("queries is required")
	}
	if p.Count <= 0 {
		p.Count = 3
	}

	parts := strings.Split(p.Queries, ",")
	if len(parts) > 5 {
		parts = parts[:5]
	}

	type result struct {
		query string
		resp  SearchResponse
		err   error
	}
	ch := make(chan result, len(parts))
	for _, q := range parts {
		q := strings.TrimSpace(q)
		go func() {
			resp, err := searchWebWithFallback(ctx, SearchOptions{Query: q, Count: p.Count, SearchType: "web"})
			ch <- result{query: q, resp: resp, err: err}
		}()
	}

	grouped := make([]map[string]any, 0, len(parts))
	var warnings []string
	for range parts {
		res := <-ch
		if res.err != nil {
			grouped = append(grouped, map[string]any{
				"query": res.query,
				"error": compactPreview(res.err.Error(), 180),
			})
			warnings = append(warnings, fmt.Sprintf("%s: %v", res.query, res.err))
			continue
		}
		results := make([]map[string]any, 0, len(res.resp.Results))
		for _, item := range res.resp.Results {
			results = append(results, searchResultArtifact(item))
		}
		grouped = append(grouped, map[string]any{
			"query":      res.query,
			"provider":   res.resp.Provider,
			"filters":    res.resp.Filters,
			"results":    results,
			"fetched_at": res.resp.FetchedAt,
			"cached":     res.resp.Cached,
		})
	}
	return ToolResult{
		Success:    true,
		Summary:    fmt.Sprintf("Ran multi-search for %d querie(s).", len(parts)),
		Artifacts:  map[string]any{"queries": grouped},
		Warnings:   compactToolList(warnings, 5, 160),
		NextActions: nil,
	}, nil
}

// ── web.extract_links ─────────────────────────────────────────────────────────

var hrefRe = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)

func webExtractLinks(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		URL   string `json:"url"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}

	req, err := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	req.Header.Set("User-Agent", "Atlas/1.0 (link-extractor)")

	resp, err := newWebClient(10 * time.Second).Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 200*1024))
	matches := hrefRe.FindAllSubmatch(bodyBytes, -1)

	seen := map[string]bool{}
	var links []string
	base, _ := url.Parse(p.URL)

	for _, m := range matches {
		href := strings.TrimSpace(string(m[1]))
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") {
			continue
		}
		// Resolve relative URLs
		if ref, err := url.Parse(href); err == nil && base != nil {
			href = base.ResolveReference(ref).String()
		}
		if seen[href] {
			continue
		}
		seen[href] = true
		links = append(links, href)
		if len(links) >= p.Limit {
			break
		}
	}

	if len(links) == 0 {
		return "No links found on " + p.URL, nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Links from %s (%d):\n", p.URL, len(links)))
	for i, l := range links {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, l))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ── web.summarize_url ─────────────────────────────────────────────────────────

func webSummarizeURL(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.URL == "" {
		return ToolResult{}, fmt.Errorf("url is required")
	}
	fetchArgs, _ := json.Marshal(map[string]string{"url": p.URL})
	fetchResult, err := webFetchPage(ctx, fetchArgs)
	if err != nil {
		return ToolResult{}, err
	}
	title, _ := fetchResult.Artifacts["title"].(string)
	preview, _ := fetchResult.Artifacts["preview"].(string)
	headings, _ := fetchResult.Artifacts["headings"].([]string)
	keyPoints := headings
	if len(keyPoints) == 0 && preview != "" {
		keyPoints = splitPreviewChunks(preview, 3)
	}
	summary := fmt.Sprintf("Summarized %s.", p.URL)
	if title != "" {
		summary = fmt.Sprintf("Summarized %s (%s).", title, p.URL)
	}
	artifacts := map[string]any{
		"url":         p.URL,
		"title":       title,
		"summary":     preview,
		"key_points":  compactToolList(keyPoints, 3, 140),
		"source":      fetchResult.Artifacts["source"],
		"headings":    fetchResult.Artifacts["headings"],
		"chunks":      fetchResult.Artifacts["chunks"],
		"description": fetchResult.Artifacts["description"],
		"published_at": fetchResult.Artifacts["published_at"],
		"canonical_url": fetchResult.Artifacts["canonical_url"],
	}
	return OKResult(summary, artifacts), nil
}

func webSearchLatest(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Query) == "" {
		return ToolResult{}, fmt.Errorf("query is required")
	}
	resp, err := searchWebWithFallback(ctx, SearchOptions{Query: p.Query, Count: p.Count, SearchType: "latest", Freshness: "day"})
	if err != nil {
		return ToolResult{}, err
	}
	results := make([]map[string]any, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, searchResultArtifact(item))
	}
	return OKResult(searchSummary("Latest search", p.Query, len(resp.Results), resp.Provider, resp.Cached), map[string]any{
		"query": p.Query, "provider": resp.Provider, "filters": resp.Filters, "results": results, "fetched_at": resp.FetchedAt,
	}), nil
}

func webSearchDocs(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Query   string `json:"query"`
		Site    string `json:"site"`
		Domains string `json:"domains"`
		Count   int    `json:"count"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Query) == "" {
		return ToolResult{}, fmt.Errorf("query is required")
	}
	resp, err := searchWebWithFallback(ctx, SearchOptions{
		Query: p.Query, Count: p.Count, SearchType: "docs", Site: p.Site, Domains: splitQueryList(p.Domains),
	})
	if err != nil {
		return ToolResult{}, err
	}
	results := make([]map[string]any, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, searchResultArtifact(item))
	}
	return OKResult(searchSummary("Documentation search", p.Query, len(resp.Results), resp.Provider, resp.Cached), map[string]any{
		"query": p.Query, "provider": resp.Provider, "filters": resp.Filters, "results": results, "fetched_at": resp.FetchedAt,
	}), nil
}

func webSearchEntities(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Query) == "" {
		return ToolResult{}, fmt.Errorf("query is required")
	}
	resp, err := searchWebWithFallback(ctx, SearchOptions{Query: p.Query, Count: p.Count, SearchType: "entities"})
	if err != nil {
		return ToolResult{}, err
	}
	results := make([]map[string]any, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, searchResultArtifact(item))
	}
	return OKResult(searchSummary("Entity search", p.Query, len(resp.Results), resp.Provider, resp.Cached), map[string]any{
		"query": p.Query, "provider": resp.Provider, "filters": resp.Filters, "results": results, "fetched_at": resp.FetchedAt,
	}), nil
}

func splitQueryList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
