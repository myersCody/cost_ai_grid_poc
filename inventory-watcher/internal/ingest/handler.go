package ingest

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metering"
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

// VMaaS heartbeat event data (from osac-metering-discover-poc collect.sh).
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

// CaaS heartbeat event data (from osac-metering-discover-poc collect-caas.sh).
type ClusterEventData struct {
	DurationSeconds    int    `json:"duration_seconds"`
	WorkerNodeSeconds  int64  `json:"worker_node_seconds"`
	NodeCount          int32  `json:"node_count"`
	TenantID           string `json:"tenant_id"`
	ClusterID          string `json:"cluster_id"`
	Template           string `json:"template"`
	State              string `json:"state"`
	HostType           string `json:"host_type"`
}

// MaaS event data (proposed — OSAC doesn't emit these yet).
type MaaSEventData struct {
	TenantID        string `json:"tenant_id"`
	ModelID         string `json:"model_id"`
	ModelName       string `json:"model_name"`
	Template        string `json:"template"`
	State           string `json:"state"`
	TokensIn        int64  `json:"tokens_in"`
	TokensOut       int64  `json:"tokens_out"`
	Requests        int64  `json:"requests"`
	DurationSeconds int    `json:"duration_seconds"`
}

const (
	EventTypeComputeInstance = "osac.compute_instance.lifecycle"
	EventTypeCluster         = "osac.cluster.lifecycle"
	EventTypeModel           = "osac.model.lifecycle"
)

type Handler struct {
	store  *inventory.Store
	meter  *metering.Meter
	logger *slog.Logger
}

func NewHandler(store *inventory.Store, meter *metering.Meter, logger *slog.Logger) *Handler {
	return &Handler{store: store, meter: meter, logger: logger}
}

func (h *Handler) ServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/events", h.handleEvent)
	mux.HandleFunc("GET /api/v1/quotas/", h.handleQuotaStatus)
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	return mux
}

func (h *Handler) handleEvent(w http.ResponseWriter, r *http.Request) {
	var ce CloudEvent
	if err := json.NewDecoder(r.Body).Decode(&ce); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	resourceType, resourceID, tenantID := classifyEvent(ce)

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
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if !inserted {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintln(w, `{"status":"duplicate"}`)
		return
	}

	switch ce.Type {
	case EventTypeComputeInstance:
		h.handleComputeInstanceEvent(ctx, ce)
	case EventTypeCluster:
		h.handleClusterEvent(ctx, ce)
	case EventTypeModel:
		h.handleModelEvent(ctx, ce)
	default:
		h.logger.Warn("unknown CloudEvent type", "type", ce.Type)
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintln(w, `{"status":"accepted"}`)
}

func (h *Handler) handleComputeInstanceEvent(ctx interface{ Deadline() (time.Time, bool); Done() <-chan struct{}; Err() error; Value(any) any }, ce CloudEvent) {
	var data ComputeInstanceEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		h.logger.Error("failed to parse compute instance event data", "error", err)
		return
	}

	if !metering.IsComputeInstanceBillable(data.State) {
		return
	}

	_ = h.store.UpsertComputeInstance(ctx, inventory.ComputeInstanceRecord{
		InstanceID:  data.InstanceID,
		Tenant:      data.TenantID,
		Cores:       data.Cores,
		MemoryGiB:   data.MemoryGiB,
		State:       data.State,
		CreatedAt:   ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second),
		LastEventID: ce.ID,
	})

	periodStart := ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second)

	entries := []inventory.MeteringEntry{
		{
			ResourceType: "compute_instance",
			ResourceID:   data.InstanceID,
			TenantID:     data.TenantID,
			MeterName:    "vm_uptime_seconds",
			Value:        float64(data.DurationSeconds),
			Unit:         "seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    ce.Time,
		},
		{
			ResourceType: "compute_instance",
			ResourceID:   data.InstanceID,
			TenantID:     data.TenantID,
			MeterName:    "vm_cpu_core_seconds",
			Value:        float64(data.CPUCoreSeconds),
			Unit:         "core_seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    ce.Time,
		},
		{
			ResourceType: "compute_instance",
			ResourceID:   data.InstanceID,
			TenantID:     data.TenantID,
			MeterName:    "vm_memory_gib_seconds",
			Value:        float64(data.MemoryGiBSeconds),
			Unit:         "gib_seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    ce.Time,
		},
	}

	for _, entry := range entries {
		if err := h.store.InsertMeteringEntry(ctx, entry); err != nil {
			h.logger.Error("failed to insert VM metering entry", "error", err)
		}
	}

	_ = h.store.UpdateComputeInstanceLastMetered(ctx, data.InstanceID, ce.Time)

	h.logger.Debug("ingested VM heartbeat", "instance", data.InstanceID,
		"cores", data.Cores, "duration", data.DurationSeconds)
}

func (h *Handler) handleClusterEvent(ctx interface{ Deadline() (time.Time, bool); Done() <-chan struct{}; Err() error; Value(any) any }, ce CloudEvent) {
	var data ClusterEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		h.logger.Error("failed to parse cluster event data", "error", err)
		return
	}

	if !metering.IsClusterBillable(data.State) {
		return
	}

	periodStart := ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second)

	var entries []inventory.MeteringEntry

	if data.HostType == "_control_plane" {
		entries = append(entries, inventory.MeteringEntry{
			ResourceType: "cluster",
			ResourceID:   data.ClusterID,
			TenantID:     data.TenantID,
			MeterName:    "cluster_uptime_seconds",
			Value:        float64(data.DurationSeconds),
			Unit:         "seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    ce.Time,
		})
	}

	if data.WorkerNodeSeconds > 0 {
		entries = append(entries, inventory.MeteringEntry{
			ResourceType: "cluster",
			ResourceID:   data.ClusterID,
			TenantID:     data.TenantID,
			MeterName:    "cluster_worker_node_seconds",
			Value:        float64(data.WorkerNodeSeconds),
			Unit:         "node_seconds",
			PeriodStart:  periodStart,
			PeriodEnd:    ce.Time,
		})
	}

	for _, entry := range entries {
		if err := h.store.InsertMeteringEntry(ctx, entry); err != nil {
			h.logger.Error("failed to insert cluster metering entry", "error", err)
		}
	}

	_ = h.store.UpdateClusterLastMetered(ctx, data.ClusterID, ce.Time)

	h.logger.Debug("ingested cluster heartbeat", "cluster", data.ClusterID,
		"host_type", data.HostType, "duration", data.DurationSeconds)
}

func (h *Handler) handleModelEvent(ctx interface{ Deadline() (time.Time, bool); Done() <-chan struct{}; Err() error; Value(any) any }, ce CloudEvent) {
	var data MaaSEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		h.logger.Error("failed to parse model event data", "error", err)
		return
	}

	createdAt := ce.Time.Add(-time.Duration(data.DurationSeconds) * time.Second)
	_ = h.store.UpsertModel(ctx, inventory.ModelRecord{
		ModelID:     data.ModelID,
		Name:        data.ModelName,
		ModelName:   data.ModelName,
		Tenant:      data.TenantID,
		Template:    data.Template,
		State:       data.State,
		CreatedAt:   createdAt,
		LastEventID: ce.ID,
	})

	h.meter.MeterMaaSEvent(ctx, metering.MaaSUsage{
		ModelID:         data.ModelID,
		ModelName:       data.ModelName,
		TenantID:        data.TenantID,
		State:           data.State,
		TokensIn:        data.TokensIn,
		TokensOut:       data.TokensOut,
		Requests:        data.Requests,
		EventTime:       ce.Time,
		DurationSeconds: float64(data.DurationSeconds),
	})
}

func classifyEvent(ce CloudEvent) (resourceType, resourceID, tenantID string) {
	var peek struct {
		TenantID   string `json:"tenant_id"`
		InstanceID string `json:"instance_id"`
		ClusterID  string `json:"cluster_id"`
		ModelID    string `json:"model_id"`
	}
	_ = json.Unmarshal(ce.Data, &peek)

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
	default:
		return ce.Type, "", tenantID
	}
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
		http.Error(w, `{"error":"tenant_id required"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)
	periodLabel := now.Format("2006-01")

	quotas, err := h.store.QuotasForTenant(ctx, tenantID, now)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
