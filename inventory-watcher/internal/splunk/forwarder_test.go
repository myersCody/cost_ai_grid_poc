package splunk

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
)

func TestBuildBatch(t *testing.T) {
	f := &Forwarder{index: "test-index"}

	rows := []inventory.RawEventRow{
		{
			ID:           1,
			EventID:      "evt-001",
			EventType:    "osac.cluster.lifecycle",
			EventSource:  "watcher",
			EventTime:    time.Date(2026, 7, 10, 12, 0, 0, 500_000_000, time.UTC),
			TenantID:     "tenant-1",
			ResourceType: "cluster",
			ResourceID:   "cluster-abc",
			Data:         json.RawMessage(`{"state":"RUNNING"}`),
			ReceivedAt:   time.Date(2026, 7, 10, 12, 0, 1, 0, time.UTC),
		},
		{
			ID:           2,
			EventID:      "evt-002",
			EventType:    "osac.compute_instance.lifecycle",
			EventSource:  "watcher",
			EventTime:    time.Date(2026, 7, 10, 12, 1, 0, 0, time.UTC),
			TenantID:     "tenant-2",
			ResourceType: "compute_instance",
			ResourceID:   "vm-xyz",
			Data:         json.RawMessage(`{"cores":4}`),
			ReceivedAt:   time.Date(2026, 7, 10, 12, 1, 1, 0, time.UTC),
		},
	}

	payload := f.buildBatch(rows)

	// Should be newline-delimited JSON (one HEC event per line)
	lines := 0
	for i, b := range payload {
		if b == '\n' {
			lines++
			// Each line should be valid JSON
			start := 0
			if lines > 1 {
				// find previous newline
				for j := i - 1; j >= 0; j-- {
					if payload[j] == '\n' {
						start = j + 1
						break
					}
				}
			}
			var evt hecEvent
			if err := json.Unmarshal(payload[start:i], &evt); err != nil {
				t.Fatalf("line %d not valid JSON: %v", lines, err)
			}
			if evt.Index != "test-index" {
				t.Errorf("expected index test-index, got %s", evt.Index)
			}
			if evt.SourceType != "_json" {
				t.Errorf("expected sourcetype _json, got %s", evt.SourceType)
			}
		}
	}

	if lines != 2 {
		t.Errorf("expected 2 lines, got %d", lines)
	}

	// Verify first event's timestamp includes sub-second precision
	var first hecEvent
	firstLine := payload[:indexByte(payload, '\n')]
	if err := json.Unmarshal(firstLine, &first); err != nil {
		t.Fatal(err)
	}
	if first.Time != 1783684800.5 {
		t.Errorf("expected timestamp 1783684800.5, got %f", first.Time)
	}
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

func TestPostSuccess(t *testing.T) {
	var gotAuth string
	var gotBody []byte

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"Success","code":0}`))
	}))
	defer srv.Close()

	f := &Forwarder{
		client:   srv.Client(),
		hecURL:   srv.URL,
		hecToken: "test-token-123",
		logger:   slog.Default(),
	}

	err := f.post(context.Background(), []byte(`{"event":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuth != "Splunk test-token-123" {
		t.Errorf("expected auth header 'Splunk test-token-123', got %q", gotAuth)
	}
	if string(gotBody) != `{"event":"test"}` {
		t.Errorf("unexpected body: %s", gotBody)
	}
}

func TestPostError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	f := &Forwarder{
		client:   srv.Client(),
		hecURL:   srv.URL,
		hecToken: "bad-token",
		logger:   slog.Default(),
	}

	err := f.post(context.Background(), []byte(`{"event":"test"}`))
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestPostServerDown(t *testing.T) {
	f := &Forwarder{
		client:   &http.Client{Timeout: 100 * time.Millisecond},
		hecURL:   "http://127.0.0.1:1",
		hecToken: "token",
		logger:   slog.Default(),
	}

	err := f.post(context.Background(), []byte(`{"event":"test"}`))
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}
