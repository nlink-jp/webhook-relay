package config

import (
	"os"
	"strings"
	"testing"
)

func clearEnv() {
	for _, key := range []string{
		"PORT",
		"WEBHOOK_RELAY_API_KEY",
		"WEBHOOK_RELAY_GCS_BUCKET",
		"WEBHOOK_RELAY_GCS_PROJECT",
		"WEBHOOK_RELAY_RATE_LIMIT_RPS",
		"WEBHOOK_RELAY_RATE_LIMIT_BURST",
		"WEBHOOK_RELAY_MAX_REQUEST_BYTES",
		"WEBHOOK_RELAY_ALLOWED_EXTENSIONS",
	} {
		os.Unsetenv(key)
	}
}

func TestLoadRequiresAPIKey(t *testing.T) {
	clearEnv()
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when WEBHOOK_RELAY_API_KEY is missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv()
	os.Setenv("WEBHOOK_RELAY_API_KEY", "test-key")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.MaxRequestBytes != 25*1024*1024 {
		t.Errorf("MaxRequestBytes = %d, want %d", cfg.MaxRequestBytes, 25*1024*1024)
	}
	if cfg.RateLimitRPS != 10 {
		t.Errorf("RateLimitRPS = %f, want 10", cfg.RateLimitRPS)
	}
	if cfg.RateLimitBurst != 20 {
		t.Errorf("RateLimitBurst = %d, want 20", cfg.RateLimitBurst)
	}
	if cfg.AllowedExtensions != ".eml,.msg" {
		t.Errorf("AllowedExtensions = %q, want '.eml,.msg'", cfg.AllowedExtensions)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnv()
	os.Setenv("WEBHOOK_RELAY_API_KEY", "my-key")
	os.Setenv("PORT", "9090")
	os.Setenv("WEBHOOK_RELAY_GCS_BUCKET", "my-bucket")
	os.Setenv("WEBHOOK_RELAY_GCS_PROJECT", "my-project")
	os.Setenv("WEBHOOK_RELAY_RATE_LIMIT_RPS", "0.5")
	os.Setenv("WEBHOOK_RELAY_RATE_LIMIT_BURST", "10")
	os.Setenv("WEBHOOK_RELAY_MAX_REQUEST_BYTES", "1048576")
	os.Setenv("WEBHOOK_RELAY_ALLOWED_EXTENSIONS", ".eml")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.GCSBucket != "my-bucket" {
		t.Errorf("GCSBucket = %q, want 'my-bucket'", cfg.GCSBucket)
	}
	if cfg.GCSProject != "my-project" {
		t.Errorf("GCSProject = %q, want 'my-project'", cfg.GCSProject)
	}
	if cfg.RateLimitRPS != 0.5 {
		t.Errorf("RateLimitRPS = %f, want 0.5", cfg.RateLimitRPS)
	}
	if cfg.RateLimitBurst != 10 {
		t.Errorf("RateLimitBurst = %d, want 10", cfg.RateLimitBurst)
	}
	if cfg.MaxRequestBytes != 1048576 {
		t.Errorf("MaxRequestBytes = %d, want 1048576", cfg.MaxRequestBytes)
	}
	if cfg.AllowedExtensions != ".eml" {
		t.Errorf("AllowedExtensions = %q, want '.eml'", cfg.AllowedExtensions)
	}
}

// A present value that cannot be used stops startup and the error names the
// key. Before this, an operator's typo silently ran the service on defaults
// looser than the ones they chose.
func TestLoadRejectsUnusableValues(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		// Unparseable
		{"PORT", "not-a-number"},
		{"PORT", "8080.0"},
		{"WEBHOOK_RELAY_MAX_REQUEST_BYTES", "10MB"},
		{"WEBHOOK_RELAY_MAX_REQUEST_BYTES", ""},
		{"WEBHOOK_RELAY_RATE_LIMIT_RPS", "1/s"},
		{"WEBHOOK_RELAY_RATE_LIMIT_RPS", ""},
		{"WEBHOOK_RELAY_RATE_LIMIT_BURST", "5.0"},
		{"WEBHOOK_RELAY_RATE_LIMIT_BURST", " 20"},
		// Out of range: non-positive
		{"PORT", "0"},
		{"PORT", "65536"},
		{"WEBHOOK_RELAY_MAX_REQUEST_BYTES", "0"},
		{"WEBHOOK_RELAY_MAX_REQUEST_BYTES", "-1"},
		{"WEBHOOK_RELAY_RATE_LIMIT_RPS", "0"},
		{"WEBHOOK_RELAY_RATE_LIMIT_RPS", "-10"},
		{"WEBHOOK_RELAY_RATE_LIMIT_BURST", "0"},
		{"WEBHOOK_RELAY_RATE_LIMIT_BURST", "-1"},
		// Non-finite: ParseFloat accepts these, rate.Limit(+Inf) disables limiting
		{"WEBHOOK_RELAY_RATE_LIMIT_RPS", "inf"},
		{"WEBHOOK_RELAY_RATE_LIMIT_RPS", "+Inf"},
		{"WEBHOOK_RELAY_RATE_LIMIT_RPS", "NaN"},
		// Allow-list with no entries would turn the extension check off
		{"WEBHOOK_RELAY_ALLOWED_EXTENSIONS", ""},
		{"WEBHOOK_RELAY_ALLOWED_EXTENSIONS", ","},
		{"WEBHOOK_RELAY_ALLOWED_EXTENSIONS", " , "},
	}

	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			clearEnv()
			os.Setenv("WEBHOOK_RELAY_API_KEY", "test-key")
			os.Setenv(tt.key, tt.value)
			defer clearEnv()

			cfg, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted %s=%q, got %+v", tt.key, tt.value, cfg)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error %q does not name the key %s", err, tt.key)
			}
		})
	}
}

func TestLoadAcceptsBoundaryValues(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{"PORT", "1"},
		{"PORT", "65535"},
		{"WEBHOOK_RELAY_MAX_REQUEST_BYTES", "1"},
		{"WEBHOOK_RELAY_RATE_LIMIT_RPS", "0.001"},
		{"WEBHOOK_RELAY_RATE_LIMIT_BURST", "1"},
		{"WEBHOOK_RELAY_ALLOWED_EXTENSIONS", ".eml, .msg,"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			clearEnv()
			os.Setenv("WEBHOOK_RELAY_API_KEY", "test-key")
			os.Setenv(tt.key, tt.value)
			defer clearEnv()
			if _, err := Load(); err != nil {
				t.Fatalf("Load() rejected %s=%q: %v", tt.key, tt.value, err)
			}
		})
	}
}
