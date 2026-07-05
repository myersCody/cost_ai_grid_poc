package custommetrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
)

var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

// mockStore captures InsertMeteringEntry calls.
type mockStore struct {
	entries []inventory.MeteringEntry
}

func (m *mockStore) InsertMeteringEntry(_ context.Context, entry inventory.MeteringEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockStore) InsertMeteringEntryBatch(_ context.Context, entries []inventory.MeteringEntry) error {
	m.entries = append(m.entries, entries...)
	return nil
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "custom-metrics.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validConfig = `{
  "custom_metrics": [
    {
      "event_type": "osac.gpu.lifecycle",
      "resource_type": "gpu_instance",
      "resource_id_field": "instance_id",
      "tenant_id_field": "tenant_id",
      "meters": [
        {"meter_name": "gpu_memory_gib_seconds", "value_field": "gpu_memory_gib_seconds", "unit": "gib_seconds"},
        {"meter_name": "gpu_compute_seconds", "value_field": "gpu_compute_seconds", "unit": "seconds"}
      ]
    }
  ]
}`

func TestLoadFromFile(t *testing.T) {
	path := writeConfig(t, validConfig)
	r, err := LoadFromFile(path, testLogger)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
	if !r.HasEventType("osac.gpu.lifecycle") {
		t.Error("expected HasEventType to return true")
	}
	if r.HasEventType("osac.unknown") {
		t.Error("expected HasEventType to return false for unknown type")
	}
}

func TestLoadFromFile_EmptyPath(t *testing.T) {
	r, err := LoadFromFile("", testLogger)
	if err != nil {
		t.Fatal(err)
	}
	if r != nil {
		t.Error("expected nil registry for empty path")
	}
}

func TestLoadFromFile_InvalidJSON(t *testing.T) {
	path := writeConfig(t, `{not json}`)
	_, err := LoadFromFile(path, testLogger)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadFromFile_FileNotFound(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/path.json", testLogger)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestValidation_MissingEventType(t *testing.T) {
	cfg := `{"custom_metrics": [{"event_type": "", "resource_type": "x", "resource_id_field": "id", "tenant_id_field": "tid", "meters": [{"meter_name": "m", "value_field": "v", "unit": "u"}]}]}`
	path := writeConfig(t, cfg)
	_, err := LoadFromFile(path, testLogger)
	if err == nil {
		t.Error("expected validation error for empty event_type")
	}
}

func TestValidation_MissingResourceType(t *testing.T) {
	cfg := `{"custom_metrics": [{"event_type": "x", "resource_type": "", "resource_id_field": "id", "tenant_id_field": "tid", "meters": [{"meter_name": "m", "value_field": "v", "unit": "u"}]}]}`
	path := writeConfig(t, cfg)
	_, err := LoadFromFile(path, testLogger)
	if err == nil {
		t.Error("expected validation error for empty resource_type")
	}
}

func TestValidation_NoMeters(t *testing.T) {
	cfg := `{"custom_metrics": [{"event_type": "x", "resource_type": "y", "resource_id_field": "id", "tenant_id_field": "tid", "meters": []}]}`
	path := writeConfig(t, cfg)
	_, err := LoadFromFile(path, testLogger)
	if err == nil {
		t.Error("expected validation error for empty meters")
	}
}

func TestValidation_DuplicateEventType(t *testing.T) {
	cfg := `{"custom_metrics": [
		{"event_type": "x", "resource_type": "y", "resource_id_field": "id", "tenant_id_field": "tid", "meters": [{"meter_name": "m", "value_field": "v", "unit": "u"}]},
		{"event_type": "x", "resource_type": "z", "resource_id_field": "id", "tenant_id_field": "tid", "meters": [{"meter_name": "m2", "value_field": "v2", "unit": "u2"}]}
	]}`
	path := writeConfig(t, cfg)
	_, err := LoadFromFile(path, testLogger)
	if err == nil {
		t.Error("expected validation error for duplicate event_type")
	}
}

func TestValidation_MeterMissingFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
	}{
		{"missing meter_name", `{"custom_metrics": [{"event_type": "x", "resource_type": "y", "resource_id_field": "id", "tenant_id_field": "tid", "meters": [{"meter_name": "", "value_field": "v", "unit": "u"}]}]}`},
		{"missing value_field", `{"custom_metrics": [{"event_type": "x", "resource_type": "y", "resource_id_field": "id", "tenant_id_field": "tid", "meters": [{"meter_name": "m", "value_field": "", "unit": "u"}]}]}`},
		{"missing unit", `{"custom_metrics": [{"event_type": "x", "resource_type": "y", "resource_id_field": "id", "tenant_id_field": "tid", "meters": [{"meter_name": "m", "value_field": "v", "unit": ""}]}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.cfg)
			_, err := LoadFromFile(path, testLogger)
			if err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestExtractField_Flat(t *testing.T) {
	data := map[string]interface{}{"foo": 42.0}
	v, ok := extractField(data, "foo")
	if !ok || v != 42.0 {
		t.Errorf("expected 42.0, got %v (ok=%v)", v, ok)
	}
}

func TestExtractField_Nested(t *testing.T) {
	data := map[string]interface{}{
		"spec": map[string]interface{}{"gpu_count": 4.0},
	}
	v, ok := extractField(data, "spec.gpu_count")
	if !ok || v != 4.0 {
		t.Errorf("expected 4.0, got %v (ok=%v)", v, ok)
	}
}

func TestExtractField_DataPrefix(t *testing.T) {
	data := map[string]interface{}{"instance_id": "abc"}
	v, ok := extractField(data, "data.instance_id")
	if !ok || v != "abc" {
		t.Errorf("expected abc, got %v (ok=%v)", v, ok)
	}
}

func TestExtractField_Missing(t *testing.T) {
	data := map[string]interface{}{"foo": 1.0}
	_, ok := extractField(data, "bar")
	if ok {
		t.Error("expected not found")
	}
}

func TestExtractField_NestedMissing(t *testing.T) {
	data := map[string]interface{}{"foo": "not a map"}
	_, ok := extractField(data, "foo.bar")
	if ok {
		t.Error("expected not found for non-map intermediate")
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want float64
		ok   bool
	}{
		{"float64", 3.14, 3.14, true},
		{"int", 42, 42.0, true},
		{"int64", int64(100), 100.0, true},
		{"json.Number", json.Number("99.5"), 99.5, true},
		{"string numeric", "123.4", 123.4, true},
		{"string non-numeric", "abc", 0, false},
		{"string trailing garbage", "123abc", 0, false},
		{"string NaN", "NaN", 0, false},
		{"string Inf", "Inf", 0, false},
		{"float64 NaN", math.NaN(), 0, false},
		{"float64 Inf", math.Inf(1), 0, false},
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toFloat64(tc.in)
			if ok != tc.ok {
				t.Errorf("ok: got %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("value: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyEvent(t *testing.T) {
	path := writeConfig(t, validConfig)
	r, err := LoadFromFile(path, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	data := map[string]interface{}{
		"instance_id": "gpu-i-123",
		"tenant_id":   "tenant-acme",
	}
	rt, rid, tid := r.ClassifyEvent("osac.gpu.lifecycle", data)
	if rt != "gpu_instance" {
		t.Errorf("resource_type: got %q, want %q", rt, "gpu_instance")
	}
	if rid != "gpu-i-123" {
		t.Errorf("resource_id: got %q, want %q", rid, "gpu-i-123")
	}
	if tid != "tenant-acme" {
		t.Errorf("tenant_id: got %q, want %q", tid, "tenant-acme")
	}
}

func TestClassifyEvent_UnknownType(t *testing.T) {
	path := writeConfig(t, validConfig)
	r, err := LoadFromFile(path, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	rt, rid, tid := r.ClassifyEvent("unknown.type", map[string]interface{}{})
	if rt != "" || rid != "" || tid != "" {
		t.Errorf("expected empty classification for unknown type, got %q %q %q", rt, rid, tid)
	}
}

func TestProcessEvent(t *testing.T) {
	path := writeConfig(t, validConfig)
	r, err := LoadFromFile(path, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	store := &mockStore{}
	eventTime := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	rawData := json.RawMessage(`{
		"instance_id": "gpu-i-abc123",
		"tenant_id": "tenant-acme",
		"gpu_memory_gib_seconds": 245760.0,
		"gpu_compute_seconds": 3600.0,
		"duration_seconds": 3600
	}`)

	err = r.ProcessEvent(context.Background(), store, "osac.gpu.lifecycle", rawData, eventTime, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	if len(store.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(store.entries))
	}

	e0 := store.entries[0]
	if e0.ResourceType != "gpu_instance" || e0.ResourceID != "gpu-i-abc123" || e0.TenantID != "tenant-acme" {
		t.Errorf("entry 0 identity: %+v", e0)
	}
	if e0.MeterName != "gpu_memory_gib_seconds" || e0.Value != 245760.0 || e0.Unit != "gib_seconds" {
		t.Errorf("entry 0 meter: %+v", e0)
	}
	expectedStart := eventTime.Add(-3600 * time.Second)
	if !e0.PeriodStart.Equal(expectedStart) {
		t.Errorf("period_start: got %v, want %v", e0.PeriodStart, expectedStart)
	}
	if !e0.PeriodEnd.Equal(eventTime) {
		t.Errorf("period_end: got %v, want %v", e0.PeriodEnd, eventTime)
	}

	e1 := store.entries[1]
	if e1.MeterName != "gpu_compute_seconds" || e1.Value != 3600.0 || e1.Unit != "seconds" {
		t.Errorf("entry 1 meter: %+v", e1)
	}
}

func TestProcessEvent_MissingField(t *testing.T) {
	path := writeConfig(t, validConfig)
	r, err := LoadFromFile(path, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	store := &mockStore{}
	rawData := json.RawMessage(`{
		"instance_id": "gpu-i-abc123",
		"tenant_id": "tenant-acme",
		"gpu_compute_seconds": 3600.0
	}`)

	err = r.ProcessEvent(context.Background(), store, "osac.gpu.lifecycle", rawData, time.Now(), testLogger)
	if err != nil {
		t.Fatal(err)
	}

	if len(store.entries) != 1 {
		t.Fatalf("expected 1 entry (missing field skipped), got %d", len(store.entries))
	}
	if store.entries[0].MeterName != "gpu_compute_seconds" {
		t.Errorf("expected gpu_compute_seconds, got %s", store.entries[0].MeterName)
	}
}

func TestProcessEvent_ZeroValue(t *testing.T) {
	path := writeConfig(t, validConfig)
	r, err := LoadFromFile(path, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	store := &mockStore{}
	rawData := json.RawMessage(`{
		"instance_id": "gpu-i-abc123",
		"tenant_id": "tenant-acme",
		"gpu_memory_gib_seconds": 0,
		"gpu_compute_seconds": 3600.0
	}`)

	err = r.ProcessEvent(context.Background(), store, "osac.gpu.lifecycle", rawData, time.Now(), testLogger)
	if err != nil {
		t.Fatal(err)
	}

	if len(store.entries) != 1 {
		t.Fatalf("expected 1 entry (zero skipped), got %d", len(store.entries))
	}
}

func TestProcessEvent_NegativeValue(t *testing.T) {
	path := writeConfig(t, validConfig)
	r, err := LoadFromFile(path, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	store := &mockStore{}
	rawData := json.RawMessage(`{
		"instance_id": "gpu-i-abc123",
		"tenant_id": "tenant-acme",
		"gpu_memory_gib_seconds": -100.0,
		"gpu_compute_seconds": 3600.0
	}`)

	err = r.ProcessEvent(context.Background(), store, "osac.gpu.lifecycle", rawData, time.Now(), testLogger)
	if err != nil {
		t.Fatal(err)
	}

	if len(store.entries) != 1 {
		t.Fatalf("expected 1 entry (negative skipped), got %d", len(store.entries))
	}
	if store.entries[0].MeterName != "gpu_compute_seconds" {
		t.Errorf("expected gpu_compute_seconds, got %s", store.entries[0].MeterName)
	}
}

func TestProcessEvent_NoDuration(t *testing.T) {
	path := writeConfig(t, validConfig)
	r, err := LoadFromFile(path, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	store := &mockStore{}
	eventTime := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	rawData := json.RawMessage(`{
		"instance_id": "gpu-i-abc123",
		"tenant_id": "tenant-acme",
		"gpu_compute_seconds": 100.0
	}`)

	err = r.ProcessEvent(context.Background(), store, "osac.gpu.lifecycle", rawData, eventTime, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	if len(store.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(store.entries))
	}
	if !store.entries[0].PeriodStart.Equal(eventTime) {
		t.Errorf("without duration, period_start should equal period_end; got %v", store.entries[0].PeriodStart)
	}
}

func TestHasEventType_NilRegistry(t *testing.T) {
	var r *Registry
	if r.HasEventType("anything") {
		t.Error("nil registry should return false")
	}
}
