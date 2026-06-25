package summarizer

import (
	"context"
	"log/slog"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
)

type Summarizer struct {
	store    *inventory.Store
	interval time.Duration
	logger   *slog.Logger
}

func New(store *inventory.Store, interval time.Duration, logger *slog.Logger) *Summarizer {
	return &Summarizer{store: store, interval: interval, logger: logger}
}

// Run periodically summarizes inventory data into daily usage summaries.
func (s *Summarizer) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.summarizeYesterday(ctx)
		}
	}
}

// SummarizeDate calculates usage for a specific date and writes summary rows.
func (s *Summarizer) SummarizeDate(ctx context.Context, date time.Time) error {
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)

	s.logger.Info("summarizing", "date", dayStart.Format("2006-01-02"))

	// Clear existing summaries for re-calculation.
	if err := s.store.DeleteDailyUsageSummaries(ctx, dayStart); err != nil {
		return err
	}

	instances, err := s.store.ComputeInstancesAliveDuring(ctx, dayStart, dayEnd)
	if err != nil {
		return err
	}

	count := 0
	for _, inst := range instances {
		effectiveStart := inst.CreatedAt
		if effectiveStart.Before(dayStart) {
			effectiveStart = dayStart
		}

		effectiveEnd := dayEnd
		if inst.DeletedAt != nil && inst.DeletedAt.Before(dayEnd) {
			effectiveEnd = *inst.DeletedAt
		}

		if !effectiveEnd.After(effectiveStart) {
			continue
		}

		durationHours := effectiveEnd.Sub(effectiveStart).Hours()

		cores := inst.Cores
		memGiB := inst.MemoryGiB

		// If cores/memory not directly on the instance, try instance type lookup.
		if cores == 0 && inst.InstanceType != "" {
			if it, err := s.store.GetInstanceType(ctx, inst.InstanceType); err == nil {
				cores = it.Cores
				memGiB = it.MemoryGiB
			}
		}

		summary := inventory.DailyUsageSummary{
			UsageDate:     dayStart,
			ClusterID:     inst.ClusterID,
			Tenant:        inst.Tenant,
			Project:       inst.Project,
			ResourceID:    inst.InstanceID,
			ResourceType:  "compute_instance",
			InstanceType:  inst.InstanceType,
			Cores:         cores,
			MemoryGiB:     memGiB,
			CPUCoreHours:  float64(cores) * durationHours,
			MemoryGBHours: float64(memGiB) * durationHours,
			DurationHours: durationHours,
		}

		if err := s.store.InsertDailyUsageSummary(ctx, summary); err != nil {
			s.logger.Error("failed to insert summary", "resource", inst.InstanceID, "error", err)
			continue
		}
		count++
	}

	s.logger.Info("summarization complete", "date", dayStart.Format("2006-01-02"), "summaries", count)
	return nil
}

func (s *Summarizer) summarizeYesterday(ctx context.Context) {
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	if err := s.SummarizeDate(ctx, yesterday); err != nil {
		s.logger.Error("failed to summarize", "error", err)
	}
}
