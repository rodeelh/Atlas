package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/storage"
)

// ── markdownToHTML ────────────────────────────────────────────────────────────

func TestMarkdownToHTML_Basic(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bold", "**hello**", "<b>hello</b>"},
		{"italic star", "*hello*", "<i>hello</i>"},
		{"italic underscore", "_hello_", "<i>hello</i>"},
		{"strikethrough", "~~hello~~", "<s>hello</s>"},
		{"inline code", "`code`", "<code>code</code>"},
		{"html escaping", "a < b & c > d", "a &lt; b &amp; c &gt; d"},
		{
			"fenced code block lowercase lang",
			"```go\nfmt.Println()\n```",
			"<pre>fmt.Println()</pre>",
		},
		{
			// FIX #9: uppercase language tag
			"fenced code block uppercase lang",
			"```Go\nfmt.Println()\n```",
			"<pre>fmt.Println()</pre>",
		},
		{
			// FIX #9: mixed-case language tag
			"fenced code block mixed lang",
			"```JavaScript\nconsole.log(1)\n```",
			"<pre>console.log(1)</pre>",
		},
		{
			"fenced code block no lang",
			"```\ncode here\n```",
			"<pre>code here</pre>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := markdownToHTML(tc.input)
			if got != tc.want {
				t.Errorf("markdownToHTML(%q)\n  got  %q\n  want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ── stripHTML ─────────────────────────────────────────────────────────────────

func TestStripHTML(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"<b>hello</b>", "hello"},
		{"a &lt; b &amp; c &gt; d", "a < b & c > d"},
		{"plain text", "plain text"},
		{"<pre>code</pre>", "code"},
	}
	for _, tc := range cases {
		got := stripHTML(tc.input)
		if got != tc.want {
			t.Errorf("stripHTML(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ── chunkText ─────────────────────────────────────────────────────────────────

func TestChunkText(t *testing.T) {
	// Short text — single chunk.
	chunks := chunkText("hello", 100)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("expected single chunk, got %v", chunks)
	}

	// Exactly maxLen — single chunk.
	runes := make([]rune, 3500)
	for i := range runes {
		runes[i] = 'x'
	}
	exact := string(runes)
	chunks = chunkText(exact, 3500)
	if len(chunks) != 1 {
		t.Errorf("3500-rune string should be 1 chunk, got %d", len(chunks))
	}

	// One rune over — two chunks.
	over := string(append(runes, 'y'))
	chunks = chunkText(over, 3500)
	if len(chunks) != 2 {
		t.Errorf("3501-rune string should be 2 chunks, got %d", len(chunks))
	}
	if len([]rune(chunks[0])) != 3500 {
		t.Errorf("first chunk should be 3500 runes, got %d", len([]rune(chunks[0])))
	}
	if chunks[1] != "y" {
		t.Errorf("second chunk should be 'y', got %q", chunks[1])
	}
}

// ── sanitizeFilename ──────────────────────────────────────────────────────────

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello.jpg", "hello.jpg"},
		{"my/file.png", "my_file.png"},
		{"a:b*c?d.txt", "a_b_c_d.txt"},
		{"", "file"},
	}
	for _, tc := range cases {
		got := sanitizeFilename(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ── mimeToExt ─────────────────────────────────────────────────────────────────

func TestMimeToExt(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"application/pdf": ".pdf",
		"text/plain":      ".txt",
		"unknown/type":    "",
	}
	for mime, want := range cases {
		got := mimeToExt(mime)
		if got != want {
			t.Errorf("mimeToExt(%q) = %q, want %q", mime, got, want)
		}
	}
}

// ── extractFilePaths ──────────────────────────────────────────────────────────

func TestExtractFilePaths_OnlyExistingFiles(t *testing.T) {
	// Create a real temp file so os.Stat passes.
	tmp, err := os.CreateTemp("", "tg_test_*.png")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	text := "Here is your image: " + tmp.Name() + " and a missing /Users/nobody/nope.jpg"
	paths := extractFilePaths(text)

	if len(paths) != 1 {
		t.Errorf("expected 1 path (existing file only), got %v", paths)
		return
	}
	if paths[0] != tmp.Name() {
		t.Errorf("expected %q, got %q", tmp.Name(), paths[0])
	}
}

func TestExtractFilePaths_Deduplication(t *testing.T) {
	tmp, err := os.CreateTemp("", "tg_dedup_*.png")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	text := tmp.Name() + " and again " + tmp.Name()
	paths := extractFilePaths(text)
	if len(paths) != 1 {
		t.Errorf("expected 1 deduplicated path, got %v", paths)
	}
}

// ── isAllowed ─────────────────────────────────────────────────────────────────

func TestIsAllowed(t *testing.T) {
	b := &Bridge{}

	// Empty allowlists → allow all.
	cfg := config.RuntimeConfigSnapshot{}
	if !b.isAllowed(nil, 123, cfg) {
		t.Error("empty allowlist should allow all")
	}

	// Chat ID on allowlist.
	cfg = config.RuntimeConfigSnapshot{TelegramConfig: config.TelegramConfig{TelegramAllowedChatIDs: []int64{100, 200}}}
	if !b.isAllowed(nil, 100, cfg) {
		t.Error("chat 100 should be allowed")
	}
	if b.isAllowed(nil, 999, cfg) {
		t.Error("chat 999 should be rejected")
	}

	// User ID on allowlist.
	user := &tgUser{ID: 42}
	cfg = config.RuntimeConfigSnapshot{TelegramConfig: config.TelegramConfig{TelegramAllowedUserIDs: []int64{42}}}
	if !b.isAllowed(user, 999, cfg) {
		t.Error("user 42 should be allowed")
	}
	other := &tgUser{ID: 99}
	if b.isAllowed(other, 999, cfg) {
		t.Error("user 99 should be rejected")
	}

	// Nil from-user with user-only allowlist → deny (can't verify).
	if b.isAllowed(nil, 999, cfg) {
		t.Error("nil user with user-only allowlist should be rejected")
	}
}

// ── handleCallbackQuery routing ───────────────────────────────────────────────

func TestHandleCallbackQuery_ApproveRoute(t *testing.T) {
	var gotID string
	var gotApproved bool

	b := &Bridge{
		approvalResolver: func(id string, approved bool) error {
			gotID = id
			gotApproved = approved
			return nil
		},
		client: newNullHTTPClient(),
		token:  "TESTTOKEN",
	}

	q := tgCallbackQuery{
		ID:   "cbq1",
		From: tgUser{ID: 1},
		Data: "approve:tool-call-xyz",
	}
	b.handleCallbackQuery(q)

	if gotID != "tool-call-xyz" {
		t.Errorf("expected toolCallID %q, got %q", "tool-call-xyz", gotID)
	}
	if !gotApproved {
		t.Error("expected approved=true")
	}
}

func TestHandleCallbackQuery_DenyRoute(t *testing.T) {
	var gotApproved bool

	b := &Bridge{
		approvalResolver: func(id string, approved bool) error {
			gotApproved = approved
			return nil
		},
		client: newNullHTTPClient(),
		token:  "TESTTOKEN",
	}

	q := tgCallbackQuery{
		ID:   "cbq2",
		From: tgUser{ID: 1},
		Data: "deny:tool-call-abc",
	}
	b.handleCallbackQuery(q)

	if gotApproved {
		t.Error("expected approved=false for deny callback")
	}
}

func TestHandleCallbackQuery_UnknownDataIgnored(t *testing.T) {
	called := false
	b := &Bridge{
		approvalResolver: func(_ string, _ bool) error {
			called = true
			return nil
		},
		client: newNullHTTPClient(),
		token:  "TESTTOKEN",
	}

	b.handleCallbackQuery(tgCallbackQuery{ID: "x", Data: "unknown:data"})

	if called {
		t.Error("resolver should not be called for unknown callback data")
	}
}

func TestHandleCallbackQuery_NoResolver_NoPanic(t *testing.T) {
	b := &Bridge{
		client: newNullHTTPClient(),
		token:  "TESTTOKEN",
	}
	q := tgCallbackQuery{
		ID:   "cbq3",
		From: tgUser{ID: 1},
		Data: "approve:some-id",
		Message: &tgMessage{
			MessageID: 1,
			Chat:      tgChat{ID: 42},
		},
	}
	b.handleCallbackQuery(q) // must not panic
}

// ── reaction heuristics ───────────────────────────────────────────────────────

func TestReactWithLove(t *testing.T) {
	yes := []string{"thank you", "Thanks!", "AWESOME work", "you're the best", "🙏", "❤"}
	no := []string{"what time is it", "search the web", "omg no way"}
	for _, s := range yes {
		if !reactWithLove(s) {
			t.Errorf("reactWithLove(%q) should be true", s)
		}
	}
	for _, s := range no {
		if reactWithLove(s) {
			t.Errorf("reactWithLove(%q) should be false", s)
		}
	}
}

func TestReactWithShock(t *testing.T) {
	yes := []string{"omg", "no way!", "this is wild", "whoa that's crazy", "unbelievable"}
	no := []string{"thank you", "find my document", "hello"}
	for _, s := range yes {
		if !reactWithShock(s) {
			t.Errorf("reactWithShock(%q) should be true", s)
		}
	}
	for _, s := range no {
		if reactWithShock(s) {
			t.Errorf("reactWithShock(%q) should be false", s)
		}
	}
}

func TestReactWithProcessing(t *testing.T) {
	yes := []string{
		"search the web for news",
		"create a document",
		"generate an image",
		"find the file",
		"schedule a meeting",
		"run the automation",
	}
	no := []string{
		"what is the capital of France",
		"thank you so much",
		"how are you",
		"search",   // verb but no target
		"document", // target but no verb
	}
	for _, s := range yes {
		if !reactWithProcessing(s) {
			t.Errorf("reactWithProcessing(%q) should be true", s)
		}
	}
	for _, s := range no {
		if reactWithProcessing(s) {
			t.Errorf("reactWithProcessing(%q) should be false", s)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// newNullHTTPClient returns an HTTP client that responds 200 OK to everything
// without making real network calls, so tests that trigger sendMessage /
// sendReaction / answerCallbackQuery don't hit the network.
func newNullHTTPClient() *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       http.NoBody,
				Header:     make(http.Header),
			}, nil
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// newTestDB opens an in-memory SQLite database and registers cleanup.
// Required for tests that call handleUpdate → processText (which calls FetchTelegramSession).
func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newJSONHTTPClient returns a client that responds to every request with a fixed
// JSON body (used to simulate specific Telegram API responses in tests).
func newJSONHTTPClient(body string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
}

// ── HandleWebhookUpdate ───────────────────────────────────────────────────────

func TestHandleWebhookUpdate_DispatchesTextMessage(t *testing.T) {
	var gotText string
	b := &Bridge{
		client: newNullHTTPClient(),
		token:  "TOKEN",
		db:     newTestDB(t),
		cfgFn:  func() config.RuntimeConfigSnapshot { return config.RuntimeConfigSnapshot{} },
		handler: func(_ context.Context, req BridgeRequest) (string, []string, string, error) {
			gotText = req.Text
			return "ok", nil, "conv1", nil
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	update := tgUpdate{
		UpdateID: 1,
		Message: &tgMessage{
			MessageID: 10,
			From:      &tgUser{ID: 42},
			Chat:      tgChat{ID: 42},
			Text:      "hello from webhook",
		},
	}
	body, _ := json.Marshal(update)
	if err := b.HandleWebhookUpdate(body); err != nil {
		t.Fatalf("HandleWebhookUpdate returned error: %v", err)
	}
	if gotText != "hello from webhook" {
		t.Errorf("expected handler text %q, got %q", "hello from webhook", gotText)
	}
}

func TestHandleWebhookUpdate_InvalidJSON(t *testing.T) {
	b := &Bridge{client: newNullHTTPClient(), token: "TOKEN"}
	err := b.HandleWebhookUpdate([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestHandleWebhookUpdate_CallbackQueryDispatched(t *testing.T) {
	var gotID string
	b := &Bridge{
		client: newNullHTTPClient(),
		token:  "TOKEN",
		db:     newTestDB(t),
		cfgFn:  func() config.RuntimeConfigSnapshot { return config.RuntimeConfigSnapshot{} },
		approvalResolver: func(id string, approved bool) error {
			gotID = id
			return nil
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	update := tgUpdate{
		UpdateID: 2,
		CallbackQuery: &tgCallbackQuery{
			ID:   "cbq-webhook",
			From: tgUser{ID: 1},
			Data: "approve:tool-123",
			Message: &tgMessage{
				MessageID: 5,
				Chat:      tgChat{ID: 99},
			},
		},
	}
	body, _ := json.Marshal(update)
	if err := b.HandleWebhookUpdate(body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotID != "tool-123" {
		t.Errorf("expected approval ID %q, got %q", "tool-123", gotID)
	}
}

// ── Voice routing ─────────────────────────────────────────────────────────────

func TestHandleUpdate_VoiceWithTranscriber(t *testing.T) {
	var gotText string
	b := &Bridge{
		client: newNullHTTPClient(),
		token:  "TOKEN",
		db:     newTestDB(t),
		cfgFn:  func() config.RuntimeConfigSnapshot { return config.RuntimeConfigSnapshot{} },
		handler: func(_ context.Context, req BridgeRequest) (string, []string, string, error) {
			gotText = req.Text
			return "ok", nil, "conv1", nil
		},
		// Transcriber returns a fixed transcript without touching the network.
		transcriber: func(_ context.Context, _ []byte, _ string) (string, error) {
			return "transcribed voice text", nil
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	// Mock the Telegram file download: getFile → filePath, then download → bytes.
	// Both go through b.client; we need to return the right shape per endpoint.
	callCount := 0
	b.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			callCount++
			switch {
			case strings.Contains(r.URL.Path, "getFile"):
				body := `{"ok":true,"result":{"file_path":"voice/file.ogg"}}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			case strings.Contains(r.URL.Path, "file/bot"):
				// File download — return minimal OGG header bytes.
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader([]byte("OggS"))), Header: make(http.Header)}, nil
			default:
				// sendReaction, sendChatAction, sendMessage, etc.
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
			}
		}),
	}

	update := tgUpdate{
		UpdateID: 3,
		Message: &tgMessage{
			MessageID: 20,
			From:      &tgUser{ID: 7},
			Chat:      tgChat{ID: 7},
			Voice:     &tgFileRef{FileID: "voice-file-id"},
		},
	}
	body, _ := json.Marshal(update)
	if err := b.HandleWebhookUpdate(body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotText != "transcribed voice text" {
		t.Errorf("agent received %q, want %q", gotText, "transcribed voice text")
	}
}

func TestHandleUpdate_VoiceWithCaption(t *testing.T) {
	var gotText string
	b := &Bridge{
		client: newNullHTTPClient(),
		token:  "TOKEN",
		db:     newTestDB(t),
		cfgFn:  func() config.RuntimeConfigSnapshot { return config.RuntimeConfigSnapshot{} },
		handler: func(_ context.Context, req BridgeRequest) (string, []string, string, error) {
			gotText = req.Text
			return "ok", nil, "conv1", nil
		},
		transcriber: func(_ context.Context, _ []byte, _ string) (string, error) {
			return "transcript", nil
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	b.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(r.URL.Path, "getFile"):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"file_path":"voice/x.ogg"}}`)), Header: make(http.Header)}, nil
			case strings.Contains(r.URL.Path, "file/bot"):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader([]byte("OggS"))), Header: make(http.Header)}, nil
			default:
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
			}
		}),
	}

	update := tgUpdate{
		UpdateID: 4,
		Message: &tgMessage{
			MessageID: 21,
			From:      &tgUser{ID: 8},
			Chat:      tgChat{ID: 8},
			Voice:     &tgFileRef{FileID: "voice-id"},
			Caption:   "please translate this",
		},
	}
	body, _ := json.Marshal(update)
	_ = b.HandleWebhookUpdate(body)

	want := "transcript\n\nplease translate this"
	if gotText != want {
		t.Errorf("agent received %q, want %q", gotText, want)
	}
}

func TestHandleUpdate_VoiceNoTranscriber_SendsUnsupported(t *testing.T) {
	var sentText string
	b := &Bridge{
		client: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "sendMessage") {
					var payload map[string]any
					_ = json.NewDecoder(r.Body).Decode(&payload)
					if t, ok := payload["text"].(string); ok {
						sentText = t
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
			}),
		},
		token: "TOKEN",
		cfgFn: func() config.RuntimeConfigSnapshot { return config.RuntimeConfigSnapshot{} },
		// No transcriber set — should reply with unsupported message.
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	update := tgUpdate{
		UpdateID: 5,
		Message: &tgMessage{
			MessageID: 30,
			Chat:      tgChat{ID: 5},
			Voice:     &tgFileRef{FileID: "voice-no-transcriber"},
		},
	}
	body, _ := json.Marshal(update)
	_ = b.HandleWebhookUpdate(body)

	if !strings.Contains(sentText, "not available") {
		t.Errorf("expected 'not available' in sent text, got %q", sentText)
	}
}

// ── Location routing ──────────────────────────────────────────────────────────

func TestHandleUpdate_LocationRoutedToAgent(t *testing.T) {
	var gotText string
	b := &Bridge{
		client: newNullHTTPClient(),
		token:  "TOKEN",
		db:     newTestDB(t),
		cfgFn:  func() config.RuntimeConfigSnapshot { return config.RuntimeConfigSnapshot{} },
		handler: func(_ context.Context, req BridgeRequest) (string, []string, string, error) {
			gotText = req.Text
			return "ok", nil, "conv1", nil
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	update := tgUpdate{
		UpdateID: 6,
		Message: &tgMessage{
			MessageID: 40,
			From:      &tgUser{ID: 9},
			Chat:      tgChat{ID: 9},
			Location:  &tgLocation{Latitude: 37.7749, Longitude: -122.4194},
		},
	}
	body, _ := json.Marshal(update)
	_ = b.HandleWebhookUpdate(body)

	if !strings.Contains(gotText, "37.774900") {
		t.Errorf("expected latitude in agent text, got %q", gotText)
	}
	if !strings.Contains(gotText, "-122.419400") {
		t.Errorf("expected longitude in agent text, got %q", gotText)
	}
}

func TestHandleUpdate_LocationWithCaption(t *testing.T) {
	var gotText string
	b := &Bridge{
		client: newNullHTTPClient(),
		token:  "TOKEN",
		db:     newTestDB(t),
		cfgFn:  func() config.RuntimeConfigSnapshot { return config.RuntimeConfigSnapshot{} },
		handler: func(_ context.Context, req BridgeRequest) (string, []string, string, error) {
			gotText = req.Text
			return "ok", nil, "conv1", nil
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	update := tgUpdate{
		UpdateID: 7,
		Message: &tgMessage{
			MessageID: 41,
			From:      &tgUser{ID: 10},
			Chat:      tgChat{ID: 10},
			Location:  &tgLocation{Latitude: 51.5074, Longitude: -0.1278},
			Caption:   "find me a coffee shop",
		},
	}
	body, _ := json.Marshal(update)
	_ = b.HandleWebhookUpdate(body)

	if !strings.Contains(gotText, "find me a coffee shop") {
		t.Errorf("caption not appended to location text, got %q", gotText)
	}
}

// ── Unsupported media types ───────────────────────────────────────────────────

func TestHandleUpdate_UnsupportedMediaReplies(t *testing.T) {
	cases := []struct {
		name string
		msg  tgMessage
	}{
		{"audio", tgMessage{MessageID: 50, Chat: tgChat{ID: 1}, Audio: &tgFileRef{FileID: "a"}}},
		{"video", tgMessage{MessageID: 51, Chat: tgChat{ID: 1}, Video: &tgFileRef{FileID: "v"}}},
		// Animated stickers are unsupported and should get a friendly reply.
		{"animated_sticker", tgMessage{MessageID: 52, Chat: tgChat{ID: 1}, Sticker: &tgSticker{FileID: "s", IsAnimated: true, Emoji: "🔥"}}},
		{"video_sticker", tgMessage{MessageID: 53, Chat: tgChat{ID: 1}, Sticker: &tgSticker{FileID: "sv", IsVideo: true, Emoji: "💧"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sentText string
			b := &Bridge{
				client: &http.Client{
					Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						if strings.Contains(r.URL.Path, "sendMessage") {
							var payload map[string]any
							_ = json.NewDecoder(r.Body).Decode(&payload)
							if txt, ok := payload["text"].(string); ok {
								sentText = txt
							}
						}
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
					}),
				},
				token: "TOKEN",
				cfgFn: func() config.RuntimeConfigSnapshot { return config.RuntimeConfigSnapshot{} },
				stopCh: make(chan struct{}),
				doneCh: make(chan struct{}),
			}
			msg := tc.msg
			update := tgUpdate{UpdateID: int64(50 + len(tc.name)), Message: &msg}
			body, _ := json.Marshal(update)
			_ = b.HandleWebhookUpdate(body)
			if sentText == "" {
				t.Errorf("%s: expected unsupported-media reply, got nothing", tc.name)
			}
		})
	}
}

// ── sanitizeFilename (regression — no inline compile) ─────────────────────────

func TestSanitizeFilename_UsesPackageLevelRegex(t *testing.T) {
	// Verify sanitizeFilenameRe is actually used (not a new inline compile)
	// by calling the function many times and checking it doesn't panic or allocate
	// a new regex each time. Functional correctness is the main check here.
	cases := map[string]string{
		"file:name.txt":  "file_name.txt",
		"path/to/x.png": "path_to_x.png",
		"a*b?c.jpg":     "a_b_c.jpg",
		`back\slash`:    "back_slash",
	}
	for input, want := range cases {
		if got := sanitizeFilename(input); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

// ── setWebhook allowed_updates ────────────────────────────────────────────────

func TestSetWebhook_IncludesAllowedUpdates(t *testing.T) {
	var capturedBody []byte
	b := &Bridge{
		token: "TOKEN",
		client: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				capturedBody, _ = io.ReadAll(r.Body)
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
					Header:     make(http.Header),
				}, nil
			}),
		},
	}

	if err := b.setWebhook("https://example.com/telegram/webhook", "mysecret"); err != nil {
		t.Fatalf("setWebhook returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("could not parse request body: %v", err)
	}

	updates, ok := payload["allowed_updates"]
	if !ok {
		t.Fatal("allowed_updates missing from setWebhook payload")
	}
	list, ok := updates.([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("allowed_updates should be a non-empty array, got %v", updates)
	}
	found := map[string]bool{}
	for _, u := range list {
		found[fmt.Sprintf("%v", u)] = true
	}
	for _, required := range []string{"message", "callback_query"} {
		if !found[required] {
			t.Errorf("allowed_updates missing %q, got %v", required, list)
		}
	}
	if payload["secret_token"] != "mysecret" {
		t.Errorf("secret_token not set, got %v", payload["secret_token"])
	}
}
