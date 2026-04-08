// Package middleware provides HTTP middleware for security controls.
package middleware

import (
	"net/http"
	"path"
	"strings"
	"unicode"
)

// SecurityHeaders adds security response headers to every response.
// These headers instruct browsers and proxies to apply restrictive policies.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		// Prevent MIME sniffing and embedding
		w.Header().Set("X-XSS-Protection", "0")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// CSRF defense: this is an API-only service with API key auth.
		// No cookies, no sessions, no browser-based authentication.
		// CORS is intentionally not set (defaults to same-origin only).
		next.ServeHTTP(w, r)
	})
}

// MaxBodySize limits the request body to maxBytes.
// This prevents denial-of-service via memory exhaustion from oversized payloads.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// MethodOnly restricts requests to a specific HTTP method.
func MethodOnly(method string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ValidatePath checks that the path component is safe:
// - No directory traversal (.. components)
// - No empty path
// - No absolute paths or double slashes
// - No null bytes or control characters
// - No backslash (Windows path separator)
// - Must have an allowed file extension
func ValidatePath(allowedExtensions []string) func(objectPath string) error {
	extSet := make(map[string]bool, len(allowedExtensions))
	for _, ext := range allowedExtensions {
		extSet[strings.ToLower(ext)] = true
	}

	return func(objectPath string) error {
		if objectPath == "" {
			return &pathError{"empty path"}
		}

		// Reject absolute paths (leading slash).
		// GCS object names must be relative to the bucket root.
		if objectPath[0] == '/' {
			return &pathError{"absolute path not allowed"}
		}

		// Reject null bytes — prevents null byte injection where downstream
		// systems (C libraries, OS APIs) truncate at \x00.
		if strings.ContainsRune(objectPath, '\x00') {
			return &pathError{"null byte in path"}
		}

		// Reject percent-encoded sequences — Go's net/http decodes %XX in
		// r.URL.Path, so any remaining % indicates double-encoding or
		// literal percent which could bypass traversal checks in downstream systems.
		if strings.ContainsRune(objectPath, '%') {
			return &pathError{"percent-encoding not allowed in path"}
		}

		// Reject ASCII control characters (0x00-0x1F, 0x7F) and backslash.
		// Control characters can cause log injection, terminal escape attacks,
		// and inconsistent behavior across systems.
		for _, r := range objectPath {
			if r < 0x20 || r == 0x7F {
				return &pathError{"control character in path"}
			}
			if r == '\\' {
				return &pathError{"backslash not allowed in path"}
			}
		}

		// Reject non-printable Unicode (categories Cc, Co, Cs, Cf except common ones)
		for _, r := range objectPath {
			if !unicode.IsPrint(r) && r != ' ' {
				return &pathError{"non-printable character in path"}
			}
		}

		// Clean the path and check for traversal.
		// path.Clean normalizes //, /./, /../ etc.
		// If cleaned != original, the path contained traversal or redundant separators.
		cleaned := path.Clean(objectPath)
		if cleaned != objectPath {
			return &pathError{"path contains disallowed components"}
		}

		// Explicit check on each segment: reject any segment that starts with dot.
		// This blocks ".", "..", "...", ".hidden", etc.
		// Legitimate email filenames should never start with a dot.
		for _, segment := range strings.Split(objectPath, "/") {
			if len(segment) > 0 && segment[0] == '.' {
				return &pathError{"path segment starting with dot not allowed"}
			}
		}

		// Check file extension
		ext := strings.ToLower(path.Ext(objectPath))
		if len(extSet) > 0 && !extSet[ext] {
			return &pathError{"file extension not allowed: " + ext}
		}

		return nil
	}
}

type pathError struct {
	msg string
}

func (e *pathError) Error() string {
	return e.msg
}
