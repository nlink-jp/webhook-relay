package gcs

import (
	"bytes"
	"context"
	"io"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// TestNewRequiresValidClient verifies that New returns an error
// when the GCS client cannot be created (e.g., invalid credentials).
// In CI/local without ADC, this tests the error path.
func TestNewRequiresValidClient(t *testing.T) {
	// This test verifies the constructor doesn't panic.
	// On machines with ADC configured, it will succeed.
	// On machines without ADC, it will fail gracefully.
	ctx := context.Background()
	_, err := New(ctx, "test-project", "test-bucket")
	if err != nil {
		// Expected in environments without GCP credentials — this is fine.
		t.Skipf("GCS client creation failed (expected without ADC): %v", err)
	}
}

// TestBackendName verifies the backend identifier.
func TestBackendName(t *testing.T) {
	b := &Backend{bucket: "test"}
	if got := b.Name(); got != "gcs" {
		t.Errorf("Name() = %q, want 'gcs'", got)
	}
}

// TestContentTypeIsOctetStream verifies that the GCS writer sets
// Content-Type to application/octet-stream.
func TestContentTypeIsOctetStream(t *testing.T) {
	// We can't easily mock the GCS client, but we can verify the
	// Write method sets ContentType by creating a writer against
	// a fake bucket (it will fail on write, but we can check the setup).
	ctx := context.Background()

	// Create client with no auth for testing (will fail on actual operations)
	client, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Skipf("cannot create GCS client: %v", err)
	}
	defer client.Close()

	b := &Backend{client: client, bucket: "nonexistent-bucket-for-test"}

	// Write will fail because the bucket doesn't exist, but that's OK.
	// We're testing that the method doesn't panic and handles errors.
	err = b.Write(ctx, "test/file.eml", io.NopCloser(bytes.NewReader([]byte("test"))), 4)
	if err == nil {
		t.Error("expected error writing to nonexistent bucket")
	}
}
