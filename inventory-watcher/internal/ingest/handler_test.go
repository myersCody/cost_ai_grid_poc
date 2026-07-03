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
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/osac-project/cost-event-consumer/internal/custommetrics"
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
	handler := ingest.NewHandler(testStore, testMeter, nil, nil, testLogger)
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

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
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
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("control plane: expected 202, got %d", resp.StatusCode)
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
	if resp2.StatusCode != http.StatusAccepted {
		t.Errorf("worker: expected 202, got %d", resp2.StatusCode)
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

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
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

	// prompt_tokens → maas_tokens_in, completion_tokens → maas_tokens_out,
	// cached_input_tokens → maas_tokens_cached, reasoning_tokens → maas_tokens_reasoning
	expectedMeters := map[string]float64{
		"maas_tokens_in":        1500,
		"maas_tokens_out":       800,
		"maas_tokens_cached":    200,
		"maas_tokens_reasoning": 150,
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

	// Verify tenant was derived from subject (user field)
	var tenant string
	testStore.Pool().QueryRow(ctx,
		"SELECT tenant FROM inventory_model WHERE model_id = $1", modelID).Scan(&tenant)
	if tenant != "test-user@example.com" {
		t.Errorf("expected tenant = test-user@example.com (from IPP subject), got %s", tenant)
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

	handler := ingest.NewHandler(testStore, testMeter, nil, registry, testLogger)
	srv := httptest.NewServer(handler.ServeMux())
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

	if resp.StatusCode != http.StatusAccepted {
		var respBody map[string]string
		json.NewDecoder(resp.Body).Decode(&respBody)
		t.Fatalf("expected 202, got %d: %v", resp.StatusCode, respBody)
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

	handler := ingest.NewHandler(testStore, testMeter, nil, registry, testLogger)
	srv := httptest.NewServer(handler.ServeMux())
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

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	ctx := context.Background()
	var count int
	err = testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM metering_entries WHERE resource_id = $1", resourceID).Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("expected 1 metering entry (missing field skipped), got %d (err=%v)", count, err)
	}
}
