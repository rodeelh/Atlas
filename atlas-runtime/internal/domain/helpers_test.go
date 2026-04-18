package domain

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── writeJSON ─────────────────────────────────────────────────────────────────

func TestWriteJSON_StatusAndContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, map[string]string{"key": "val"})

	if w.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusCreated)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json prefix", ct)
	}
}

func TestWriteJSON_Body(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]int{"count": 3})

	var got map[string]int
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["count"] != 3 {
		t.Errorf("count: got %d, want 3", got["count"])
	}
}

func TestWriteJSON_UnmarshalableValue(t *testing.T) {
	w := httptest.NewRecorder()
	// channels cannot be JSON-marshaled — must return 500.
	writeJSON(w, http.StatusOK, make(chan int))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", w.Code)
	}
}

// ── writeError ────────────────────────────────────────────────────────────────

func TestWriteError_StatusAndBody(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "missing field")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
	var got map[string]string
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["error"] != "missing field" {
		t.Errorf("error field: got %q, want %q", got["error"], "missing field")
	}
}

func TestWriteError_ContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusInternalServerError, "boom")
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json prefix", ct)
	}
}

// ── decodeJSON ────────────────────────────────────────────────────────────────

func TestDecodeJSON_ValidBody(t *testing.T) {
	body := `{"name":"atlas"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var out struct{ Name string `json:"name"` }
	if !decodeJSON(w, r, &out) {
		t.Fatal("decodeJSON returned false for valid body")
	}
	if out.Name != "atlas" {
		t.Errorf("Name: got %q, want %q", out.Name, "atlas")
	}
}

func TestDecodeJSON_InvalidBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not-json"))
	w := httptest.NewRecorder()

	var out struct{ Name string }
	if decodeJSON(w, r, &out) {
		t.Fatal("decodeJSON returned true for invalid body")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestDecodeJSON_EmptyBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	w := httptest.NewRecorder()

	var out struct{ Name string }
	if decodeJSON(w, r, &out) {
		t.Fatal("decodeJSON returned true for empty body")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

// ── newDomainUUID ─────────────────────────────────────────────────────────────

func TestNewDomainUUID_Format(t *testing.T) {
	id := newDomainUUID()
	// Expect 8-4-4-4-12 hex segments.
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("UUID %q: expected 5 parts, got %d", id, len(parts))
	}
	lengths := []int{8, 4, 4, 4, 12}
	for i, p := range parts {
		if len(p) != lengths[i] {
			t.Errorf("segment %d: got len %d, want %d", i, len(p), lengths[i])
		}
	}
}

func TestNewDomainUUID_Unique(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := newDomainUUID()
		if seen[id] {
			t.Fatalf("duplicate UUID generated: %s", id)
		}
		seen[id] = true
	}
}

// ── isPrivateIPv4 ─────────────────────────────────────────────────────────────

func TestIsPrivateIPv4(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.255.255", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.15.255.255", false}, // just outside 172.16/12
		{"172.32.0.0", false},     // just outside 172.16/12
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip).To4()
		if ip == nil {
			t.Fatalf("ParseIP failed for %s", tc.ip)
		}
		got := isPrivateIPv4(ip)
		if got != tc.private {
			t.Errorf("isPrivateIPv4(%s) = %v, want %v", tc.ip, got, tc.private)
		}
	}
}

// ── atomicWriteFile ───────────────────────────────────────────────────────────

func TestAtomicWriteFile_WritesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello atlas")

	if err := atomicWriteFile(path, content, 0o644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content: got %q, want %q", got, content)
	}
}

func TestAtomicWriteFile_NoTempFileLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := atomicWriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file not cleaned up: %s", e.Name())
		}
	}
}

func TestAtomicWriteFile_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")

	if err := atomicWriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := atomicWriteFile(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "second" {
		t.Errorf("expected %q after overwrite, got %q", "second", got)
	}
}
