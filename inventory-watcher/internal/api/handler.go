package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/osac-project/cost-event-consumer/internal/billing"
	"github.com/osac-project/cost-event-consumer/internal/config"
	"github.com/osac-project/cost-event-consumer/internal/custommetrics"
	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metering"
	"github.com/osac-project/cost-event-consumer/internal/metrics"
	"github.com/osac-project/cost-event-consumer/internal/rating"
)

// Compile-time check that Handler implements ServerInterface.
var _ ServerInterface = (*APIHandler)(nil)

const (
	// VMaaS/CaaS event types from OSAC metering collector.
	eventTypeComputeInstance = "osac.compute_instance.lifecycle"
	eventTypeCluster         = "osac.cluster.lifecycle"
	eventTypeModel           = "osac.model.lifecycle"
	eventTypeInferenceTokens = "inference.tokens.used"

	// OSAC metering-service v1 event types (OSAC-985).
	eventTypeResourceCreated   = "osac.resource.created.v1"
	eventTypeResourceDeleted   = "osac.resource.deleted.v1"
	eventTypeResourceStarted   = "osac.resource.started.v1"
	eventTypeResourceSuspended = "osac.resource.suspended.v1"
	eventTypeResourceResumed   = "osac.resource.resumed.v1"
	eventTypeResourceUpdated   = "osac.resource.updated.v1"
	eventTypeResourceHeartbeat = "osac.resource.heartbeat.v1"
	eventTypeInferenceUsage    = "osac.inference.usage.v1"

	maxRequestBodySize = 1 << 20 // 1MB
	maxIDLength        = 256

	// Ingest timestamp validation window.
	// Events outside this window are rejected to prevent backdating attacks —
	// a client setting event_time to a past billing period could inject data
	// into closed quotas, cost history, and tier waterfall calculations.
	maxEventAge    = 2 * time.Hour   // reject events older than 2 hours
	maxEventFuture = 5 * time.Minute // reject events more than 5 min in the future

	// Events within the acceptance window but beyond this drift threshold are
	// accepted but counted as drifted. Sustained drift warns of a misconfigured
	// source clock that could still skew cumulative tier calculations.
	warnEventDrift = 30 * time.Second
)

// Reconciler triggers a full OSAC reconciliation cycle.
type Reconciler interface {
	ReconcileAll(ctx context.Context)
}

// Handler implements the generated ServerInterface with all API business logic.
type APIHandler struct {
	store         *inventory.Store
	meter         *metering.Meter
	cfg           *config.Config
	customMetrics *custommetrics.Registry
	reconciler    Reconciler
	reconciling   atomic.Bool
	logger        *slog.Logger
}

// NewHandler constructs a Handler with all required dependencies.
func NewAPIHandler(store *inventory.Store, meter *metering.Meter, cfg *config.Config, customMetrics *custommetrics.Registry, logger *slog.Logger) *APIHandler {
	return &APIHandler{
		store:         store,
		meter:         meter,
		cfg:           cfg,
		customMetrics: customMetrics,
		logger:        logger,
	}
}

// ProcessKafkaEvent implements kafka.EventProcessor. It decodes a CloudEvent
// from Kafka and runs the same processing pipeline as the HTTP ingest handler.
func (h *APIHandler) ProcessKafkaEvent(ctx context.Context, topic string, payload []byte) error {
	var ce cloudEventInternal
	if err := json.Unmarshal(payload, &ce); err != nil {
		return fmt.Errorf("kafka: invalid CloudEvent JSON: %w", err)
	}
	if err := h.processEvents(ctx, []cloudEventInternal{ce}); err != nil {
		return fmt.Errorf("kafka: process CloudEvent: %w", err)
	}
	return nil
}

// SetReconciler sets the reconciler for on-demand reconciliation triggers.
func (h *APIHandler) SetReconciler(r Reconciler) {
	h.reconciler = r
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErrorJSON(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// CsvSafe escapes a string value for safe CSV output (prevents formula injection).
func CsvSafe(s string) string {
	if len(s) > 0 && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@') {
		return "'" + s
	}
	if strings.ContainsAny(s, ",\"\n") {
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}

func isBudget(unit string) bool {
	switch unit {
	case "USD", "EUR", "GBP", "JPY", "CNY", "CHF", "CAD", "AUD":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Health probes
// ---------------------------------------------------------------------------

// GetLiveness implements ServerInterface.
func (h *APIHandler) GetLiveness(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	writeJSON(w, map[string]string{"status": "ok"})
}

// GetReadiness implements ServerInterface.
func (h *APIHandler) GetReadiness(w http.ResponseWriter, r *http.Request) {
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

// ---------------------------------------------------------------------------
// Event ingestion
// ---------------------------------------------------------------------------

// cloudEventInternal is a generic CloudEvents 1.0 envelope for internal
// decoding. The Data field is decoded separately based on the Type.
type cloudEventInternal struct {
	SpecVersion     string          `json:"specversion"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	ID              string          `json:"id"`
	Time            time.Time       `json:"time"`
	Subject         string          `json:"subject"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`

	// OSAC metering-service v1 CloudEvent extensions (structured content mode).
	OSACResourceID   string `json:"osacresourceid,omitempty"`
	OSACResourceType string `json:"osacresourcetype,omitempty"`
	OSACTenant       string `json:"osactenant,omitempty"`
	OSACProject      string `json:"osacproject,omitempty"`
	OSACTrace        string `json:"osactrace,omitempty"`
}

// meteringData is the payload from the OSAC metering-service v1.
type meteringData struct {
	ResourceID        string          `json:"resource_id"`
	ResourceType      string          `json:"resource_type"`
	TenantID          string          `json:"tenant_id"`
	ProjectID         *string         `json:"project_id"`
	CatalogItemID     *string         `json:"catalog_item_id"`
	TemplateID        *string         `json:"template_id"`
	PreviousState     *string         `json:"previous_state"`
	CurrentState      string          `json:"current_state"`
	TransitionTime    string          `json:"transition_time"`
	DurationSeconds   *float64        `json:"duration_seconds"`
	BillingDimensions json.RawMessage `json:"billing_dimensions"`
	SchemaVersion     string          `json:"schema_version"`
}

// vmBillingDimensions holds compute_instance billing dimensions.
type vmBillingDimensions struct {
	InstanceType    *string `json:"instance_type"`
	ImageRef        *string `json:"image_ref"`
	BootDiskSizeGiB *int32  `json:"boot_disk_size_gib"`
}

// clusterBillingDimensions holds cluster_order billing dimensions.
type clusterBillingDimensions struct {
	ClusterTemplate string `json:"cluster_template"`
	ReleaseImage    string `json:"release_image"`
	Component       string `json:"component"`
	HostType        string `json:"host_type"`
	NodeCount       int32  `json:"node_count"`
}

// maasBillingDimensions holds maas_inference billing dimensions.
type maasBillingDimensions struct {
	OrganizationID    string `json:"organization_id"`
	CostCenter        string `json:"cost_center"`
	Subscription      string `json:"subscription"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	PromptTokens      int64  `json:"prompt_tokens"`
	CompletionTokens  int64  `json:"completion_tokens"`
	TotalTokens       int64  `json:"total_tokens"`
	CachedInputTokens int64  `json:"cached_input_tokens"`
	ReasoningTokens   int64  `json:"reasoning_tokens"`
	DurationMs        int64  `json:"duration_ms"`
}

// computeInstanceEventData matches the OSAC metering collector VMaaS schema.
type computeInstanceEventData struct {
	DurationSeconds  float64 `json:"duration_seconds"`
	CPUCoreSeconds   int64   `json:"cpu_core_seconds"`
	MemoryGiBSeconds int64   `json:"memory_gib_seconds"`
	TenantID         string  `json:"tenant_id"`
	InstanceID       string  `json:"instance_id"`
	Template         string  `json:"template"`
	CatalogItem      string  `json:"catalog_item"`
	State            string  `json:"state"`
	Cores            int32   `json:"cores"`
	MemoryGiB        int32   `json:"memory_gib"`
}

// clusterEventData matches the OSAC metering collector CaaS schema.
type clusterEventData struct {
	DurationSeconds   float64 `json:"duration_seconds"`
	WorkerNodeSeconds int64   `json:"worker_node_seconds"`
	NodeCount         int32   `json:"node_count"`
	TenantID          string  `json:"tenant_id"`
	ClusterID         string  `json:"cluster_id"`
	Template          string  `json:"template"`
	State             string  `json:"state"`
	HostType          string  `json:"host_type"`
}

// maaSEventData accepts both legacy mock format and the real IPP external-metering plugin format.
type maaSEventData struct {
	TenantID            string  `json:"tenant_id"`
	ModelID             string  `json:"model_id"`
	ModelName           string  `json:"model_name"`
	Template            string  `json:"template"`
	State               string  `json:"state"`
	TokensIn            int64   `json:"tokens_in"`
	TokensOut           int64   `json:"tokens_out"`
	Requests            int64   `json:"requests"`
	DurationSeconds     float64 `json:"duration_seconds"`
	RequestCount        int64   `json:"request_count"`
	User                string  `json:"user"`
	Group               string  `json:"group"`
	Subscription        string  `json:"subscription"`
	OrganizationID      string  `json:"organization_id"`
	CostCenter          string  `json:"cost_center"`
	Provider            string  `json:"provider"`
	Model               string  `json:"model"`
	PromptTokens        int64   `json:"prompt_tokens"`
	CompletionTokens    int64   `json:"completion_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	CachedInputTokens   int64   `json:"cached_input_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens"`
	DurationMs          int64   `json:"duration_ms"`
}

const maxBatchEvents = 100

type eventBatch struct {
	Events []cloudEventInternal `json:"events"`
}

type eventValidationError struct{ message string }

func (e *eventValidationError) Error() string { return e.message }

// IngestEvent accepts the legacy single-event form. It intentionally uses the
// same receipt-protected processing path as batch delivery.
func (h *APIHandler) IngestEvent(w http.ResponseWriter, r *http.Request) {
	var ce cloudEventInternal
	if err := decodeJSONBody(w, r, &ce); err != nil {
		writeEventError(w, err)
		return
	}
	if err := h.processEvents(r.Context(), []cloudEventInternal{ce}); err != nil {
		writeEventError(w, err)
		return
	}
	metrics.EventsProcessedTotal.WithLabelValues(ce.Type, "accepted").Inc()
	w.WriteHeader(http.StatusNoContent)
}

// IngestEventBatch accepts the Cost Management adapter's durable delivery
// unit. Every member is validated before transaction work begins; all receipt,
// raw-event, inventory, and event-driven metering writes commit or roll back
// together.
func (h *APIHandler) IngestEventBatch(w http.ResponseWriter, r *http.Request) {
	var batch eventBatch
	if err := decodeJSONBody(w, r, &batch); err != nil {
		writeEventError(w, err)
		return
	}
	if len(batch.Events) == 0 || len(batch.Events) > maxBatchEvents {
		writeEventError(w, &eventValidationError{message: fmt.Sprintf("events must contain between 1 and %d items", maxBatchEvents)})
		return
	}
	if err := h.processEvents(r.Context(), batch.Events); err != nil {
		writeEventError(w, err)
		return
	}
	for _, ce := range batch.Events {
		metrics.EventsProcessedTotal.WithLabelValues(ce.Type, "accepted").Inc()
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("request body exceeds %d bytes: %w", maxRequestBodySize, err)
		}
		return &eventValidationError{message: "invalid JSON: " + err.Error()}
	}
	return nil
}

func writeEventError(w http.ResponseWriter, err error) {
	var invalid *eventValidationError
	switch {
	case errors.Is(err, inventory.ErrReceiptCollision):
		writeErrorJSON(w, "event identity was previously used for different content", http.StatusConflict)
	case errors.As(err, &invalid):
		writeErrorJSON(w, invalid.Error(), http.StatusBadRequest)
	case errors.As(err, new(*http.MaxBytesError)):
		writeErrorJSON(w, "request body too large", http.StatusRequestEntityTooLarge)
	default:
		writeErrorJSON(w, "failed to process events", http.StatusInternalServerError)
	}
}

func (h *APIHandler) processEvents(ctx context.Context, events []cloudEventInternal) error {
	for _, ce := range events {
		if err := h.validateCloudEvent(ce); err != nil {
			return err
		}
	}

	if err := h.store.InTransaction(ctx, func(txStore *inventory.Store) error {
		txHandler := &APIHandler{
			store:         txStore,
			meter:         h.meter,
			cfg:           h.cfg,
			customMetrics: h.customMetrics,
			logger:        h.logger,
		}
		if h.meter != nil {
			txHandler.meter = h.meter.WithStore(txStore)
		}
		for _, ce := range events {
			digest, err := cloudEventDigest(ce)
			if err != nil {
				return fmt.Errorf("digest event %s: %w", ce.ID, err)
			}
			claimed, err := txStore.ClaimIngestionReceipt(ctx, ce.Source, ce.ID, digest)
			if err != nil {
				return err
			}
			if !claimed {
				continue
			}
			if err := txHandler.processEvent(ctx, ce); err != nil {
				return fmt.Errorf("process event %s: %w", ce.ID, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func cloudEventDigest(ce cloudEventInternal) (string, error) {
	payload, err := json.Marshal(ce)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:]), nil
}

func (h *APIHandler) validateCloudEvent(ce cloudEventInternal) error {
	if ce.SpecVersion != "1.0" {
		return &eventValidationError{message: "specversion must be 1.0"}
	}
	if ce.ID == "" || ce.Type == "" || ce.Source == "" || ce.Time.IsZero() {
		return &eventValidationError{message: "specversion, id, type, source, and time are required"}
	}
	if len(ce.Data) == 0 || string(ce.Data) == "null" {
		return &eventValidationError{message: "event data is required"}
	}

	now := time.Now().UTC()
	age := now.Sub(ce.Time.UTC())
	if age > maxEventAge {
		metrics.EventsRejectedTotal.WithLabelValues("timestamp_too_old").Inc()
		return &eventValidationError{message: fmt.Sprintf("event time is too old (%.0f minutes ago; max %s)", age.Minutes(), maxEventAge)}
	}
	if age < -maxEventFuture {
		metrics.EventsRejectedTotal.WithLabelValues("timestamp_too_future").Inc()
		return &eventValidationError{message: fmt.Sprintf("event time is too far in the future (%s; max %s)", (-age).Round(time.Second), maxEventFuture)}
	}
	if age > warnEventDrift {
		metrics.EventsTimestampDriftTotal.WithLabelValues("past").Inc()
	} else if age < -warnEventDrift {
		metrics.EventsTimestampDriftTotal.WithLabelValues("future").Inc()
	}

	resourceType, resourceID, tenantID := classifyEvent(ce)
	if (resourceID == "" || tenantID == "") && h.customMetrics != nil && h.customMetrics.HasEventType(ce.Type) {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(ce.Data, &dataMap); err == nil {
			resourceType, resourceID, tenantID = h.customMetrics.ClassifyEvent(ce.Type, dataMap)
		}
	}
	if resourceType == "" || resourceID == "" || tenantID == "" {
		return &eventValidationError{message: "event data must include resource_id and tenant_id"}
	}
	if len(resourceID) > maxIDLength || len(tenantID) > maxIDLength {
		return &eventValidationError{message: "resource_id or tenant_id exceeds maximum length"}
	}
	if isOSACv1EventType(ce.Type) || ce.Type == eventTypeInferenceUsage {
		if ce.OSACResourceID == "" || ce.OSACResourceType == "" || ce.OSACTenant == "" {
			return &eventValidationError{message: "OSAC v1 event is missing required resource extensions"}
		}
		var data meteringData
		if err := json.Unmarshal(ce.Data, &data); err != nil {
			return &eventValidationError{message: "invalid OSAC v1 event data"}
		}
		if data.ResourceID == "" || data.ResourceType == "" || data.TenantID == "" ||
			data.ResourceID != ce.OSACResourceID || data.ResourceType != ce.OSACResourceType || data.TenantID != ce.OSACTenant {
			return &eventValidationError{message: "OSAC v1 data identity must match CloudEvent extensions"}
		}
	}
	return nil
}

func (h *APIHandler) processEvent(ctx context.Context, ce cloudEventInternal) error {
	resourceType, resourceID, tenantID := classifyEvent(ce)
	if (resourceID == "" || tenantID == "") && h.customMetrics != nil && h.customMetrics.HasEventType(ce.Type) {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(ce.Data, &dataMap); err == nil {
			resourceType, resourceID, tenantID = h.customMetrics.ClassifyEvent(ce.Type, dataMap)
		}
	}
	fullJSON, err := json.Marshal(ce)
	if err != nil {
		return fmt.Errorf("marshal CloudEvent: %w", err)
	}
	if _, err := h.store.InsertRawEvent(ctx, inventory.RawEvent{
		EventID: ce.ID, EventType: ce.Type, EventSource: ce.Source, EventTime: ce.Time,
		TenantID: tenantID, ResourceType: resourceType, ResourceID: resourceID, Data: fullJSON,
	}); err != nil {
		return err
	}

	switch {
	case isOSACv1EventType(ce.Type) || ce.Type == eventTypeInferenceUsage:
		return h.processOSACResourceEvent(ctx, ce)
	case ce.Type == eventTypeComputeInstance:
		return h.processComputeInstanceEvent(ctx, ce)
	case ce.Type == eventTypeCluster:
		return h.processClusterEvent(ctx, ce)
	case ce.Type == eventTypeModel || ce.Type == eventTypeInferenceTokens:
		return h.processModelEvent(ctx, ce)
	case h.customMetrics != nil && h.customMetrics.HasEventType(ce.Type):
		return h.customMetrics.ProcessEvent(ctx, h.store, ce.Type, ce.Data, ce.Time, h.logger)
	default:
		h.logger.Warn("unknown CloudEvent type", "type", ce.Type)
		return nil
	}
}

func isOSACv1EventType(t string) bool {
	switch t {
	case eventTypeResourceCreated, eventTypeResourceDeleted,
		eventTypeResourceStarted, eventTypeResourceSuspended,
		eventTypeResourceResumed, eventTypeResourceUpdated,
		eventTypeResourceHeartbeat:
		return true
	}
	return false
}

func classifyEvent(ce cloudEventInternal) (resourceType, resourceID, tenantID string) {
	if isOSACv1EventType(ce.Type) || ce.Type == eventTypeInferenceUsage {
		rt := ce.OSACResourceType
		if rt == "" {
			rt = ce.Type
		}
		return rt, ce.OSACResourceID, ce.OSACTenant
	}

	var peek struct {
		TenantID       string `json:"tenant_id"`
		OrganizationID string `json:"organization_id"`
		InstanceID     string `json:"instance_id"`
		ClusterID      string `json:"cluster_id"`
		ModelID        string `json:"model_id"`
		User           string `json:"user"`
		Model          string `json:"model"`
	}
	if err := json.Unmarshal(ce.Data, &peek); err != nil {
		return ce.Type, "", ce.Subject
	}

	tenantID = peek.TenantID
	if tenantID == "" {
		tenantID = peek.OrganizationID
	}
	if tenantID == "" {
		tenantID = ce.Subject
	}

	switch ce.Type {
	case eventTypeComputeInstance:
		return "ComputeInstance", peek.InstanceID, tenantID
	case eventTypeCluster:
		return "Cluster", peek.ClusterID, tenantID
	case eventTypeModel:
		return "Model", peek.ModelID, tenantID
	case eventTypeInferenceTokens:
		rid := peek.ModelID
		if rid == "" {
			rid = peek.Model
		}
		return "Model", rid, tenantID
	default:
		return ce.Type, "", tenantID
	}
}

func (h *APIHandler) processComputeInstanceEvent(ctx context.Context, ce cloudEventInternal) error {
	var data computeInstanceEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		return err
	}

	if !metering.IsComputeInstanceBillable(data.State) {
		return nil
	}

	if data.DurationSeconds <= 0 {
		return fmt.Errorf("invalid duration_seconds: %g (must be positive)", data.DurationSeconds)
	}

	if err := h.store.UpsertComputeInstance(ctx, inventory.ComputeInstanceRecord{
		InstanceID:  data.InstanceID,
		Tenant:      data.TenantID,
		Cores:       data.Cores,
		MemoryGiB:   data.MemoryGiB,
		State:       data.State,
		CreatedAt:   ce.Time.Add(-time.Duration(data.DurationSeconds * float64(time.Second))),
		LastEventID: ce.ID,
	}); err != nil {
		return err
	}

	periodStart := ce.Time.Add(-time.Duration(data.DurationSeconds * float64(time.Second)))

	entries := []inventory.MeteringEntry{
		{ResourceType: "compute_instance", ResourceID: data.InstanceID, TenantID: data.TenantID, MeterName: "vm_uptime_seconds", Value: data.DurationSeconds, Unit: "seconds", PeriodStart: periodStart, PeriodEnd: ce.Time},
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

func (h *APIHandler) processClusterEvent(ctx context.Context, ce cloudEventInternal) error {
	var data clusterEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		return err
	}

	if !metering.IsClusterBillable(data.State) {
		return nil
	}

	if data.DurationSeconds <= 0 {
		return fmt.Errorf("invalid duration_seconds: %g (must be positive)", data.DurationSeconds)
	}

	periodStart := ce.Time.Add(-time.Duration(data.DurationSeconds * float64(time.Second)))

	var entries []inventory.MeteringEntry

	if data.HostType == "_control_plane" {
		entries = append(entries, inventory.MeteringEntry{ResourceType: "cluster", ResourceID: data.ClusterID, TenantID: data.TenantID, MeterName: "cluster_uptime_seconds", Value: data.DurationSeconds, Unit: "seconds", PeriodStart: periodStart, PeriodEnd: ce.Time})
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

func (h *APIHandler) processModelEvent(ctx context.Context, ce cloudEventInternal) error {
	var data maaSEventData
	if err := json.Unmarshal(ce.Data, &data); err != nil {
		return err
	}

	// Normalize IPP format to internal format
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
	if data.TenantID == "" && data.OrganizationID != "" {
		data.TenantID = data.OrganizationID
	}
	if data.TenantID == "" && data.Subscription != "" {
		if idx := strings.Index(data.Subscription, "/"); idx > 0 {
			ns := data.Subscription[:idx]
			data.TenantID = strings.TrimPrefix(ns, "ai-tenant-")
		}
	}
	if data.TenantID == "" && data.Group != "" {
		data.TenantID = data.Group
	}
	if data.TenantID == "" && data.User != "" {
		data.TenantID = data.User
	}
	if data.DurationSeconds < 0 {
		return fmt.Errorf("invalid duration_seconds: %g (must be non-negative)", data.DurationSeconds)
	}
	if data.DurationMs > 0 && data.DurationSeconds == 0 {
		data.DurationSeconds = float64(data.DurationMs) / 1000.0
	}
	if data.RequestCount > 0 && data.Requests == 0 {
		data.Requests = data.RequestCount
	}
	if data.State == "" {
		data.State = "MODEL_STATE_RUNNING"
	}

	createdAt := ce.Time.Add(-time.Duration(data.DurationSeconds * float64(time.Second)))
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

	if err := h.meter.MeterMaaSEvent(ctx, metering.MaaSUsage{
		ModelID:           data.ModelID,
		ModelName:         data.ModelName,
		TenantID:          data.TenantID,
		UserID:            data.User,
		State:             data.State,
		TokensIn:          data.TokensIn,
		TokensOut:         data.TokensOut,
		CachedInputTokens: data.CachedInputTokens,
		ReasoningTokens:   data.ReasoningTokens,
		Requests:          data.Requests,
		EventTime:         ce.Time,
		DurationSeconds:   data.DurationSeconds,
	}); err != nil {
		return err
	}
	return nil
}

// processOSACResourceEvent handles OSAC metering-service v1 lifecycle events.
// These arrive on the osac.metering.lifecycle topic with resource state
// transitions. For compute_instance resources, we upsert an inventory record
// so that our existing metering sweep picks up the resource.
func (h *APIHandler) processOSACResourceEvent(ctx context.Context, ce cloudEventInternal) error {
	var md meteringData
	if err := json.Unmarshal(ce.Data, &md); err != nil {
		return fmt.Errorf("osac v1: decode metering data: %w", err)
	}

	h.logger.Info("osac v1 resource event",
		"type", ce.Type,
		"resource_type", md.ResourceType,
		"resource_id", md.ResourceID,
		"tenant_id", md.TenantID,
		"state", md.CurrentState,
	)

	switch md.ResourceType {
	case "compute_instance":
		return h.processOSACComputeInstance(ctx, ce, md)
	case "cluster_order":
		return h.processOSACClusterOrder(ctx, ce, md)
	case "maas_inference":
		return h.processOSACInference(ctx, ce, md)
	default:
		h.logger.Info("osac v1: unhandled resource type", "resource_type", md.ResourceType)
		return nil
	}
}

func (h *APIHandler) processOSACComputeInstance(ctx context.Context, ce cloudEventInternal, md meteringData) error {
	state := md.CurrentState
	if ce.Type == eventTypeResourceDeleted {
		state = "DELETING"
	}

	var bd vmBillingDimensions
	if len(md.BillingDimensions) > 0 {
		_ = json.Unmarshal(md.BillingDimensions, &bd)
	}

	instanceType := ""
	if bd.InstanceType != nil {
		instanceType = *bd.InstanceType
	}

	project := ""
	if md.ProjectID != nil {
		project = *md.ProjectID
	}

	if err := h.store.UpsertComputeInstance(ctx, inventory.ComputeInstanceRecord{
		InstanceID:   md.ResourceID,
		Tenant:       md.TenantID,
		Project:      project,
		State:        state,
		InstanceType: instanceType,
		CreatedAt:    ce.Time,
		LastEventID:  ce.ID,
	}); err != nil {
		return fmt.Errorf("osac v1: upsert compute_instance: %w", err)
	}

	h.logger.Info("osac v1: upserted compute_instance",
		"instance_id", md.ResourceID,
		"state", state,
		"instance_type", instanceType,
	)
	return nil
}

func (h *APIHandler) processOSACClusterOrder(ctx context.Context, ce cloudEventInternal, md meteringData) error {
	state := md.CurrentState
	if ce.Type == eventTypeResourceDeleted {
		state = "DELETING"
	}

	var bd clusterBillingDimensions
	if len(md.BillingDimensions) > 0 {
		_ = json.Unmarshal(md.BillingDimensions, &bd)
	}

	template := ""
	if md.TemplateID != nil {
		template = *md.TemplateID
	}
	if bd.ClusterTemplate != "" {
		template = bd.ClusterTemplate
	}

	if err := h.store.UpsertCluster(ctx, inventory.ClusterRecord{
		ClusterID:   md.ResourceID,
		Tenant:      md.TenantID,
		Template:    template,
		State:       state,
		CreatedAt:   ce.Time,
		LastEventID: ce.ID,
	}); err != nil {
		return fmt.Errorf("osac v1: upsert cluster_order: %w", err)
	}

	h.logger.Info("osac v1: upserted cluster_order",
		"cluster_id", md.ResourceID,
		"state", state,
		"component", bd.Component,
		"host_type", bd.HostType,
	)
	return nil
}

func (h *APIHandler) processOSACInference(ctx context.Context, ce cloudEventInternal, md meteringData) error {
	var bd maasBillingDimensions
	if len(md.BillingDimensions) > 0 {
		_ = json.Unmarshal(md.BillingDimensions, &bd)
	}

	modelID := bd.Model
	if modelID == "" {
		modelID = md.ResourceID
	}

	durationSeconds := 0.0
	if md.DurationSeconds != nil {
		durationSeconds = *md.DurationSeconds
	} else if bd.DurationMs > 0 {
		durationSeconds = float64(bd.DurationMs) / 1000.0
	}

	tenantID := md.TenantID
	if tenantID == "" && bd.OrganizationID != "" {
		tenantID = bd.OrganizationID
	}

	createdAt := ce.Time.Add(-time.Duration(durationSeconds * float64(time.Second)))
	if err := h.store.UpsertModel(ctx, inventory.ModelRecord{
		ModelID:     modelID,
		Name:        bd.Model,
		ModelName:   bd.Model,
		Tenant:      tenantID,
		State:       "MODEL_STATE_RUNNING",
		CreatedAt:   createdAt,
		LastEventID: ce.ID,
	}); err != nil {
		return fmt.Errorf("osac v1: upsert maas_inference: %w", err)
	}

	if err := h.meter.MeterMaaSEvent(ctx, metering.MaaSUsage{
		ModelID:           modelID,
		ModelName:         bd.Model,
		TenantID:          tenantID,
		TokensIn:          bd.PromptTokens,
		TokensOut:         bd.CompletionTokens,
		CachedInputTokens: bd.CachedInputTokens,
		ReasoningTokens:   bd.ReasoningTokens,
		Requests:          1,
		EventTime:         ce.Time,
		DurationSeconds:   durationSeconds,
	}); err != nil {
		return err
	}

	h.logger.Info("osac v1: metered inference",
		"model", bd.Model,
		"tenant", tenantID,
		"tokens_in", bd.PromptTokens,
		"tokens_out", bd.CompletionTokens,
	)
	return nil
}

// ---------------------------------------------------------------------------
// Rates
// ---------------------------------------------------------------------------

// ListRates implements ServerInterface.
func (h *APIHandler) ListRates(w http.ResponseWriter, r *http.Request, params ListRatesParams) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var tenantID string
	if params.TenantId != nil {
		tenantID = *params.TenantId
	}

	rates, err := h.store.ListRates(r.Context(), tenantID)
	if err != nil {
		writeErrorJSON(w, "failed to list rates", http.StatusInternalServerError)
		return
	}
	if rates == nil {
		rates = []inventory.RateRecord{}
	}

	// Determine format from param or Accept header
	csvFormat := false
	if params.Format != nil && *params.Format == ListRatesParamsFormatCsv {
		csvFormat = true
	} else if r.Header.Get("Accept") == "text/csv" {
		csvFormat = true
	}

	if csvFormat {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=rates.csv")
		fmt.Fprintln(w, "id,tenant_id,resource_type,instance_type,meter_name,cost_type,price_per_unit,currency,tier_mode,tier_period,tiers,description,effective_from,effective_to")
		for _, rate := range rates {
			tid := ""
			if rate.TenantID != nil {
				tid = *rate.TenantID
			}
			eto := ""
			if rate.EffectiveTo != nil {
				eto = rate.EffectiveTo.Format(time.RFC3339)
			}
			tiersJSON := ""
			if len(rate.Tiers) > 0 {
				b, _ := json.Marshal(rate.Tiers)
				tiersJSON = string(b)
			}
			fmt.Fprintf(w, "%d,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
				rate.ID, CsvSafe(tid), CsvSafe(rate.ResourceType), CsvSafe(rate.InstanceType),
				CsvSafe(rate.MeterName), CsvSafe(rate.CostType), rate.PricePerUnit.String(),
				CsvSafe(rate.Currency), CsvSafe(rate.TierMode), CsvSafe(rate.TierPeriod),
				CsvSafe(tiersJSON), CsvSafe(rate.Description),
				rate.EffectiveFrom.Format(time.RFC3339), eto)
		}
		return
	}

	writeJSON(w, map[string]any{"rates": rates, "count": len(rates)})
}

// ---------------------------------------------------------------------------
// Quotas
// ---------------------------------------------------------------------------

// CreateQuota implements ServerInterface.
func (h *APIHandler) CreateQuota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var q inventory.QuotaRecord
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeErrorJSON(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if q.TenantID == "" || q.MeterName == "" || q.Unit == "" {
		writeErrorJSON(w, "tenant_id, meter_name, and unit are required", http.StatusBadRequest)
		return
	}
	if q.LimitValue <= 0 {
		writeErrorJSON(w, "limit_value must be positive", http.StatusBadRequest)
		return
	}
	if q.Period == "" {
		q.Period = "monthly"
	}
	if _, _, err := billing.ResolvePeriod(q.Period, time.Now()); err != nil {
		writeErrorJSON(w, "invalid period: "+err.Error(), http.StatusBadRequest)
		return
	}
	if q.Policy == "" {
		q.Policy = "deny"
	}
	if q.EffectiveFrom.IsZero() {
		q.EffectiveFrom = time.Now().UTC()
	}

	if q.ProjectID != "" {
		if err := h.validateProjectOvercommit(r.Context(), q, 0); err != nil {
			writeErrorJSON(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	id, err := h.store.UpsertQuota(r.Context(), q)
	if err != nil {
		h.logger.Error("create quota failed", "error", err)
		writeErrorJSON(w, "failed to create quota", http.StatusInternalServerError)
		return
	}
	q.ID = id

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, q)
}

// ListQuotas implements ServerInterface.
func (h *APIHandler) ListQuotas(w http.ResponseWriter, r *http.Request, params ListQuotasParams) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var tenantID string
	if params.TenantId != nil {
		tenantID = *params.TenantId
	}
	withStatus := params.Status != nil && *params.Status == True

	quotas, err := h.store.ListQuotas(r.Context(), tenantID)
	if err != nil {
		writeErrorJSON(w, "failed to list quotas", http.StatusInternalServerError)
		return
	}

	if !withStatus {
		if quotas == nil {
			quotas = []inventory.QuotaRecord{}
		}
		writeJSON(w, map[string]any{"quotas": quotas})
		return
	}

	type quotaWithStatusItem struct {
		inventory.QuotaRecord
		Consumed   float64         `json:"consumed"`
		Percentage float64         `json:"percentage"`
		Thresholds map[string]bool `json:"thresholds"`
	}

	now := time.Now().UTC()
	ctx := r.Context()
	var results []quotaWithStatusItem
	for _, q := range quotas {
		qPeriod := q.Period
		if qPeriod == "" {
			qPeriod = "monthly"
		}
		periodStart, periodEnd, err := billing.ResolvePeriod(qPeriod, now)
		if err != nil {
			continue
		}
		consumed, _ := h.store.MeteringSum(ctx, q.TenantID, q.MeterName, periodStart, periodEnd)
		pct := 0.0
		if q.LimitValue > 0 {
			pct = (consumed / q.LimitValue) * 100
		}
		levels := rating.ThresholdLevels
		if len(q.Thresholds) > 0 {
			levels = q.Thresholds
		}
		thresholds := make(map[string]bool, len(levels))
		for _, t := range levels {
			thresholds[fmt.Sprintf("%.0f", t)] = pct >= t
		}
		results = append(results, quotaWithStatusItem{
			QuotaRecord: q,
			Consumed:    consumed,
			Percentage:  math.Round(pct*100) / 100,
			Thresholds:  thresholds,
		})
	}
	if results == nil {
		results = []quotaWithStatusItem{}
	}
	writeJSON(w, map[string]any{"quotas": results})
}

// DeleteQuota implements ServerInterface.
func (h *APIHandler) DeleteQuota(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if err := h.store.SoftDeleteQuota(r.Context(), id); err != nil {
		writeErrorJSON(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UpdateQuota implements ServerInterface.
func (h *APIHandler) UpdateQuota(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var q inventory.QuotaRecord
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeErrorJSON(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if q.Period != "" {
		if _, _, err := billing.ResolvePeriod(q.Period, time.Now()); err != nil {
			writeErrorJSON(w, "invalid period: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if q.ProjectID != "" && q.LimitValue > 0 {
		if err := h.validateProjectOvercommit(r.Context(), q, id); err != nil {
			writeErrorJSON(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if err := h.store.UpdateQuota(r.Context(), id, q); err != nil {
		writeErrorJSON(w, err.Error(), http.StatusNotFound)
		return
	}

	updated, _ := h.store.GetQuota(r.Context(), id)
	if updated != nil {
		writeJSON(w, updated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

// GetQuotaStatus implements ServerInterface.
func (h *APIHandler) GetQuotaStatus(w http.ResponseWriter, r *http.Request, tenantID string) {
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

	quotas, err := h.store.QuotasForTenant(ctx, tenantID, now)
	if err != nil {
		writeErrorJSON(w, "failed to query quotas", http.StatusInternalServerError)
		h.logger.Error("quota query failed", "error", err, "tenant", tenantID)
		return
	}

	var tenantStatuses []inventory.QuotaStatus
	projectStatuses := make(map[string][]inventory.QuotaStatus)
	var firstPeriodLabel string

	for _, q := range quotas {
		qPeriod := q.Period
		if qPeriod == "" {
			qPeriod = "monthly"
		}
		periodStart, periodEnd, err := billing.ResolvePeriod(qPeriod, now)
		if err != nil {
			h.logger.Warn("invalid quota period", "tenant", tenantID, "meter", q.MeterName, "period", qPeriod, "error", err)
			continue
		}
		periodLabel := billing.PeriodLabel(qPeriod, now)
		if firstPeriodLabel == "" {
			firstPeriodLabel = periodLabel
		}

		var consumed float64
		if isBudget(q.Unit) {
			if q.MeterName == "" || q.MeterName == "*" {
				consumed, _ = h.store.TenantCostSum(ctx, tenantID, periodStart, periodEnd)
			} else {
				consumed, _ = h.store.CostSum(ctx, tenantID, q.MeterName, periodStart, periodEnd)
			}
		} else if q.ProjectID != "" {
			consumed, _ = h.store.MeteringSumByProject(ctx, tenantID, q.ProjectID, q.MeterName, periodStart, periodEnd)
		} else {
			consumed, _ = h.store.MeteringSum(ctx, tenantID, q.MeterName, periodStart, periodEnd)
		}

		pct := 0.0
		if q.LimitValue > 0 {
			pct = (consumed / q.LimitValue) * 100
		}

		levels := rating.ThresholdLevels
		if len(q.Thresholds) > 0 {
			levels = q.Thresholds
		}
		thresholds := make(map[string]bool, len(levels))
		for _, t := range levels {
			thresholds[fmt.Sprintf("%.0f", t)] = pct >= t
		}

		meterAlerts, _ := h.store.AlertsForTenantMeter(ctx, tenantID, q.MeterName, periodLabel)

		status := inventory.QuotaStatus{
			MeterName:  q.MeterName,
			Unit:       q.Unit,
			Limit:      q.LimitValue,
			Consumed:   consumed,
			Percentage: math.Round(pct*100) / 100,
			Thresholds: thresholds,
			Alerts:     meterAlerts,
		}

		if q.ProjectID != "" {
			projectStatuses[q.ProjectID] = append(projectStatuses[q.ProjectID], status)
		} else {
			tenantStatuses = append(tenantStatuses, status)
		}
	}

	if firstPeriodLabel == "" {
		firstPeriodLabel = billing.PeriodLabel("monthly", now)
	}

	resp := struct {
		TenantID string                             `json:"tenant_id"`
		Period   string                             `json:"period"`
		Quotas   []inventory.QuotaStatus            `json:"quotas"`
		Projects map[string][]inventory.QuotaStatus `json:"projects,omitempty"`
	}{
		TenantID: tenantID,
		Period:   firstPeriodLabel,
		Quotas:   tenantStatuses,
	}
	if len(projectStatuses) > 0 {
		resp.Projects = projectStatuses
	}

	writeJSON(w, resp)
}

func (h *APIHandler) validateProjectOvercommit(ctx context.Context, q inventory.QuotaRecord, excludeID int64) error {
	tenantLimit, err := h.store.TenantQuotaLimit(ctx, q.TenantID, q.MeterName)
	if err != nil || tenantLimit == 0 {
		return nil
	}
	projectSum, err := h.store.ProjectLimitSum(ctx, q.TenantID, q.MeterName, excludeID)
	if err != nil {
		return nil
	}
	if projectSum+q.LimitValue > tenantLimit {
		return fmt.Errorf("project limits would exceed tenant limit: %.2f + %.2f > %.2f",
			projectSum, q.LimitValue, tenantLimit)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Wallets
// ---------------------------------------------------------------------------

// CreateWallet implements ServerInterface.
func (h *APIHandler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req struct {
		TenantID   string    `json:"tenant_id"`
		ProjectID  string    `json:"project_id"`
		Currency   string    `json:"currency"`
		Thresholds []float64 `json:"thresholds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.TenantID == "" {
		writeErrorJSON(w, "tenant_id is required", http.StatusBadRequest)
		return
	}
	if req.Currency == "" {
		req.Currency = "USD"
	}

	wallet := inventory.WalletRecord{
		ID:             uuid.New().String(),
		TenantID:       req.TenantID,
		ProjectID:      req.ProjectID,
		Currency:       req.Currency,
		LifecycleState: "active",
		Thresholds:     req.Thresholds,
	}

	if err := h.store.CreateWallet(r.Context(), wallet); err != nil {
		h.logger.Error("create wallet failed", "error", err)
		writeErrorJSON(w, "failed to create wallet", http.StatusInternalServerError)
		return
	}

	created, _ := h.store.GetWallet(r.Context(), wallet.ID)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

// GetWalletStatus implements ServerInterface.
func (h *APIHandler) GetWalletStatus(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if id == "" {
		writeErrorJSON(w, "wallet_id or tenant_id required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Try as wallet ID first, then as tenant ID
	wallet, err := h.store.GetWallet(ctx, id)
	if err != nil {
		wallet, err = h.store.GetWalletForTenant(ctx, id)
	}
	if err != nil || wallet == nil {
		writeErrorJSON(w, "wallet not found", http.StatusNotFound)
		return
	}

	remainingPct := 0.0
	if !wallet.ReferenceBalance.IsZero() {
		remainingPct = wallet.Balance.Div(wallet.ReferenceBalance).InexactFloat64() * 100
	}

	balanceStatus := "ok"
	if wallet.Balance.LessThanOrEqual(wallet.BalanceFloor) {
		balanceStatus = "depleted"
	}

	levels := []float64{50, 25, 10, 0}
	if len(wallet.Thresholds) > 0 {
		levels = wallet.Thresholds
	}
	thresholds := make(map[string]bool, len(levels))
	for _, t := range levels {
		thresholds[fmt.Sprintf("%.0f", t)] = remainingPct <= t
	}

	writeJSON(w, inventory.WalletStatus{
		WalletID:         wallet.ID,
		TenantID:         wallet.TenantID,
		Currency:         wallet.Currency,
		Balance:          wallet.Balance,
		ReferenceBalance: wallet.ReferenceBalance,
		RemainingPct:     math.Round(remainingPct*100) / 100,
		BalanceFloor:     wallet.BalanceFloor,
		BalanceStatus:    balanceStatus,
		WithinBalance:    wallet.Balance.GreaterThan(wallet.BalanceFloor),
		Thresholds:       thresholds,
	})
}

// TopUpWallet implements ServerInterface.
func (h *APIHandler) TopUpWallet(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req struct {
		Amount      decimal.Decimal `json:"amount"`
		Currency    string          `json:"currency"`
		ExternalRef string          `json:"external_ref"`
		Reason      string          `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Amount.IsZero() || req.Amount.IsNegative() {
		writeErrorJSON(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	entry, err := h.store.TopUpWallet(r.Context(), id, req.Amount, req.ExternalRef)
	if err != nil {
		writeErrorJSON(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, entry)
}

// AdjustWallet implements ServerInterface.
func (h *APIHandler) AdjustWallet(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req struct {
		Amount      decimal.Decimal `json:"amount"`
		Currency    string          `json:"currency"`
		ExternalRef string          `json:"external_ref"`
		Reason      string          `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Amount.IsZero() {
		writeErrorJSON(w, "amount must be non-zero", http.StatusBadRequest)
		return
	}

	if req.Amount.IsPositive() {
		entry, err := h.store.TopUpWallet(r.Context(), id, req.Amount, req.ExternalRef)
		if err != nil {
			writeErrorJSON(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, entry)
	} else {
		writeErrorJSON(w, "negative adjustments not yet implemented", http.StatusNotImplemented)
	}
}

// GetWalletLedger implements ServerInterface.
func (h *APIHandler) GetWalletLedger(w http.ResponseWriter, r *http.Request, id string, params GetWalletLedgerParams) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	limit := 100
	if params.Limit != nil && *params.Limit > 0 {
		limit = *params.Limit
	}

	entries, err := h.store.WalletLedger(r.Context(), id, limit)
	if err != nil {
		writeErrorJSON(w, "failed to query ledger", http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []inventory.WalletLedgerEntry{}
	}
	writeJSON(w, map[string]any{"entries": entries})
}

// ---------------------------------------------------------------------------
// Cost Reports
// ---------------------------------------------------------------------------

type costReportResponse struct {
	Meta costReportMeta            `json:"meta"`
	Data []inventory.CostReportRow `json:"data"`
}

type costReportMeta struct {
	Total      kokuCostTotal     `json:"total"`
	Period     string            `json:"period"`
	GroupBy    string            `json:"group_by"`
	Resolution string            `json:"resolution,omitempty"`
	Filters    map[string]string `json:"filters"`
}

type kokuCostLayer struct {
	Value float64 `json:"value"`
	Units string  `json:"units"`
}

type kokuCostBlock struct {
	Raw    kokuCostLayer `json:"raw"`
	Markup kokuCostLayer `json:"markup"`
	Usage  kokuCostLayer `json:"usage"`
	Total  kokuCostLayer `json:"total"`
}

type kokuCostTotal struct {
	Cost           kokuCostBlock `json:"cost"`
	Infrastructure kokuCostBlock `json:"infrastructure"`
	Supplementary  kokuCostBlock `json:"supplementary"`
	CostUnits      string        `json:"cost_units"`
}

func buildKokuTotal(cost, infraCost, suppCost float64, currency string) kokuCostTotal {
	return kokuCostTotal{
		Cost: kokuCostBlock{
			Usage: kokuCostLayer{Value: cost, Units: currency},
			Total: kokuCostLayer{Value: cost, Units: currency},
		},
		Infrastructure: kokuCostBlock{
			Usage: kokuCostLayer{Value: infraCost, Units: currency},
			Total: kokuCostLayer{Value: infraCost, Units: currency},
		},
		Supplementary: kokuCostBlock{
			Usage: kokuCostLayer{Value: suppCost, Units: currency},
			Total: kokuCostLayer{Value: suppCost, Units: currency},
		},
		CostUnits: currency,
	}
}

type costBreakdownResponse struct {
	Meta costBreakdownMeta            `json:"meta"`
	Data []inventory.CostBreakdownRow `json:"data"`
}

type costBreakdownMeta struct {
	Count   int               `json:"count"`
	Filters map[string]string `json:"filters"`
}

// GetCostReport implements ServerInterface.
func (h *APIHandler) GetCostReport(w http.ResponseWriter, r *http.Request, params GetCostReportParams) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var tenantID, resourceType string
	if params.TenantId != nil {
		tenantID = *params.TenantId
	}
	if params.ResourceType != nil {
		resourceType = *params.ResourceType
	}

	groupBy := "tenant"
	if params.GroupBy != nil {
		groupBy = string(*params.GroupBy)
	}

	var resolution string
	if params.Resolution != nil {
		resolution = string(*params.Resolution)
	}

	var periodStart, periodEnd time.Time
	var period string

	if params.From != nil && *params.From != "" {
		fromStr := *params.From
		var err error
		periodStart, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			periodStart, err = time.Parse("2006-01-02", fromStr)
			if err != nil {
				writeErrorJSON(w, "invalid 'from' format, use YYYY-MM-DD or RFC3339", http.StatusBadRequest)
				return
			}
		}
		if params.To != nil && *params.To != "" {
			toStr := *params.To
			periodEnd, err = time.Parse(time.RFC3339, toStr)
			if err != nil {
				periodEnd, err = time.Parse("2006-01-02", toStr)
				if err != nil {
					writeErrorJSON(w, "invalid 'to' format, use YYYY-MM-DD or RFC3339", http.StatusBadRequest)
					return
				}
			}
		} else {
			periodEnd = time.Now().UTC()
		}
		period = periodStart.Format("2006-01-02") + "/" + periodEnd.Format("2006-01-02")
	} else {
		period = ""
		if params.Period != nil {
			period = *params.Period
		}
		if period == "" {
			period = time.Now().UTC().Format("2006-01")
		}
		var err error
		periodStart, err = time.Parse("2006-01", period)
		if err != nil {
			writeErrorJSON(w, "invalid period format, use YYYY-MM", http.StatusBadRequest)
			return
		}
		periodEnd = periodStart.AddDate(0, 1, 0)
	}

	ctx := r.Context()
	rows, err := h.store.CostReport(ctx, tenantID, resourceType, groupBy, resolution, periodStart, periodEnd)
	if err != nil {
		h.logger.Error("cost report query failed", "error", err)
		writeErrorJSON(w, "report query failed", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []inventory.CostReportRow{}
	}

	var totalCost, totalInfra, totalSupp float64
	for _, row := range rows {
		totalCost += row.Cost
		totalInfra += row.InfrastructureCost
		totalSupp += row.SupplementaryCost
	}

	filters := map[string]string{}
	if tenantID != "" {
		filters["tenant_id"] = tenantID
	}
	if resourceType != "" {
		filters["resource_type"] = resourceType
	}

	// Determine format from param or Accept header
	csvFormat := false
	if params.Format != nil && *params.Format == GetCostReportParamsFormatCsv {
		csvFormat = true
	} else if r.Header.Get("Accept") == "text/csv" {
		csvFormat = true
	}

	if csvFormat {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=costs.csv")
		if resolution == "daily" {
			fmt.Fprintln(w, "date,group,entries,cost,infrastructure_cost,supplementary_cost,currency")
			for _, row := range rows {
				fmt.Fprintf(w, "%s,%s,%d,%.6f,%.6f,%.6f,%s\n",
					row.Date, CsvSafe(row.Group), row.Entries, row.Cost, row.InfrastructureCost, row.SupplementaryCost, CsvSafe(row.Currency))
			}
		} else {
			fmt.Fprintln(w, "group,entries,cost,infrastructure_cost,supplementary_cost,currency")
			for _, row := range rows {
				fmt.Fprintf(w, "%s,%d,%.6f,%.6f,%.6f,%s\n",
					CsvSafe(row.Group), row.Entries, row.Cost, row.InfrastructureCost, row.SupplementaryCost, CsvSafe(row.Currency))
			}
		}
		return
	}

	writeJSON(w, costReportResponse{
		Meta: costReportMeta{
			Total:      buildKokuTotal(totalCost, totalInfra, totalSupp, "USD"),
			Period:     period,
			GroupBy:    groupBy,
			Resolution: resolution,
			Filters:    filters,
		},
		Data: rows,
	})
}

// GetCostBreakdown implements ServerInterface.
func (h *APIHandler) GetCostBreakdown(w http.ResponseWriter, r *http.Request, params GetCostBreakdownParams) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var tenantID, resourceType string
	if params.TenantId != nil {
		tenantID = *params.TenantId
	}
	if params.ResourceType != nil {
		resourceType = *params.ResourceType
	}

	var from, to time.Time
	if params.From != nil && *params.From != "" {
		fromStr := *params.From
		var err error
		from, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			from, err = time.Parse("2006-01-02", fromStr)
			if err != nil {
				writeErrorJSON(w, "invalid 'from' format", http.StatusBadRequest)
				return
			}
		}
	} else {
		from = time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	if params.To != nil && *params.To != "" {
		toStr := *params.To
		var err error
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			to, err = time.Parse("2006-01-02", toStr)
			if err != nil {
				writeErrorJSON(w, "invalid 'to' format", http.StatusBadRequest)
				return
			}
		}
	} else {
		to = time.Now().UTC()
	}

	limit := 100
	if params.Limit != nil && *params.Limit > 0 {
		limit = *params.Limit
	}

	ctx := r.Context()
	rows, err := h.store.CostBreakdown(ctx, tenantID, resourceType, from, to, limit)
	if err != nil {
		h.logger.Error("cost breakdown query failed", "error", err)
		writeErrorJSON(w, "breakdown query failed", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []inventory.CostBreakdownRow{}
	}

	filters := map[string]string{}
	if tenantID != "" {
		filters["tenant_id"] = tenantID
	}
	if resourceType != "" {
		filters["resource_type"] = resourceType
	}

	// Determine format from param or Accept header
	csvFormat := false
	if params.Format != nil && *params.Format == GetCostBreakdownParamsFormatCsv {
		csvFormat = true
	} else if r.Header.Get("Accept") == "text/csv" {
		csvFormat = true
	}

	if csvFormat {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=breakdown.csv")
		fmt.Fprintln(w, "date,tenant_id,project_id,user_id,resource_type,resource_id,meter_name,metered_value,cost_amount,cost_type,currency")
		for _, row := range rows {
			fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s,%s,%.6f,%.10f,%s,%s\n",
				row.Date, CsvSafe(row.TenantID), CsvSafe(row.ProjectID), CsvSafe(row.UserID),
				CsvSafe(row.ResourceType), CsvSafe(row.ResourceID),
				CsvSafe(row.MeterName), row.MeteredValue, row.CostAmount,
				CsvSafe(row.CostType), CsvSafe(row.Currency))
		}
		return
	}

	writeJSON(w, costBreakdownResponse{
		Meta: costBreakdownMeta{
			Count:   len(rows),
			Filters: filters,
		},
		Data: rows,
	})
}

// GetPipelineSummary implements ServerInterface.
func (h *APIHandler) GetPipelineSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx := r.Context()
	summary, err := h.store.PipelineSummary(ctx)
	if err != nil {
		h.logger.Error("pipeline summary query failed", "error", err)
		writeErrorJSON(w, "summary query failed", http.StatusInternalServerError)
		return
	}
	metrics.LiveModels.Set(float64(summary.LiveModels))
	writeJSON(w, summary)
}

// ---------------------------------------------------------------------------
// IPP Balance Check (Entitlement)
// ---------------------------------------------------------------------------

// GetEntitlementValue implements ServerInterface.
func (h *APIHandler) GetEntitlementValue(w http.ResponseWriter, r *http.Request, customerID string, featureKey string, params GetEntitlementValueParams) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	_ = featureKey // available for future feature-scoped quotas

	ctx := r.Context()
	now := time.Now().UTC()

	quotas, err := h.store.QuotasForTenant(ctx, customerID, now)
	if err != nil || len(quotas) == 0 {
		writeJSON(w, map[string]interface{}{
			"hasAccess": true,
			"balance":   math.MaxFloat64,
			"usage":     0.0,
			"overage":   0.0,
		})
		return
	}

	totalLimit := 0.0
	totalUsage := 0.0
	for _, q := range quotas {
		qPeriod := q.Period
		if qPeriod == "" {
			qPeriod = "monthly"
		}
		periodStart, periodEnd, err := billing.ResolvePeriod(qPeriod, now)
		if err != nil {
			continue
		}
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

	writeJSON(w, map[string]interface{}{
		"hasAccess": totalUsage < totalLimit,
		"balance":   balance,
		"usage":     totalUsage,
		"overage":   overage,
	})
}

// ---------------------------------------------------------------------------
// Debug / Reconcile
// ---------------------------------------------------------------------------

// GetDebugConfig implements ServerInterface.
func (h *APIHandler) GetDebugConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if h.cfg == nil {
		writeJSON(w, map[string]string{"error": "config not available"})
		return
	}
	writeJSON(w, h.cfg.Diagnostics())
}

// TriggerReconcile implements ServerInterface.
func (h *APIHandler) TriggerReconcile(w http.ResponseWriter, r *http.Request) {
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

// GetReports serves the manager-facing cost reports UI (HTML).
func (h *APIHandler) GetReports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(reportsHTML))
}

// GetDebugDashboard serves the built-in diagnostic dashboard (HTML).
func (h *APIHandler) GetDebugDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

// RegisterDebugRoutes adds GET / (redirect to /reports).
// /reports and /debug/dashboard are registered via HandlerFromMux (both in server.gen.go).
func (h *APIHandler) RegisterDebugRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/reports", http.StatusFound)
		}
	})
}
