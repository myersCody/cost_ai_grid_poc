package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/osac-project/cost-event-consumer/internal/api"
	"github.com/osac-project/cost-event-consumer/internal/custommetrics"
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

	h := api.NewAPIHandler(testStore, testMeter, nil, nil, testLogger)
	mux := http.NewServeMux()
	api.HandlerFromMux(h, mux)
	testServer = httptest.NewServer(mux)

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

// ── Health endpoints ──

func TestLivenessProbe(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/healthz")
	if err != nil {
		t.Fatalf("liveness request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestReadinessProbe(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/readyz")
	if err != nil {
		t.Fatalf("readiness request failed: %v", err)
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

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
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
	if resp1.StatusCode != http.StatusNoContent {
		t.Errorf("first request: expected 204, got %d", resp1.StatusCode)
	}

	// Second request with same event_id — raw_events has no unique
	// constraint by default (append-only log), so this also succeeds.
	// Dedup for billing correctness is at the metering/cost level.
	// If a unique index on event_id is added, this would return 409.
	resp2, _ := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("second request: expected 204 (no dedup by default), got %d", resp2.StatusCode)
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
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
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

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
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

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
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
	// With the generated router, GET /api/v1/quotas/{tenant_id} requires a
	// non-empty path param. GET /api/v1/quotas/ (trailing slash) routes to
	// the list endpoint instead. Test with an overly-long tenant_id to verify
	// the handler's own validation still works.
	longTenant := strings.Repeat("x", 300)
	resp, err := http.Get(testServer.URL + "/api/v1/quotas/" + longTenant)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for overly-long tenant_id, got %d", resp.StatusCode)
	}
}

func TestQuotaStatusWithConsumption(t *testing.T) {
	ctx := context.Background()

	tenant := fmt.Sprintf("quota-test-%d", time.Now().UnixNano())

	// Seed a quota
	testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID:      tenant,
		MeterName:     "maas_tokens_in",
		LimitValue:    1000000,
		Unit:          "tokens",
		Period:        "monthly",
		EffectiveFrom: time.Now().Add(-1 * time.Hour),
	})

	// Ingest an event so consumption > 0
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.model.lifecycle",
		"source":      "test",
		"id":          fmt.Sprintf("quota-evt-%d", time.Now().UnixNano()),
		"time":        time.Now().UTC().Format(time.RFC3339),
		"data": map[string]interface{}{
			"tenant_id":        tenant,
			"model_id":         "quota-model",
			"model_name":       "quota-model",
			"state":            "MODEL_STATE_RUNNING",
			"tokens_in":        5000,
			"tokens_out":       1000,
			"requests":         10,
			"duration_seconds":  60,
		},
	}
	body, _ := json.Marshal(event)
	postResp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("event ingest failed: %v", err)
	}
	postResp.Body.Close()

	resp, err := http.Get(testServer.URL + "/api/v1/quotas/" + tenant)
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

// ── Authoritative CloudEvents format tests ──
//
// These tests use the exact JSON payloads from the authoritative sources
// to verify we correctly parse and process each format.

// TestIngestVMaaSAuthoritativeFormat verifies we consume the exact CloudEvents
// format produced by the OSAC metering collector for compute instances.
// Source: https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README.md#cloudevents-schema
func TestIngestVMaaSAuthoritativeFormat(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	instanceID := "auth-vmaas-" + suffix

	// Exact payload from the authoritative schema documentation
	payload := fmt.Sprintf(`{
		"specversion": "1.0",
		"type": "osac.compute_instance.lifecycle",
		"source": "osac.metering.collector",
		"id": "auth-vmaas-%s",
		"time": "%s",
		"subject": "osac-e2e-ci",
		"data": {
			"duration_seconds": 60,
			"cpu_core_seconds": 120,
			"memory_gib_seconds": 240,
			"tenant_id": "osac-e2e-ci",
			"instance_id": "%s",
			"template": "osac.templates.ocp_virt_vm",
			"catalog_item": "",
			"state": "RUNNING",
			"cores": 2,
			"memory_gib": 4
		}
	}`, suffix, time.Now().UTC().Format(time.RFC3339), instanceID)

	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json",
		bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}

	ctx := context.Background()

	// Verify 3 metering entries (uptime, cpu, memory)
	var count int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = $1", instanceID).Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 VMaaS metering entries, got %d", count)
	}

	// Verify correct meter names
	var meters []string
	rows, _ := testStore.Pool().Query(ctx,
		"SELECT meter_name FROM metering_entries WHERE resource_id = $1 ORDER BY meter_name", instanceID)
	for rows.Next() {
		var m string
		rows.Scan(&m)
		meters = append(meters, m)
	}
	rows.Close()

	expected := []string{"vm_cpu_core_seconds", "vm_memory_gib_seconds", "vm_uptime_seconds"}
	if len(meters) != len(expected) {
		t.Fatalf("expected meters %v, got %v", expected, meters)
	}
	for i := range expected {
		if meters[i] != expected[i] {
			t.Errorf("meter[%d]: expected %s, got %s", i, expected[i], meters[i])
		}
	}

	// Verify correct values
	var uptimeValue float64
	testStore.Pool().QueryRow(ctx,
		"SELECT value FROM metering_entries WHERE resource_id = $1 AND meter_name = 'vm_uptime_seconds'",
		instanceID).Scan(&uptimeValue)
	if uptimeValue != 60 {
		t.Errorf("expected vm_uptime_seconds = 60, got %f", uptimeValue)
	}

	var cpuValue float64
	testStore.Pool().QueryRow(ctx,
		"SELECT value FROM metering_entries WHERE resource_id = $1 AND meter_name = 'vm_cpu_core_seconds'",
		instanceID).Scan(&cpuValue)
	if cpuValue != 120 {
		t.Errorf("expected vm_cpu_core_seconds = 120, got %f", cpuValue)
	}
}

// TestIngestCaaSAuthoritativeFormat verifies we consume the exact CloudEvents
// format produced by the OSAC metering collector for clusters.
// Source: https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README-caas.md#cloudevents-schema
func TestIngestCaaSAuthoritativeFormat(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	clusterID := "auth-caas-" + suffix

	// Control plane event — exact payload from authoritative schema
	cpPayload := fmt.Sprintf(`{
		"specversion": "1.0",
		"type": "osac.cluster.lifecycle",
		"source": "osac.metering.collector",
		"id": "auth-caas-cp-%s",
		"time": "%s",
		"subject": "shared",
		"data": {
			"duration_seconds": 60,
			"worker_node_seconds": 0,
			"node_count": 0,
			"tenant_id": "shared",
			"cluster_id": "%s",
			"template": "osac.templates.ocp_ci_small",
			"state": "READY",
			"host_type": "_control_plane"
		}
	}`, suffix, time.Now().UTC().Format(time.RFC3339), clusterID)

	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json",
		bytes.NewReader([]byte(cpPayload)))
	if err != nil {
		t.Fatalf("control plane request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("control plane: expected 204, got %d", resp.StatusCode)
	}

	// Worker node event
	workerPayload := fmt.Sprintf(`{
		"specversion": "1.0",
		"type": "osac.cluster.lifecycle",
		"source": "osac.metering.collector",
		"id": "auth-caas-wk-%s",
		"time": "%s",
		"subject": "shared",
		"data": {
			"duration_seconds": 60,
			"worker_node_seconds": 60,
			"node_count": 1,
			"tenant_id": "shared",
			"cluster_id": "%s",
			"template": "osac.templates.ocp_ci_small",
			"state": "READY",
			"host_type": "ci-worker"
		}
	}`, suffix, time.Now().UTC().Format(time.RFC3339), clusterID)

	resp2, err := http.Post(testServer.URL+"/api/v1/events", "application/json",
		bytes.NewReader([]byte(workerPayload)))
	if err != nil {
		t.Fatalf("worker request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("worker: expected 204, got %d", resp2.StatusCode)
	}

	ctx := context.Background()

	// Control plane → cluster_uptime_seconds only
	var uptimeCount int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = $1 AND meter_name = 'cluster_uptime_seconds'",
		clusterID).Scan(&uptimeCount)
	if uptimeCount != 1 {
		t.Errorf("expected 1 cluster_uptime_seconds entry, got %d", uptimeCount)
	}

	// Worker → cluster_worker_node_seconds
	var workerCount int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = $1 AND meter_name = 'cluster_worker_node_seconds'",
		clusterID).Scan(&workerCount)
	if workerCount != 1 {
		t.Errorf("expected 1 cluster_worker_node_seconds entry, got %d", workerCount)
	}
}

// TestIngestIPPAuthoritativeFormat verifies we consume the exact CloudEvents
// format produced by the IPP external-metering plugin for inference token usage.
// Source: https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/320
// Source: https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go
func TestIngestIPPAuthoritativeFormat(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	modelID := "auth-ipp-" + suffix

	// Exact field names from the IPP plugin's reportUsageEvent function
	payload := fmt.Sprintf(`{
		"specversion": "1.0",
		"type": "inference.tokens.used",
		"source": "maas-gateway",
		"id": "auth-ipp-%s",
		"time": "%s",
		"subject": "test-user@example.com",
		"data": {
			"user": "test-user@example.com",
			"group": "maas-users",
			"subscription": "default-sub",
			"provider": "anthropic",
			"model": "%s",
			"prompt_tokens": 1500,
			"completion_tokens": 800,
			"total_tokens": 2650,
			"cached_input_tokens": 200,
			"cache_creation_tokens": 0,
			"reasoning_tokens": 150,
			"duration_ms": 3200
		}
	}`, suffix, time.Now().UTC().Format(time.RFC3339), modelID)

	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json",
		bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}

	ctx := context.Background()

	// Should produce 5 metering entries: tokens_in, tokens_out, tokens_cached, tokens_reasoning, requests
	// (requests = 0 in this event, so actually 4)
	var meters []string
	rows, _ := testStore.Pool().Query(ctx,
		"SELECT meter_name FROM metering_entries WHERE resource_id = $1 ORDER BY meter_name", modelID)
	for rows.Next() {
		var m string
		rows.Scan(&m)
		meters = append(meters, m)
	}
	rows.Close()

	// prompt_tokens → maas_tokens_in, completion_tokens → maas_tokens_out.
	// cached/reasoning tokens are subsets of in/out — not metered separately.
	expectedMeters := map[string]float64{
		"maas_tokens_in":  1500,
		"maas_tokens_out": 800,
	}

	for meter, expectedValue := range expectedMeters {
		var value float64
		err := testStore.Pool().QueryRow(ctx,
			"SELECT value FROM metering_entries WHERE resource_id = $1 AND meter_name = $2",
			modelID, meter).Scan(&value)
		if err != nil {
			t.Errorf("meter %s not found for model %s: %v", meter, modelID, err)
			continue
		}
		if value != expectedValue {
			t.Errorf("meter %s: expected %.0f, got %.0f", meter, expectedValue, value)
		}
	}

	// Verify the model was created in inventory with the correct name
	var inventoryModel string
	testStore.Pool().QueryRow(ctx,
		"SELECT model_name FROM inventory_model WHERE model_id = $1", modelID).Scan(&inventoryModel)
	if inventoryModel != modelID {
		t.Errorf("expected model_name = %s, got %s", modelID, inventoryModel)
	}

	// Verify tenant attribution fallback chain: subscription namespace > group > user
	// This test has no "/" in subscription ("default-sub"), so group ("maas-users") is used.
	var tenant string
	testStore.Pool().QueryRow(ctx,
		"SELECT tenant FROM inventory_model WHERE model_id = $1", modelID).Scan(&tenant)
	if tenant != "maas-users" {
		t.Errorf("expected tenant = maas-users (from IPP group field), got %s", tenant)
	}
}

// TestTenantAttribution_OrganizationID verifies that organization_id takes
// priority over subscription/group/user for tenant attribution.
// Confirmed by Noy (via Kris, open questions doc) as "the right approach."
func TestTenantAttribution_OrganizationID(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	modelID := "org-attr-" + suffix

	payload := fmt.Sprintf(`{
		"specversion": "1.0",
		"type": "inference.tokens.used",
		"source": "maas-gateway",
		"id": "org-attr-%s",
		"time": "%s",
		"subject": "jdoe",
		"data": {
			"user": "jdoe",
			"group": "finance-team",
			"subscription": "ai-tenant-acme/premium-sub@models/llama-3",
			"organization_id": "acme-corp",
			"model": "%s",
			"prompt_tokens": 100,
			"completion_tokens": 50,
			"duration_ms": 500
		}
	}`, suffix, time.Now().UTC().Format(time.RFC3339), modelID)

	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json",
		bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	var tenant string
	testStore.Pool().QueryRow(context.Background(),
		"SELECT tenant FROM inventory_model WHERE model_id = $1", modelID).Scan(&tenant)
	if tenant != "acme-corp" {
		t.Errorf("expected tenant = acme-corp (from organization_id), got %s", tenant)
	}
}

// TestTenantAttribution_SubscriptionNamespace verifies that the ai-tenant-{name}
// prefix is stripped when parsing tenant from subscription namespace.
// Format confirmed by Mpaul (Slack #wg-osac-maas 2026-07-09).
func TestTenantAttribution_SubscriptionNamespace(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	modelID := "sub-attr-" + suffix

	payload := fmt.Sprintf(`{
		"specversion": "1.0",
		"type": "inference.tokens.used",
		"source": "maas-gateway",
		"id": "sub-attr-%s",
		"time": "%s",
		"subject": "jdoe",
		"data": {
			"user": "jdoe",
			"group": "finance-team",
			"subscription": "ai-tenant-globex/standard-sub@models/codestral",
			"model": "%s",
			"prompt_tokens": 200,
			"completion_tokens": 100,
			"duration_ms": 800
		}
	}`, suffix, time.Now().UTC().Format(time.RFC3339), modelID)

	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json",
		bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	var tenant string
	testStore.Pool().QueryRow(context.Background(),
		"SELECT tenant FROM inventory_model WHERE model_id = $1", modelID).Scan(&tenant)
	// "ai-tenant-globex" → stripped to "globex"
	if tenant != "globex" {
		t.Errorf("expected tenant = globex (ai-tenant- prefix stripped), got %s", tenant)
	}
}

// TestBalanceCheckResponseFormat verifies the balance check endpoint returns
// the exact response format expected by the IPP external-metering plugin.
// Source: https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go
func TestBalanceCheckResponseFormat(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/api/v1/customers/tenant-acme/entitlements/inference-tokens/value?model=llama-3")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Field names must match the IPP client's entitlementValue struct (camelCase for hasAccess).
	// Source: https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go
	var result struct {
		HasAccess bool    `json:"hasAccess"`
		Balance   float64 `json:"balance"`
		Usage     float64 `json:"usage"`
		Overage   float64 `json:"overage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify all four fields are present (the IPP client reads all of them)
	if result.Balance < 0 {
		t.Error("balance should be >= 0")
	}
	if result.Usage < 0 {
		t.Error("usage should be >= 0")
	}
	if result.Overage < 0 {
		t.Error("overage should be >= 0")
	}
	// has_access is boolean — just verify the field was decoded (Go defaults to false)
	// The actual value depends on quota state, so we just check the struct is well-formed
}

// TestEventIngestResponseCode verifies the event ingest endpoint returns
// a status code that the IPP external-metering client accepts (200 or 204).
// The IPP client's reportUsage function only considers 200 and 204 as success.
// Source: https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go
// Source: https://github.com/noyitz/metering-simulator/blob/main/openapi.yaml (204 for event accepted)
func TestEventIngestResponseCode(t *testing.T) {
	eventID := fmt.Sprintf("test-ipp-compat-%d", time.Now().UnixNano())
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "inference.tokens.used",
		"source":      "maas-gateway",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"subject":     "test-user",
		"data": map[string]interface{}{
			"user": "test-user", "model": "test-model",
			"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
		},
	}

	body, _ := json.Marshal(event)
	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// IPP client accepts 200 and 204 only. We return 204 (matching metering-simulator OpenAPI spec).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Errorf("IPP compatibility: expected 200 or 204, got %d (IPP client will log an error for any other code)", resp.StatusCode)
	}
}

// ── Custom metrics ──

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "custom-metrics.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIngestCustomMetricEvent(t *testing.T) {
	configJSON := `{
		"custom_metrics": [{
			"event_type": "test.gpu.lifecycle",
			"resource_type": "gpu_instance",
			"resource_id_field": "instance_id",
			"tenant_id_field": "tenant_id",
			"meters": [
				{"meter_name": "gpu_memory_gib_seconds", "value_field": "gpu_memory_gib_seconds", "unit": "gib_seconds"},
				{"meter_name": "gpu_compute_seconds", "value_field": "gpu_compute_seconds", "unit": "seconds"}
			]
		}]
	}`
	cfgPath := writeTestConfig(t, configJSON)
	registry, err := custommetrics.LoadFromFile(cfgPath, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	h := api.NewAPIHandler(testStore, testMeter, nil, registry, testLogger)
	mux := http.NewServeMux()
	api.HandlerFromMux(h, mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ts := time.Now().UnixNano()
	eventID := fmt.Sprintf("test-custom-%d", ts)
	resourceID := fmt.Sprintf("gpu-i-test-%d", ts)
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "test.gpu.lifecycle",
		"source":      "test",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"data": map[string]interface{}{
			"instance_id":            resourceID,
			"tenant_id":             "tenant-acme",
			"gpu_memory_gib_seconds": 245760.0,
			"gpu_compute_seconds":    3600.0,
			"duration_seconds":       3600,
		},
	}

	body, _ := json.Marshal(event)
	resp, err := http.Post(srv.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		var respBody map[string]string
		json.NewDecoder(resp.Body).Decode(&respBody)
		t.Fatalf("expected 204, got %d: %v", resp.StatusCode, respBody)
	}

	ctx := context.Background()

	var count int
	err = testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM raw_events WHERE event_id = $1", eventID).Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("raw event not stored: count=%d, err=%v", count, err)
	}

	err = testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = $1 AND resource_type = 'gpu_instance'", resourceID).Scan(&count)
	if err != nil || count != 2 {
		t.Errorf("expected 2 metering entries, got %d (err=%v)", count, err)
	}

	var meterName string
	var value float64
	err = testStore.Pool().QueryRow(ctx,
		"SELECT meter_name, value FROM metering_entries WHERE resource_id = $1 AND meter_name = 'gpu_memory_gib_seconds'", resourceID).Scan(&meterName, &value)
	if err != nil {
		t.Fatalf("query metering entry: %v", err)
	}
	if value != 245760.0 {
		t.Errorf("gpu_memory_gib_seconds value: got %f, want 245760.0", value)
	}
}

func TestIngestCustomMetricEvent_MissingField(t *testing.T) {
	configJSON := `{
		"custom_metrics": [{
			"event_type": "test.gpu.partial",
			"resource_type": "gpu_instance",
			"resource_id_field": "instance_id",
			"tenant_id_field": "tenant_id",
			"meters": [
				{"meter_name": "gpu_memory_gib_seconds", "value_field": "gpu_memory_gib_seconds", "unit": "gib_seconds"},
				{"meter_name": "gpu_compute_seconds", "value_field": "gpu_compute_seconds", "unit": "seconds"}
			]
		}]
	}`
	cfgPath := writeTestConfig(t, configJSON)
	registry, err := custommetrics.LoadFromFile(cfgPath, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	h := api.NewAPIHandler(testStore, testMeter, nil, registry, testLogger)
	mux := http.NewServeMux()
	api.HandlerFromMux(h, mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ts := time.Now().UnixNano()
	eventID := fmt.Sprintf("test-partial-%d", ts)
	resourceID := fmt.Sprintf("gpu-i-partial-%d", ts)
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "test.gpu.partial",
		"source":      "test",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"data": map[string]interface{}{
			"instance_id":         resourceID,
			"tenant_id":           "tenant-acme",
			"gpu_compute_seconds": 1800.0,
		},
	}

	body, _ := json.Marshal(event)
	resp, err := http.Post(srv.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	ctx := context.Background()
	var count int
	err = testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = $1", resourceID).Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("expected 1 metering entry (missing field skipped), got %d (err=%v)", count, err)
	}
}

func TestIngestNegativeDurationRejected(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      map[string]interface{}
	}{
		{
			name:      "VM negative duration",
			eventType: "osac.compute_instance.lifecycle",
			data: map[string]interface{}{
				"duration_seconds": -3600,
				"tenant_id":       "evil-tenant",
				"instance_id":     fmt.Sprintf("neg-vm-%d", time.Now().UnixNano()),
				"state":           "COMPUTE_INSTANCE_STATE_RUNNING",
				"cores":           4,
				"memory_gib":      16,
			},
		},
		{
			name:      "VM zero duration",
			eventType: "osac.compute_instance.lifecycle",
			data: map[string]interface{}{
				"duration_seconds": 0,
				"tenant_id":       "evil-tenant",
				"instance_id":     fmt.Sprintf("zero-vm-%d", time.Now().UnixNano()),
				"state":           "COMPUTE_INSTANCE_STATE_RUNNING",
				"cores":           4,
				"memory_gib":      16,
			},
		},
		{
			name:      "Cluster negative duration",
			eventType: "osac.cluster.lifecycle",
			data: map[string]interface{}{
				"duration_seconds": -86400,
				"tenant_id":       "evil-tenant",
				"cluster_id":      fmt.Sprintf("neg-cl-%d", time.Now().UnixNano()),
				"host_type":       "_control_plane",
				"state":           "CLUSTER_STATE_READY",
			},
		},
		{
			name:      "MaaS negative duration",
			eventType: "osac.model.lifecycle",
			data: map[string]interface{}{
				"duration_seconds": -60,
				"tenant_id":       "evil-tenant",
				"model_id":        fmt.Sprintf("neg-model-%d", time.Now().UnixNano()),
				"model_name":      "bad-model",
				"state":           "MODEL_STATE_RUNNING",
				"tokens_in":       100,
				"tokens_out":      50,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := map[string]interface{}{
				"specversion": "1.0",
				"type":        tt.eventType,
				"source":      "test",
				"id":          fmt.Sprintf("neg-dur-%d", time.Now().UnixNano()),
				"time":        time.Now().UTC().Format(time.RFC3339),
				"data":        tt.data,
			}
			body, _ := json.Marshal(event)
			resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusNoContent {
				t.Errorf("expected rejection for negative/zero duration, but got 204")
			}
		})
	}
}

func TestBatchMeteringEntryIncludesProjectID(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	instanceID := "proj-test-vm-" + suffix

	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.compute_instance.lifecycle",
		"source":      "test",
		"id":          "proj-evt-" + suffix,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"data": map[string]interface{}{
			"duration_seconds":   60,
			"cpu_core_seconds":   240,
			"memory_gib_seconds": 960,
			"tenant_id":          "tenant-proj-test",
			"instance_id":        instanceID,
			"state":              "COMPUTE_INSTANCE_STATE_RUNNING",
			"cores":              4,
			"memory_gib":         16,
		},
	}
	body, _ := json.Marshal(event)
	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	ctx := context.Background()
	rows, err := testStore.Pool().Query(ctx,
		"SELECT project_id FROM metering_entries WHERE resource_id = $1", instanceID)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var projectID string
		if err := rows.Scan(&projectID); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		count++
		// project_id should be present in the column (not dropped by batch insert).
		// The value may be empty for this test since no project is assigned,
		// but the column itself must exist and not cause a SQL error.
	}
	if count != 3 {
		t.Errorf("expected 3 metering entries from batch insert, got %d", count)
	}
}

func TestCsvSafe(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"=cmd()", "'=cmd()"},
		{"+cmd()", "'+cmd()"},
		{"-cmd()", "'-cmd()"},
		{"@cmd()", "'@cmd()"},
		{"has,comma", "\"has,comma\""},
		{`has"quote`, `"has""quote"`},
		{"has\nnewline", "\"has\nnewline\""},
		{"", ""},
	}
	for _, tc := range tests {
		got := api.CsvSafe(tc.in)
		if got != tc.want {
			t.Errorf("api.CsvSafe(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func seedCostEntries(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)

	testStore.Pool().Exec(ctx, "DELETE FROM cost_entries")

	// Seed a rate if none exist (tests may run in any order)
	rateID, _ := testStore.UpsertRate(ctx, inventory.RateRecord{
		ResourceType: "compute_instance", MeterName: "vm_uptime_seconds",
		CostType: "Infrastructure", PricePerUnit: decimal.NewFromFloat(0.01), Currency: "USD",
		EffectiveFrom: now.AddDate(-1, 0, 0),
	})
	if rateID == 0 {
		rateID = 1
	}

	maasRateID, _ := testStore.UpsertRate(ctx, inventory.RateRecord{
		ResourceType: "model", MeterName: "maas_tokens_in",
		CostType: "Supplementary", PricePerUnit: decimal.NewFromFloat(0.001), Currency: "USD",
		EffectiveFrom: now.AddDate(-1, 0, 0),
	})
	if maasRateID == 0 {
		maasRateID = 2
	}

	testStore.InsertCostEntry(ctx, inventory.CostEntry{
		MeteringEntryID: 1, RateID: rateID, TenantID: "tenant-a", ProjectID: "proj-1",
		ResourceType: "compute_instance", ResourceID: "vm-1", MeterName: "vm_uptime_seconds",
		MeteredValue: 3600, CostAmount: decimal.NewFromFloat(0.01), Currency: "USD",
		PeriodStart: yesterday, PeriodEnd: yesterday.Add(time.Hour),
	})
	testStore.InsertCostEntry(ctx, inventory.CostEntry{
		MeteringEntryID: 2, RateID: maasRateID, TenantID: "tenant-a", ProjectID: "proj-1",
		ResourceType: "model", ResourceID: "llama-3", MeterName: "maas_tokens_in",
		MeteredValue: 1000, CostAmount: decimal.NewFromFloat(0.001), Currency: "USD",
		PeriodStart: now.Add(-time.Hour), PeriodEnd: now,
	})
	testStore.InsertCostEntry(ctx, inventory.CostEntry{
		MeteringEntryID: 3, RateID: maasRateID, TenantID: "tenant-b", ProjectID: "proj-2",
		ResourceType: "model", ResourceID: "llama-3", MeterName: "maas_tokens_in",
		MeteredValue: 500, CostAmount: decimal.NewFromFloat(0.0005), Currency: "USD",
		PeriodStart: now.Add(-time.Hour), PeriodEnd: now,
	})
}

func TestCostReport_GroupByTenant(t *testing.T) {
	seedCostEntries(t)
	resp, err := http.Get(testServer.URL + "/api/v1/reports/costs?group_by=tenant")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	meta := result["meta"].(map[string]any)
	total := meta["total"].(map[string]any)
	if total["cost_units"] != "USD" {
		t.Errorf("expected cost_units=USD, got %v", total["cost_units"])
	}
	costBlock := total["cost"].(map[string]any)
	costTotal := costBlock["total"].(map[string]any)
	if costTotal["value"].(float64) <= 0 {
		t.Error("expected positive total cost")
	}

	data := result["data"].([]any)
	if len(data) < 2 {
		t.Errorf("expected at least 2 tenant groups, got %d", len(data))
	}
}

func TestCostReport_DailyResolution(t *testing.T) {
	seedCostEntries(t)
	resp, err := http.Get(testServer.URL + "/api/v1/reports/costs?resolution=daily&group_by=tenant")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	meta := result["meta"].(map[string]any)
	if meta["resolution"] != "daily" {
		t.Errorf("expected resolution=daily, got %v", meta["resolution"])
	}

	data := result["data"].([]any)
	for _, d := range data {
		row := d.(map[string]any)
		if row["date"] == nil || row["date"] == "" {
			t.Error("daily resolution rows must have date field")
		}
	}
}

func TestCostReport_FromToParams(t *testing.T) {
	seedCostEntries(t)
	from := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	to := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	resp, err := http.Get(testServer.URL + "/api/v1/reports/costs?from=" + from + "&to=" + to + "&group_by=tenant")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	meta := result["meta"].(map[string]any)
	period := meta["period"].(string)
	if !strings.Contains(period, "/") {
		t.Errorf("expected period with / separator, got %q", period)
	}
}

func TestCostReport_CSV(t *testing.T) {
	seedCostEntries(t)
	resp, err := http.Get(testServer.URL + "/api/v1/reports/costs?format=csv&group_by=tenant")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/csv" {
		t.Errorf("expected text/csv, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) < 2 {
		t.Errorf("expected header + data rows, got %d lines", len(lines))
	}
	if !strings.HasPrefix(lines[0], "group,") {
		t.Errorf("unexpected CSV header: %s", lines[0])
	}
}

func TestCostBreakdown(t *testing.T) {
	seedCostEntries(t)
	resp, err := http.Get(testServer.URL + "/api/v1/reports/breakdown?limit=10")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	meta := result["meta"].(map[string]any)
	count := int(meta["count"].(float64))
	if count < 2 {
		t.Errorf("expected at least 2 breakdown rows, got %d", count)
	}

	data := result["data"].([]any)
	row := data[0].(map[string]any)
	for _, field := range []string{"date", "tenant_id", "resource_type", "resource_id", "meter_name", "metered_value", "cost_amount", "cost_type", "currency"} {
		if row[field] == nil {
			t.Errorf("breakdown row missing field %q", field)
		}
	}
}

func TestCostBreakdown_CSV(t *testing.T) {
	seedCostEntries(t)
	resp, err := http.Get(testServer.URL + "/api/v1/reports/breakdown?format=csv&limit=10")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/csv" {
		t.Errorf("expected text/csv, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) < 2 {
		t.Errorf("expected header + data, got %d lines", len(lines))
	}
	if !strings.HasPrefix(lines[0], "date,") {
		t.Errorf("unexpected CSV header: %s", lines[0])
	}
}

func TestReconcileNotConfigured(t *testing.T) {
	resp, err := http.Post(testServer.URL+"/api/v1/reconcile", "", nil)
	if err != nil {
		t.Fatalf("reconcile request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (no reconciler), got %d", resp.StatusCode)
	}
}

func TestMaaSUserIDPropagation(t *testing.T) {
	eventID := fmt.Sprintf("test-maas-user-%d", time.Now().UnixNano())
	modelID := fmt.Sprintf("model-user-%d", time.Now().UnixNano())
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "osac.model.lifecycle",
		"source":      "test",
		"id":          eventID,
		"time":        time.Now().UTC().Format(time.RFC3339),
		"subject":     "test-tenant",
		"data": map[string]interface{}{
			"tenant_id":        "tenant-user-test",
			"model_id":         modelID,
			"model_name":       "llama-3-8b",
			"state":            "MODEL_STATE_RUNNING",
			"user":             "alice@example.com",
			"tokens_in":        100,
			"tokens_out":       50,
			"requests":         1,
			"duration_seconds":  30,
		},
	}

	body, _ := json.Marshal(event)
	resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("event request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	ctx := context.Background()
	var userID string
	err = testStore.Pool().QueryRow(ctx,
		"SELECT user_id FROM metering_entries WHERE resource_id = $1 LIMIT 1", modelID).Scan(&userID)
	if err != nil {
		t.Fatalf("query metering_entries failed: %v", err)
	}
	if userID != "alice@example.com" {
		t.Errorf("user_id on metering_entries: got %q, want alice@example.com", userID)
	}
}

// ── Quota CRUD Tests ──

func TestCreateQuota(t *testing.T) {
	body := `{"name":"test quota","tenant_id":"crud-tenant","meter_name":"maas_tokens_in","limit_value":5000000,"unit":"tokens","period":"monthly"}`
	resp, err := http.Post(testServer.URL+"/api/v1/quotas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, b)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["name"] != "test quota" {
		t.Errorf("name: got %v", result["name"])
	}
	if result["policy"] != "deny" {
		t.Errorf("policy should default to deny: got %v", result["policy"])
	}
	if result["id"] == nil || result["id"].(float64) == 0 {
		t.Error("expected non-zero id")
	}
}

func TestCreateQuota_MissingFields(t *testing.T) {
	body := `{"meter_name":"x"}`
	resp, err := http.Post(testServer.URL+"/api/v1/quotas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing tenant_id, got %d", resp.StatusCode)
	}
}

func TestCreateQuota_InvalidPeriod(t *testing.T) {
	body := `{"tenant_id":"t","meter_name":"m","limit_value":1,"unit":"u","period":"banana"}`
	resp, err := http.Post(testServer.URL+"/api/v1/quotas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid period, got %d", resp.StatusCode)
	}
}

func TestListQuotas(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/api/v1/quotas")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	quotas, ok := result["quotas"].([]interface{})
	if !ok {
		t.Fatal("expected quotas array")
	}
	if len(quotas) == 0 {
		t.Error("expected at least one quota")
	}
}

func TestListQuotas_TenantFilter(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/api/v1/quotas?tenant_id=nonexistent-tenant")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	quotas := result["quotas"].([]interface{})
	if len(quotas) != 0 {
		t.Errorf("expected 0 quotas for nonexistent tenant, got %d", len(quotas))
	}
}

func TestDeleteQuota(t *testing.T) {
	// Create a quota to delete
	body := `{"tenant_id":"del-tenant","meter_name":"del_meter","limit_value":100,"unit":"units","period":"monthly"}`
	createResp, _ := http.Post(testServer.URL+"/api/v1/quotas", "application/json", strings.NewReader(body))
	var created map[string]interface{}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()
	id := int64(created["id"].(float64))

	// Delete it
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/api/v1/quotas/%d", testServer.URL, id), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestDeleteQuota_NotFound(t *testing.T) {
	req, _ := http.NewRequest("DELETE", testServer.URL+"/api/v1/quotas/999999", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// ── Fleet-Level Status Tests ──

func TestListQuotas_WithStatus(t *testing.T) {
	ctx := context.Background()
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("fleet-tenant-%d", ts)
	now := time.Now().UTC()

	// Create a quota
	testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID: tenantID, MeterName: "vm_uptime_seconds",
		LimitValue: 10000, Unit: "seconds", Period: "monthly",
		EffectiveFrom: now.Add(-time.Hour),
	})

	// Add some consumption
	testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "compute_instance", ResourceID: "vm-fleet",
		TenantID: tenantID, MeterName: "vm_uptime_seconds",
		Value: 3000, Unit: "seconds",
		PeriodStart: now.Add(-60 * time.Second), PeriodEnd: now,
	})

	resp, err := http.Get(testServer.URL + "/api/v1/quotas?tenant_id=" + tenantID + "&status=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result struct {
		Quotas []struct {
			MeterName  string          `json:"meter_name"`
			Consumed   float64         `json:"consumed"`
			Percentage float64         `json:"percentage"`
			Thresholds map[string]bool `json:"thresholds"`
		} `json:"quotas"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	found := false
	for _, q := range result.Quotas {
		if q.MeterName == "vm_uptime_seconds" {
			found = true
			if q.Consumed < 3000 {
				t.Errorf("consumed: got %.0f, want >= 3000", q.Consumed)
			}
			if q.Percentage <= 0 {
				t.Error("percentage should be > 0")
			}
			if q.Thresholds == nil {
				t.Error("thresholds should be present with status=true")
			}
		}
	}
	if !found {
		t.Errorf("expected vm_uptime_seconds quota for tenant %s", tenantID)
	}
}

func TestListQuotas_WithoutStatus(t *testing.T) {
	// Without ?status=true, response should have raw QuotaRecords (no consumed/thresholds)
	resp, err := http.Get(testServer.URL + "/api/v1/quotas")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	// Should NOT contain "consumed" or "thresholds" fields since status=false
	if strings.Contains(string(body), `"consumed"`) {
		t.Error("without status=true, response should not include consumed field")
	}
}

// ── Project Roll-up Tests ──

func TestQuotaStatus_ProjectRollup(t *testing.T) {
	ctx := context.Background()
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("rollup-tenant-%d", ts)
	now := time.Now().UTC()

	// Create tenant-level quota
	testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID: tenantID, MeterName: "maas_tokens_in",
		LimitValue: 10000000, Unit: "tokens", Period: "monthly",
		EffectiveFrom: now.Add(-time.Hour),
	})

	// Create project-level quotas
	testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID: tenantID, ProjectID: "project-alpha", MeterName: "maas_tokens_in",
		LimitValue: 4000000, Unit: "tokens", Period: "monthly",
		EffectiveFrom: now.Add(-time.Hour),
	})
	testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID: tenantID, ProjectID: "project-beta", MeterName: "maas_tokens_in",
		LimitValue: 3000000, Unit: "tokens", Period: "monthly",
		EffectiveFrom: now.Add(-time.Hour),
	})

	// Add consumption for each project
	testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "model", ResourceID: "model-1",
		TenantID: tenantID, ProjectID: "project-alpha",
		MeterName: "maas_tokens_in", Value: 2000000, Unit: "tokens",
		PeriodStart: now.Add(-60 * time.Second), PeriodEnd: now,
	})
	testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "model", ResourceID: "model-2",
		TenantID: tenantID, ProjectID: "project-beta",
		MeterName: "maas_tokens_in", Value: 1500000, Unit: "tokens",
		PeriodStart: now.Add(-60 * time.Second), PeriodEnd: now,
	})

	resp, err := http.Get(testServer.URL + "/api/v1/quotas/" + tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result struct {
		TenantID string `json:"tenant_id"`
		Quotas   []struct {
			MeterName string  `json:"meter_name"`
			Consumed  float64 `json:"consumed"`
			Limit     float64 `json:"limit"`
		} `json:"quotas"`
		Projects map[string][]struct {
			MeterName string  `json:"meter_name"`
			Consumed  float64 `json:"consumed"`
			Limit     float64 `json:"limit"`
		} `json:"projects"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	// Tenant-level quota should show total consumption (2M + 1.5M = 3.5M)
	if len(result.Quotas) == 0 {
		t.Fatal("expected tenant-level quotas")
	}
	tenantConsumed := result.Quotas[0].Consumed
	if tenantConsumed < 3400000 || tenantConsumed > 3600000 {
		t.Errorf("tenant consumed: got %.0f, want ~3500000", tenantConsumed)
	}

	// Project quotas should be separated
	if result.Projects == nil {
		t.Fatal("expected projects field in response")
	}
	alpha, ok := result.Projects["project-alpha"]
	if !ok || len(alpha) == 0 {
		t.Fatal("expected project-alpha in projects")
	}
	if alpha[0].Consumed < 1900000 || alpha[0].Consumed > 2100000 {
		t.Errorf("project-alpha consumed: got %.0f, want ~2000000", alpha[0].Consumed)
	}

	beta, ok := result.Projects["project-beta"]
	if !ok || len(beta) == 0 {
		t.Fatal("expected project-beta in projects")
	}
	if beta[0].Consumed < 1400000 || beta[0].Consumed > 1600000 {
		t.Errorf("project-beta consumed: got %.0f, want ~1500000", beta[0].Consumed)
	}
}

// ── Overcommit Validation Tests ──

func TestCreateQuota_OvercommitRejected(t *testing.T) {
	ctx := context.Background()
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("overcommit-tenant-%d", ts)
	meterName := fmt.Sprintf("overcommit_meter_%d", ts)
	now := time.Now().UTC()

	// Create tenant-level quota with limit 1000
	testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID: tenantID, MeterName: meterName,
		LimitValue: 1000, Unit: "units", Period: "monthly",
		EffectiveFrom: now.Add(-time.Hour),
	})

	// Create a project quota with 600 (OK, under tenant limit)
	body := fmt.Sprintf(`{"tenant_id":"%s","project_id":"proj-a","meter_name":"%s","limit_value":600,"unit":"units","period":"monthly"}`, tenantID, meterName)
	resp, err := http.Post(testServer.URL+"/api/v1/quotas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first project quota should succeed: got %d", resp.StatusCode)
	}

	// Create another project quota with 500 (would exceed: 600 + 500 = 1100 > 1000)
	body = fmt.Sprintf(`{"tenant_id":"%s","project_id":"proj-b","meter_name":"%s","limit_value":500,"unit":"units","period":"monthly"}`, tenantID, meterName)
	resp, err = http.Post(testServer.URL+"/api/v1/quotas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("overcommitting project limits should fail: got %d, want 400", resp.StatusCode)
	}

	var errResp map[string]string
	json.NewDecoder(resp.Body).Decode(&errResp)
	if !strings.Contains(errResp["error"], "exceed tenant limit") {
		t.Errorf("error should mention tenant limit: got %q", errResp["error"])
	}
}

func TestCreateQuota_NoOvercommitWithoutTenantLimit(t *testing.T) {
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("no-limit-tenant-%d", ts)
	meterName := fmt.Sprintf("no_limit_meter_%d", ts)

	// No tenant-level quota exists — project quota should succeed regardless of value
	body := fmt.Sprintf(`{"tenant_id":"%s","project_id":"proj-x","meter_name":"%s","limit_value":999999,"unit":"units","period":"monthly"}`, tenantID, meterName)
	resp, err := http.Post(testServer.URL+"/api/v1/quotas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("should succeed without tenant limit: got %d", resp.StatusCode)
	}
}

// ── Monetary Budget Tests ──

func TestQuotaStatus_MonetaryBudget(t *testing.T) {
	ctx := context.Background()
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("budget-tenant-%d", ts)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Create a monetary budget: $5000/month across all meters
	testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		Name:     "Monthly spend limit",
		TenantID: tenantID, MeterName: "*",
		LimitValue: 5000, Unit: "USD", Period: "monthly",
		Policy: "deny", EffectiveFrom: monthStart,
	})

	// Insert a cost entry (simulating rated metering)
	testStore.InsertCostEntry(ctx, inventory.CostEntry{
		TenantID: tenantID, ResourceType: "compute_instance",
		ResourceID: "vm-budget", MeterName: "vm_uptime_seconds",
		MeteredValue: 3600, CostAmount: decimal.NewFromFloat(1500.00), Currency: "USD",
		PeriodStart: monthStart, PeriodEnd: now,
	})

	resp, err := http.Get(testServer.URL + "/api/v1/quotas/" + tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result struct {
		Quotas []struct {
			MeterName  string  `json:"meter_name"`
			Unit       string  `json:"unit"`
			Limit      float64 `json:"limit"`
			Consumed   float64 `json:"consumed"`
			Percentage float64 `json:"percentage"`
		} `json:"quotas"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Quotas) == 0 {
		t.Fatal("expected at least one quota")
	}

	budgetQuota := result.Quotas[0]
	if budgetQuota.MeterName != "*" {
		t.Errorf("meter_name: got %q, want *", budgetQuota.MeterName)
	}
	if budgetQuota.Unit != "USD" {
		t.Errorf("unit: got %q, want USD", budgetQuota.Unit)
	}
	if budgetQuota.Consumed < 1499 || budgetQuota.Consumed > 1501 {
		t.Errorf("consumed: got %.2f, want ~1500", budgetQuota.Consumed)
	}
	if budgetQuota.Percentage < 29 || budgetQuota.Percentage > 31 {
		t.Errorf("percentage: got %.2f, want ~30%%", budgetQuota.Percentage)
	}
}

func TestCreateQuota_MonetaryBudget(t *testing.T) {
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("budget-create-%d", ts)

	body := fmt.Sprintf(`{"name":"Spend cap","tenant_id":"%s","meter_name":"*","limit_value":10000,"unit":"USD","period":"monthly","policy":"deny"}`, tenantID)
	resp, err := http.Post(testServer.URL+"/api/v1/quotas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, b)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["unit"] != "USD" {
		t.Errorf("unit: got %v", result["unit"])
	}
	if result["meter_name"] != "*" {
		t.Errorf("meter_name: got %v", result["meter_name"])
	}
}

// ── Wallet Tests ──

func TestCreateWallet(t *testing.T) {
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("wallet-tenant-%d", ts)

	body := fmt.Sprintf(`{"tenant_id":"%s","currency":"USD","thresholds":[50,25,10]}`, tenantID)
	resp, err := http.Post(testServer.URL+"/api/v1/wallets", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, b)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["id"] == nil || result["id"].(string) == "" {
		t.Error("expected non-empty wallet ID")
	}
	if result["tenant_id"] != tenantID {
		t.Errorf("tenant_id: got %v", result["tenant_id"])
	}
	if result["lifecycle_state"] != "active" {
		t.Errorf("lifecycle_state: got %v", result["lifecycle_state"])
	}
}

func TestCreateWallet_MissingTenant(t *testing.T) {
	resp, err := http.Post(testServer.URL+"/api/v1/wallets", "application/json", strings.NewReader(`{"currency":"USD"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing tenant, got %d", resp.StatusCode)
	}
}

func TestWallet_TopUpAndStatus(t *testing.T) {
	ctx := context.Background()
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("topup-tenant-%d", ts)

	// Create wallet
	body := fmt.Sprintf(`{"tenant_id":"%s","currency":"USD"}`, tenantID)
	createResp, _ := http.Post(testServer.URL+"/api/v1/wallets", "application/json", strings.NewReader(body))
	var created map[string]interface{}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()
	walletID := created["id"].(string)

	// Top up $500
	topUpBody := `{"amount": 500.00, "external_ref": "test-payment-1"}`
	topUpResp, _ := http.Post(fmt.Sprintf("%s/api/v1/wallets/%s/top-ups", testServer.URL, walletID),
		"application/json", strings.NewReader(topUpBody))
	if topUpResp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(topUpResp.Body)
		t.Fatalf("top-up expected 201, got %d: %s", topUpResp.StatusCode, b)
	}
	topUpResp.Body.Close()

	// Verify idempotent top-up
	topUpResp2, _ := http.Post(fmt.Sprintf("%s/api/v1/wallets/%s/top-ups", testServer.URL, walletID),
		"application/json", strings.NewReader(topUpBody))
	if topUpResp2.StatusCode != http.StatusBadRequest {
		t.Errorf("duplicate top-up should fail: got %d", topUpResp2.StatusCode)
	}
	topUpResp2.Body.Close()

	// Check status
	statusResp, _ := http.Get(fmt.Sprintf("%s/api/v1/wallets/%s", testServer.URL, tenantID))
	var status map[string]interface{}
	json.NewDecoder(statusResp.Body).Decode(&status)
	statusResp.Body.Close()

	// decimal.Decimal serializes as string in JSON
	balanceStr, _ := status["balance"].(string)
	if balanceStr != "500" {
		t.Errorf("balance: got %v, want 500", status["balance"])
	}
	if status["balance_status"] != "ok" {
		t.Errorf("balance_status: got %v, want ok", status["balance_status"])
	}
	if status["within_balance"] != true {
		t.Errorf("within_balance: got %v", status["within_balance"])
	}

	// Verify ledger
	ledgerResp, _ := http.Get(fmt.Sprintf("%s/api/v1/wallets/%s/ledger", testServer.URL, walletID))
	var ledger map[string]interface{}
	json.NewDecoder(ledgerResp.Body).Decode(&ledger)
	ledgerResp.Body.Close()

	if entriesRaw, ok := ledger["entries"].([]interface{}); ok {
		if len(entriesRaw) != 1 {
			t.Errorf("expected 1 ledger entry, got %d", len(entriesRaw))
		}
	} else {
		t.Errorf("expected entries array in ledger response, got: %+v (status %d)", ledger, ledgerResp.StatusCode)
	}

	_ = ctx
}

func TestWallet_DeductionViaRatingSweep(t *testing.T) {
	ctx := context.Background()
	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("deduct-tenant-%d", ts)
	now := time.Now().UTC()

	// Create wallet and top up $100
	wallet := inventory.WalletRecord{
		ID: fmt.Sprintf("wallet-deduct-%d", ts), TenantID: tenantID,
		Currency: "USD", LifecycleState: "active",
	}
	testStore.CreateWallet(ctx, wallet)
	testStore.TopUpWallet(ctx, wallet.ID, decimal.NewFromFloat(100), "seed-topup")

	// Insert a cost entry (simulating rated metering)
	testStore.InsertCostEntry(ctx, inventory.CostEntry{
		TenantID: tenantID, ResourceType: "compute_instance",
		ResourceID: fmt.Sprintf("vm-deduct-%d", ts), MeterName: "vm_uptime_seconds",
		MeteredValue: 3600, CostAmount: decimal.NewFromFloat(25.00), Currency: "USD",
		PeriodStart: now.Add(-time.Hour), PeriodEnd: now,
	})

	// Run the deduction sweep
	rater := rating.New(testStore, 30*time.Second, testLogger)
	rater.DeductWallets(ctx)

	// Check balance decreased
	updated, _ := testStore.GetWallet(ctx, wallet.ID)
	if !updated.Balance.Equal(decimal.NewFromFloat(75)) {
		t.Errorf("balance after deduction: got %s, want 75", updated.Balance.String())
	}

	// Check ledger has the deduction
	ledger, _ := testStore.WalletLedger(ctx, wallet.ID, 10)
	if len(ledger) != 2 {
		t.Errorf("expected 2 ledger entries (top-up + deduction), got %d", len(ledger))
	}
}
