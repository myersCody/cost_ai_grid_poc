package metering

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metrics"
)

// Meter runs a periodic sweep of all billable resources and produces
// metering entries based on elapsed time since last metering.
//
// Design decision: we sweep every 60 seconds to match the metering
// collector's emission interval defined in the OSAC CloudEvents spec
// (event-types.md). This means metering entries have ~60s granularity,
// which is sufficient for the 60-second processing SLA in the requirements.
// The Watch stream gives us state transitions, not periodic heartbeats,
// so we need this sweep to produce time-based metering entries.
type Meter struct {
	store    *inventory.Store
	interval time.Duration
	logger   *slog.Logger
}

func New(store *inventory.Store, interval time.Duration, logger *slog.Logger) *Meter {
	return &Meter{store: store, interval: interval, logger: logger}
}

func (m *Meter) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			m.sweep(ctx)
		}
	}
}

func (m *Meter) sweep(ctx context.Context) {
	start := time.Now()
	now := start.UTC()

	m.meterComputeInstances(ctx, now)
	m.meterClusters(ctx, now)
	m.meterBareMetalInstances(ctx, now)

	metrics.MeteringSweepDuration.Observe(time.Since(start).Seconds())
}

func (m *Meter) meterComputeInstances(ctx context.Context, now time.Time) {
	instances, err := m.store.BillableComputeInstances(ctx)
	if err != nil {
		m.logger.Error("failed to list billable compute instances", "error", err)
		return
	}

	metered := 0
	for _, inst := range instances {
		periodStart := inst.CreatedAt
		if inst.LastMeteredAt != nil {
			periodStart = *inst.LastMeteredAt
		}

		durationSeconds := now.Sub(periodStart).Seconds()
		if durationSeconds <= 0 {
			continue
		}

		entries := computeInstanceMeters(inst, durationSeconds, periodStart, now)
		for _, entry := range entries {
			if err := m.store.InsertMeteringEntry(ctx, entry); err != nil {
				m.logger.Error("failed to insert metering entry",
					"resource", inst.InstanceID, "meter", entry.MeterName, "error", err)
			} else {
				metrics.MeteringEntriesCreated.WithLabelValues(entry.ResourceType, entry.MeterName).Inc()
			}
		}

		if err := m.store.UpdateComputeInstanceLastMetered(ctx, inst.InstanceID, now); err != nil {
			m.logger.Error("failed to update last_metered_at",
				"resource", inst.InstanceID, "error", err)
		}
		metered++
	}

	if metered > 0 {
		m.logger.Info("metering sweep complete", "compute_instances", metered)
	}
}

// MeterComputeInstanceFinal produces final metering entries for a
// compute instance that is being deleted. Called by the watcher on
// DELETE events to capture usage up to the deletion timestamp.
func (m *Meter) MeterComputeInstanceFinal(ctx context.Context, instanceID string, deletedAt time.Time) {
	inst, err := m.store.GetComputeInstance(ctx, instanceID)
	if err != nil {
		m.logger.Debug("no inventory record for final metering", "id", instanceID)
		return
	}

	if !IsComputeInstanceBillable(inst.State) {
		return
	}

	periodStart := inst.CreatedAt
	if inst.LastMeteredAt != nil {
		periodStart = *inst.LastMeteredAt
	}

	durationSeconds := deletedAt.Sub(periodStart).Seconds()
	if durationSeconds <= 0 {
		return
	}

	entries := computeInstanceMeters(*inst, durationSeconds, periodStart, deletedAt)
	for _, entry := range entries {
		if err := m.store.InsertMeteringEntry(ctx, entry); err != nil {
			m.logger.Error("failed to insert final metering entry",
				"resource", instanceID, "meter", entry.MeterName, "error", err)
		}
	}

	m.logger.Debug("final metering for deleted instance", "id", instanceID, "duration_seconds", durationSeconds)
}

func (m *Meter) meterClusters(ctx context.Context, now time.Time) {
	clusters, err := m.store.BillableClusters(ctx)
	if err != nil {
		m.logger.Error("failed to list billable clusters", "error", err)
		return
	}

	metered := 0
	for _, cl := range clusters {
		periodStart := cl.CreatedAt
		if cl.LastMeteredAt != nil {
			periodStart = *cl.LastMeteredAt
		}

		durationSeconds := now.Sub(periodStart).Seconds()
		if durationSeconds <= 0 {
			continue
		}

		entries := clusterMeters(cl, durationSeconds, periodStart, now)
		for _, entry := range entries {
			if err := m.store.InsertMeteringEntry(ctx, entry); err != nil {
				m.logger.Error("failed to insert cluster metering entry",
					"resource", cl.ClusterID, "meter", entry.MeterName, "error", err)
			} else {
				metrics.MeteringEntriesCreated.WithLabelValues(entry.ResourceType, entry.MeterName).Inc()
			}
		}

		if err := m.store.UpdateClusterLastMetered(ctx, cl.ClusterID, now); err != nil {
			m.logger.Error("failed to update cluster last_metered_at",
				"resource", cl.ClusterID, "error", err)
		}
		metered++
	}

	if metered > 0 {
		m.logger.Info("metering sweep complete", "clusters", metered)
	}
}

func clusterMeters(cl inventory.ClusterRecord, durationSeconds float64, periodStart, periodEnd time.Time) []inventory.MeteringEntry {
	entries := []inventory.MeteringEntry{
		{
			ResourceType: "cluster",
			ResourceID:   cl.ClusterID,
			TenantID:     cl.Tenant,
			MeterName:    "cluster_uptime_seconds",
			Value:        durationSeconds,
			Unit:         "seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		},
	}

	var nodeSets map[string]struct {
		HostType string `json:"host_type"`
		Size     int32  `json:"size"`
	}
	if cl.NodeSetsJSON != nil {
		_ = json.Unmarshal(cl.NodeSetsJSON, &nodeSets)
	}

	totalWorkerNodeSeconds := 0.0
	for _, ns := range nodeSets {
		totalWorkerNodeSeconds += float64(ns.Size) * durationSeconds
	}

	if totalWorkerNodeSeconds > 0 {
		entries = append(entries, inventory.MeteringEntry{
			ResourceType: "cluster",
			ResourceID:   cl.ClusterID,
			TenantID:     cl.Tenant,
			MeterName:    "cluster_worker_node_seconds",
			Value:        totalWorkerNodeSeconds,
			Unit:         "node_seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		})
	}

	return entries
}

func (m *Meter) meterBareMetalInstances(ctx context.Context, now time.Time) {
	instances, err := m.store.BillableBareMetalInstances(ctx)
	if err != nil {
		m.logger.Error("failed to list billable bare metal instances", "error", err)
		return
	}

	metered := 0
	for _, inst := range instances {
		periodStart := inst.CreatedAt
		if inst.LastMeteredAt != nil {
			periodStart = *inst.LastMeteredAt
		}

		durationSeconds := now.Sub(periodStart).Seconds()
		if durationSeconds <= 0 {
			continue
		}

		entries := []inventory.MeteringEntry{
			{ResourceType: "bare_metal", ResourceID: inst.InstanceID, TenantID: inst.Tenant, MeterName: "bm_uptime_seconds", Value: durationSeconds, Unit: "seconds", PeriodStart: periodStart, PeriodEnd: now},
		}

		for _, entry := range entries {
			if err := m.store.InsertMeteringEntry(ctx, entry); err != nil {
				m.logger.Error("failed to insert BM metering entry", "resource", inst.InstanceID, "error", err)
			} else {
				metrics.MeteringEntriesCreated.WithLabelValues(entry.ResourceType, entry.MeterName).Inc()
			}
		}

		if err := m.store.UpdateBareMetalInstanceLastMetered(ctx, inst.InstanceID, now); err != nil {
			m.logger.Error("failed to update BM last_metered_at", "resource", inst.InstanceID, "error", err)
		}
		metered++
	}

	if metered > 0 {
		m.logger.Info("metering sweep complete", "bare_metal_instances", metered)
	}
}

// MeterBareMetalInstanceFinal produces final metering entries for a
// bare metal instance that is being deleted.
func (m *Meter) MeterBareMetalInstanceFinal(ctx context.Context, instanceID string, deletedAt time.Time) {
	inst, err := m.store.GetBareMetalInstance(ctx, instanceID)
	if err != nil {
		m.logger.Debug("no inventory record for final BM metering", "id", instanceID)
		return
	}

	if !IsBareMetalBillable(inst.State) {
		return
	}

	periodStart := inst.CreatedAt
	if inst.LastMeteredAt != nil {
		periodStart = *inst.LastMeteredAt
	}

	durationSeconds := deletedAt.Sub(periodStart).Seconds()
	if durationSeconds <= 0 {
		return
	}

	entry := inventory.MeteringEntry{
		ResourceType: "bare_metal",
		ResourceID:   inst.InstanceID,
		TenantID:     inst.Tenant,
		MeterName:    "bm_uptime_seconds",
		Value:        durationSeconds,
		Unit:         "seconds",
		PeriodStart:  periodStart,
		PeriodEnd:    deletedAt,
	}
	if err := m.store.InsertMeteringEntry(ctx, entry); err != nil {
		m.logger.Error("failed to insert final BM metering entry", "resource", instanceID, "error", err)
	}

	m.logger.Debug("final metering for deleted bare metal instance", "id", instanceID, "duration_seconds", durationSeconds)
}

// MaaS metering data passed from event ingestion.
type MaaSUsage struct {
	ModelID             string
	ModelName           string
	TenantID            string
	State               string
	TokensIn            int64
	TokensOut           int64
	CachedInputTokens   int64
	ReasoningTokens     int64
	Requests            int64
	EventTime           time.Time
	DurationSeconds     float64
}

// MeterMaaSEvent produces metering entries from a MaaS usage event.
// Unlike VM metering (sweep-based), MaaS metering is event-driven:
// each event carries the consumption values directly.
func (m *Meter) MeterMaaSEvent(ctx context.Context, usage MaaSUsage) {
	if !IsModelBillable(usage.State) {
		return
	}

	periodStart := usage.EventTime.Add(-time.Duration(usage.DurationSeconds) * time.Second)
	periodEnd := usage.EventTime

	entries := maasMeters(usage, periodStart, periodEnd)
	for _, entry := range entries {
		if err := m.store.InsertMeteringEntry(ctx, entry); err != nil {
			m.logger.Error("failed to insert MaaS metering entry",
				"model", usage.ModelID, "meter", entry.MeterName, "error", err)
		} else {
			metrics.MeteringEntriesCreated.WithLabelValues(entry.ResourceType, entry.MeterName).Inc()
		}
	}

	m.logger.Debug("metered MaaS event", "model", usage.ModelID,
		"tokens_in", usage.TokensIn, "tokens_out", usage.TokensOut, "requests", usage.Requests)
}

func maasMeters(usage MaaSUsage, periodStart, periodEnd time.Time) []inventory.MeteringEntry {
	var entries []inventory.MeteringEntry

	if usage.TokensIn > 0 {
		entries = append(entries, inventory.MeteringEntry{
			ResourceType: "model",
			ResourceID:   usage.ModelID,
			TenantID:     usage.TenantID,
			MeterName:    "maas_tokens_in",
			Value:        float64(usage.TokensIn),
			Unit:         "tokens",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		})
	}

	if usage.TokensOut > 0 {
		entries = append(entries, inventory.MeteringEntry{
			ResourceType: "model",
			ResourceID:   usage.ModelID,
			TenantID:     usage.TenantID,
			MeterName:    "maas_tokens_out",
			Value:        float64(usage.TokensOut),
			Unit:         "tokens",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		})
	}

	if usage.CachedInputTokens > 0 {
		entries = append(entries, inventory.MeteringEntry{
			ResourceType: "model",
			ResourceID:   usage.ModelID,
			TenantID:     usage.TenantID,
			MeterName:    "maas_tokens_cached",
			Value:        float64(usage.CachedInputTokens),
			Unit:         "tokens",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		})
	}

	if usage.ReasoningTokens > 0 {
		entries = append(entries, inventory.MeteringEntry{
			ResourceType: "model",
			ResourceID:   usage.ModelID,
			TenantID:     usage.TenantID,
			MeterName:    "maas_tokens_reasoning",
			Value:        float64(usage.ReasoningTokens),
			Unit:         "tokens",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		})
	}

	if usage.Requests > 0 {
		entries = append(entries, inventory.MeteringEntry{
			ResourceType: "model",
			ResourceID:   usage.ModelID,
			TenantID:     usage.TenantID,
			MeterName:    "maas_requests",
			Value:        float64(usage.Requests),
			Unit:         "requests",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		})
	}

	return entries
}

func computeInstanceMeters(inst inventory.ComputeInstanceRecord, durationSeconds float64, periodStart, periodEnd time.Time) []inventory.MeteringEntry {
	cores := inst.Cores
	memGiB := inst.MemoryGiB

	return []inventory.MeteringEntry{
		{
			ResourceType: "compute_instance",
			ResourceID:   inst.InstanceID,
			TenantID:     inst.Tenant,
			MeterName:    "vm_uptime_seconds",
			Value:        durationSeconds,
			Unit:         "seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		},
		{
			ResourceType: "compute_instance",
			ResourceID:   inst.InstanceID,
			TenantID:     inst.Tenant,
			MeterName:    "vm_cpu_core_seconds",
			Value:        float64(cores) * durationSeconds,
			Unit:         "core_seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		},
		{
			ResourceType: "compute_instance",
			ResourceID:   inst.InstanceID,
			TenantID:     inst.Tenant,
			MeterName:    "vm_memory_gib_seconds",
			Value:        float64(memGiB) * durationSeconds,
			Unit:         "gib_seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    periodEnd,
		},
	}
}
