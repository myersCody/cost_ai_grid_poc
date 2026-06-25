package metering

import (
	"context"
	"log/slog"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
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
	now := time.Now().UTC()

	m.meterComputeInstances(ctx, now)
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
