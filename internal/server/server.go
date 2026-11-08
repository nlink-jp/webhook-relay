// Package server provides the HTTP server and request handling.
package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/nlink-jp/webhook-relay/internal/auth"
	"github.com/nlink-jp/webhook-relay/internal/backend"
	"github.com/nlink-jp/webhook-relay/internal/config"
	"github.com/nlink-jp/webhook-relay/internal/middleware"
)

// Server is the webhook relay HTTP server.
type Server struct {
	mux      *http.ServeMux
	backends map[string]backend.Backend
	validate func(string) error
	maxBytes int64
}

// New creates a new Server with the given configuration and backends.
func New(cfg *config.Config, backends map[string]backend.Backend) *Server {
	// Parse allowed extensions
	exts := parseExtensions(cfg.AllowedExtensions)

	s := &Server{
		mux:      http.NewServeMux(),
		backends: backends,
		validate: middleware.ValidatePath(exts),
		maxBytes: cfg.MaxRequestBytes,
	}

	// Rate limiter
	rl := middleware.NewRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst)

	// Build the ingest chain (order matters — outermost first):
	// MaxBodySize → RateLimit → APIKeyAuth → MethodOnly → handler.
	// AuditLog and SecurityHeaders wrap the whole mux in Handler, so they
	// are not repeated here.
	ingest := chain(
		http.HandlerFunc(s.handleIngest),
		middleware.MaxBodySize(cfg.MaxRequestBytes),
		rl.Middleware,
		auth.APIKeyAuth(cfg.APIKey),
		middleware.MethodOnly(http.MethodPost),
	)

	s.mux.Handle("/ingest/", ingest)
	// Health check — no auth, no rate limit, minimal info
	s.mux.HandleFunc("/healthz", s.handleHealth)

	return s
}

// Handler returns the http.Handler for the server. Every request is audit
// logged and gets the security headers, including /healthz, unknown paths
// (the mux's 404) and the mux's redirects, so path probing shows up in the
// structured trail and not only in the platform's request log.
func (s *Server) Handler() http.Handler {
	return chain(s.mux, middleware.AuditLog, middleware.SecurityHeaders)
}

// handleHealth returns a minimal health check response.
// Intentionally reveals no version or internal state.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ok"}`)
}

// handleIngest processes incoming webhook payloads.
// URL format: /ingest/{backend}/{path...}
// Example:    /ingest/gcs/inbox/alert.eml
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	// Parse URL: /ingest/{backend}/{path...}
	trimmed := strings.TrimPrefix(r.URL.Path, "/ingest/")
	parts := strings.SplitN(trimmed, "/", 2)

	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		jsonError(w, "invalid path: expected /ingest/{backend}/{path...}", http.StatusBadRequest)
		return
	}

	backendName := parts[0]
	objectPath := parts[1]

	// Validate backend name — only alphanumeric and hyphens.
	// Prevents injection via crafted backend names in error responses.
	if !isValidIdentifier(backendName) {
		jsonError(w, "invalid backend name", http.StatusBadRequest)
		return
	}

	// Look up backend
	b, ok := s.backends[backendName]
	if !ok {
		jsonError(w, "unknown backend", http.StatusBadRequest)
		return
	}

	// Validate object path (traversal, null bytes, control chars, extension)
	if err := s.validate(objectPath); err != nil {
		jsonError(w, "invalid path", http.StatusBadRequest)
		return
	}

	// A body declared larger than the limit is refused before any upload
	// starts. MaxBodySize would stop it part-way, but by then a backend
	// writer is open; this keeps the common oversized case out of that path.
	if r.ContentLength > s.maxBytes {
		jsonError(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Reject empty bodies — a 0-byte file is never valid input and would
	// cause downstream parsing failures. A chunked body has ContentLength
	// -1, so the first byte is read to tell an empty one from a real one.
	defer r.Body.Close()
	if r.ContentLength == 0 {
		jsonError(w, "empty request body", http.StatusBadRequest)
		return
	}
	body, empty, err := peekNonEmpty(r.Body)
	if err != nil {
		writeBodyError(w, err)
		return
	}
	if empty {
		jsonError(w, "empty request body", http.StatusBadRequest)
		return
	}

	// Generate unique object path to prevent collisions.
	// Format: {dir}/{timestamp}_{random}_{original_filename}
	// Example: inbox/20260409T102300Z_a1b2c3d4_alert.eml
	uniquePath := uniqueObjectPath(objectPath)

	// Stream body to backend
	if err := b.Write(r.Context(), uniquePath, body, r.ContentLength); err != nil {
		writeBodyError(w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"backend": backendName,
		"path":    uniquePath,
	})
}

// peekNonEmpty reads the first byte of body. It reports empty when the body
// ends before any byte, and otherwise returns a reader that yields the whole
// body, first byte included.
func peekNonEmpty(body io.Reader) (io.Reader, bool, error) {
	var first [1]byte
	n, err := io.ReadFull(body, first[:])
	switch {
	case n == 1:
		return io.MultiReader(bytes.NewReader(first[:1]), body), false, nil
	case err == io.EOF || err == io.ErrUnexpectedEOF:
		return nil, true, nil
	default:
		return nil, false, err
	}
}

// writeBodyError answers a failed body read or backend write. A body that
// outgrew MaxBodySize is the client's fault and gets 413; anything else is
// an internal failure, reported without detail.
func writeBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		jsonError(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	log.Printf("ERROR: backend write failed: %v", err)
	jsonError(w, "backend write failed", http.StatusInternalServerError)
}

// jsonError writes a JSON error response using json.Marshal to prevent
// injection via user-controlled input in error messages.
func jsonError(w http.ResponseWriter, msg string, code int) {
	resp, _ := json.Marshal(map[string]string{"error": msg})
	http.Error(w, string(resp), code)
}

// isValidIdentifier checks that a string contains only safe characters
// for use in backend names (alphanumeric, hyphens, underscores).
func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// chain applies middleware in reverse order so the first middleware
// in the list is the outermost (executed first).
func chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

func parseExtensions(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// uniqueObjectPath generates a unique object path by prepending a timestamp
// and random suffix to the filename. This prevents collisions when multiple
// emails arrive with the same name (e.g., from Power Automate).
//
// Input:  "inbox/alert.eml"
// Output: "inbox/20260409T102300Z_a1b2c3d4_alert.eml"
func uniqueObjectPath(objectPath string) string {
	dir := path.Dir(objectPath)
	base := path.Base(objectPath)

	ts := time.Now().UTC().Format("20060102T150405Z")

	var buf [4]byte
	rand.Read(buf[:])
	rnd := hex.EncodeToString(buf[:])

	unique := fmt.Sprintf("%s_%s_%s", ts, rnd, base)
	if dir == "." {
		return unique
	}
	return dir + "/" + unique
}

// ReadBody reads the request body with a size limit.
// Returns the data or an error if the body exceeds the limit.
func ReadBody(r *http.Request, maxBytes int64) ([]byte, error) {
	limited := io.LimitReader(r.Body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("request body exceeds %d bytes", maxBytes)
	}
	return data, nil
}
