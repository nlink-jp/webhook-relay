package middleware

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// auditEntry is a structured audit log record.
// Fields are chosen for security monitoring and incident investigation.
type auditEntry struct {
	Timestamp  string `json:"timestamp"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	RemoteAddr string `json:"remote_addr"`
	ForwardFor string `json:"x_forwarded_for,omitempty"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	BytesIn    int64  `json:"bytes_in"`
	UserAgent  string `json:"user_agent"`
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

// AuditLog logs every request as structured JSON for security auditing.
// These logs are ingested by Cloud Logging and can be queried for
// incident investigation, abuse detection, and compliance reporting.
func AuditLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(recorder, r)

		entry := auditEntry{
			Timestamp:  start.UTC().Format(time.RFC3339),
			Method:     r.Method,
			Path:       r.URL.Path,
			RemoteAddr: r.RemoteAddr,
			ForwardFor: r.Header.Get("X-Forwarded-For"),
			Status:     recorder.statusCode,
			DurationMs: time.Since(start).Milliseconds(),
			BytesIn:    r.ContentLength,
			UserAgent:  r.UserAgent(),
		}

		data, err := json.Marshal(entry)
		if err != nil {
			log.Printf("ERROR: failed to marshal audit log: %v", err)
			return
		}
		log.Println(string(data))
	})
}
