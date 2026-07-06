package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/config"
	"github.com/osac-project/cost-event-consumer/internal/custommetrics"
	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metering"
	"github.com/osac-project/cost-event-consumer/internal/metrics"
)

// CloudEvent is a generic CloudEvents 1.0 envelope. The Data field is
// decoded separately based on the Type.
type CloudEvent struct {
	SpecVersion     string          `json:"specversion"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	ID              string          `json:"id"`
	Time            time.Time       `json:"time"`
	Subject         string          `json:"subject"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
}

// ComputeInstanceEventData matches the OSAC metering collector VMaaS schema.
// Source: https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README.md#cloudevents-schema
type ComputeInstanceEventData struct {
	DurationSeconds  int    `json:"duration_seconds"`
	CPUCoreSeconds   int64  `json:"cpu_core_seconds"`
	MemoryGiBSeconds int64  `json:"memory_gib_seconds"`
	TenantID         string `json:"tenant_id"`
	InstanceID       string `json:"instance_id"`
	Template         string `json:"template"`
	CatalogItem      string `json:"catalog_item"`
	State            string `json:"state"`
	Cores            int32  `json:"cores"`
	MemoryGiB        int32  `json:"memory_gib"`
}

// ClusterEventData matches the OSAC metering collector CaaS schema.
// Source: https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README-caas.md#cloudevents-schema
type ClusterEventData struct {
	DurationSeconds   int    `json:"duration_seconds"`
	WorkerNodeSeconds int64  `json:"worker_node_seconds"`
	NodeCount         int32  `json:"node_count"`
	TenantID          string `json:"tenant_id"`
	ClusterID         string `json:"cluster_id"`
	Template          string `json:"template"`
	State             string `json:"state"`
	HostType          string `json:"host_type"`
}

// MaaSEventData accepts both our mock format and the real IPP external-metering plugin format.
// IPP source: https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/320
// IPP client: https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go
type MaaSEventData struct {
	// Legacy mock format fields (our simulator, backwards compat)
	TenantID        string `json:"tenant_id"`
	ModelID         string `json:"model_id"`
	ModelName       string `json:"model_name"`
	Template        string `json:"template"`
	State           string `json:"state"`
	TokensIn        int64  `json:"tokens_in"`
	TokensOut       int64  `json:"tokens_out"`
	Requests        int64  `json:"requests"`
	DurationSeconds int    `json:"duration_seconds"`
	RequestCount    int64  `json:"request_count"`
	// IPP external-metering plugin fields (authoritative format)
	User                string `json:"user"`
	Group               string `json:"group"`
	Subscription        string `json:"subscription"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	PromptTokens        int64  `json:"prompt_tokens"`
	CompletionTokens    int64  `json:"completion_tokens"`
	TotalTokens         int64  `json:"total_tokens"`
	CachedInputTokens   int64  `json:"cached_input_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
	DurationMs          int64  `json:"duration_ms"`
}

const (
	// VMaaS/CaaS event types from OSAC metering collector.
	// Source: https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/
	EventTypeComputeInstance = "osac.compute_instance.lifecycle"
	EventTypeCluster         = "osac.cluster.lifecycle"
	// Legacy mock MaaS event type (our simulator).
	EventTypeModel = "osac.model.lifecycle"
	// Real IPP external-metering plugin event type.
	// Source: https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/320
	EventTypeInferenceTokens = "inference.tokens.used"

	maxRequestBodySize = 1 << 20 // 1MB
	maxIDLength        = 256
)

type Reconciler interface {
	ReconcileAll(ctx context.Context)
}

type Handler struct {
	store         *inventory.Store
	meter         *metering.Meter
	cfg           *config.Config
	customMetrics *custommetrics.Registry
	reconciler    Reconciler
	reconciling   atomic.Bool
	logger        *slog.Logger
}

func NewHandler(store *inventory.Store, meter *metering.Meter, cfg *config.Config, customMetrics *custommetrics.Registry, logger *slog.Logger) *Handler {
	return &Handler{store: store, meter: meter, cfg: cfg, customMetrics: customMetrics, logger: logger}
}

func (h *Handler) SetReconciler(r Reconciler) {
	h.reconciler = r
}

func (h *Handler) ServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/events", h.handleEvent)
	mux.HandleFunc("GET /api/v1/quotas/", h.handleQuotaStatus)
	mux.HandleFunc("GET /api/v1/reports/costs", h.handleCostReport)
	mux.HandleFunc("GET /api/v1/reports/summary", h.handlePipelineSummary)
	mux.HandleFunc("GET /api/v1/customers/", h.handleBalanceCheck)
	mux.HandleFunc("GET /api/v1/debug/config", h.handleDebugConfig)
	mux.HandleFunc("POST /api/v1/reconcile", h.handleReconcile)
	mux.HandleFunc("GET /healthz", h.handleLiveness)
	mux.HandleFunc("GET /readyz", h.handleReadiness)
	if h.cfg != nil && h.cfg.DebugDashboard {
		mux.HandleFunc("GET /debug/dashboard", h.handleDebugDashboard)
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/debug/dashboard", http.StatusFound)
			}
		})
	}
	return mux
}

func (h *Handler) handleEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var ce CloudEvent
	if err := json.NewDecoder(r.Body).Decode(&ce); err != nil {
		writeErrorJSON(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if ce.ID == "" || ce.Type == "" {
		writeErrorJSON(w, "id and type are required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	resourceType, resourceID, tenantID := classifyEvent(ce)
	if (resourceID == "" || tenantID == "") && h.customMetrics != nil && h.customMetrics.HasEventType(ce.Type) {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(ce.Data, &dataMap); err == nil {
			resourceType, resourceID, tenantID = h.customMetrics.ClassifyEvent(ce.Type, dataMap)
		}
	}
	if resourceID == "" || tenantID == "" {
		writeErrorJSON(w, "event data must include resource_id and tenant_id", http.StatusBadRequest)
		return
	}
	if len(resourceID) > maxIDLength || len(tenantID) > maxIDLength {
		writeErrorJSON(w, "resource_id or tenant_id exceeds maximum length", http.StatusBadRequest)
		return
	}

	fullJSON, _ := json.Marshal(ce)
	inserted, err := h.store.InsertRawEvent(ctx, inventory.RawEvent{
		EventID:      ce.ID,
		EventType:    ce.Type,
		EventSource:  ce.Source,
		EventTime:    ce.Time,
		TenantID:     tenantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Data:         fullJSON,
	})
	if err != nil {
		writeErrorJSON(w, "failed to store event", http.StatusInternalServerError)
		h.logger.Error("failed to store raw event", "error", err, "event_id", ce.ID)
		return
	}
	if !inserted {
		metrics.EventsProcessedTotal.WithLabelValues(ce.Type, "duplicate").Inc()
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]string{"status": "duplicate"})
		return
	}

	var processingErr error
	switch ce.Type {
	case EventTypeComputeInstance:
		processingErr = h.handleComputeInstanceEvent(ctx, ce)
	case EventTypeCluster:
		processingErr = h.handleClusterEvent(ctx, ce)
	case EventTypeModel, EventTypeInferenceTokens:
		processingErr = h.handleModelEvent(ctx, ce)
	default:
		if h.customMetrics != nil && h.customMetrics.HasEventType(ce.Type) {
			processingErr = h.customMetrics.ProcessEvent(ctx, h.store, ce.Type, ce.Data, ce.Time, h.logger)
		} else {
			h.logger.Warn("unknown CloudEvent type", "type", ce.Type)
		}
	}

	if processingErr != nil {
		metrics.EventsProcessedTotal.WithLabelValues(ce.Type, "error").Inc()
		h.logger.Error("event processing failed", "error", processingErr, "event_id", ce.ID, "type", ce.Type)
		writeErrorJSON(w, "event stored but processing failed", http.StatusInternalServerError)
		return
	}

	metrics.EventsProcessedTotal.WithLabelValues(ce.Type, "accepted").Inc()
	// IPP external-metering client accepts 200 and 204 (not 202).
	// Metering-simulator OpenAPI spec uses 204 for event accepted.
	// Source: docs/specs/maas-metering-openapi.yaml
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleComputeInstanceEvent(ctx context.Context, ce CloudEvent) error {
	var data ComputeInstanceEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		return err
	}

	if !metering.IsComputeInstanceBillable(data.State) {
		return nil
	}

	if data.DurationSeconds <= 0 {
		return fmt.Errorf("invalid duration_seconds: %d (must be positive)", data.DurationSeconds)
	}

	if err := h.store.UpsertComputeInstance(ctx, inventory.ComputeInstanceRecord{
		InstanceID:  data.InstanceID,
		Tenant:      data.TenantID,
		Cores:       data.Cores,
		MemoryGiB:   data.MemoryGiB,
		State:       data.State,
		CreatedAt:   ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second),
		LastEventID: ce.ID,
	}); err != nil {
		return err
	}

	periodStart := ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second)

	entries := []inventory.MeteringEntry{
		{ResourceType: "compute_instance", ResourceID: data.InstanceID, TenantID: data.TenantID, MeterName: "vm_uptime_seconds", Value: float64(data.DurationSeconds), Unit: "seconds", PeriodStart: periodStart, PeriodEnd: ce.Time},
		{ResourceType: "compute_instance", ResourceID: data.InstanceID, TenantID: data.TenantID, MeterName: "vm_cpu_core_seconds", Value: float64(data.CPUCoreSeconds), Unit: "core_seconds", PeriodStart: periodStart, PeriodEnd: ce.Time},
		{ResourceType: "compute_instance", ResourceID: data.InstanceID, TenantID: data.TenantID, MeterName: "vm_memory_gib_seconds", Value: float64(data.MemoryGiBSeconds), Unit: "gib_seconds", PeriodStart: periodStart, PeriodEnd: ce.Time},
	}

	if err := h.store.InsertMeteringEntryBatch(ctx, entries); err != nil {
		return err
	}

	if err := h.store.UpdateComputeInstanceLastMetered(ctx, data.InstanceID, ce.Time); err != nil {
		return err
	}

	h.logger.Debug("ingested VM heartbeat", "instance", data.InstanceID, "cores", data.Cores, "duration", data.DurationSeconds)
	return nil
}

func (h *Handler) handleClusterEvent(ctx context.Context, ce CloudEvent) error {
	var data ClusterEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		return err
	}

	if !metering.IsClusterBillable(data.State) {
		return nil
	}

	if data.DurationSeconds <= 0 {
		return fmt.Errorf("invalid duration_seconds: %d (must be positive)", data.DurationSeconds)
	}

	periodStart := ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second)

	var entries []inventory.MeteringEntry

	if data.HostType == "_control_plane" {
		entries = append(entries, inventory.MeteringEntry{ResourceType: "cluster", ResourceID: data.ClusterID, TenantID: data.TenantID, MeterName: "cluster_uptime_seconds", Value: float64(data.DurationSeconds), Unit: "seconds", PeriodStart: periodStart, PeriodEnd: ce.Time})
	}

	if data.WorkerNodeSeconds > 0 {
		entries = append(entries, inventory.MeteringEntry{ResourceType: "cluster", ResourceID: data.ClusterID, TenantID: data.TenantID, MeterName: "cluster_worker_node_seconds", Value: float64(data.WorkerNodeSeconds), Unit: "node_seconds", PeriodStart: periodStart, PeriodEnd: ce.Time})
	}

	if err := h.store.InsertMeteringEntryBatch(ctx, entries); err != nil {
		return err
	}

	if err := h.store.UpdateClusterLastMetered(ctx, data.ClusterID, ce.Time); err != nil {
		return err
	}

	h.logger.Debug("ingested cluster heartbeat", "cluster", data.ClusterID, "host_type", data.HostType, "duration", data.DurationSeconds)
	return nil
}

func (h *Handler) handleModelEvent(ctx context.Context, ce CloudEvent) error {
	var data MaaSEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		return err
	}

	// Normalize IPP format → our internal format
	if data.PromptTokens > 0 && data.TokensIn == 0 {
		data.TokensIn = data.PromptTokens
	}
	if data.CompletionTokens > 0 && data.TokensOut == 0 {
		data.TokensOut = data.CompletionTokens
	}
	if data.Model != "" && data.ModelName == "" {
		data.ModelName = data.Model
	}
	if data.Model != "" && data.ModelID == "" {
		data.ModelID = data.Model
	}
	// Tenant attribution from IPP CloudEvent identity fields.
	// The IPP event has no tenant_id. We try in order:
	// 1. subscription namespace (if format is "{namespace}/{name}")
	// 2. group (K8s group membership from Authorino)
	// 3. user (username from Authorino)
	// NOTE: It is unclear whether the subscription field carries the namespace
	// prefix. The MaaSSubscription CR is namespaced but may live in a shared
	// MaaS namespace, not a per-tenant namespace. This mapping needs
	// confirmation from the OSAC/RHOAI team.
	// Source: docs/research/maas-tenant-attribution.md
	if data.TenantID == "" && data.Subscription != "" {
		if idx := strings.Index(data.Subscription, "/"); idx > 0 {
			data.TenantID = data.Subscription[:idx]
		}
	}
	if data.TenantID == "" && data.Group != "" {
		data.TenantID = data.Group
	}
	if data.TenantID == "" && data.User != "" {
		data.TenantID = data.User
	}
	if data.DurationSeconds < 0 {
		return fmt.Errorf("invalid duration_seconds: %d (must be non-negative)", data.DurationSeconds)
	}
	if data.DurationMs > 0 && data.DurationSeconds == 0 {
		data.DurationSeconds = int(data.DurationMs / 1000)
	}
	if data.RequestCount > 0 && data.Requests == 0 {
		data.Requests = data.RequestCount
	}
	if data.State == "" {
		data.State = "MODEL_STATE_RUNNING"
	}

	createdAt := ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second)
	if err := h.store.UpsertModel(ctx, inventory.ModelRecord{
		ModelID:     data.ModelID,
		Name:        data.ModelName,
		ModelName:   data.ModelName,
		Tenant:      data.TenantID,
		Template:    data.Template,
		State:       data.State,
		CreatedAt:   createdAt,
		LastEventID: ce.ID,
	}); err != nil {
		return err
	}

	h.meter.MeterMaaSEvent(ctx, metering.MaaSUsage{
		ModelID:             data.ModelID,
		ModelName:           data.ModelName,
		TenantID:            data.TenantID,
		State:               data.State,
		TokensIn:            data.TokensIn,
		TokensOut:           data.TokensOut,
		CachedInputTokens:   data.CachedInputTokens,
		ReasoningTokens:     data.ReasoningTokens,
		Requests:            data.Requests,
		EventTime:           ce.Time,
		DurationSeconds:     float64(data.DurationSeconds),
	})
	return nil
}

func classifyEvent(ce CloudEvent) (resourceType, resourceID, tenantID string) {
	var peek struct {
		TenantID   string `json:"tenant_id"`
		InstanceID string `json:"instance_id"`
		ClusterID  string `json:"cluster_id"`
		ModelID    string `json:"model_id"`
		User       string `json:"user"`
		Model      string `json:"model"`
	}
	if err := json.Unmarshal(ce.Data, &peek); err != nil {
		return ce.Type, "", ce.Subject
	}

	tenantID = peek.TenantID
	if tenantID == "" {
		tenantID = ce.Subject
	}

	switch ce.Type {
	case EventTypeComputeInstance:
		return "ComputeInstance", peek.InstanceID, tenantID
	case EventTypeCluster:
		return "Cluster", peek.ClusterID, tenantID
	case EventTypeModel:
		return "Model", peek.ModelID, tenantID
	case EventTypeInferenceTokens:
		rid := peek.ModelID
		if rid == "" {
			rid = peek.Model
		}
		return "Model", rid, tenantID
	default:
		return ce.Type, "", tenantID
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErrorJSON(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type quotaStatusResponse struct {
	TenantID string                 `json:"tenant_id"`
	Period   string                 `json:"period"`
	Quotas   []inventory.QuotaStatus `json:"quotas"`
}

var thresholdLevels = []float64{50, 70, 90, 100}

func (h *Handler) handleQuotaStatus(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/quotas/"), "/")
		if len(parts) > 0 {
			tenantID = parts[0]
		}
	}
	if tenantID == "" {
		writeErrorJSON(w, "tenant_id required", http.StatusBadRequest)
		return
	}
	if len(tenantID) > maxIDLength {
		writeErrorJSON(w, "tenant_id exceeds maximum length", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)
	periodLabel := now.Format("2006-01")

	quotas, err := h.store.QuotasForTenant(ctx, tenantID, now)
	if err != nil {
		writeErrorJSON(w, "failed to query quotas", http.StatusInternalServerError)
		h.logger.Error("quota query failed", "error", err, "tenant", tenantID)
		return
	}

	var statuses []inventory.QuotaStatus
	for _, q := range quotas {
		consumed, err := h.store.MeteringSum(ctx, tenantID, q.MeterName, periodStart, periodEnd)
		if err != nil {
			h.logger.Error("failed to sum metering", "tenant", tenantID, "meter", q.MeterName, "error", err)
			continue
		}

		pct := 0.0
		if q.LimitValue > 0 {
			pct = (consumed / q.LimitValue) * 100
		}

		thresholds := make(map[string]bool, len(thresholdLevels))
		for _, t := range thresholdLevels {
			thresholds[fmt.Sprintf("%.0f", t)] = pct >= t
		}

		meterAlerts, _ := h.store.AlertsForTenantMeter(ctx, tenantID, q.MeterName, periodLabel)

		statuses = append(statuses, inventory.QuotaStatus{
			MeterName:  q.MeterName,
			Unit:       q.Unit,
			Limit:      q.LimitValue,
			Consumed:   consumed,
			Percentage: math.Round(pct*100) / 100,
			Thresholds: thresholds,
			Alerts:     meterAlerts,
		})
	}

	resp := quotaStatusResponse{
		TenantID: tenantID,
		Period:   periodLabel,
		Quotas:   statuses,
	}

	writeJSON(w, resp)
}

// ── Cost Report ──

type costReportResponse struct {
	Meta costReportMeta            `json:"meta"`
	Data []inventory.CostReportRow `json:"data"`
}

type costReportMeta struct {
	Total   costTotal         `json:"total"`
	Period  string            `json:"period"`
	GroupBy string            `json:"group_by"`
	Filters map[string]string `json:"filters"`
}

type costTotal struct {
	Cost               float64 `json:"cost"`
	InfrastructureCost float64 `json:"infrastructure_cost"`
	SupplementaryCost  float64 `json:"supplementary_cost"`
	Currency           string  `json:"currency"`
}

func (h *Handler) handleCostReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	q := r.URL.Query()
	tenantID := q.Get("tenant_id")
	resourceType := q.Get("resource_type")
	groupBy := q.Get("group_by")
	if groupBy == "" {
		groupBy = "tenant"
	}
	period := q.Get("period")
	if period == "" {
		period = time.Now().UTC().Format("2006-01")
	}

	periodStart, err := time.Parse("2006-01", period)
	if err != nil {
		writeErrorJSON(w, "invalid period format, use YYYY-MM", http.StatusBadRequest)
		return
	}
	periodEnd := periodStart.AddDate(0, 1, 0)

	ctx := r.Context()
	rows, err := h.store.CostReport(ctx, tenantID, resourceType, groupBy, periodStart, periodEnd)
	if err != nil {
		h.logger.Error("cost report query failed", "error", err)
		writeErrorJSON(w, "report query failed", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []inventory.CostReportRow{}
	}

	var total costTotal
	total.Currency = "USD"
	for _, row := range rows {
		total.Cost += row.Cost
		total.InfrastructureCost += row.InfrastructureCost
		total.SupplementaryCost += row.SupplementaryCost
	}

	filters := map[string]string{}
	if tenantID != "" {
		filters["tenant_id"] = tenantID
	}
	if resourceType != "" {
		filters["resource_type"] = resourceType
	}

	format := q.Get("format")
	if format == "" && r.Header.Get("Accept") == "text/csv" {
		format = "csv"
	}

	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=costs.csv")
		fmt.Fprintln(w, "group,entries,cost,infrastructure_cost,supplementary_cost,currency")
		for _, row := range rows {
			fmt.Fprintf(w, "%s,%d,%.6f,%.6f,%.6f,%s\n",
				row.Group, row.Entries, row.Cost, row.InfrastructureCost, row.SupplementaryCost, row.Currency)
		}
		return
	}

	writeJSON(w, costReportResponse{
		Meta: costReportMeta{
			Total:   total,
			Period:  period,
			GroupBy: groupBy,
			Filters: filters,
		},
		Data: rows,
	})
}

func (h *Handler) handlePipelineSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx := r.Context()
	summary, err := h.store.PipelineSummary(ctx)
	if err != nil {
		h.logger.Error("pipeline summary query failed", "error", err)
		writeErrorJSON(w, "summary query failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, summary)
}

// ── Balance Check (IPP compatibility) ──
// GET /api/v1/customers/{customerID}/entitlements/{featureKey}/value?model={model}
//
// Response format matches the entitlementValue struct from the IPP external-metering plugin.
// Source: https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go

type entitlementValue struct {
	HasAccess bool    `json:"hasAccess"`
	Balance   float64 `json:"balance"`
	Usage     float64 `json:"usage"`
	Overage   float64 `json:"overage"`
}

func (h *Handler) handleBalanceCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/customers/")
	parts := strings.Split(path, "/")
	// Expect: {customerID}/entitlements/{featureKey}/value
	if len(parts) < 4 || parts[1] != "entitlements" || parts[3] != "value" {
		writeErrorJSON(w, "expected /api/v1/customers/{id}/entitlements/{key}/value", http.StatusBadRequest)
		return
	}

	customerID := parts[0]
	featureKey := parts[2]
	_ = featureKey // available for future feature-scoped quotas

	ctx := r.Context()
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)

	quotas, err := h.store.QuotasForTenant(ctx, customerID, now)
	if err != nil || len(quotas) == 0 {
		writeJSON(w, entitlementValue{HasAccess: true, Balance: math.MaxFloat64})
		return
	}

	totalLimit := 0.0
	totalUsage := 0.0
	for _, q := range quotas {
		consumed, err := h.store.MeteringSum(ctx, customerID, q.MeterName, periodStart, periodEnd)
		if err != nil {
			continue
		}
		totalLimit += q.LimitValue
		totalUsage += consumed
	}

	balance := totalLimit - totalUsage
	overage := 0.0
	if balance < 0 {
		overage = -balance
		balance = 0
	}

	writeJSON(w, entitlementValue{
		HasAccess: totalUsage < totalLimit,
		Balance:   balance,
		Usage:     totalUsage,
		Overage:   overage,
	})
}

func (h *Handler) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if h.cfg == nil {
		writeJSON(w, map[string]string{"error": "config not available"})
		return
	}
	writeJSON(w, h.cfg.Diagnostics())
}

func (h *Handler) handleDebugDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

func (h *Handler) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if h.reconciler == nil {
		writeErrorJSON(w, "reconciler not configured", http.StatusServiceUnavailable)
		return
	}
	if !h.reconciling.CompareAndSwap(false, true) {
		writeErrorJSON(w, "reconciliation already in progress", http.StatusTooManyRequests)
		return
	}
	go func() {
		defer h.reconciling.Store(false)
		h.reconciler.ReconcileAll(context.Background())
	}()
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"status": "reconciliation triggered"})
}

func (h *Handler) handleLiveness(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.store.Pool().Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]string{"status": "not_ready", "error": "database unreachable"})
		return
	}
	w.WriteHeader(http.StatusOK)
	writeJSON(w, map[string]string{"status": "ready"})
}
