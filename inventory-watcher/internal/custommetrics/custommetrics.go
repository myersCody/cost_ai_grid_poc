package custommetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
)

var hardcodedEventTypes = map[string]bool{
	"osac.compute_instance.lifecycle": true,
	"osac.cluster.lifecycle":          true,
	"osac.model.lifecycle":            true,
	"inference.tokens.used":           true,
}

type Config struct {
	CustomMetrics []MetricDef `json:"custom_metrics"`
}

type MetricDef struct {
	EventType       string     `json:"event_type"`
	ResourceType    string     `json:"resource_type"`
	ResourceIDField string     `json:"resource_id_field"`
	TenantIDField   string     `json:"tenant_id_field"`
	Meters          []MeterDef `json:"meters"`
}

type MeterDef struct {
	MeterName  string `json:"meter_name"`
	ValueField string `json:"value_field"`
	Unit       string `json:"unit"`
}

type Registry struct {
	defs   map[string]MetricDef
	logger *slog.Logger
}

type MeteringStore interface {
	InsertMeteringEntry(ctx context.Context, entry inventory.MeteringEntry) error
}

func LoadFromFile(path string, logger *slog.Logger) (*Registry, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading custom metrics config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing custom metrics config: %w", err)
	}

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validating custom metrics config: %w", err)
	}

	r := &Registry{
		defs:   make(map[string]MetricDef, len(cfg.CustomMetrics)),
		logger: logger,
	}
	for _, def := range cfg.CustomMetrics {
		if hardcodedEventTypes[def.EventType] {
			logger.Warn("custom metric event_type shadows a hardcoded handler",
				"event_type", def.EventType)
		}
		r.defs[def.EventType] = def
	}

	logger.Info("custom metrics loaded", "count", len(r.defs))
	return r, nil
}

func validate(cfg Config) error {
	seen := make(map[string]bool)
	for i, def := range cfg.CustomMetrics {
		if def.EventType == "" {
			return fmt.Errorf("custom_metrics[%d]: event_type is required", i)
		}
		if def.ResourceType == "" {
			return fmt.Errorf("custom_metrics[%d]: resource_type is required", i)
		}
		if def.ResourceIDField == "" {
			return fmt.Errorf("custom_metrics[%d]: resource_id_field is required", i)
		}
		if def.TenantIDField == "" {
			return fmt.Errorf("custom_metrics[%d]: tenant_id_field is required", i)
		}
		if len(def.Meters) == 0 {
			return fmt.Errorf("custom_metrics[%d]: at least one meter is required", i)
		}
		if seen[def.EventType] {
			return fmt.Errorf("custom_metrics[%d]: duplicate event_type %q", i, def.EventType)
		}
		seen[def.EventType] = true

		for j, m := range def.Meters {
			if m.MeterName == "" {
				return fmt.Errorf("custom_metrics[%d].meters[%d]: meter_name is required", i, j)
			}
			if m.ValueField == "" {
				return fmt.Errorf("custom_metrics[%d].meters[%d]: value_field is required", i, j)
			}
			if m.Unit == "" {
				return fmt.Errorf("custom_metrics[%d].meters[%d]: unit is required", i, j)
			}
		}
	}
	return nil
}

func (r *Registry) HasEventType(eventType string) bool {
	if r == nil {
		return false
	}
	_, ok := r.defs[eventType]
	return ok
}

func (r *Registry) ClassifyEvent(eventType string, data map[string]interface{}) (resourceType, resourceID, tenantID string) {
	def, ok := r.defs[eventType]
	if !ok {
		return "", "", ""
	}
	resourceType = def.ResourceType
	if v, ok := extractField(data, def.ResourceIDField); ok {
		resourceID = toString(v)
	}
	if v, ok := extractField(data, def.TenantIDField); ok {
		tenantID = toString(v)
	}
	return resourceType, resourceID, tenantID
}

func (r *Registry) ProcessEvent(ctx context.Context, store MeteringStore, eventType string, rawData json.RawMessage, eventTime time.Time, logger *slog.Logger) error {
	def, ok := r.defs[eventType]
	if !ok {
		return fmt.Errorf("no custom metric definition for event type %q", eventType)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(rawData, &data); err != nil {
		return fmt.Errorf("unmarshaling event data: %w", err)
	}

	resourceID := ""
	if v, ok := extractField(data, def.ResourceIDField); ok {
		resourceID = toString(v)
	}
	tenantID := ""
	if v, ok := extractField(data, def.TenantIDField); ok {
		tenantID = toString(v)
	}

	periodEnd := eventTime
	periodStart := periodEnd
	if v, ok := extractField(data, "duration_seconds"); ok {
		if dur, ok := toFloat64(v); ok && dur > 0 {
			periodStart = periodEnd.Add(-time.Duration(dur) * time.Second)
		}
	}

	inserted := 0
	for _, m := range def.Meters {
		raw, ok := extractField(data, m.ValueField)
		if !ok {
			logger.Debug("custom metric field not found in event data",
				"meter", m.MeterName, "field", m.ValueField, "event_type", eventType)
			continue
		}
		value, ok := toFloat64(raw)
		if !ok {
			logger.Warn("custom metric field is not numeric",
				"meter", m.MeterName, "field", m.ValueField, "value", raw)
			continue
		}
		if value == 0 {
			continue
		}

		if err := store.InsertMeteringEntry(ctx, inventory.MeteringEntry{
			ResourceType: def.ResourceType,
			ResourceID:   resourceID,
			TenantID:     tenantID,
			MeterName:    m.MeterName,
			Value:        value,
			Unit:         m.Unit,
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		}); err != nil {
			return fmt.Errorf("inserting custom metering entry %s: %w", m.MeterName, err)
		}
		inserted++
	}

	logger.Debug("processed custom metric event",
		"event_type", eventType, "resource_id", resourceID, "meters_inserted", inserted)
	return nil
}

func extractField(data map[string]interface{}, path string) (interface{}, bool) {
	path = strings.TrimPrefix(path, "data.")

	parts := strings.Split(path, ".")
	current := data
	for i, part := range parts {
		val, ok := current[part]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return val, true
		}
		next, ok := val.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		var f float64
		_, err := fmt.Sscanf(n, "%f", &f)
		return f, err == nil
	default:
		return 0, false
	}
}

func toString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case fmt.Stringer:
		return s.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
