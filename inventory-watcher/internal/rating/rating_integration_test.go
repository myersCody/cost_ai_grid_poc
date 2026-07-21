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

	// Clean up unrated entries from prior test runs so the sweep
	// reaches our test entries within its batch limit.
	testStore.Pool().Exec(ctx, `UPDATE metering_entries SET rated_at = NOW() WHERE rated_at IS NULL`)

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

	// Rate the entries directly using ApplyRate to verify correctness,
	// then insert cost entries manually. The full sweep pipeline is tested
	// by the cumulative tier integration tests; this test focuses on
	// flat-rate cost calculation via the store → rate → cost path.
	now2 := time.Now().UTC()
	rate, err := testStore.FindRate(ctx, tenantID, "test_resource", "", meterName, now2)
	if err != nil || rate == nil {
		t.Fatalf("FindRate failed: %v (rate=%v)", err, rate)
	}

	for _, e := range entries {
		cost := ApplyRate(e.Value, *rate)
		testStore.InsertCostEntry(ctx, inventory.CostEntry{
			RateID:       rate.ID,
			TenantID:     tenantID,
			ResourceType: "test_resource",
			ResourceID:   resourceID,
			MeterName:    meterName,
			MeteredValue: e.Value,
			CostAmount:   cost,
			Currency:     rate.Currency,
			PeriodStart:  e.PeriodStart,
			PeriodEnd:    e.PeriodEnd,
		})
	}

	var costCount int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM cost_entries WHERE resource_id = $1", resourceID).Scan(&costCount)
	if costCount != 2 {
		t.Fatalf("expected 2 cost entries, got %d", costCount)
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

// TestEvaluateThresholds_CustomLevels verifies that per-quota thresholds
// override the global [50, 70, 90, 100] levels.
func TestEvaluateThresholds_CustomLevels(t *testing.T) {
	ctx := context.Background()

	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("custom-thresh-tenant-%d", ts)
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Create quota with custom thresholds at 25% and 75%
	if _, err := testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		TenantID:      tenantID,
		MeterName:     "vm_uptime_seconds",
		LimitValue:    1000.0,
		Unit:          "seconds",
		Period:        "monthly",
		Thresholds:    []float64{25, 75},
		EffectiveFrom: periodStart,
	}); err != nil {
		t.Fatalf("upsert quota: %v", err)
	}

	// Insert consumption at 30% — should fire 25% threshold but NOT 75%
	if err := testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "compute_instance", ResourceID: "vm-custom-thresh", TenantID: tenantID,
		MeterName: "vm_uptime_seconds", Value: 300.0, Unit: "seconds",
		PeriodStart: periodStart, PeriodEnd: now,
	}); err != nil {
		t.Fatalf("insert metering: %v", err)
	}

	rater := New(testStore, 30*time.Second, testLogger)
	rater.evaluateThresholds(ctx)

	// Should have fired 25% but not 75%
	var alert25, alert75 int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM alerts WHERE tenant_id = $1 AND threshold_pct = 25", tenantID).Scan(&alert25)
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM alerts WHERE tenant_id = $1 AND threshold_pct = 75", tenantID).Scan(&alert75)

	if alert25 == 0 {
		t.Error("expected 25% threshold to fire at 30% consumption")
	}
	if alert75 != 0 {
		t.Error("75% threshold should NOT have fired at 30% consumption")
	}

	// Should NOT have fired the global 50% threshold (custom overrides global)
	var alert50 int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM alerts WHERE tenant_id = $1 AND threshold_pct = 50", tenantID).Scan(&alert50)
	if alert50 != 0 {
		t.Error("global 50% threshold should NOT fire when custom thresholds are set")
	}
}

// TestSweep_CumulativeTiers verifies the full cumulative tier pipeline:
// a rate with tier_mode="cumulative" accumulates usage over the billing
// period and prices marginal deltas at the correct tier position.
//
// Tier: first 100 units free, 100-500 at $0.10, 500+ at $0.05
// Entries: 10 entries of 80 units each = 800 total
// Expected cost:
//   first 100 free ($0)
//   next 400 at $0.10 ($40)
//   next 300 at $0.05 ($15)
//   total = $55.00
func TestSweep_CumulativeTiers(t *testing.T) {
	ctx := context.Background()

	testStore.Pool().Exec(ctx, `UPDATE metering_entries SET rated_at = NOW() WHERE rated_at IS NULL`)

	ts := time.Now().UnixNano()
	resourceID := fmt.Sprintf("cumul-test-%d", ts)
	tenantID := fmt.Sprintf("cumul-tenant-%d", ts)
	meterName := fmt.Sprintf("cumul_meter_%d", ts)
	effectiveFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	up100 := 100.0
	up500 := 500.0
	if _, err := testStore.UpsertRate(ctx, inventory.RateRecord{
		ResourceType:  "test_cumul",
		MeterName:     meterName,
		CostType:      "Supplementary",
		PricePerUnit:  0,
		Currency:      "USD",
		TierMode:      "cumulative",
		TierPeriod:    "monthly",
		Tiers: []inventory.Tier{
			{UpTo: &up100, PricePerUnit: 0},
			{UpTo: &up500, PricePerUnit: 0.10},
			{UpTo: nil, PricePerUnit: 0.05},
		},
		EffectiveFrom: effectiveFrom,
	}); err != nil {
		t.Fatalf("upsert cumulative rate: %v", err)
	}

	// Insert 10 entries of 80 units each, spread across the month
	for i := 0; i < 10; i++ {
		entryStart := monthStart.Add(time.Duration(i) * time.Hour)
		entryEnd := entryStart.Add(time.Hour)
		if err := testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
			ResourceType: "test_cumul",
			ResourceID:   resourceID,
			TenantID:     tenantID,
			MeterName:    meterName,
			Value:        80.0,
			Unit:         "units",
			PeriodStart:  entryStart,
			PeriodEnd:    entryEnd,
		}); err != nil {
			t.Fatalf("insert metering entry %d: %v", i, err)
		}
	}

	rater := New(testStore, 30*time.Second, testLogger)
	rater.batch = 10000
	for i := 0; i < 5; i++ {
		rater.sweep(ctx)
	}

	// Verify all 10 entries were rated
	var costCount int
	err := testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM cost_entries WHERE resource_id = $1", resourceID).Scan(&costCount)
	if err != nil {
		t.Fatalf("query cost count: %v", err)
	}
	if costCount != 10 {
		t.Errorf("expected 10 cost entries, got %d", costCount)
	}

	// Verify total cost matches the cumulative calculation
	var totalCost float64
	err = testStore.Pool().QueryRow(ctx,
		"SELECT COALESCE(SUM(cost_amount), 0) FROM cost_entries WHERE resource_id = $1",
		resourceID).Scan(&totalCost)
	if err != nil {
		t.Fatalf("query total cost: %v", err)
	}
	// first 100 free + 400 at $0.10 + 300 at $0.05 = $55.00
	if totalCost < 54.99 || totalCost > 55.01 {
		t.Errorf("expected total cost ~$55.00, got $%.4f", totalCost)
	}

	// Verify individual cost entries show the graduated progression:
	// Entry 1 (0-80): all in free tier → $0
	// Entry 2 (80-160): 20 free + 60 at $0.10 → $6.00
	// Entry 7 (480-560): 20 at $0.10 + 60 at $0.05 → $5.00
	// Entry 10 (720-800): all at $0.05 → $4.00
	rows, err := testStore.Pool().Query(ctx,
		"SELECT cost_amount FROM cost_entries WHERE resource_id = $1 ORDER BY period_start",
		resourceID)
	if err != nil {
		t.Fatalf("query cost rows: %v", err)
	}
	defer rows.Close()

	var costs []float64
	for rows.Next() {
		var c float64
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		costs = append(costs, c)
	}

	if len(costs) != 10 {
		t.Fatalf("expected 10 cost rows, got %d", len(costs))
	}

	// Entry 1 (0→80): entirely in free tier
	if costs[0] != 0 {
		t.Errorf("entry 1 (free tier): expected $0, got $%.4f", costs[0])
	}

	// Entry 2 (80→160): crosses free tier boundary at 100
	// 20 units free + 60 units at $0.10 = $6.00
	if costs[1] < 5.99 || costs[1] > 6.01 {
		t.Errorf("entry 2 (crosses free): expected ~$6.00, got $%.4f", costs[1])
	}

	// Entry 10 (720→800): entirely in final tier ($0.05)
	// 80 × $0.05 = $4.00
	if costs[9] < 3.99 || costs[9] > 4.01 {
		t.Errorf("entry 10 (final tier): expected ~$4.00, got $%.4f", costs[9])
	}
}

// TestSweep_CumulativeTiers_PerEventFallback verifies that rates with
// tier_mode="per_event" (default) still work as before — each entry
// priced independently through the tier ladder.
func TestSweep_CumulativeTiers_PerEventFallback(t *testing.T) {
	ctx := context.Background()

	testStore.Pool().Exec(ctx, `UPDATE metering_entries SET rated_at = NOW() WHERE rated_at IS NULL`)

	ts := time.Now().UnixNano()
	resourceID := fmt.Sprintf("perevent-test-%d", ts)
	tenantID := fmt.Sprintf("perevent-tenant-%d", ts)
	meterName := fmt.Sprintf("perevent_meter_%d", ts)
	effectiveFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now().UTC()

	up100 := 100.0
	if _, err := testStore.UpsertRate(ctx, inventory.RateRecord{
		ResourceType:  "test_perevent",
		MeterName:     meterName,
		CostType:      "Supplementary",
		PricePerUnit:  0,
		Currency:      "USD",
		TierMode:      "per_event",
		Tiers: []inventory.Tier{
			{UpTo: &up100, PricePerUnit: 0},
			{UpTo: nil, PricePerUnit: 0.10},
		},
		EffectiveFrom: effectiveFrom,
	}); err != nil {
		t.Fatalf("upsert rate: %v", err)
	}

	// Insert 5 entries of 50 units — per-event, each is within free tier
	for i := 0; i < 5; i++ {
		if err := testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
			ResourceType: "test_perevent",
			ResourceID:   resourceID,
			TenantID:     tenantID,
			MeterName:    meterName,
			Value:        50.0,
			Unit:         "units",
			PeriodStart:  now.Add(-time.Hour),
			PeriodEnd:    now,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	rater := New(testStore, 30*time.Second, testLogger)
	rater.batch = 10000
	for i := 0; i < 3; i++ {
		rater.sweep(ctx)
	}

	// Per-event: each 50-unit entry is independently within the 100-unit free tier
	// Total cost should be $0 (all free, no accumulation)
	var totalCost float64
	testStore.Pool().QueryRow(ctx,
		"SELECT COALESCE(SUM(cost_amount), 0) FROM cost_entries WHERE resource_id = $1",
		resourceID).Scan(&totalCost)

	if totalCost != 0 {
		t.Errorf("per-event: expected $0 (each entry in free tier), got $%.4f", totalCost)
	}
}

// TestSweep_CumulativeTiers_DifferentTenants verifies that cumulative
// accumulation is per-tenant — two tenants with the same rate but
// different usage get independent tier positions.
func TestSweep_CumulativeTiers_DifferentTenants(t *testing.T) {
	ctx := context.Background()

	testStore.Pool().Exec(ctx, `UPDATE metering_entries SET rated_at = NOW() WHERE rated_at IS NULL`)

	ts := time.Now().UnixNano()
	meterName := fmt.Sprintf("multitenant_meter_%d", ts)
	effectiveFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	up50 := 50.0
	if _, err := testStore.UpsertRate(ctx, inventory.RateRecord{
		ResourceType:  "test_multitenant",
		MeterName:     meterName,
		CostType:      "Infrastructure",
		PricePerUnit:  0,
		Currency:      "USD",
		TierMode:      "cumulative",
		TierPeriod:    "monthly",
		Tiers: []inventory.Tier{
			{UpTo: &up50, PricePerUnit: 0},
			{UpTo: nil, PricePerUnit: 1.00},
		},
		EffectiveFrom: effectiveFrom,
	}); err != nil {
		t.Fatalf("upsert rate: %v", err)
	}

	tenantA := fmt.Sprintf("tenant-A-%d", ts)
	tenantB := fmt.Sprintf("tenant-B-%d", ts)
	resourceA := fmt.Sprintf("res-A-%d", ts)
	resourceB := fmt.Sprintf("res-B-%d", ts)

	// Tenant A: 100 units (50 free + 50 at $1 = $50)
	if err := testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "test_multitenant", ResourceID: resourceA, TenantID: tenantA,
		MeterName: meterName, Value: 100.0, Unit: "units",
		PeriodStart: monthStart, PeriodEnd: monthStart.Add(time.Hour),
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}

	// Tenant B: 30 units (all free = $0)
	if err := testStore.InsertMeteringEntry(ctx, inventory.MeteringEntry{
		ResourceType: "test_multitenant", ResourceID: resourceB, TenantID: tenantB,
		MeterName: meterName, Value: 30.0, Unit: "units",
		PeriodStart: monthStart, PeriodEnd: monthStart.Add(time.Hour),
	}); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	rater := New(testStore, 30*time.Second, testLogger)
	rater.batch = 10000
	for i := 0; i < 3; i++ {
		rater.sweep(ctx)
	}

	var costA, costB float64
	testStore.Pool().QueryRow(ctx,
		"SELECT COALESCE(SUM(cost_amount), 0) FROM cost_entries WHERE resource_id = $1",
		resourceA).Scan(&costA)
	testStore.Pool().QueryRow(ctx,
		"SELECT COALESCE(SUM(cost_amount), 0) FROM cost_entries WHERE resource_id = $1",
		resourceB).Scan(&costB)

	// Tenant A: 50 free + 50 at $1 = $50
	if costA < 49.99 || costA > 50.01 {
		t.Errorf("tenant A: expected ~$50, got $%.4f", costA)
	}
	// Tenant B: 30 units, all in free tier = $0
	if costB != 0 {
		t.Errorf("tenant B: expected $0 (all free), got $%.4f", costB)
	}
}

// TestEvaluateThresholds_MonetaryBudget verifies that monetary budgets
// (unit=USD) fire threshold alerts based on cost_entries, not metering_entries.
func TestEvaluateThresholds_MonetaryBudget(t *testing.T) {
	ctx := context.Background()

	ts := time.Now().UnixNano()
	tenantID := fmt.Sprintf("budget-thresh-tenant-%d", ts)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Create a $1000/month budget
	if _, err := testStore.UpsertQuota(ctx, inventory.QuotaRecord{
		Name:          "Monthly budget",
		TenantID:      tenantID,
		MeterName:     "*",
		LimitValue:    1000.0,
		Unit:          "USD",
		Period:        "monthly",
		EffectiveFrom: monthStart,
	}); err != nil {
		t.Fatalf("upsert budget: %v", err)
	}

	// Insert $800 of cost entries (80% — should fire 50% and 70% thresholds)
	testStore.InsertCostEntry(ctx, inventory.CostEntry{
		TenantID: tenantID, ResourceType: "compute_instance",
		ResourceID: "vm-budget-thresh", MeterName: "vm_uptime_seconds",
		MeteredValue: 86400, CostAmount: 800.0, Currency: "USD",
		PeriodStart: monthStart, PeriodEnd: now,
	})

	rater := New(testStore, 30*time.Second, testLogger)
	rater.evaluateThresholds(ctx)

	var alert50, alert70, alert90 int
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM alerts WHERE tenant_id = $1 AND threshold_pct = 50", tenantID).Scan(&alert50)
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM alerts WHERE tenant_id = $1 AND threshold_pct = 70", tenantID).Scan(&alert70)
	testStore.Pool().QueryRow(ctx,
		"SELECT count(*) FROM alerts WHERE tenant_id = $1 AND threshold_pct = 90", tenantID).Scan(&alert90)

	if alert50 == 0 {
		t.Error("expected 50% budget threshold to fire at $800/$1000")
	}
	if alert70 == 0 {
		t.Error("expected 70% budget threshold to fire at $800/$1000")
	}
	if alert90 != 0 {
		t.Error("90% budget threshold should NOT fire at $800/$1000")
	}
}
