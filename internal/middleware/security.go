// Package middleware provides HTTP middleware for security controls.
package middleware

import (
	"net/http"
	"path"
	"strings"
)

// SecurityHeaders adds security response headers to every response.
// These headers instruct browsers and proxies to apply restrictive policies.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
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

		// Clean the path and check for traversal
		cleaned := path.Clean(objectPath)
		if cleaned != objectPath {
			// path.Clean changed the path — likely had .., //, or trailing /
			return &pathError{"path contains disallowed components"}
		}

		// Explicit traversal check
		for _, segment := range strings.Split(objectPath, "/") {
			if segment == ".." || segment == "." {
				return &pathError{"path traversal not allowed"}
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
