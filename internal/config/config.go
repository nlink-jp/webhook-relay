// Package config provides configuration for webhook-relay.
// All configuration is via environment variables — no config files are used.
//
// Load fails closed: a variable that is present but cannot be used (an
// unparseable number, a non-positive limit, an allow-list with no entries)
// is an error that stops startup, never a silent fall-back to the default.
package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration.
type Config struct {
	// Server
	Port            int
	MaxRequestBytes int64

	// Authentication
	APIKey string

	// Rate limiting
	RateLimitRPS   float64 // Requests per second per IP
	RateLimitBurst int     // Burst size per IP

	// GCS backend
	GCSBucket  string
	GCSProject string

	// Allowed file extensions (comma-separated, e.g. ".eml,.msg").
	// Never empty: Load rejects a present value with no entries.
	AllowedExtensions string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	c := &Config{
		APIKey:     os.Getenv("WEBHOOK_RELAY_API_KEY"),
		GCSBucket:  os.Getenv("WEBHOOK_RELAY_GCS_BUCKET"),
		GCSProject: os.Getenv("WEBHOOK_RELAY_GCS_PROJECT"),
	}

	if c.APIKey == "" {
		return nil, fmt.Errorf("WEBHOOK_RELAY_API_KEY is required")
	}

	var err error
	if c.Port, err = intEnv("PORT", 8080, 1, 65535); err != nil {
		return nil, err
	}
	if c.MaxRequestBytes, err = int64Env("WEBHOOK_RELAY_MAX_REQUEST_BYTES", 25*1024*1024); err != nil { // 25 MB
		return nil, err
	}
	if c.RateLimitRPS, err = floatEnv("WEBHOOK_RELAY_RATE_LIMIT_RPS", 10); err != nil {
		return nil, err
	}
	if c.RateLimitBurst, err = intEnv("WEBHOOK_RELAY_RATE_LIMIT_BURST", 20, 1, math.MaxInt32); err != nil {
		return nil, err
	}
	if c.AllowedExtensions, err = extensionsEnv("WEBHOOK_RELAY_ALLOWED_EXTENSIONS", ".eml,.msg"); err != nil {
		return nil, err
	}

	return c, nil
}

// intEnv reads an integer in [min, max]. An absent variable yields fallback;
// a present value that does not parse or is out of range is an error.
func intEnv(key string, fallback, min, max int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q", key, v)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("%s: %d is out of range [%d, %d]", key, n, min, max)
	}
	return n, nil
}

// int64Env reads a positive 64-bit integer (a byte count).
func int64Env(key string, fallback int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q", key, v)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s: %d must be positive", key, n)
	}
	return n, nil
}

// floatEnv reads a positive, finite float. strconv accepts "inf" and "nan",
// and rate.Limit(+Inf) would switch the limiter off, so both are rejected.
func floatEnv(key string, fallback float64) (float64, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid number %q", key, v)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return 0, fmt.Errorf("%s: %q must be a positive finite number", key, v)
	}
	return f, nil
}

// extensionsEnv reads the comma-separated extension allow-list. A present
// value with no non-blank entries (for example "" or ",") would have turned
// the extension check off silently; it is an error instead.
func extensionsEnv(key, fallback string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	for _, p := range strings.Split(v, ",") {
		if strings.TrimSpace(p) != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s: %q holds no extensions; unset it to use the default %q", key, v, fallback)
}
