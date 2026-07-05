package rating

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
)

var (
	testStore  *inventory.Store
	testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
)

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = "postgres://user:pass@localhost:5434/costdb_test"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to test DB: %v\n", err)
		os.Exit(1)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "test DB not reachable: %v\n", err)
		os.Exit(1)
	}

	testStore = inventory.NewStore(pool, testLogger)
	if err := testStore.RunMigrations(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrations failed: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func TestSeedDefaultRates(t *testing.T) {
	ctx := context.Background()

	// SeedDefaultRates is idempotent — skips if any rates exist.
	if err := SeedDefaultRates(ctx, testStore, testLogger); err != nil {
		t.Fatalf("SeedDefaultRates failed: %v", err)
	}

	count, err := testStore.RateCount(ctx)
	if err != nil {
		t.Fatalf("RateCount: %v", err)
	}
	if count == 0 {
		t.Error("expected rates to exist after seeding")
	}
}

func TestSeedDefaultQuotas(t *testing.T) {
	ctx := context.Background()

	if err := SeedDefaultQuotas(ctx, testStore, testLogger); err != nil {
		t.Fatalf("SeedDefaultQuotas failed: %v", err)
	}

	count, err := testStore.QuotaCount(ctx)
	if err != nil {
		t.Fatalf("QuotaCount: %v", err)
	}
	if count < 24 {
		t.Errorf("expected at least 24 default quotas (4 tenants × 6 meters), got %d", count)
	}
}

func TestSweep_RatesUnratedEntries(t *testing.T) {
	ctx := context.Background()

	// Clean up orphaned unrated entries from prior test runs so the
	// sweep reaches our test entries within its batch limit.
	testStore.Pool().Exec(ctx, `
		DELETE FROM metering_entries WHERE id IN (
			SELECT me.id FROM metering_entries me
			LEFT JOIN cost_entries ce ON ce.metering_entry_id = me.id
			WHERE ce.id IS NULL
		)`)

	ts := time.Now().UnixNano()
	resourceID := fmt.Sprintf("sweep-test-vm-%d", ts)
	tenantID := fmt.Sprintf("sweep-tenant-%d", ts)
	meterName := fmt.Sprintf("sweep_meter_%d", ts)
	effectiveFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now().UTC()
	periodStart := now.Add(-60 * time.Second)

	// Insert a rate with a known effective_from well in the past.
	if _, err := testStore.UpsertRate(ctx, inventory.RateRecord{
		ResourceType: "test_resource",
		MeterName:    meterName,
		CostType:     "Infrastructure",
		PricePerUnit: 0.50,
		Currency:     "USD",
		EffectiveFrom: effectiveFrom,
	}); err != nil {
		t.Fatalf("upsert rate: %v", err)
	}

	entries := []inventory.MeteringEntry{
		{ResourceType: "test_resource", ResourceID: resourceID, TenantID: tenantID, MeterName: meterName, Value: 100.0, Unit: "units", PeriodStart: periodStart, PeriodEnd: now},
		{ResourceType: "test_resource", ResourceID: resourceID, TenantID: tenantID, MeterName: meterName, Value: 200.0, Unit: "units", PeriodStart: periodStart, PeriodEnd: now},
	}
	for _, e := range entries {
		if err := testStore.InsertMeteringEntry(ctx, e); err != nil {
			t.Fatalf("insert metering entry: %v", err)
		}
	}

	rater := New(testStore, 30*time.Second, testLogger)
	rater.batch = 10000
	// Run sweep multiple times to drain any backlog from prior test runs.
	for i := 0; i < 5; i++ {
		rater.sweep(ctx)
	}

	var costCount int
	err := testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM cost_entries WHERE resource_id = $1", resourceID).Scan(&costCount)
	if err != nil {
		t.Fatalf("query cost entries: %v", err)
	}
	if costCount != 2 {
		t.Errorf("expected 2 cost entries after sweep, got %d", costCount)
	}

	var costAmount float64
	err = testStore.Pool().QueryRow(ctx,
		"SELECT cost_amount FROM cost_entries WHERE resource_id = $1 ORDER BY cost_amount LIMIT 1",
		resourceID).Scan(&costAmount)
	if err != nil {
		t.Fatalf("query cost amount: %v", err)
	}
	// 100.0 * 0.50 = 50.0
	if costAmount != 50.0 {
		t.Errorf("expected cost_amount 50.0, got %f", costAmount)
	}
}

func TestSweep_SkipsAlreadyRated(t *testing.T) {
	ctx := context.Background()

	if err := SeedDefaultRates(ctx, testStore, testLogger); err != nil {
		t.Fatalf("seed rates: %v", err)
	}

	ts := time.Now().UnixNano()
	resourceID := fmt.Sprintf("rated-test-vm-%d", ts)
	now := time.Now().UTC()

	if err := testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "compute_instance", ResourceID: resourceID, TenantID: "tenant-acme",
		MeterName: "vm_uptime_seconds", Value: 60.0, Unit: "seconds",
		PeriodStart: now.Add(-60 * time.Second), PeriodEnd: now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rater := New(testStore, 30*time.Second, testLogger)

	rater.sweep(ctx)
	var count1 int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM cost_entries WHERE resource_id = $1", resourceID).Scan(&count1)

	rater.sweep(ctx)
	var count2 int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM cost_entries WHERE resource_id = $1", resourceID).Scan(&count2)

	if count1 != count2 {
		t.Errorf("second sweep should not produce duplicates: count1=%d, count2=%d", count1, count2)
	}
}

func TestSweep_SkipsUnknownMeterName(t *testing.T) {
	ctx := context.Background()

	ts := time.Now().UnixNano()
	resourceID := fmt.Sprintf("unknown-meter-%d", ts)
	now := time.Now().UTC()

	if err := testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "mystery_resource", ResourceID: resourceID, TenantID: "tenant-acme",
		MeterName: "nonexistent_meter", Value: 100.0, Unit: "widgets",
		PeriodStart: now.Add(-60 * time.Second), PeriodEnd: now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rater := New(testStore, 30*time.Second, testLogger)
	rater.sweep(ctx)

	var count int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM cost_entries WHERE resource_id = $1", resourceID).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 cost entries for unknown meter, got %d", count)
	}
}

func TestEvaluateThresholds_FiresAlerts(t *testing.T) {
	ctx := context.Background()

	if err := SeedDefaultRates(ctx, testStore, testLogger); err != nil {
		t.Fatalf("seed rates: %v", err)
	}
	if err := SeedDefaultQuotas(ctx, testStore, testLogger); err != nil {
		t.Fatalf("seed quotas: %v", err)
	}

	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("threshold-tenant-%d", ts)
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	if _, err := testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID:      tenantID,
		MeterName:     "vm_uptime_seconds",
		LimitValue:    1000.0,
		Unit:          "seconds",
		Period:        "monthly",
		EffectiveFrom: periodStart,
	}); err != nil {
		t.Fatalf("upsert quota: %v", err)
	}

	if err := testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "compute_instance", ResourceID: "vm-threshold", TenantID: tenantID,
		MeterName: "vm_uptime_seconds", Value: 900.0, Unit: "seconds",
		PeriodStart: periodStart, PeriodEnd: now,
	}); err != nil {
		t.Fatalf("insert metering: %v", err)
	}

	rater := New(testStore, 30*time.Second, testLogger)
	rater.evaluateThresholds(ctx)

	var alertCount int
	err := testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM alerts WHERE tenant_id = $1", tenantID).Scan(&alertCount)
	if err != nil {
		t.Fatalf("query alerts: %v", err)
	}
	if alertCount == 0 {
		t.Error("expected at least one alert for 90% consumption, got 0")
	}
}
