// Package backend defines the interface for storage backends.
// New backends can be added by implementing the Backend interface.
package backend

import (
	"context"
	"io"
)

// Backend is the interface that storage backends must implement.
// Each backend receives raw bytes and writes them to its storage system.
type Backend interface {
	// Write stores the data at the given path.
	// The path is pre-validated by the middleware chain (no traversal, valid extension).
	Write(ctx context.Context, path string, data io.Reader, contentLength int64) error

	// Name returns the backend identifier (e.g. "gcs", "pubsub").
	Name() string
}
