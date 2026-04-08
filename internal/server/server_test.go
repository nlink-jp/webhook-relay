package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func (m *mockBackend) Write(ctx context.Context, path string, data io.Reader, contentLength int64) error {
	d, _ := io.ReadAll(data)
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
	if resp["path"] != "inbox/test.eml" {
		t.Errorf("path = %q, want inbox/test.eml", resp["path"])
	}

	if len(mock.writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(mock.writes))
	}
	if mock.writes[0].Path != "inbox/test.eml" {
		t.Errorf("write path = %q", mock.writes[0].Path)
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
