package metering

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
)

var (
	t0 = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	t1 = time.Date(2026, 7, 1, 10, 1, 0, 0, time.UTC)
)

func TestComputeInstanceMeters(t *testing.T) {
	inst := inventory.ComputeInstanceRecord{
		InstanceID: "vm-1",
		Tenant:     "tenant-a",
		Cores:      4,
		MemoryGiB:  16,
	}
	entries := computeInstanceMeters(inst, "default", 60.0, t0, t1)

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	tests := []struct {
		idx       int
		meter     string
		value     float64
		unit      string
	}{
		{0, "vm_uptime_seconds", 60.0, "seconds"},
		{1, "vm_cpu_core_seconds", 240.0, "core_seconds"},
		{2, "vm_memory_gib_seconds", 960.0, "gib_seconds"},
	}
	for _, tc := range tests {
		e := entries[tc.idx]
		if e.MeterName != tc.meter {
			t.Errorf("[%d] meter: got %q, want %q", tc.idx, e.MeterName, tc.meter)
		}
		if e.Value != tc.value {
			t.Errorf("[%d] value: got %v, want %v", tc.idx, e.Value, tc.value)
		}
		if e.Unit != tc.unit {
			t.Errorf("[%d] unit: got %q, want %q", tc.idx, e.Unit, tc.unit)
		}
		if e.ResourceType != "compute_instance" {
			t.Errorf("[%d] resource_type: got %q", tc.idx, e.ResourceType)
		}
		if e.ResourceID != "vm-1" {
			t.Errorf("[%d] resource_id: got %q", tc.idx, e.ResourceID)
		}
		if e.TenantID != "tenant-a" {
			t.Errorf("[%d] tenant_id: got %q", tc.idx, e.TenantID)
		}
	}
}

func TestComputeInstanceMeters_ZeroCores(t *testing.T) {
	inst := inventory.ComputeInstanceRecord{
		InstanceID: "vm-2",
		Tenant:     "t",
		Cores:      0,
		MemoryGiB:  8,
	}
	entries := computeInstanceMeters(inst, "default", 60.0, t0, t1)
	if entries[1].Value != 0 {
		t.Errorf("cpu_core_seconds should be 0 with 0 cores, got %v", entries[1].Value)
	}
}

func TestClusterMeters_WithNodeSets(t *testing.T) {
	nodeSets := map[string]struct {
		HostType string `json:"host_type"`
		Size     int32  `json:"size"`
	}{
		"workers":     {HostType: "worker", Size: 3},
		"infra-nodes": {HostType: "infra", Size: 2},
	}
	nsJSON, _ := json.Marshal(nodeSets)

	cl := inventory.ClusterRecord{
		ClusterID:    "cl-1",
		Tenant:       "tenant-b",
		NodeSetsJSON: nsJSON,
	}
	entries := clusterMeters(cl, "default", 60.0, t0, t1)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (uptime + worker_node), got %d", len(entries))
	}

	if entries[0].MeterName != "cluster_uptime_seconds" || entries[0].Value != 60.0 {
		t.Errorf("uptime entry: %+v", entries[0])
	}

	// 3 workers + 2 infra = 5 nodes × 60s = 300 node_seconds
	if entries[1].MeterName != "cluster_worker_node_seconds" || entries[1].Value != 300.0 {
		t.Errorf("worker_node entry: got value %v, want 300.0", entries[1].Value)
	}
}

func TestClusterMeters_NoNodeSets(t *testing.T) {
	cl := inventory.ClusterRecord{
		ClusterID:    "cl-2",
		Tenant:       "tenant-c",
		NodeSetsJSON: nil,
	}
	entries := clusterMeters(cl, "default", 60.0, t0, t1)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (uptime only), got %d", len(entries))
	}
	if entries[0].MeterName != "cluster_uptime_seconds" {
		t.Errorf("expected cluster_uptime_seconds, got %q", entries[0].MeterName)
	}
}

func TestClusterMeters_EmptyNodeSets(t *testing.T) {
	cl := inventory.ClusterRecord{
		ClusterID:    "cl-3",
		Tenant:       "tenant-d",
		NodeSetsJSON: json.RawMessage(`{}`),
	}
	entries := clusterMeters(cl, "default", 60.0, t0, t1)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (no worker nodes), got %d", len(entries))
	}
}

func TestMaaSMeters_AllDimensions(t *testing.T) {
	usage := MaaSUsage{
		ModelID:           "model-1",
		TenantID:          "tenant-e",
		TokensIn:          1000,
		TokensOut:         500,
		CachedInputTokens: 200,
		ReasoningTokens:   100,
		Requests:          1,
	}
	entries := maasMeters(usage, "default", t0, t1)

	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	expected := map[string]float64{
		"maas_tokens_in":        1000,
		"maas_tokens_out":       500,
		"maas_tokens_cached":    200,
		"maas_tokens_reasoning": 100,
		"maas_requests":         1,
	}
	for _, e := range entries {
		want, ok := expected[e.MeterName]
		if !ok {
			t.Errorf("unexpected meter %q", e.MeterName)
			continue
		}
		if e.Value != want {
			t.Errorf("%s: got %v, want %v", e.MeterName, e.Value, want)
		}
		if e.ResourceType != "model" {
			t.Errorf("%s: resource_type got %q", e.MeterName, e.ResourceType)
		}
	}
}

func TestMaaSMeters_ZeroDimensionsSkipped(t *testing.T) {
	usage := MaaSUsage{
		ModelID:  "model-2",
		TenantID: "tenant-f",
		TokensIn: 500,
		// all others zero
	}
	entries := maasMeters(usage, "default", t0, t1)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (only tokens_in > 0), got %d", len(entries))
	}
	if entries[0].MeterName != "maas_tokens_in" {
		t.Errorf("expected maas_tokens_in, got %q", entries[0].MeterName)
	}
}

func TestMaaSMeters_AllZero(t *testing.T) {
	usage := MaaSUsage{ModelID: "model-3", TenantID: "tenant-g"}
	entries := maasMeters(usage, "default", t0, t1)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for all-zero usage, got %d", len(entries))
	}
}

func TestMaaSMeters_Units(t *testing.T) {
	usage := MaaSUsage{
		ModelID:  "m",
		TenantID: "t",
		TokensIn: 1,
		Requests: 1,
	}
	entries := maasMeters(usage, "default", t0, t1)
	for _, e := range entries {
		switch e.MeterName {
		case "maas_tokens_in":
			if e.Unit != "tokens" {
				t.Errorf("tokens_in unit: got %q, want tokens", e.Unit)
			}
		case "maas_requests":
			if e.Unit != "requests" {
				t.Errorf("requests unit: got %q, want requests", e.Unit)
			}
		}
	}
}

func TestMaaSMeters_UserIDPropagation(t *testing.T) {
	usage := MaaSUsage{
		ModelID:  "model-user",
		TenantID: "tenant-u",
		UserID:   "alice@example.com",
		TokensIn: 100,
		Requests: 1,
	}
	entries := maasMeters(usage, "proj-1", t0, t1)
	for _, e := range entries {
		if e.UserID != "alice@example.com" {
			t.Errorf("%s: user_id got %q, want alice@example.com", e.MeterName, e.UserID)
		}
		if e.ProjectID != "proj-1" {
			t.Errorf("%s: project_id got %q, want proj-1", e.MeterName, e.ProjectID)
		}
	}
}

func TestMaaSMeters_EmptyUserID(t *testing.T) {
	usage := MaaSUsage{
		ModelID:  "model-no-user",
		TenantID: "tenant-v",
		TokensIn: 50,
	}
	entries := maasMeters(usage, "default", t0, t1)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].UserID != "" {
		t.Errorf("expected empty user_id for non-MaaS, got %q", entries[0].UserID)
	}
}

func TestComputeInstanceMeters_PeriodPropagation(t *testing.T) {
	inst := inventory.ComputeInstanceRecord{InstanceID: "vm", Tenant: "t", Cores: 1, MemoryGiB: 1}
	entries := computeInstanceMeters(inst, "default", 60.0, t0, t1)
	for i, e := range entries {
		if !e.PeriodStart.Equal(t0) || !e.PeriodEnd.Equal(t1) {
			t.Errorf("[%d] period: got %v-%v, want %v-%v", i, e.PeriodStart, e.PeriodEnd, t0, t1)
		}
	}
}
