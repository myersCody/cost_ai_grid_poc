package watcher

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metering"
	"github.com/osac-project/cost-event-consumer/internal/osac"
)

type Watcher struct {
	client *osac.Client
	store  *inventory.Store
	meter  *metering.Meter
	logger *slog.Logger
}

func New(client *osac.Client, store *inventory.Store, meter *metering.Meter, logger *slog.Logger) *Watcher {
	return &Watcher{client: client, store: store, meter: meter, logger: logger}
}

// Run connects to the OSAC event stream and processes events.
// It reconnects with exponential backoff on disconnection.
func (w *Watcher) Run(ctx context.Context) error {
	backoff := time.Second

	for {
		w.logger.Info("connecting to OSAC event stream")

		err := w.client.WatchEvents(ctx, func(event osac.Event) error {
			return w.handleEvent(ctx, event)
		})

		if ctx.Err() != nil {
			return ctx.Err()
		}

		w.logger.Warn("event stream disconnected, reconnecting", "error", err, "backoff", backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, 30*time.Second)
	}
}

func (w *Watcher) handleEvent(ctx context.Context, event osac.Event) error {
	resourceType := eventResourceType(event)
	w.logger.Info("received event", "id", event.ID, "type", event.Type, "resource", resourceType)

	if err := w.storeRawEvent(ctx, event, resourceType); err != nil {
		w.logger.Error("failed to store raw event", "error", err, "id", event.ID)
	}

	switch event.Type {
	case osac.EventTypeCreated, osac.EventTypeUpdated:
		return w.handleCreateOrUpdate(ctx, event)
	case osac.EventTypeDeleted:
		return w.handleDelete(ctx, event)
	default:
		w.logger.Warn("unknown event type", "type", event.Type)
		return nil
	}
}

func (w *Watcher) storeRawEvent(ctx context.Context, event osac.Event, resourceType string) error {
	resourceID, tenantID, eventTime := extractEventMeta(event)

	dataJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = w.store.InsertRawEvent(ctx, inventory.RawEvent{
		EventID:      event.ID,
		EventType:    event.Type,
		EventSource:  "osac.fulfillment-service",
		EventTime:    eventTime,
		TenantID:     tenantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Data:         dataJSON,
	})
	return err
}

func extractEventMeta(event osac.Event) (resourceID, tenantID string, eventTime time.Time) {
	eventTime = time.Now()

	switch {
	case event.ComputeInstance != nil:
		resourceID = event.ComputeInstance.ID
		tenantID = event.ComputeInstance.Metadata.Tenant
		if event.ComputeInstance.Metadata.CreationTimestamp != nil {
			eventTime = *event.ComputeInstance.Metadata.CreationTimestamp
		}
	case event.Cluster != nil:
		resourceID = event.Cluster.ID
		tenantID = event.Cluster.Metadata.Tenant
		if event.Cluster.Metadata.CreationTimestamp != nil {
			eventTime = *event.Cluster.Metadata.CreationTimestamp
		}
	case event.InstanceType != nil:
		resourceID = event.InstanceType.ID
		tenantID = event.InstanceType.Metadata.Tenant
		if event.InstanceType.Metadata.CreationTimestamp != nil {
			eventTime = *event.InstanceType.Metadata.CreationTimestamp
		}
	case event.Project != nil:
		resourceID = event.Project.ID
		tenantID = event.Project.Metadata.Tenant
	case event.Tenant != nil:
		resourceID = event.Tenant.ID
		tenantID = event.Tenant.ID
	}
	return
}

func (w *Watcher) handleCreateOrUpdate(ctx context.Context, event osac.Event) error {
	if ci := event.ComputeInstance; ci != nil {
		return w.upsertComputeInstance(ctx, event.ID, ci)
	}
	if cl := event.Cluster; cl != nil {
		return w.upsertCluster(ctx, event.ID, cl)
	}
	if it := event.InstanceType; it != nil {
		return w.store.UpsertInstanceType(ctx, inventory.InstanceTypeRecord{
			InstanceTypeID: it.ID,
			Name:           it.Metadata.Name,
			Cores:          it.Spec.Cores,
			MemoryGiB:      it.Spec.MemoryGib,
			State:          it.Spec.State,
		})
	}
	if p := event.Project; p != nil {
		createdAt := time.Now()
		if p.Metadata.CreationTimestamp != nil {
			createdAt = *p.Metadata.CreationTimestamp
		}
		labelsJSON, _ := json.Marshal(p.Metadata.Labels)
		return w.store.UpsertProject(ctx, inventory.ProjectRecord{
			ProjectID: p.ID,
			Name:      p.Metadata.Name,
			Tenant:    p.Metadata.Tenant,
			Labels:    labelsJSON,
			CreatedAt: createdAt,
		})
	}
	return nil
}

func (w *Watcher) handleDelete(ctx context.Context, event osac.Event) error {
	now := time.Now()

	if ci := event.ComputeInstance; ci != nil {
		deletedAt := now
		if ci.Metadata.DeletionTimestamp != nil {
			deletedAt = *ci.Metadata.DeletionTimestamp
		}
		w.meter.MeterComputeInstanceFinal(ctx, ci.ID, deletedAt)
		return w.store.MarkComputeInstanceDeleted(ctx, ci.ID, deletedAt, event.ID)
	}
	if cl := event.Cluster; cl != nil {
		deletedAt := now
		if cl.Metadata.DeletionTimestamp != nil {
			deletedAt = *cl.Metadata.DeletionTimestamp
		}
		return w.store.MarkClusterDeleted(ctx, cl.ID, deletedAt, event.ID)
	}
	return nil
}

func (w *Watcher) upsertComputeInstance(ctx context.Context, eventID string, ci *osac.ComputeInstance) error {
	createdAt := time.Now()
	if ci.Metadata.CreationTimestamp != nil {
		createdAt = *ci.Metadata.CreationTimestamp
	}

	var cores, memGiB int32
	if ci.Spec.Cores != nil {
		cores = *ci.Spec.Cores
	}
	if ci.Spec.MemoryGib != nil {
		memGiB = *ci.Spec.MemoryGib
	}

	labelsJSON, _ := json.Marshal(ci.Metadata.Labels)

	return w.store.UpsertComputeInstance(ctx, inventory.ComputeInstanceRecord{
		InstanceID:   ci.ID,
		Name:         ci.Metadata.Name,
		Tenant:       ci.Metadata.Tenant,
		InstanceType: ci.Spec.InstanceType,
		Cores:        cores,
		MemoryGiB:    memGiB,
		State:        ci.Status.State,
		Labels:       labelsJSON,
		CreatedAt:    createdAt,
		LastEventID:  eventID,
	})
}

func (w *Watcher) upsertCluster(ctx context.Context, eventID string, cl *osac.Cluster) error {
	createdAt := time.Now()
	if cl.Metadata.CreationTimestamp != nil {
		createdAt = *cl.Metadata.CreationTimestamp
	}

	labelsJSON, _ := json.Marshal(cl.Metadata.Labels)
	nodeSetsJSON, _ := json.Marshal(cl.Spec.NodeSets)

	return w.store.UpsertCluster(ctx, inventory.ClusterRecord{
		ClusterID:    cl.ID,
		Name:         cl.Metadata.Name,
		Tenant:       cl.Metadata.Tenant,
		Template:     cl.Spec.Template,
		NodeSetsJSON: nodeSetsJSON,
		State:        cl.Status.State,
		Labels:       labelsJSON,
		CreatedAt:    createdAt,
		LastEventID:  eventID,
	})
}

func eventResourceType(event osac.Event) string {
	switch {
	case event.ComputeInstance != nil:
		return "ComputeInstance"
	case event.Cluster != nil:
		return "Cluster"
	case event.InstanceType != nil:
		return "InstanceType"
	case event.Project != nil:
		return "Project"
	case event.Tenant != nil:
		return "Tenant"
	case event.BareMetalInstance != nil:
		return "BareMetalInstance"
	case event.HostType != nil:
		return "HostType"
	case event.ClusterTemplate != nil:
		return "ClusterTemplate"
	case event.ComputeInstanceTemplate != nil:
		return "ComputeInstanceTemplate"
	case event.Role != nil:
		return "Role"
	case event.RoleBinding != nil:
		return "RoleBinding"
	default:
		return "Unknown"
	}
}
