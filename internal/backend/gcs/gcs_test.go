package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeGCS stands in for the JSON API upload endpoint. It counts the upload
// requests that completed and remembers the media bytes of the last one.
// Everything the storage client sends goes to it via STORAGE_EMULATOR_HOST.
type fakeGCS struct {
	t        *testing.T
	uploads  atomic.Int32
	mu       sync.Mutex
	lastBody []byte
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/upload/storage/v1/b/") {
		f.t.Errorf("fake gcs: unexpected request %s %s", r.Method, r.URL)
		http.Error(w, "unexpected", http.StatusNotImplemented)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// The client went away mid-upload: nothing was committed.
		return
	}
	f.mu.Lock()
	f.lastBody = body
	f.mu.Unlock()
	f.uploads.Add(1)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"kind":"storage#object","bucket":"b","name":"%s","size":"%d"}`, r.URL.Query().Get("name"), len(body))
}

func newTestBackend(t *testing.T) (*Backend, *fakeGCS) {
	t.Helper()
	fake := &fakeGCS{t: t}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	t.Setenv("STORAGE_EMULATOR_HOST", strings.TrimPrefix(srv.URL, "http://"))

	b, err := New(context.Background(), "", "b")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return b, fake
}

// failingReader yields data and then fails, the way http.MaxBytesReader
// does once the body passes its limit.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestWriteCommitsCompleteBody(t *testing.T) {
	b, fake := newTestBackend(t)
	payload := "From: a@example.com\r\n\r\nhello"

	if err := b.Write(context.Background(), "inbox/a.eml", strings.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := fake.uploads.Load(); got != 1 {
		t.Fatalf("uploads = %d, want 1", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !strings.Contains(string(fake.lastBody), payload) {
		t.Errorf("uploaded media does not contain the payload")
	}
}

// A read error part-way through must abort the upload, not commit the bytes
// read so far as a truncated object.
func TestWriteAbortsOnReadError(t *testing.T) {
	b, fake := newTestBackend(t)
	readErr := errors.New("http: request body too large")

	err := b.Write(context.Background(), "inbox/a.eml", &failingReader{data: []byte("partial"), err: readErr}, -1)
	if err == nil {
		t.Fatal("Write returned nil for a failing body")
	}
	if !errors.Is(err, readErr) {
		t.Errorf("error %v does not wrap the read error", err)
	}
	if got := fake.uploads.Load(); got != 0 {
		t.Errorf("uploads = %d, want 0: the partial body was committed", got)
	}
}

// The writer's cancellation stays inside Write: the caller's context is
// still live afterwards.
func TestWriteDoesNotCancelCallerContext(t *testing.T) {
	b, _ := newTestBackend(t)
	ctx := context.Background()
	_ = b.Write(ctx, "inbox/a.eml", &failingReader{data: []byte("x"), err: io.ErrUnexpectedEOF}, -1)
	if ctx.Err() != nil {
		t.Errorf("caller context cancelled: %v", ctx.Err())
	}
}
