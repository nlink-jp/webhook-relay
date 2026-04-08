// Package config provides configuration for webhook-relay.
// All configuration is via environment variables — no config files are used.
package config

import (
	"fmt"
	"os"
	"strconv"
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

	// Allowed file extensions (comma-separated, e.g. ".eml,.msg")
	AllowedExtensions string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	c := &Config{
		Port:            intEnv("PORT", 8080),
		MaxRequestBytes: int64(intEnv("WEBHOOK_RELAY_MAX_REQUEST_BYTES", 25*1024*1024)), // 25 MB
		APIKey:          os.Getenv("WEBHOOK_RELAY_API_KEY"),
		RateLimitRPS:    floatEnv("WEBHOOK_RELAY_RATE_LIMIT_RPS", 10),
		RateLimitBurst:  intEnv("WEBHOOK_RELAY_RATE_LIMIT_BURST", 20),
		GCSBucket:       os.Getenv("WEBHOOK_RELAY_GCS_BUCKET"),
		GCSProject:      os.Getenv("WEBHOOK_RELAY_GCS_PROJECT"),
		AllowedExtensions: envOrDefault("WEBHOOK_RELAY_ALLOWED_EXTENSIONS", ".eml,.msg"),
	}

	if c.APIKey == "" {
		return nil, fmt.Errorf("WEBHOOK_RELAY_API_KEY is required")
	}

	return c, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func floatEnv(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
