package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxVisitors bounds the number of client keys the limiter tracks at once.
// Each entry is a token bucket plus a key of at most 45 bytes, so the cap
// holds the map to a few megabytes however many distinct clients appear.
// When the map is full, the least recently seen entry makes room; the
// evicted client simply starts a fresh bucket on its next request.
const maxVisitors = 10000

// visitor tracks a rate limiter per client IP.
type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter provides per-IP rate limiting to prevent abuse.
// Each unique client IP gets its own token bucket.
type RateLimiter struct {
	mu          sync.Mutex
	visitors    map[string]*visitor
	rps         rate.Limit
	burst       int
	maxVisitors int
}

// NewRateLimiter creates a rate limiter with the given rate and burst.
// It starts a background goroutine to clean up stale entries.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors:    make(map[string]*visitor),
		rps:         rate.Limit(rps),
		burst:       burst,
		maxVisitors: maxVisitors,
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		if len(rl.visitors) >= rl.maxVisitors {
			rl.evictOldestLocked()
		}
		limiter := rate.NewLimiter(rl.rps, rl.burst)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}

	v.lastSeen = time.Now()
	return v.limiter
}

// evictOldestLocked removes the least recently seen visitor. The caller
// holds rl.mu. The scan is linear, but the map never exceeds maxVisitors.
func (rl *RateLimiter) evictOldestLocked() {
	var (
		oldestKey string
		oldest    time.Time
		found     bool
	)
	for ip, v := range rl.visitors {
		if !found || v.lastSeen.Before(oldest) {
			oldestKey, oldest, found = ip, v.lastSeen, true
		}
	}
	if found {
		delete(rl.visitors, oldestKey)
	}
}

// cleanup removes visitors that haven't been seen in 3 minutes.
func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Middleware returns an HTTP middleware that enforces rate limits.
// When the limit is exceeded, it returns 429 Too Many Requests.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		limiter := rl.getVisitor(ip)

		if !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractIP returns the rate-limit key for the request: the client IP as
// seen by the trusted front end.
//
// Cloud Run appends the IP it observed to the right end of X-Forwarded-For
// and passes through whatever the client already sent, so every entry but
// the rightmost is attacker-controlled. Only that rightmost entry is used,
// and only when it parses as an IP; otherwise the key falls back to
// RemoteAddr. IPv6 clients are keyed by their /64, since a single customer
// usually holds at least that much address space.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		if ip := net.ParseIP(last); ip != nil {
			return clientKey(ip)
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		return clientKey(ip)
	}
	return host
}

// clientKey normalises an IP into a bucket key: IPv4 addresses as-is,
// IPv6 addresses truncated to their /64 prefix.
func clientKey(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}
