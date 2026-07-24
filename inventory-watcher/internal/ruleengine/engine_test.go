package ruleengine

import (
	"encoding/json"
	"os"
	"path"
	"testing"

	zen "github.com/gorules/zen-go"
)

func loadRule(key string) ([]byte, error) {
	return os.ReadFile(path.Join("..", "..", "rules", key))
}

func TestComputePricing_StandardRate(t *testing.T) {
	engine := zen.NewEngine(zen.EngineConfig{Loader: loadRule})
	defer engine.Dispose()

	input := map[string]any{
		"instance_type": "standard-4-16",
		"tenant_tier":   "bronze",
		"value":         3600.0, // 1 hour in seconds
	}

	resp, err := engine.Evaluate("compute-pricing.json", input)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	t.Logf("Result: %+v", result)

	cost, ok := result["cost_amount"].(float64)
	if !ok {
		t.Fatalf("cost_amount not a float64: %v", result["cost_amount"])
	}

	// standard-4-16, no gold tier → $0.20/hr × 1hr = $0.20
	if cost < 0.19 || cost > 0.21 {
		t.Errorf("expected ~$0.20, got $%.4f", cost)
	}
}

func TestComputePricing_GoldDiscount(t *testing.T) {
	engine := zen.NewEngine(zen.EngineConfig{Loader: loadRule})
	defer engine.Dispose()

	input := map[string]any{
		"instance_type": "standard-4-16",
		"tenant_tier":   "gold",
		"value":         3600.0,
	}

	resp, err := engine.Evaluate("compute-pricing.json", input)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}

	var result map[string]any
	json.Unmarshal(resp.Result, &result)

	t.Logf("Result: %+v", result)

	cost := result["cost_amount"].(float64)

	// standard-4-16, gold → $0.20/hr × (1 - 20%) × 1hr = $0.16
	if cost < 0.15 || cost > 0.17 {
		t.Errorf("expected ~$0.16 (gold 20%% off $0.20), got $%.4f", cost)
	}

	desc := result["description"]
	t.Logf("Description: %v", desc)
}

func TestComputePricing_DefaultRate(t *testing.T) {
	engine := zen.NewEngine(zen.EngineConfig{Loader: loadRule})
	defer engine.Dispose()

	input := map[string]any{
		"instance_type": "unknown-sku",
		"tenant_tier":   "whatever",
		"value":         7200.0, // 2 hours
	}

	resp, err := engine.Evaluate("compute-pricing.json", input)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}

	var result map[string]any
	json.Unmarshal(resp.Result, &result)

	t.Logf("Result: %+v", result)

	cost := result["cost_amount"].(float64)

	// unknown → default $0.10/hr × 2hr = $0.20
	if cost < 0.19 || cost > 0.21 {
		t.Errorf("expected ~$0.20 (default rate × 2hr), got $%.4f", cost)
	}
}

func TestComputePricing_SmallVM(t *testing.T) {
	engine := zen.NewEngine(zen.EngineConfig{Loader: loadRule})
	defer engine.Dispose()

	input := map[string]any{
		"instance_type": "standard-2-8",
		"tenant_tier":   "gold",
		"value":         60.0, // 1 minute
	}

	resp, err := engine.Evaluate("compute-pricing.json", input)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}

	var result map[string]any
	json.Unmarshal(resp.Result, &result)

	t.Logf("Result: %+v", result)

	cost := result["cost_amount"].(float64)

	// standard-2-8, gold → $0.08/hr × (60/3600)hr = $0.001333
	if cost < 0.001 || cost > 0.002 {
		t.Errorf("expected ~$0.00133 (gold small VM 1min), got $%.6f", cost)
	}
}

// --- Committed-Use Pricing Tests ---

func TestCUD_WithinCommitment(t *testing.T) {
	engine := zen.NewEngine(zen.EngineConfig{Loader: loadRule})
	defer engine.Dispose()

	input := map[string]any{
		"tenant_id":           "tenant-acme",
		"resource_type":       "compute_instance",
		"instance_type":       "standard-4-16",
		"value":               3600.0, // 1 hour
		"running_vms":         3.0,    // within commitment of 5
		"base_price_per_hour": 0.20,
		"monthly_utilization_pct": 80.0,
	}

	resp, err := engine.Evaluate("committed-use-pricing.json", input)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}

	var result map[string]any
	json.Unmarshal(resp.Result, &result)
	t.Logf("Result: %+v", result)

	cost := result["cost_amount"].(float64)
	// within CUD: $0.20/hr × (1 - 40%) = $0.12/hr × 1hr = $0.12
	if cost < 0.11 || cost > 0.13 {
		t.Errorf("expected ~$0.12 (CUD 40%% off), got $%.4f", cost)
	}

	desc := result["description"]
	t.Logf("Description: %v", desc)

	within := result["within_commitment"]
	if within != true {
		t.Errorf("expected within_commitment=true, got %v", within)
	}
}

func TestCUD_OverCommitment_SustainedUse(t *testing.T) {
	engine := zen.NewEngine(zen.EngineConfig{Loader: loadRule})
	defer engine.Dispose()

	input := map[string]any{
		"tenant_id":           "tenant-acme",
		"resource_type":       "compute_instance",
		"instance_type":       "standard-4-16",
		"value":               3600.0,
		"running_vms":         8.0,    // over commitment of 5
		"base_price_per_hour": 0.20,
		"monthly_utilization_pct": 80.0, // qualifies for 20% sustained-use
	}

	resp, err := engine.Evaluate("committed-use-pricing.json", input)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}

	var result map[string]any
	json.Unmarshal(resp.Result, &result)
	t.Logf("Result: %+v", result)

	cost := result["cost_amount"].(float64)
	// over CUD → sustained-use 20%: $0.20/hr × (1 - 20%) = $0.16/hr × 1hr = $0.16
	if cost < 0.15 || cost > 0.17 {
		t.Errorf("expected ~$0.16 (sustained-use 20%% off), got $%.4f", cost)
	}

	within := result["within_commitment"]
	if within != false {
		t.Errorf("expected within_commitment=false, got %v", within)
	}
}

func TestCUD_NoCUD_LowUtilization(t *testing.T) {
	engine := zen.NewEngine(zen.EngineConfig{Loader: loadRule})
	defer engine.Dispose()

	input := map[string]any{
		"tenant_id":           "tenant-initech",
		"resource_type":       "compute_instance",
		"instance_type":       "standard-4-16",
		"value":               3600.0,
		"running_vms":         2.0,
		"base_price_per_hour": 0.20,
		"monthly_utilization_pct": 10.0, // below 25%, no sustained-use
	}

	resp, err := engine.Evaluate("committed-use-pricing.json", input)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}

	var result map[string]any
	json.Unmarshal(resp.Result, &result)
	t.Logf("Result: %+v", result)

	cost := result["cost_amount"].(float64)
	// no CUD, no sustained-use: $0.20/hr × 1hr = $0.20
	if cost < 0.19 || cost > 0.21 {
		t.Errorf("expected ~$0.20 (on-demand, no discount), got $%.4f", cost)
	}
}

func TestCUD_GlobexHighCommitment(t *testing.T) {
	engine := zen.NewEngine(zen.EngineConfig{Loader: loadRule})
	defer engine.Dispose()

	input := map[string]any{
		"tenant_id":           "tenant-globex",
		"resource_type":       "compute_instance",
		"instance_type":       "standard-8-32",
		"value":               7200.0, // 2 hours
		"running_vms":         8.0,    // within commitment of 10
		"base_price_per_hour": 0.40,
		"monthly_utilization_pct": 95.0,
	}

	resp, err := engine.Evaluate("committed-use-pricing.json", input)
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}

	var result map[string]any
	json.Unmarshal(resp.Result, &result)
	t.Logf("Result: %+v", result)

	cost := result["cost_amount"].(float64)
	// within CUD 50% off: $0.40/hr × (1 - 50%) = $0.20/hr × 2hr = $0.40
	if cost < 0.39 || cost > 0.41 {
		t.Errorf("expected ~$0.40 (CUD 50%% off × 2hr), got $%.4f", cost)
	}
}
