package rating

import (
	"context"
	"log/slog"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
)

// Rater periodically processes unrated metering entries, looks up applicable
// rates, and produces cost entries.
type Rater struct {
	store    *inventory.Store
	interval time.Duration
	batch    int
	logger   *slog.Logger
}

func New(store *inventory.Store, interval time.Duration, logger *slog.Logger) *Rater {
	return &Rater{store: store, interval: interval, batch: 500, logger: logger}
}

func (r *Rater) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

func (r *Rater) sweep(ctx context.Context) {
	entries, err := r.store.UnratedMeteringEntries(ctx, r.batch)
	if err != nil {
		r.logger.Error("failed to fetch unrated entries", "error", err)
		return
	}

	if len(entries) == 0 {
		r.evaluateThresholds(ctx)
		return
	}

	rated := 0
	skipped := 0
	for _, me := range entries {
		rate, err := r.store.FindRate(ctx, me.TenantID, me.ResourceType, me.MeterName, me.PeriodEnd)
		if err != nil {
			skipped++
			continue
		}

		cost := ApplyRate(me.Value, *rate)

		if err := r.store.InsertCostEntry(ctx, inventory.CostEntry{
			MeteringEntryID: me.ID,
			RateID:          rate.ID,
			TenantID:        me.TenantID,
			ResourceType:    me.ResourceType,
			ResourceID:      me.ResourceID,
			MeterName:       me.MeterName,
			MeteredValue:    me.Value,
			CostAmount:      cost,
			Currency:        rate.Currency,
			PeriodStart:     me.PeriodStart,
			PeriodEnd:       me.PeriodEnd,
		}); err != nil {
			r.logger.Error("failed to insert cost entry", "metering_id", me.ID, "error", err)
			continue
		}
		rated++
	}

	r.logger.Info("rating sweep complete", "rated", rated, "skipped", skipped)

	r.evaluateThresholds(ctx)
}

var thresholdLevels = []float64{50, 70, 90, 100}

func (r *Rater) evaluateThresholds(ctx context.Context) {
	now := time.Now().UTC()
	period := now.Format("2006-01")
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)

	tenants, err := r.store.AllTenantsWithQuotas(ctx, now)
	if err != nil {
		r.logger.Error("failed to list tenants for threshold check", "error", err)
		return
	}

	fired := 0
	for _, tenantID := range tenants {
		quotas, err := r.store.QuotasForTenant(ctx, tenantID, now)
		if err != nil {
			continue
		}

		for _, q := range quotas {
			consumed, err := r.store.MeteringSum(ctx, tenantID, q.MeterName, periodStart, periodEnd)
			if err != nil || consumed == 0 || q.LimitValue <= 0 {
				continue
			}

			pct := (consumed / q.LimitValue) * 100

			for _, threshold := range thresholdLevels {
				if pct >= threshold {
					inserted, err := r.store.InsertAlert(ctx, inventory.AlertRecord{
						TenantID:     tenantID,
						MeterName:    q.MeterName,
						ThresholdPct: threshold,
						Consumed:     consumed,
						LimitValue:   q.LimitValue,
						Period:       period,
					})
					if err != nil {
						r.logger.Error("failed to insert alert", "tenant", tenantID, "meter", q.MeterName, "error", err)
					}
					if inserted {
						r.logger.Info("threshold alert fired",
							"tenant", tenantID, "meter", q.MeterName,
							"threshold", threshold, "consumed", consumed, "limit", q.LimitValue)
						fired++
					}
				}
			}
		}
	}

	if fired > 0 {
		r.logger.Info("threshold evaluation complete", "new_alerts", fired)
	}
}

// ApplyRate computes cost for a metered value using flat or tiered pricing.
func ApplyRate(value float64, rate inventory.RateRecord) float64 {
	if len(rate.Tiers) > 0 {
		return applyTieredRate(value, rate.Tiers)
	}
	return value * rate.PricePerUnit
}

func applyTieredRate(value float64, tiers []inventory.Tier) float64 {
	cost := 0.0
	remaining := value
	prev := 0.0

	for _, tier := range tiers {
		if remaining <= 0 {
			break
		}

		var tierSize float64
		if tier.UpTo != nil {
			tierSize = *tier.UpTo - prev
			prev = *tier.UpTo
		} else {
			tierSize = remaining
		}

		consumed := tierSize
		if consumed > remaining {
			consumed = remaining
		}

		cost += consumed * tier.PricePerUnit
		remaining -= consumed
	}

	return cost
}

// SeedDefaultRates populates the rates table with sensible defaults if empty.
func SeedDefaultRates(ctx context.Context, store *inventory.Store, logger *slog.Logger) error {
	count, err := store.RateCount(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		logger.Info("rates already seeded", "count", count)
		return nil
	}

	now := time.Now().UTC()
	defaults := []inventory.RateRecord{
		{ResourceType: "compute_instance", MeterName: "vm_uptime_seconds", KokuMetric: "vm_cost_per_hour", CostType: "Infrastructure", PricePerUnit: 0.01 / 3600, Currency: "USD", EffectiveFrom: now},
		{ResourceType: "compute_instance", MeterName: "vm_cpu_core_seconds", KokuMetric: "cpu_core_request_per_hour", CostType: "Supplementary", PricePerUnit: 0.005 / 3600, Currency: "USD", EffectiveFrom: now},
		{ResourceType: "compute_instance", MeterName: "vm_memory_gib_seconds", KokuMetric: "memory_gb_request_per_hour", CostType: "Supplementary", PricePerUnit: 0.002 / 3600, Currency: "USD", EffectiveFrom: now},
		{ResourceType: "cluster", MeterName: "cluster_uptime_seconds", KokuMetric: "cluster_cost_per_hour", CostType: "Infrastructure", PricePerUnit: 0.50 / 3600, Currency: "USD", EffectiveFrom: now},
		{ResourceType: "cluster", MeterName: "cluster_worker_node_seconds", KokuMetric: "node_cost_per_month", CostType: "Infrastructure", PricePerUnit: 0.10 / 3600, Currency: "USD", EffectiveFrom: now},
		{ResourceType: "model", MeterName: "maas_tokens_in", KokuMetric: "", CostType: "Supplementary", PricePerUnit: 0.50 / 1_000_000, Currency: "USD", EffectiveFrom: now},
		{ResourceType: "model", MeterName: "maas_tokens_out", KokuMetric: "", CostType: "Supplementary", PricePerUnit: 1.50 / 1_000_000, Currency: "USD", EffectiveFrom: now},
		{ResourceType: "model", MeterName: "maas_requests", KokuMetric: "", CostType: "Supplementary", PricePerUnit: 5.00 / 1_000_000, Currency: "USD", EffectiveFrom: now},
		{ResourceType: "bare_metal", MeterName: "bm_uptime_seconds", KokuMetric: "node_cost_per_month", CostType: "Infrastructure", PricePerUnit: 0.05 / 3600, Currency: "USD", EffectiveFrom: now},
	}

	for _, rate := range defaults {
		if _, err := store.UpsertRate(ctx, rate); err != nil {
			return err
		}
	}

	logger.Info("seeded default rates", "count", len(defaults))
	return nil
}

// SeedDefaultQuotas populates the quotas table with demo defaults if empty.
func SeedDefaultQuotas(ctx context.Context, store *inventory.Store, logger *slog.Logger) error {
	count, err := store.QuotaCount(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		logger.Info("quotas already seeded", "count", count)
		return nil
	}

	now := time.Now().UTC()
	tenants := []string{"tenant-acme", "tenant-globex", "tenant-initech", "shared"}

	type quotaDef struct {
		meterName string
		limit     float64
		unit      string
	}
	defs := []quotaDef{
		{"vm_cpu_core_seconds", 360000, "core_seconds"},
		{"vm_memory_gib_seconds", 1440000, "gib_seconds"},
		{"vm_uptime_seconds", 86400, "seconds"},
		{"maas_tokens_in", 10_000_000, "tokens"},
		{"maas_tokens_out", 5_000_000, "tokens"},
		{"maas_requests", 100_000, "requests"},
	}

	seeded := 0
	for _, tenant := range tenants {
		for _, d := range defs {
			if _, err := store.UpsertQuota(ctx, inventory.QuotaRecord{
				TenantID:      tenant,
				MeterName:     d.meterName,
				LimitValue:    d.limit,
				Unit:          d.unit,
				Period:        "monthly",
				EffectiveFrom: now,
			}); err != nil {
				return err
			}
			seeded++
		}
	}

	logger.Info("seeded default quotas", "count", seeded)
	return nil
}
