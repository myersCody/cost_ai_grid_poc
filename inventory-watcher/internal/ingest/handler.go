package ingest

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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

// ServeMux returns an HTTP mux with the ingest endpoints.
func (h *Handler) ServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/events", h.handleEvent)
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
