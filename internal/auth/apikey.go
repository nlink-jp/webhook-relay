// Package auth provides authentication middleware.
package auth

import (
	"crypto/subtle"
	"net/http"
)

const headerName = "X-API-Key"

// APIKeyAuth returns middleware that validates the X-API-Key header
// using constant-time comparison to prevent timing attacks.
func APIKeyAuth(validKey string) func(http.Handler) http.Handler {
	validKeyBytes := []byte(validKey)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := r.Header.Get(headerName)
			if provided == "" {
				http.Error(w, `{"error":"missing API key"}`, http.StatusUnauthorized)
				return
			}

			// Constant-time comparison prevents timing side-channel attacks.
			// An attacker cannot determine how many bytes of the key are correct
			// by measuring response latency.
			if subtle.ConstantTimeCompare([]byte(provided), validKeyBytes) != 1 {
				http.Error(w, `{"error":"invalid API key"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
