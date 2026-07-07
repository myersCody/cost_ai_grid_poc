package metrics

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		// Known static paths pass through
		{"/api/v1/events", "/api/v1/events"},
		{"/api/v1/reports/costs", "/api/v1/reports/costs"},
		{"/api/v1/reports/summary", "/api/v1/reports/summary"},
		{"/api/v1/debug/config", "/api/v1/debug/config"},
		{"/healthz", "/healthz"},
		{"/readyz", "/readyz"},
		{"/debug/dashboard", "/debug/dashboard"},
		{"/", "/"},

		// Parameterized paths normalized
		{"/api/v1/quotas/tenant-acme", "/api/v1/quotas/{tenant_id}"},
		{"/api/v1/quotas/tenant-with-dashes-123", "/api/v1/quotas/{tenant_id}"},
		{"/api/v1/customers/cust-001", "/api/v1/customers/{tenant_id}"},
		{"/api/v1/customers/cust-001/entitlements/key/value", "/api/v1/customers/{tenant_id}"},

		// Unknown paths bucketed to prevent cardinality explosion
		{"/random/path", "/other"},
		{"/api/v2/events", "/other"},
		{"/api/v1/unknown", "/other"},
		{"", "/other"},
		{"/x1", "/other"},
		{"/../../etc/passwd", "/other"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := normalizePath(tc.path)
			if got != tc.want {
				t.Errorf("normalizePath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestNormalizePath_QuotasExactPrefix(t *testing.T) {
	got := normalizePath("/api/v1/quotas/")
	if got != "/api/v1/quotas/{tenant_id}" {
		t.Errorf("trailing slash should still normalize, got %q", got)
	}
}

func TestStatusWriter_CapturesStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

	sw.WriteHeader(http.StatusNotFound)

	if sw.status != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", sw.status)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("underlying recorder: got %d, want 404", rec.Code)
	}
}

func TestStatusWriter_DefaultsTo200(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

	if sw.status != http.StatusOK {
		t.Errorf("default status: got %d, want 200", sw.status)
	}
}

func TestRequestLogger_LogsRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := RequestLogger(logger, handler)
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	log := buf.String()
	if !strings.Contains(log, "GET") {
		t.Error("log should contain method")
	}
	if !strings.Contains(log, "/healthz") {
		t.Error("log should contain path")
	}
	if !strings.Contains(log, "request_id") {
		t.Error("log should contain request_id")
	}
}

func TestRequestLogger_UsesXRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := RequestLogger(logger, handler)
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("X-Request-ID", "my-custom-id")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if !strings.Contains(buf.String(), "my-custom-id") {
		t.Error("log should contain custom X-Request-ID")
	}
}

func TestRequestLogger_ProbesAtDebugLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := RequestLogger(logger, handler)
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if buf.String() != "" {
		t.Errorf("probe requests at INFO level should be silent, got: %s", buf.String())
	}
}

func TestRequestLogger_SetsRequestIDInContext(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wrapped := RequestLogger(logger, inner)

	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if capturedID == "" {
		t.Error("request ID not set in context")
	}
	if rec.Header().Get("X-Request-ID") != capturedID {
		t.Errorf("response header X-Request-ID = %q, context has %q", rec.Header().Get("X-Request-ID"), capturedID)
	}
}

func TestRequestLogger_PreservesIncomingRequestID(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wrapped := RequestLogger(logger, inner)

	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	req.Header.Set("X-Request-ID", "incoming-123")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if capturedID != "incoming-123" {
		t.Errorf("expected incoming-123, got %q", capturedID)
	}
}

func TestRequestIDFromContext_Empty(t *testing.T) {
	if id := RequestIDFromContext(context.Background()); id != "" {
		t.Errorf("expected empty, got %q", id)
	}
}

func TestHTTPMiddleware_RecordsRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	wrapped := HTTPMiddleware(handler)
	req := httptest.NewRequest("POST", "/api/v1/events", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("response code: got %d, want 202", rec.Code)
	}
}
