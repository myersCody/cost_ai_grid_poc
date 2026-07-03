package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(sw, r)

		status := strconv.Itoa(sw.status)
		path := normalizePath(r.URL.Path)
		HTTPRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	})
}

var knownPaths = map[string]bool{
	"/api/v1/events":         true,
	"/api/v1/reports/costs":  true,
	"/api/v1/reports/summary": true,
	"/api/v1/debug/config":   true,
	"/healthz":               true,
	"/readyz":                true,
	"/debug/dashboard":       true,
	"/":                      true,
}

func normalizePath(path string) string {
	if strings.HasPrefix(path, "/api/v1/quotas/") {
		return "/api/v1/quotas/{tenant_id}"
	}
	if strings.HasPrefix(path, "/api/v1/customers/") {
		return "/api/v1/customers/{tenant_id}"
	}
	if knownPaths[path] {
		return path
	}
	return "/other"
}
