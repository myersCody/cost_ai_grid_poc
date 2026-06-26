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

// MaaSCloudEvent matches the proposed CloudEvents schema for MaaS events.
type MaaSCloudEvent struct {
	SpecVersion     string       `json:"specversion"`
	Type            string       `json:"type"`
	Source          string       `json:"source"`
	ID              string       `json:"id"`
	Time            time.Time    `json:"time"`
	Subject         string       `json:"subject"`
	DataContentType string       `json:"datacontenttype"`
	Data            MaaSEventData `json:"data"`
}

type MaaSEventData struct {
	TenantID        string `json:"tenant_id"`
	ModelID         string `json:"model_id"`
	ModelName       string `json:"model_name"`
	Template        string `json:"template"`
	State           string `json:"state"`
	TokensIn        int64  `json:"tokens_in"`
	TokensOut       int64  `json:"tokens_out"`
	InferenceTokens int64  `json:"inference_tokens"`
	Requests        int64  `json:"requests"`
	DurationSeconds int    `json:"duration_seconds"`
}

type Handler struct {
	store  *inventory.Store
	meter  *metering.Meter
	logger *slog.Logger
}

func NewHandler(store *inventory.Store, meter *metering.Meter, logger *slog.Logger) *Handler {
	return &Handler{store: store, meter: meter, logger: logger}
}

// ServeMux returns an HTTP mux with the ingest and query endpoints.
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
	var ce MaaSCloudEvent
	if err := json.NewDecoder(r.Body).Decode(&ce); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	dataJSON, _ := json.Marshal(ce)
	inserted, err := h.store.InsertRawEvent(ctx, inventory.RawEvent{
		EventID:      ce.ID,
		EventType:    ce.Type,
		EventSource:  ce.Source,
		EventTime:    ce.Time,
		TenantID:     ce.Data.TenantID,
		ResourceType: "Model",
		ResourceID:   ce.Data.ModelID,
		Data:         dataJSON,
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

	createdAt := ce.Time.Add(-time.Duration(ce.Data.DurationSeconds) * time.Second)
	_ = h.store.UpsertModel(ctx, inventory.ModelRecord{
		ModelID:     ce.Data.ModelID,
		Name:        ce.Data.ModelName,
		ModelName:   ce.Data.ModelName,
		Tenant:      ce.Data.TenantID,
		Template:    ce.Data.Template,
		State:       ce.Data.State,
		CreatedAt:   createdAt,
		LastEventID: ce.ID,
	})

	h.meter.MeterMaaSEvent(ctx, metering.MaaSUsage{
		ModelID:         ce.Data.ModelID,
		ModelName:       ce.Data.ModelName,
		TenantID:        ce.Data.TenantID,
		State:           ce.Data.State,
		TokensIn:        ce.Data.TokensIn,
		TokensOut:       ce.Data.TokensOut,
		InferenceTokens: ce.Data.InferenceTokens,
		Requests:        ce.Data.Requests,
		EventTime:       ce.Time,
		DurationSeconds: float64(ce.Data.DurationSeconds),
	})

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintln(w, `{"status":"accepted"}`)
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

		statuses = append(statuses, inventory.QuotaStatus{
			MeterName:  q.MeterName,
			Unit:       q.Unit,
			Limit:      q.LimitValue,
			Consumed:   consumed,
			Percentage: math.Round(pct*100) / 100,
			Thresholds: thresholds,
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
