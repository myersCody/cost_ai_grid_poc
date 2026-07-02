package metrics

import (
	"net/http"
	"strconv"
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

func normalizePath(path string) string {
	switch {
	case len(path) > len("/api/v1/quotas/") && path[:len("/api/v1/quotas/")] == "/api/v1/quotas/":
		return "/api/v1/quotas/{tenant_id}"
	case len(path) > len("/api/v1/customers/") && path[:len("/api/v1/customers/")] == "/api/v1/customers/":
		return "/api/v1/customers/{tenant_id}"
	default:
		return path
	}
}
