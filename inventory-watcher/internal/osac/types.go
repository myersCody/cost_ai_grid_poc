package osac

import "time"

// Event represents an OSAC event from the Watch stream.
// JSON field names use proto JSON format (snake_case).
type Event struct {
	ID                      string           `json:"id"`
	Type                    string           `json:"type"`
	Cluster                 *Cluster         `json:"cluster,omitempty"`
	ClusterTemplate         *ClusterTemplate `json:"cluster_template,omitempty"`
	ComputeInstance         *ComputeInstance `json:"compute_instance,omitempty"`
	ComputeInstanceTemplate *ComputeInstanceTemplate `json:"compute_instance_template,omitempty"`
	HostType                *HostType        `json:"host_type,omitempty"`
	InstanceType            *InstanceType    `json:"instance_type,omitempty"`
	Project                 *Project         `json:"project,omitempty"`
	Tenant                  *Tenant          `json:"tenant,omitempty"`
	Role                    *Role            `json:"role,omitempty"`
	RoleBinding             *RoleBinding     `json:"role_binding,omitempty"`
	BareMetalInstance       *BareMetalInstance `json:"bare_metal_instance,omitempty"`
}

type EventsWatchResponse struct {
	Result *EventResult `json:"result,omitempty"`
}

type EventResult struct {
	Event *Event `json:"event,omitempty"`
}

type Metadata struct {
	CreationTimestamp *time.Time        `json:"creation_timestamp,omitempty"`
	DeletionTimestamp *time.Time        `json:"deletion_timestamp,omitempty"`
	Creator          string            `json:"creator,omitempty"`
	Name             string            `json:"name,omitempty"`
	Tenant           string            `json:"tenant,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Annotations      map[string]string `json:"annotations,omitempty"`
	Version          int32             `json:"version,omitempty"`
}

type Cluster struct {
	ID       string        `json:"id"`
	Metadata Metadata      `json:"metadata"`
	Spec     ClusterSpec   `json:"spec"`
	Status   ClusterStatus `json:"status"`
}

type ClusterSpec struct {
	Template    string                    `json:"template,omitempty"`
	NodeSets    map[string]ClusterNodeSet `json:"node_sets,omitempty"`
	CatalogItem string                    `json:"catalog_item,omitempty"`
}

type ClusterNodeSet struct {
	HostType string `json:"host_type"`
	Size     int32  `json:"size"`
}

type ClusterStatus struct {
	State    string                    `json:"state"`
	NodeSets map[string]ClusterNodeSet `json:"node_sets,omitempty"`
}

type ComputeInstance struct {
	ID       string                `json:"id"`
	Metadata Metadata              `json:"metadata"`
	Spec     ComputeInstanceSpec   `json:"spec"`
	Status   ComputeInstanceStatus `json:"status"`
}

type ComputeInstanceSpec struct {
	Template     string `json:"template,omitempty"`
	CatalogItem  string `json:"catalog_item,omitempty"`
	Cores        *int32 `json:"cores,omitempty"`
	MemoryGib    *int32 `json:"memory_gib,omitempty"`
	InstanceType string `json:"instance_type,omitempty"`
}

type ComputeInstanceStatus struct {
	State string `json:"state"`
}

type ComputeInstanceTemplate struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
}

type ClusterTemplate struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
}

type HostType struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
}

type InstanceType struct {
	ID       string           `json:"id"`
	Metadata Metadata         `json:"metadata"`
	Spec     InstanceTypeSpec `json:"spec"`
}

type InstanceTypeSpec struct {
	Cores     int32  `json:"cores"`
	MemoryGib int32  `json:"memory_gib"`
	State     string `json:"state,omitempty"`
}

type Project struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
}

type Tenant struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
}

type Role struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
}

type RoleBinding struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
}

type BareMetalInstance struct {
	ID       string                  `json:"id"`
	Metadata Metadata                `json:"metadata"`
	Spec     BareMetalInstanceSpec   `json:"spec"`
	Status   BareMetalInstanceStatus `json:"status"`
}

type BareMetalInstanceSpec struct {
	CatalogItem string `json:"catalog_item,omitempty"`
}

type BareMetalInstanceStatus struct {
	State string `json:"state"`
}

// Event type constants matching the protobuf enum.
const (
	EventTypeCreated = "EVENT_TYPE_OBJECT_CREATED"
	EventTypeUpdated = "EVENT_TYPE_OBJECT_UPDATED"
	EventTypeDeleted = "EVENT_TYPE_OBJECT_DELETED"
)
