package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	// Very strict limit: 1 rps, burst 2
	rl := NewRateLimiter(1, 2)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 2 requests should pass (burst)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got status %d, want %d", i+1, w.Code, http.StatusOK)
		}
	}

	// Third request should be rate limited
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// Different IP should still pass
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.RemoteAddr = "192.0.2.2:1234"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("different IP: got status %d, want %d", w2.Code, http.StatusOK)
	}
}

// A client that rotates the leftmost X-Forwarded-For entry on every request
// must still share one bucket: the key comes from the hop Cloud Run appends.
func TestRateLimiterIgnoresClientSuppliedForwardedFor(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "169.254.1.1:1234"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.9.%d.%d, 203.0.113.5", i, i))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		want := http.StatusOK
		if i == 2 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("request %d: got status %d, want %d", i+1, w.Code, want)
		}
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"remote addr only", "192.0.2.1:1234", "", "192.0.2.1"},
		{"remote addr without port", "192.0.2.1", "", "192.0.2.1"},
		{"xff single", "10.0.0.1:1234", "203.0.113.5", "203.0.113.5"},
		// The rightmost entry is the one Cloud Run appended; the leftmost is
		// whatever the client chose to send.
		{"xff multiple takes rightmost", "10.0.0.1:1234", "198.51.100.7, 203.0.113.5", "203.0.113.5"},
		{"xff with spaces", "10.0.0.1:1234", "198.51.100.7 ,  203.0.113.5  ", "203.0.113.5"},
		{"xff rightmost not an ip falls back", "10.0.0.1:1234", "203.0.113.5, evil", "10.0.0.1"},
		{"xff garbage falls back", "10.0.0.1:1234", "not-an-ip", "10.0.0.1"},
		{"xff ipv4-mapped ipv6", "10.0.0.1:1234", "::ffff:203.0.113.5", "203.0.113.5"},
		{"ipv6 keyed by /64", "10.0.0.1:1234", "2001:db8:1:2:3:4:5:6", "2001:db8:1:2::"},
		{"ipv6 remote addr keyed by /64", "[2001:db8:1:2:aaaa::1]:1234", "", "2001:db8:1:2::"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			got := extractIP(req)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRateLimiterVisitorCap(t *testing.T) {
	rl := newRateLimiter(1, 1, 3)

	for i := 0; i < 3; i++ {
		rl.getVisitor(fmt.Sprintf("192.0.2.%d", i))
	}
	if got := len(rl.visitors); got != 3 {
		t.Fatalf("visitors = %d, want 3", got)
	}

	// Touch the oldest so that it is no longer the eviction candidate.
	rl.getVisitor("192.0.2.0")

	// The fourth key evicts the least recently seen one, now 192.0.2.1.
	rl.getVisitor("192.0.2.3")
	if got := len(rl.visitors); got != 3 {
		t.Errorf("visitors = %d after overflow, want 3 (cap)", got)
	}
	if _, ok := rl.visitors["192.0.2.1"]; ok {
		t.Error("least recently seen visitor 192.0.2.1 should have been evicted")
	}
	for _, key := range []string{"192.0.2.0", "192.0.2.2", "192.0.2.3"} {
		if _, ok := rl.visitors[key]; !ok {
			t.Errorf("visitor %s should still be tracked", key)
		}
	}
	if rl.order.Len() != len(rl.visitors) {
		t.Errorf("list has %d entries, map has %d", rl.order.Len(), len(rl.visitors))
	}

	// A fresh bucket for the evicted client is not a bypass: it is the same
	// allowance a first-time client gets.
	if !rl.getVisitor("192.0.2.1").Allow() {
		t.Error("re-inserted visitor should get a fresh bucket")
	}
}

func TestRateLimiterEvictStale(t *testing.T) {
	rl := newRateLimiter(1, 1, 10)
	rl.getVisitor("192.0.2.1")
	rl.getVisitor("192.0.2.2")
	rl.visitors["192.0.2.1"].Value.(*visitor).lastSeen = time.Now().Add(-5 * time.Minute)

	rl.evictStale(3 * time.Minute)

	if _, ok := rl.visitors["192.0.2.1"]; ok {
		t.Error("stale visitor should have been evicted")
	}
	if _, ok := rl.visitors["192.0.2.2"]; !ok {
		t.Error("fresh visitor should remain")
	}
	if rl.order.Len() != 1 {
		t.Errorf("list has %d entries, want 1", rl.order.Len())
	}
}
