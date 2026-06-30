package reconciler

import (
	"context"
	"log/slog"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/osac"
	"github.com/osac-project/cost-event-consumer/internal/watcher"
)

type Reconciler struct {
	client   *osac.Client
	store    *inventory.Store
	watcher  *watcher.Watcher
	interval time.Duration
	logger   *slog.Logger
}

func New(client *osac.Client, store *inventory.Store, w *watcher.Watcher, interval time.Duration, logger *slog.Logger) *Reconciler {
	return &Reconciler{
		client:   client,
		store:    store,
		watcher:  w,
		interval: interval,
		logger:   logger,
	}
}

// Run periodically reconciles OSAC state with the local inventory.
func (r *Reconciler) Run(ctx context.Context) error {
	// Run an initial reconciliation immediately.
	r.reconcileAll(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *Reconciler) reconcileAll(ctx context.Context) {
	r.logger.Info("starting reconciliation")

	r.reconcileProjects(ctx)
	r.reconcileComputeInstances(ctx)
	r.reconcileClusters(ctx)
	r.reconcileInstanceTypes(ctx)
	r.reconcileBareMetalInstances(ctx)

	r.logger.Info("reconciliation complete")
}

func (r *Reconciler) reconcileComputeInstances(ctx context.Context) {
	osacInstances, err := r.client.ListComputeInstances(ctx)
	if err != nil {
		r.logger.Error("failed to list OSAC compute instances", "error", err)
		return
	}

	knownInstances, err := r.store.ListAliveComputeInstances(ctx)
	if err != nil {
		r.logger.Error("failed to list inventory compute instances", "error", err)
		return
	}

	osacSet := make(map[string]*osac.ComputeInstance, len(osacInstances))
	for i := range osacInstances {
		osacSet[osacInstances[i].ID] = &osacInstances[i]
	}

	knownSet := make(map[string]bool, len(knownInstances))
	for _, ki := range knownInstances {
		knownSet[ki.InstanceID] = true
	}

	// Instances in OSAC but not in inventory: missed CREATED events.
	created := 0
	for id, ci := range osacSet {
		if !knownSet[id] {
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

			if err := r.store.UpsertComputeInstance(ctx, inventory.ComputeInstanceRecord{
				InstanceID:   ci.ID,
				Name:         ci.Metadata.Name,
				Tenant:       ci.Metadata.Tenant,
				InstanceType: ci.Spec.InstanceType,
				Cores:        cores,
				MemoryGiB:    memGiB,
				State:        ci.Status.State,
				CreatedAt:    createdAt,
				LastEventID:  "reconcile",
			}); err != nil {
				r.logger.Error("failed to upsert instance from reconciliation", "id", id, "error", err)
			} else {
				created++
			}
		}
	}

	// Instances in inventory but not in OSAC: missed DELETED events.
	deleted := 0
	for _, ki := range knownInstances {
		if _, exists := osacSet[ki.InstanceID]; !exists {
			if err := r.store.MarkComputeInstanceDeleted(ctx, ki.InstanceID, time.Now(), "reconcile"); err != nil {
				r.logger.Error("failed to mark instance deleted from reconciliation", "id", ki.InstanceID, "error", err)
			} else {
				deleted++
			}
		}
	}

	r.logger.Info("reconciled compute instances",
		"osac_count", len(osacInstances),
		"inventory_count", len(knownInstances),
		"created", created,
		"deleted", deleted,
	)
}

func (r *Reconciler) reconcileClusters(ctx context.Context) {
	osacClusters, err := r.client.ListClusters(ctx)
	if err != nil {
		r.logger.Error("failed to list OSAC clusters", "error", err)
		return
	}

	knownClusters, err := r.store.ListAliveClusters(ctx)
	if err != nil {
		r.logger.Error("failed to list inventory clusters", "error", err)
		return
	}

	osacSet := make(map[string]*osac.Cluster, len(osacClusters))
	for i := range osacClusters {
		osacSet[osacClusters[i].ID] = &osacClusters[i]
	}

	knownSet := make(map[string]bool, len(knownClusters))
	for _, kc := range knownClusters {
		knownSet[kc.ClusterID] = true
	}

	created := 0
	for id, cl := range osacSet {
		if !knownSet[id] {
			createdAt := time.Now()
			if cl.Metadata.CreationTimestamp != nil {
				createdAt = *cl.Metadata.CreationTimestamp
			}

			if err := r.store.UpsertCluster(ctx, inventory.ClusterRecord{
				ClusterID:   cl.ID,
				Name:        cl.Metadata.Name,
				Tenant:      cl.Metadata.Tenant,
				Template:    cl.Spec.Template,
				State:       cl.Status.State,
				CreatedAt:   createdAt,
				LastEventID: "reconcile",
			}); err != nil {
				r.logger.Error("failed to upsert cluster from reconciliation", "id", id, "error", err)
			} else {
				created++
			}
		}
	}

	deleted := 0
	for _, kc := range knownClusters {
		if _, exists := osacSet[kc.ClusterID]; !exists {
			if err := r.store.MarkClusterDeleted(ctx, kc.ClusterID, time.Now(), "reconcile"); err != nil {
				r.logger.Error("failed to mark cluster deleted from reconciliation", "id", kc.ClusterID, "error", err)
			} else {
				deleted++
			}
		}
	}

	r.logger.Info("reconciled clusters",
		"osac_count", len(osacClusters),
		"inventory_count", len(knownClusters),
		"created", created,
		"deleted", deleted,
	)
}

func (r *Reconciler) reconcileProjects(ctx context.Context) {
	osacProjects, err := r.client.ListProjects(ctx)
	if err != nil {
		r.logger.Error("failed to list OSAC projects", "error", err)
		return
	}

	for _, p := range osacProjects {
		createdAt := time.Now()
		if p.Metadata.CreationTimestamp != nil {
			createdAt = *p.Metadata.CreationTimestamp
		}

		if err := r.store.UpsertProject(ctx, inventory.ProjectRecord{
			ProjectID: p.ID,
			Name:      p.Metadata.Name,
			Tenant:    p.Metadata.Tenant,
			CreatedAt: createdAt,
		}); err != nil {
			r.logger.Error("failed to upsert project", "id", p.ID, "error", err)
		}
	}

	r.logger.Info("reconciled projects", "count", len(osacProjects))
}

func (r *Reconciler) reconcileBareMetalInstances(ctx context.Context) {
	osacInstances, err := r.client.ListBareMetalInstances(ctx)
	if err != nil {
		r.logger.Error("failed to list OSAC bare metal instances", "error", err)
		return
	}

	knownInstances, err := r.store.ListAliveBareMetalInstances(ctx)
	if err != nil {
		r.logger.Error("failed to list inventory bare metal instances", "error", err)
		return
	}

	osacSet := make(map[string]*osac.BareMetalInstance, len(osacInstances))
	for i := range osacInstances {
		osacSet[osacInstances[i].ID] = &osacInstances[i]
	}

	knownSet := make(map[string]bool, len(knownInstances))
	for _, ki := range knownInstances {
		knownSet[ki.InstanceID] = true
	}

	created := 0
	for id, bm := range osacSet {
		if !knownSet[id] {
			createdAt := time.Now()
			if bm.Metadata.CreationTimestamp != nil {
				createdAt = *bm.Metadata.CreationTimestamp
			}

			if err := r.store.UpsertBareMetalInstance(ctx, inventory.BareMetalInstanceRecord{
				InstanceID:  bm.ID,
				Name:        bm.Metadata.Name,
				Tenant:      bm.Metadata.Tenant,
				CatalogItem: bm.Spec.CatalogItem,
				State:       bm.Status.State,
				CreatedAt:   createdAt,
				LastEventID: "reconcile",
			}); err != nil {
				r.logger.Error("failed to upsert bare metal from reconciliation", "id", id, "error", err)
			} else {
				created++
			}
		}
	}

	deleted := 0
	for _, ki := range knownInstances {
		if _, exists := osacSet[ki.InstanceID]; !exists {
			if err := r.store.MarkBareMetalInstanceDeleted(ctx, ki.InstanceID, time.Now(), "reconcile"); err != nil {
				r.logger.Error("failed to mark bare metal deleted from reconciliation", "id", ki.InstanceID, "error", err)
			} else {
				deleted++
			}
		}
	}

	r.logger.Info("reconciled bare metal instances",
		"osac_count", len(osacInstances),
		"inventory_count", len(knownInstances),
		"created", created,
		"deleted", deleted,
	)
}

func (r *Reconciler) reconcileInstanceTypes(ctx context.Context) {
	osacTypes, err := r.client.ListInstanceTypes(ctx)
	if err != nil {
		r.logger.Error("failed to list OSAC instance types", "error", err)
		return
	}

	for _, it := range osacTypes {
		if err := r.store.UpsertInstanceType(ctx, inventory.InstanceTypeRecord{
			InstanceTypeID: it.ID,
			Name:           it.Metadata.Name,
			Cores:          it.Spec.Cores,
			MemoryGiB:      it.Spec.MemoryGib,
			State:          it.Spec.State,
		}); err != nil {
			r.logger.Error("failed to upsert instance type", "id", it.ID, "error", err)
		}
	}

	r.logger.Info("reconciled instance types", "count", len(osacTypes))
}
