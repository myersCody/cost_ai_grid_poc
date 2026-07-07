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
