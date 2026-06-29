package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/osac-project/cost-event-consumer/internal/ingest"
	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metering"
	"github.com/osac-project/cost-event-consumer/internal/rating"
)

var (
	testStore  *inventory.Store
	testMeter  *metering.Meter
	testServer *httptest.Server
	testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
)

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = "postgres://user:pass@localhost:5434/costdb_test"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to test DB: %v\n", err)
		fmt.Fprintf(os.Stderr, "set TEST_DB_URL or run: docker exec cost-db psql -U user -d costdb -c 'CREATE DATABASE costdb_test;'\n")
		os.Exit(1)
	}

	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "test DB not reachable: %v\n", err)
		os.Exit(1)
	}

	testStore = inventory.NewStore(pool, testLogger)
	if err := testStore.RunMigrations(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrations failed: %v\n", err)
		os.Exit(1)
	}

	testMeter = metering.New(testStore, 60*time.Second, testLogger)
	handler := ingest.NewHandler(testStore, testMeter, testLogger)
	testServer = httptest.NewServer(handler.ServeMux())

	if err := rating.SeedDefaultRates(ctx, testStore, testLogger); err != nil {
		fmt.Fprintf(os.Stderr, "seed rates failed: %v\n", err)
		os.Exit(1)
	}
	if err := rating.SeedDefaultQuotas(ctx, testStore, testLogger); err != nil {
		fmt.Fprintf(os.Stderr, "seed quotas failed: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	testServer.Close()
	pool.Close()
	os.Exit(code)
}

// ── Health endpoint ──

func TestHealthEndpoint(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ── Event ingest: MaaS ──

func TestIngestMaaSEvent(t *testing.T) {
	eventID := fmt.Sprintf("test-maas-%d", time.Now().UnixNano())
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.model.lifecycle",
		"source":      "test",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"subject":     "test-tenant",
		"data": map[string]interface{}{
			"tenant_id":        "test-tenant",
			"model_id":         "test-model-1",
			"model_name":       "llama-3-8b",
			"template":         "osac.templates.maas_small",
			"state":            "MODEL_STATE_RUNNING",
			"tokens_in":        25000,
			"tokens_out":       12000,
			"requests":         42,
			"duration_seconds":  60,
		},
	}

	body, _ := json.Marshal(event)
	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("event request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}

	// Verify raw event stored
	var count int
	ctx := context.Background()
	err = testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM raw_events WHERE event_id = $1", eventID).Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("raw event not stored: count=%d, err=%v", count, err)
	}

	// Verify metering entries created (tokens_in, tokens_out, requests = 3)
	err = testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = 'test-model-1' AND resource_type = 'model'").Scan(&count)
	if err != nil || count < 3 {
		t.Errorf("expected >= 3 metering entries, got %d", count)
	}
}

func TestIngestMaaSEventDuplicate(t *testing.T) {
	eventID := fmt.Sprintf("test-maas-dup-%d", time.Now().UnixNano())
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.model.lifecycle",
		"source":      "test",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"subject":     "test-tenant",
		"data": map[string]interface{}{
			"tenant_id": "test-tenant", "model_id": "test-model-dup",
			"model_name": "test", "state": "MODEL_STATE_RUNNING",
			"tokens_in": 100, "tokens_out": 50, "requests": 1, "duration_seconds": 60,
		},
	}

	body, _ := json.Marshal(event)

	// First request
	resp1, _ := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusAccepted {
		t.Errorf("first request: expected 202, got %d", resp1.StatusCode)
	}

	// Second request (duplicate)
	resp2, _ := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("duplicate request: expected 409, got %d", resp2.StatusCode)
	}
}

func TestIngestMaaSEventNonBillable(t *testing.T) {
	eventID := fmt.Sprintf("test-maas-stopped-%d", time.Now().UnixNano())
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.model.lifecycle",
		"source":      "test",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"subject":     "test-tenant",
		"data": map[string]interface{}{
			"tenant_id": "test-tenant", "model_id": "test-model-stopped",
			"model_name": "test", "state": "MODEL_STATE_STOPPED",
			"tokens_in": 100, "tokens_out": 50, "requests": 1, "duration_seconds": 60,
		},
	}

	body, _ := json.Marshal(event)
	resp, _ := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// Event accepted (stored in raw_events) but no metering
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}

	var count int
	ctx := context.Background()
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = 'test-model-stopped'").Scan(&count)
	if count != 0 {
		t.Errorf("stopped model should have 0 metering entries, got %d", count)
	}
}

// ── Event ingest: VM heartbeat ──

func TestIngestVMHeartbeat(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	eventID := "test-vm-" + suffix
	instanceID := "test-vm-" + suffix
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.compute_instance.lifecycle",
		"source":      "osac.metering.collector",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"subject":     "test-tenant",
		"data": map[string]interface{}{
			"duration_seconds":   60,
			"cpu_core_seconds":   480,
			"memory_gib_seconds": 1920,
			"tenant_id":          "test-tenant",
			"instance_id":        instanceID,
			"template":           "osac.templates.ocp_virt_vm",
			"state":              "COMPUTE_INSTANCE_STATE_RUNNING",
			"cores":              8,
			"memory_gib":         32,
		},
	}

	body, _ := json.Marshal(event)
	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}

	ctx := context.Background()
	var count int

	// Verify 3 metering entries (uptime, cpu, memory)
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = $1 AND resource_type = 'compute_instance'", instanceID).Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 VM metering entries, got %d", count)
	}

	// Verify inventory created
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM inventory_compute_instance WHERE instance_id = $1", instanceID).Scan(&count)
	if count != 1 {
		t.Errorf("expected VM in inventory, got %d", count)
	}

	// Verify last_metered_at set
	var metered bool
	testStore.Pool().QueryRow(ctx,
		"SELECT last_metered_at IS NOT NULL FROM inventory_compute_instance WHERE instance_id = $1", instanceID).Scan(&metered)
	if !metered {
		t.Error("last_metered_at should be set")
	}
}

func TestIngestVMHeartbeatNonBillable(t *testing.T) {
	eventID := fmt.Sprintf("test-vm-stopped-%d", time.Now().UnixNano())
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.compute_instance.lifecycle",
		"source":      "test",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"subject":     "test-tenant",
		"data": map[string]interface{}{
			"duration_seconds": 60, "cpu_core_seconds": 0, "memory_gib_seconds": 0,
			"tenant_id": "test-tenant", "instance_id": "test-vm-stopped",
			"state": "COMPUTE_INSTANCE_STATE_STOPPED", "cores": 4, "memory_gib": 16,
		},
	}

	body, _ := json.Marshal(event)
	resp, _ := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	var count int
	ctx := context.Background()
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = 'test-vm-stopped'").Scan(&count)
	if count != 0 {
		t.Errorf("stopped VM should have 0 metering entries, got %d", count)
	}
}

// ── Event ingest: Cluster heartbeat ──

func TestIngestClusterHeartbeat(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	eventID := "test-cluster-" + suffix
	clusterID := "test-cluster-" + suffix
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.cluster.lifecycle",
		"source":      "osac.metering.collector",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"subject":     "test-tenant",
		"data": map[string]interface{}{
			"duration_seconds":    60,
			"worker_node_seconds": 180,
			"node_count":          3,
			"tenant_id":           "test-tenant",
			"cluster_id":          clusterID,
			"template":            "osac.templates.ocp_ci_small",
			"state":               "CLUSTER_STATE_READY",
			"host_type":           "_control_plane",
		},
	}

	body, _ := json.Marshal(event)
	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}

	ctx := context.Background()
	var count int

	// Control plane event → cluster_uptime_seconds + cluster_worker_node_seconds
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = $1 AND resource_type = 'cluster'", clusterID).Scan(&count)
	if count < 1 {
		t.Errorf("expected >= 1 cluster metering entries, got %d", count)
	}
}

// ── Event ingest: bad request ──

func TestIngestBadJSON(t *testing.T) {
	resp, _ := http.Post(testServer.URL+"/api/v1/events", "application/json",
		bytes.NewReader([]byte("not json")))
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// ── Quota status endpoint ──

func TestQuotaStatus(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/api/v1/quotas/test-tenant")
	if err != nil {
		t.Fatalf("quota request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		TenantID string `json:"tenant_id"`
		Period   string `json:"period"`
		Quotas   []struct {
			MeterName  string          `json:"meter_name"`
			Limit      float64         `json:"limit"`
			Consumed   float64         `json:"consumed"`
			Percentage float64         `json:"percentage"`
			Thresholds map[string]bool `json:"thresholds"`
			Alerts     []struct {
				ThresholdPct float64 `json:"threshold_pct"`
				State        string  `json:"state"`
			} `json:"alerts"`
		} `json:"quotas"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.TenantID != "test-tenant" {
		t.Errorf("expected tenant_id=test-tenant, got %s", result.TenantID)
	}

	if result.Period == "" {
		t.Error("period should not be empty")
	}
}

func TestQuotaStatusMissingTenant(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/api/v1/quotas/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing tenant, got %d", resp.StatusCode)
	}
}

func TestQuotaStatusWithConsumption(t *testing.T) {
	ctx := context.Background()

	// Seed a quota for test-tenant so consumption is visible
	testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID:      "test-tenant",
		MeterName:     "maas_tokens_in",
		LimitValue:    1000000,
		Unit:          "tokens",
		Period:        "monthly",
		EffectiveFrom: time.Now().Add(-1 * time.Hour),
	})

	// We already ingested MaaS events for test-tenant in earlier tests.
	resp, err := http.Get(testServer.URL + "/api/v1/quotas/test-tenant")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Quotas []struct {
			MeterName string  `json:"meter_name"`
			Consumed  float64 `json:"consumed"`
		} `json:"quotas"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	hasConsumption := false
	for _, q := range result.Quotas {
		if q.Consumed > 0 {
			hasConsumption = true
			break
		}
	}

	if !hasConsumption {
		t.Error("expected at least one quota with consumption > 0 after ingesting events")
	}
}
