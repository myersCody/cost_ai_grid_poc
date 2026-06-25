package inventory

import (
	"encoding/json"
	"time"
)

type ComputeInstanceRecord struct {
	InstanceID   string          `json:"instance_id"`
	Name         string          `json:"name"`
	Tenant       string          `json:"tenant"`
	Project      string          `json:"project"`
	ClusterID    string          `json:"cluster_id"`
	InstanceType string          `json:"instance_type"`
	Cores        int32           `json:"cores"`
	MemoryGiB    int32           `json:"memory_gib"`
	State        string          `json:"state"`
	Labels       json.RawMessage `json:"labels"`
	CreatedAt    time.Time       `json:"created_at"`
	DeletedAt    *time.Time      `json:"deleted_at"`
	LastEventID  string          `json:"last_event_id"`
	LastUpdated  time.Time       `json:"last_updated"`
}

type ClusterRecord struct {
	ClusterID   string          `json:"cluster_id"`
	Name        string          `json:"name"`
	Tenant      string          `json:"tenant"`
	Template    string          `json:"template"`
	NodeSetsJSON json.RawMessage `json:"node_sets"`
	State       string          `json:"state"`
	Labels      json.RawMessage `json:"labels"`
	CreatedAt   time.Time       `json:"created_at"`
	DeletedAt   *time.Time      `json:"deleted_at"`
	LastEventID string          `json:"last_event_id"`
	LastUpdated time.Time       `json:"last_updated"`
}

type InstanceTypeRecord struct {
	InstanceTypeID string    `json:"instance_type_id"`
	Name           string    `json:"name"`
	Cores          int32     `json:"cores"`
	MemoryGiB      int32     `json:"memory_gib"`
	State          string    `json:"state"`
	LastUpdated    time.Time `json:"last_updated"`
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
