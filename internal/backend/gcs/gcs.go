// Package gcs implements the GCS storage backend.
package gcs

import (
	"context"
	"fmt"
	"io"
	"log"

	"cloud.google.com/go/storage"
)

// Backend writes incoming payloads to a GCS bucket.
type Backend struct {
	client *storage.Client
	bucket string
}

// New creates a GCS backend. The client uses Application Default Credentials,
// which on Cloud Run resolves to the service account attached to the service.
func New(ctx context.Context, project, bucket string) (*Backend, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs: failed to create client: %w", err)
	}

	return &Backend{
		client: client,
		bucket: bucket,
	}, nil
}

// Write stores data at the given object path in the GCS bucket.
// Content-Type is forced to application/octet-stream to prevent GCS from
// inferring a type (e.g. text/html) that could be dangerous if the bucket
// is ever accidentally made public.
func (b *Backend) Write(ctx context.Context, path string, data io.Reader, contentLength int64) error {
	obj := b.client.Bucket(b.bucket).Object(path)
	w := obj.NewWriter(ctx)
	w.ContentType = "application/octet-stream"

	if _, err := io.Copy(w, data); err != nil {
		w.Close()
		return fmt.Errorf("gcs: failed to write %s: %w", path, err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("gcs: failed to finalize %s: %w", path, err)
	}

	log.Printf("gcs: wrote gs://%s/%s (%d bytes)", b.bucket, path, contentLength)
	return nil
}

// Name returns the backend identifier.
func (b *Backend) Name() string {
	return "gcs"
}

// Close releases the GCS client resources.
func (b *Backend) Close() error {
	return b.client.Close()
}
