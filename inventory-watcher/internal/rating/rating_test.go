package rating

import (
	"math"
	"testing"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
)

func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func ptr(f float64) *float64 { return &f }

func TestApplyRate_Flat(t *testing.T) {
	rate := inventory.RateRecord{PricePerUnit: 0.01}

	tests := []struct {
		name  string
		value float64
		want  float64
	}{
		{"zero value", 0, 0},
		{"unit value", 1, 0.01},
		{"typical VM uptime (3600s)", 3600, 36.0},
		{"fractional", 0.5, 0.005},
		{"large value (1M tokens)", 1_000_000, 10_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyRate(tc.value, rate)
			if got != tc.want {
				t.Errorf("ApplyRate(%v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestApplyRate_Tiered(t *testing.T) {
	rate := inventory.RateRecord{
		Tiers: []inventory.Tier{
			{UpTo: ptr(100), PricePerUnit: 0.10},
			{UpTo: ptr(500), PricePerUnit: 0.05},
			{UpTo: nil, PricePerUnit: 0.01},
		},
	}

	tests := []struct {
		name  string
		value float64
		want  float64
	}{
		{"within first tier", 50, 5.0},
		{"exactly at first tier boundary", 100, 10.0},
		{"into second tier", 200, 10.0 + 5.0},
		{"exactly at second tier boundary", 500, 10.0 + 20.0},
		{"into unlimited tier", 1000, 10.0 + 20.0 + 5.0},
		{"zero", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyRate(tc.value, rate)
			if got != tc.want {
				t.Errorf("ApplyRate(%v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestApplyRate_SingleUnlimitedTier(t *testing.T) {
	rate := inventory.RateRecord{
		Tiers: []inventory.Tier{
			{UpTo: nil, PricePerUnit: 0.02},
		},
	}
	got := ApplyRate(500, rate)
	if got != 10.0 {
		t.Errorf("got %v, want 10.0", got)
	}
}

func TestApplyRate_FreeTierThenPaid(t *testing.T) {
	rate := inventory.RateRecord{
		Tiers: []inventory.Tier{
			{UpTo: ptr(20), PricePerUnit: 0},
			{UpTo: ptr(120), PricePerUnit: 0.08},
			{UpTo: nil, PricePerUnit: 0.07},
		},
	}

	tests := []struct {
		name  string
		value float64
		want  float64
	}{
		{"within free tier", 10, 0},
		{"exactly at free tier", 20, 0},
		{"into paid tier", 50, 0 + 30*0.08},
		{"into final tier", 200, 0 + 100*0.08 + 80*0.07},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyRate(tc.value, rate)
			if !approxEq(got, tc.want) {
				t.Errorf("ApplyRate(%v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestApplyRate_TieredOverridesFlat(t *testing.T) {
	rate := inventory.RateRecord{
		PricePerUnit: 999.0,
		Tiers: []inventory.Tier{
			{UpTo: nil, PricePerUnit: 0.01},
		},
	}
	got := ApplyRate(100, rate)
	if got != 1.0 {
		t.Errorf("tiers should override PricePerUnit; got %v, want 1.0", got)
	}
}

func TestApplyRate_EmptyTiers(t *testing.T) {
	rate := inventory.RateRecord{
		PricePerUnit: 0.05,
		Tiers:        []inventory.Tier{},
	}
	got := ApplyRate(100, rate)
	if got != 5.0 {
		t.Errorf("empty tiers should fall back to flat; got %v, want 5.0", got)
	}
}

var thresholds = []float64{50, 70, 90, 100}

func TestThresholdLevels(t *testing.T) {
	if len(ThresholdLevels) != 4 {
		t.Fatalf("expected 4 threshold levels, got %d", len(ThresholdLevels))
	}
	for i, want := range []float64{50, 70, 90, 100} {
		if ThresholdLevels[i] != want {
			t.Errorf("ThresholdLevels[%d] = %v, want %v", i, ThresholdLevels[i], want)
		}
	}
}

func strPtr(s string) *string { return &s }

func TestBuildRateIndex(t *testing.T) {
	rates := []inventory.RateRecord{
		{ID: 1, TenantID: nil, ResourceType: "compute_instance", MeterName: "vm_uptime_seconds", PricePerUnit: 0.01},
		{ID: 2, TenantID: strPtr("tenant-acme"), ResourceType: "compute_instance", MeterName: "vm_uptime_seconds", PricePerUnit: 0.02},
		{ID: 3, TenantID: nil, ResourceType: "model", MeterName: "maas_tokens_in", PricePerUnit: 0.001},
		{ID: 4, TenantID: strPtr(""), ResourceType: "cluster", MeterName: "cluster_uptime_seconds", PricePerUnit: 0.50},
	}

	idx := buildRateIndex(rates)

	if len(idx) != 4 {
		t.Fatalf("expected 4 entries in rate index, got %d", len(idx))
	}

	// Global rate (nil tenant → "")
	r := idx[rateKey{tenant: "", resourceType: "compute_instance", meterName: "vm_uptime_seconds"}]
	if r == nil || r.ID != 1 {
		t.Error("expected global VM rate (ID=1)")
	}

	// Tenant-specific rate
	r = idx[rateKey{tenant: "tenant-acme", resourceType: "compute_instance", meterName: "vm_uptime_seconds"}]
	if r == nil || r.ID != 2 {
		t.Error("expected tenant-acme VM rate (ID=2)")
	}
}

func TestMatchRate_TenantSpecificTakesPrecedence(t *testing.T) {
	rates := []inventory.RateRecord{
		{ID: 1, TenantID: nil, ResourceType: "compute_instance", MeterName: "vm_uptime_seconds", PricePerUnit: 0.01},
		{ID: 2, TenantID: strPtr("tenant-acme"), ResourceType: "compute_instance", MeterName: "vm_uptime_seconds", PricePerUnit: 0.02},
	}
	idx := buildRateIndex(rates)

	r := matchRate(idx, "tenant-acme", "compute_instance", "vm_uptime_seconds")
	if r == nil || r.ID != 2 {
		t.Errorf("expected tenant-specific rate (ID=2), got %+v", r)
	}
}

func TestMatchRate_FallsBackToGlobal(t *testing.T) {
	rates := []inventory.RateRecord{
		{ID: 1, TenantID: nil, ResourceType: "compute_instance", MeterName: "vm_uptime_seconds", PricePerUnit: 0.01},
	}
	idx := buildRateIndex(rates)

	r := matchRate(idx, "tenant-unknown", "compute_instance", "vm_uptime_seconds")
	if r == nil || r.ID != 1 {
		t.Errorf("expected global fallback rate (ID=1), got %+v", r)
	}
}

func TestMatchRate_ReturnsNilWhenNoMatch(t *testing.T) {
	rates := []inventory.RateRecord{
		{ID: 1, TenantID: nil, ResourceType: "compute_instance", MeterName: "vm_uptime_seconds", PricePerUnit: 0.01},
	}
	idx := buildRateIndex(rates)

	r := matchRate(idx, "any", "gpu_instance", "gpu_compute_seconds")
	if r != nil {
		t.Errorf("expected nil for unmatched meter, got %+v", r)
	}
}

func TestMatchRate_EmptyStringTenantIsSameAsGlobal(t *testing.T) {
	rates := []inventory.RateRecord{
		{ID: 1, TenantID: strPtr(""), ResourceType: "model", MeterName: "maas_tokens_in", PricePerUnit: 0.001},
	}
	idx := buildRateIndex(rates)

	r := matchRate(idx, "any-tenant", "model", "maas_tokens_in")
	if r == nil || r.ID != 1 {
		t.Errorf("expected empty-string tenant to serve as global, got %+v", r)
	}
}

func TestBuildRateIndex_FirstEntryWins(t *testing.T) {
	rates := []inventory.RateRecord{
		{ID: 1, TenantID: nil, ResourceType: "model", MeterName: "maas_tokens_in", PricePerUnit: 0.001},
		{ID: 2, TenantID: nil, ResourceType: "model", MeterName: "maas_tokens_in", PricePerUnit: 0.999},
	}
	idx := buildRateIndex(rates)

	r := idx[rateKey{tenant: "", resourceType: "model", meterName: "maas_tokens_in"}]
	if r == nil || r.ID != 1 {
		t.Errorf("expected first rate to win (ID=1), got %+v", r)
	}
}
