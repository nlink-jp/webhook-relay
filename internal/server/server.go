// Package server provides the HTTP server and request handling.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

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
}

// New creates a new Server with the given configuration and backends.
func New(cfg *config.Config, backends map[string]backend.Backend) *Server {
	// Parse allowed extensions
	exts := parseExtensions(cfg.AllowedExtensions)

	s := &Server{
		mux:      http.NewServeMux(),
		backends: backends,
		validate: middleware.ValidatePath(exts),
	}

	// Rate limiter
	rl := middleware.NewRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst)

	// Build middleware chain (order matters — outermost first):
	// AuditLog → SecurityHeaders → MaxBodySize → RateLimit → APIKeyAuth → handler
	ingest := chain(
		http.HandlerFunc(s.handleIngest),
		middleware.AuditLog,
		middleware.SecurityHeaders,
		middleware.MaxBodySize(cfg.MaxRequestBytes),
		rl.Middleware,
		auth.APIKeyAuth(cfg.APIKey),
		middleware.MethodOnly(http.MethodPost),
	)

	// Health check — no auth, no rate limit, minimal info
	health := chain(
		http.HandlerFunc(s.handleHealth),
		middleware.SecurityHeaders,
	)

	s.mux.Handle("/ingest/", ingest)
	s.mux.Handle("/healthz", health)

	return s
}

// Handler returns the http.Handler for the server.
func (s *Server) Handler() http.Handler {
	return s.mux
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
		http.Error(w, `{"error":"invalid path: expected /ingest/{backend}/{path...}"}`, http.StatusBadRequest)
		return
	}

	backendName := parts[0]
	objectPath := parts[1]

	// Look up backend
	b, ok := s.backends[backendName]
	if !ok {
		http.Error(w, `{"error":"unknown backend: `+backendName+`"}`, http.StatusBadRequest)
		return
	}

	// Validate object path (traversal, extension)
	if err := s.validate(objectPath); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	// Read body and write to backend
	defer r.Body.Close()
	if err := b.Write(r.Context(), objectPath, r.Body, r.ContentLength); err != nil {
		log.Printf("ERROR: backend write failed: %v", err)
		http.Error(w, `{"error":"backend write failed"}`, http.StatusInternalServerError)
		return
	}

	resp := map[string]string{
		"status":  "ok",
		"backend": backendName,
		"path":    objectPath,
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
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
