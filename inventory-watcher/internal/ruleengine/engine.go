package ruleengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path"

	zen "github.com/gorules/zen-go"
)

type Engine struct {
	engine   zen.Engine
	rulesDir string
}

func New(rulesDir string) *Engine {
	loader := func(key string) ([]byte, error) {
		return os.ReadFile(path.Join(rulesDir, key))
	}
	return &Engine{
		engine:   zen.NewEngine(zen.EngineConfig{Loader: loader}),
		rulesDir: rulesDir,
	}
}

func (e *Engine) Close() {
	e.engine.Dispose()
}

type PricingInput struct {
	InstanceType string  `json:"instance_type"`
	TenantTier   string  `json:"tenant_tier"`
	TenantID     string  `json:"tenant_id"`
	ResourceType string  `json:"resource_type"`
	MeterName    string  `json:"meter_name"`
	Value        float64 `json:"value"`
}

type PricingOutput struct {
	CostAmount    float64 `json:"cost_amount"`
	EffectiveRate float64 `json:"effective_rate"`
	Currency      string  `json:"currency"`
	Description   string  `json:"description"`
	PricePerHour  float64 `json:"price_per_hour"`
	DiscountPct   float64 `json:"discount_pct"`
}

func (e *Engine) EvaluateRate(ruleFile string, input PricingInput) (*PricingOutput, error) {
	inputMap := map[string]any{
		"instance_type": input.InstanceType,
		"tenant_tier":   input.TenantTier,
		"tenant_id":     input.TenantID,
		"resource_type": input.ResourceType,
		"meter_name":    input.MeterName,
		"value":         input.Value,
	}

	resp, err := e.engine.Evaluate(ruleFile, inputMap)
	if err != nil {
		return nil, fmt.Errorf("rule evaluation failed: %w", err)
	}

	var output PricingOutput
	if err := json.Unmarshal(resp.Result, &output); err != nil {
		return nil, fmt.Errorf("unmarshal rule output: %w", err)
	}

	return &output, nil
}
