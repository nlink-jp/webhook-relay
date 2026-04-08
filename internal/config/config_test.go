package config

import (
	"os"
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
	os.Setenv("WEBHOOK_RELAY_RATE_LIMIT_RPS", "5")
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
	if cfg.RateLimitRPS != 5 {
		t.Errorf("RateLimitRPS = %f, want 5", cfg.RateLimitRPS)
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

func TestLoadInvalidIntFallsBackToDefault(t *testing.T) {
	clearEnv()
	os.Setenv("WEBHOOK_RELAY_API_KEY", "test-key")
	os.Setenv("PORT", "not-a-number")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080 (default on invalid input)", cfg.Port)
	}
}
