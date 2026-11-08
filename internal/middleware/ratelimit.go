package middleware

import (
	"container/list"
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

// visitor tracks a rate limiter per client key.
type visitor struct {
	key      string
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter provides per-IP rate limiting to prevent abuse.
// Each unique client IP gets its own token bucket.
//
// Entries live in a map for lookup and in a list ordered by last use, so
// that eviction of the least recently seen entry is O(1) and never walks
// the whole map while holding the lock.
type RateLimiter struct {
	mu          sync.Mutex
	visitors    map[string]*list.Element // value: *visitor
	order       *list.List               // front = most recently seen
	rps         rate.Limit
	burst       int
	maxVisitors int
}

// NewRateLimiter creates a rate limiter with the given rate and burst.
// It starts a background goroutine to clean up stale entries.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := newRateLimiter(rps, burst, maxVisitors)
	go rl.cleanup()
	return rl
}

func newRateLimiter(rps float64, burst, cap int) *RateLimiter {
	return &RateLimiter{
		visitors:    make(map[string]*list.Element),
		order:       list.New(),
		rps:         rate.Limit(rps),
		burst:       burst,
		maxVisitors: cap,
	}
}

func (rl *RateLimiter) getVisitor(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if e, ok := rl.visitors[key]; ok {
		v := e.Value.(*visitor)
		v.lastSeen = time.Now()
		rl.order.MoveToFront(e)
		return v.limiter
	}

	if len(rl.visitors) >= rl.maxVisitors {
		rl.evictLocked(rl.order.Back())
	}
	v := &visitor{key: key, limiter: rate.NewLimiter(rl.rps, rl.burst), lastSeen: time.Now()}
	rl.visitors[key] = rl.order.PushFront(v)
	return v.limiter
}

// evictLocked removes one entry. The caller holds rl.mu.
func (rl *RateLimiter) evictLocked(e *list.Element) {
	if e == nil {
		return
	}
	delete(rl.visitors, e.Value.(*visitor).key)
	rl.order.Remove(e)
}

// cleanup removes visitors that haven't been seen in 3 minutes. The list is
// ordered by last use, so the walk stops at the first fresh entry.
func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(time.Minute)
		rl.evictStale(3 * time.Minute)
	}
}

func (rl *RateLimiter) evictStale(idle time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for e := rl.order.Back(); e != nil; e = rl.order.Back() {
		if time.Since(e.Value.(*visitor).lastSeen) <= idle {
			return
		}
		rl.evictLocked(e)
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
