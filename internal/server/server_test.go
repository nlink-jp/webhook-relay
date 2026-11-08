package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nlink-jp/webhook-relay/internal/backend"
	"github.com/nlink-jp/webhook-relay/internal/config"
)

// mockBackend records writes for testing.
type mockBackend struct {
	writes []mockWrite
}

type mockWrite struct {
	Path string
	Data []byte
}

// Write reads the whole body, like the GCS backend, and reports its read
// error instead of recording a partial write.
func (m *mockBackend) Write(ctx context.Context, path string, data io.Reader, contentLength int64) error {
	d, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	m.writes = append(m.writes, mockWrite{Path: path, Data: d})
	return nil
}

func (m *mockBackend) Name() string { return "mock" }

func newTestServer(apiKey string) (*Server, *mockBackend) {
	mock := &mockBackend{}
	cfg := &config.Config{
		Port:              8080,
		MaxRequestBytes:   1024 * 1024,
		APIKey:            apiKey,
		RateLimitRPS:      100,
		RateLimitBurst:    200,
		AllowedExtensions: ".eml,.msg",
	}
	backends := map[string]backend.Backend{"mock": mock}
	srv := New(cfg, backends)
	return srv, mock
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("healthz: got %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Errorf("healthz: body %q missing 'ok'", w.Body.String())
	}
}

// captureLog routes the standard logger into a buffer for one test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0) // as main.go does: Cloud Run adds the timestamp
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })
	return &buf
}

// Every route is audit logged and carries the security headers, not only
// /ingest/: a probe of an unknown path must leave a structured record.
func TestEveryRouteIsAuditedAndHardened(t *testing.T) {
	srv, _ := newTestServer("test-key")
	for _, tt := range []struct {
		method, path string
		wantStatus   int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodGet, "/.env", http.StatusNotFound},
		{http.MethodPost, "/ingest/mock/inbox/test.eml", http.StatusUnauthorized},
	} {
		t.Run(tt.path, func(t *testing.T) {
			buf := captureLog(t)
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			lines := strings.Count(strings.TrimSpace(buf.String()), "\n") + 1
			if strings.TrimSpace(buf.String()) == "" || lines != 1 {
				t.Fatalf("want exactly one audit line, got %d:\n%s", lines, buf.String())
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
				t.Fatalf("audit line is not JSON: %v\n%s", err, buf.String())
			}
			if entry["path"] != tt.path || int(entry["status"].(float64)) != tt.wantStatus {
				t.Errorf("audit entry = %v, want path %s status %d", entry, tt.path, tt.wantStatus)
			}
		})
	}
}

func TestIngestSuccess(t *testing.T) {
	srv, mock := newTestServer("test-key")
	body := []byte("From: test@example.com\r\nSubject: Test\r\n\r\nBody")
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/test.eml", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("got %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	// Path should contain the original filename with timestamp+random prefix
	if !strings.Contains(resp["path"], "test.eml") {
		t.Errorf("path %q should contain 'test.eml'", resp["path"])
	}
	if !strings.HasPrefix(resp["path"], "inbox/") {
		t.Errorf("path %q should start with 'inbox/'", resp["path"])
	}

	if len(mock.writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(mock.writes))
	}
	if !strings.Contains(mock.writes[0].Path, "test.eml") {
		t.Errorf("write path %q should contain 'test.eml'", mock.writes[0].Path)
	}
}

func TestIngestNoAuth(t *testing.T) {
	srv, _ := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/test.eml", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestIngestWrongMethod(t *testing.T) {
	srv, _ := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodGet, "/ingest/mock/inbox/test.eml", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestIngestPathTraversal(t *testing.T) {
	srv, _ := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/../../../etc/passwd", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// Should be rejected (bad path or missing .eml/.msg extension)
	if w.Code == http.StatusCreated {
		t.Error("path traversal should be rejected")
	}
}

func TestIngestUnknownBackend(t *testing.T) {
	srv, _ := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodPost, "/ingest/nonexistent/inbox/test.eml", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestIngestDisallowedExtension(t *testing.T) {
	srv, _ := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/malware.exe", strings.NewReader("data"))
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestIngestJsonInjectionInBackendName(t *testing.T) {
	srv, _ := newTestServer("test-key")
	// Attempt to inject JSON via backend name
	req := httptest.NewRequest(http.MethodPost, `/ingest/evil","hacked":"true/inbox/test.eml`, strings.NewReader("data"))
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Error("JSON injection in backend name should be rejected")
	}
	// Response must be valid JSON
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Errorf("response is not valid JSON: %s", w.Body.String())
	}
}

func TestIngestEmptyBody(t *testing.T) {
	srv, _ := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/test.eml", nil)
	req.Header.Set("X-API-Key", "test-key")
	req.ContentLength = 0
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d for empty body", w.Code, http.StatusBadRequest)
	}
}

// A chunked request (unknown Content-Length) with no body must be refused
// like a declared-empty one, not written as a 0-byte object.
func TestIngestEmptyChunkedBody(t *testing.T) {
	srv, mock := newTestServer("test-key")
	// io.MultiReader is not one of the body types httptest recognises, so
	// ContentLength stays -1 as for a chunked upload.
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/test.eml", io.MultiReader(strings.NewReader("")))
	req.Header.Set("X-API-Key", "test-key")
	if req.ContentLength != -1 {
		t.Fatalf("test setup: ContentLength = %d, want -1", req.ContentLength)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d for empty chunked body", w.Code, http.StatusBadRequest)
	}
	if len(mock.writes) != 0 {
		t.Errorf("empty body reached the backend: %d writes", len(mock.writes))
	}
}

// A chunked body with content still gets through whole, first byte included.
func TestIngestChunkedBody(t *testing.T) {
	srv, mock := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/test.eml", io.MultiReader(strings.NewReader("hello")))
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
	if len(mock.writes) != 1 || string(mock.writes[0].Data) != "hello" {
		t.Errorf("writes = %+v, want one write of \"hello\"", mock.writes)
	}
}

// A body declared larger than the limit is refused with 413 before the
// backend is touched.
func TestIngestDeclaredOversizedBody(t *testing.T) {
	srv, mock := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/test.eml", strings.NewReader("x"))
	req.Header.Set("X-API-Key", "test-key")
	req.ContentLength = 1024*1024 + 1
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if len(mock.writes) != 0 {
		t.Errorf("oversized body reached the backend: %d writes", len(mock.writes))
	}
}

// A chunked body that outgrows the limit while streaming is the client's
// fault: 413, not 500, and nothing is recorded as written.
func TestIngestStreamedOversizedBody(t *testing.T) {
	srv, mock := newTestServer("test-key")
	big := bytes.Repeat([]byte("a"), 1024*1024+1)
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/test.eml", io.MultiReader(bytes.NewReader(big)))
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got %d, want %d. Body: %s", w.Code, http.StatusRequestEntityTooLarge, w.Body.String())
	}
	if len(mock.writes) != 0 {
		t.Errorf("oversized body recorded as written: %d writes", len(mock.writes))
	}
}

func TestIngestNullByteInPath(t *testing.T) {
	srv, _ := newTestServer("test-key")
	req := httptest.NewRequest(http.MethodPost, "/ingest/mock/inbox/test.eml%00.txt", strings.NewReader("data"))
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Error("null byte in path should be rejected")
	}
}

func TestUniqueObjectPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"with dir", "inbox/alert.eml"},
		{"nested dir", "inbox/2026/04/report.msg"},
		{"no dir", "alert.eml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p1 := uniqueObjectPath(tt.input)
			p2 := uniqueObjectPath(tt.input)

			// Must be different (random component)
			if p1 == p2 {
				t.Errorf("two calls returned same path: %q", p1)
			}

			// Must end with original filename
			base := tt.input[strings.LastIndex(tt.input, "/")+1:]
			if !strings.HasSuffix(p1, "_"+base) {
				t.Errorf("path %q should end with _%s", p1, base)
			}

			// Must preserve directory
			if strings.Contains(tt.input, "/") {
				dir := tt.input[:strings.LastIndex(tt.input, "/")]
				if !strings.HasPrefix(p1, dir+"/") {
					t.Errorf("path %q should start with %s/", p1, dir)
				}
			}
		})
	}
}
