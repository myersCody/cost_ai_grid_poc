package inventory

import (
	"encoding/json"
	"time"
)

type ProjectRecord struct {
	ProjectID   string          `json:"project_id"`
	Name        string          `json:"name"`
	Tenant      string          `json:"tenant"`
	Labels      json.RawMessage `json:"labels"`
	CreatedAt   time.Time       `json:"created_at"`
	DeletedAt   *time.Time      `json:"deleted_at"`
	LastUpdated time.Time       `json:"last_updated"`
}

type ComputeInstanceRecord struct {
	InstanceID    string          `json:"instance_id"`
	Name          string          `json:"name"`
	Tenant        string          `json:"tenant"`
	Project       string          `json:"project"`
	ClusterID     string          `json:"cluster_id"`
	InstanceType  string          `json:"instance_type"`
	Cores         int32           `json:"cores"`
	MemoryGiB     int32           `json:"memory_gib"`
	State         string          `json:"state"`
	Labels        json.RawMessage `json:"labels"`
	CreatedAt     time.Time       `json:"created_at"`
	DeletedAt     *time.Time      `json:"deleted_at"`
	LastEventID   string          `json:"last_event_id"`
	LastUpdated   time.Time       `json:"last_updated"`
	LastMeteredAt *time.Time      `json:"last_metered_at"`
}

type ClusterRecord struct {
	ClusterID     string          `json:"cluster_id"`
	Name          string          `json:"name"`
	Tenant        string          `json:"tenant"`
	Template      string          `json:"template"`
	NodeSetsJSON  json.RawMessage `json:"node_sets"`
	State         string          `json:"state"`
	Labels        json.RawMessage `json:"labels"`
	CreatedAt     time.Time       `json:"created_at"`
	DeletedAt     *time.Time      `json:"deleted_at"`
	LastEventID   string          `json:"last_event_id"`
	LastUpdated   time.Time       `json:"last_updated"`
	LastMeteredAt *time.Time      `json:"last_metered_at"`
}

type ModelRecord struct {
	ModelID     string          `json:"model_id"`
	Name        string          `json:"name"`
	ModelName   string          `json:"model_name"`
	Tenant      string          `json:"tenant"`
	Project     string          `json:"project"`
	Template    string          `json:"template"`
	State       string          `json:"state"`
	Labels      json.RawMessage `json:"labels"`
	CreatedAt   time.Time       `json:"created_at"`
	DeletedAt   *time.Time      `json:"deleted_at"`
	LastEventID string          `json:"last_event_id"`
	LastUpdated time.Time       `json:"last_updated"`
}

type BareMetalInstanceRecord struct {
	InstanceID    string          `json:"instance_id"`
	Name          string          `json:"name"`
	Tenant        string          `json:"tenant"`
	CatalogItem   string          `json:"catalog_item"`
	State         string          `json:"state"`
	Labels        json.RawMessage `json:"labels"`
	CreatedAt     time.Time       `json:"created_at"`
	DeletedAt     *time.Time      `json:"deleted_at"`
	LastEventID   string          `json:"last_event_id"`
	LastUpdated   time.Time       `json:"last_updated"`
	LastMeteredAt *time.Time      `json:"last_metered_at"`
}

type InstanceTypeRecord struct {
	InstanceTypeID string    `json:"instance_type_id"`
	Name           string    `json:"name"`
	Cores          int32     `json:"cores"`
	MemoryGiB      int32     `json:"memory_gib"`
	State          string    `json:"state"`
	LastUpdated    time.Time `json:"last_updated"`
}

type RawEvent struct {
	ID           string          `json:"id"`
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	EventSource  string          `json:"event_source"`
	EventTime    time.Time       `json:"event_time"`
	TenantID     string          `json:"tenant_id"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Data         json.RawMessage `json:"data"`
	ReceivedAt   time.Time       `json:"received_at"`
}

type MeteringEntry struct {
	ID           int64     `json:"id"`
	RawEventID   *int64    `json:"raw_event_id"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	TenantID     string    `json:"tenant_id"`
	MeterName    string    `json:"meter_name"`
	Value        float64   `json:"value"`
	Unit         string    `json:"unit"`
	PeriodStart  time.Time `json:"period_start"`
	PeriodEnd    time.Time `json:"period_end"`
}

type Tier struct {
	UpTo         *float64 `json:"up_to"`
	PricePerUnit float64  `json:"price_per_unit"`
}

type RateRecord struct {
	ID            int64      `json:"id"`
	TenantID      *string    `json:"tenant_id"`
	ResourceType  string     `json:"resource_type"`
	MeterName     string     `json:"meter_name"`
	KokuMetric    string     `json:"koku_metric"`
	CostType      string     `json:"cost_type"`
	PricePerUnit  float64    `json:"price_per_unit"`
	Currency      string     `json:"currency"`
	Tiers         []Tier     `json:"tiers"`
	Description   string     `json:"description"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
}

type CostEntry struct {
	ID              int64     `json:"id"`
	MeteringEntryID int64     `json:"metering_entry_id"`
	RateID          int64     `json:"rate_id"`
	TenantID        string    `json:"tenant_id"`
	ResourceType    string    `json:"resource_type"`
	ResourceID      string    `json:"resource_id"`
	MeterName       string    `json:"meter_name"`
	MeteredValue    float64   `json:"metered_value"`
	CostAmount      float64   `json:"cost_amount"`
	Currency        string    `json:"currency"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
}

type QuotaRecord struct {
	ID            int64      `json:"id"`
	TenantID      string     `json:"tenant_id"`
	ProjectID     string     `json:"project_id"`
	ResourceType  string     `json:"resource_type"`
	MeterName     string     `json:"meter_name"`
	LimitValue    float64    `json:"limit_value"`
	Unit          string     `json:"unit"`
	Period        string     `json:"period"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
}

type QuotaStatus struct {
	MeterName  string             `json:"meter_name"`
	Unit       string             `json:"unit"`
	Limit      float64            `json:"limit"`
	Consumed   float64            `json:"consumed"`
	Percentage float64            `json:"percentage"`
	Thresholds map[string]bool    `json:"thresholds"`
	Alerts     []AlertRecord      `json:"alerts,omitempty"`
}

type AlertRecord struct {
	ID           int64     `json:"id"`
	TenantID     string    `json:"tenant_id"`
	MeterName    string    `json:"meter_name"`
	ThresholdPct float64   `json:"threshold_pct"`
	Consumed     float64   `json:"consumed"`
	LimitValue   float64   `json:"limit_value"`
	Period       string    `json:"period"`
	State        string    `json:"state"`
	FiredAt      time.Time `json:"fired_at"`
}

type DailyUsageSummary struct {
	UsageDate    time.Time `json:"usage_date"`
	ClusterID    string    `json:"cluster_id"`
	Tenant       string    `json:"tenant"`
	Project      string    `json:"project"`
	ResourceID   string    `json:"resource_id"`
	ResourceType string    `json:"resource_type"`
	InstanceType string    `json:"instance_type"`
	Cores        int32     `json:"cores"`
	MemoryGiB    int32     `json:"memory_gib"`
	CPUCoreHours float64   `json:"cpu_core_hours"`
	MemoryGBHours float64  `json:"memory_gb_hours"`
	DurationHours float64  `json:"duration_hours"`
}
