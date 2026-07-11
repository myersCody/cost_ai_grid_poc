package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "cost_consumer"

// HTTP request metrics (populated by middleware).
var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "http_requests_total",
		Help:      "Total HTTP requests.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request latency.",
		Buckets:   []float64{.001, .005, .01, .05, .1, .25, .5, 1, 5},
	}, []string{"method", "path"})
)

// Pipeline metrics (populated by sweep/handler code).
var (
	EventsProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "events_processed_total",
		Help:      "CloudEvents ingested.",
	}, []string{"type", "status"})

	MeteringEntriesCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "metering_entries_created_total",
		Help:      "Metering entries inserted.",
	}, []string{"resource_type", "meter_name"})

	CostEntriesCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cost_entries_created_total",
		Help:      "Cost entries produced by rating.",
	}, []string{"resource_type", "cost_type"})

	MeteringSweepErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "metering_sweep_errors_total",
		Help:      "Errors during metering sweep.",
	})

	RatingSweepErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "rating_sweep_errors_total",
		Help:      "Errors during rating sweep.",
	})

	MeteringSweepDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "metering_sweep_duration_seconds",
		Help:      "Time spent in the metering sweep.",
		Buckets:   prometheus.DefBuckets,
	})

	RatingSweepDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "rating_sweep_duration_seconds",
		Help:      "Time spent in the rating sweep.",
		Buckets:   prometheus.DefBuckets,
	})

	ReconcileDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "reconcile_duration_seconds",
		Help:      "Reconciliation sweep time.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"resource_type"})

	ReconcileDriftCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "reconcile_drift_created_total",
		Help:      "Resources found in OSAC but missing locally.",
	}, []string{"resource_type"})

	ReconcileDriftDeleted = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "reconcile_drift_deleted_total",
		Help:      "Resources missing from OSAC, marked deleted locally.",
	}, []string{"resource_type"})

	AlertsFiredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "alerts_fired_total",
		Help:      "Quota threshold alerts fired.",
	}, []string{"threshold"})
)

// Resource gauges (updated each sweep).
var (
	LiveComputeInstances = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "live_compute_instances",
		Help:      "Active VMs in inventory.",
	})

	LiveClusters = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "live_clusters",
		Help:      "Active clusters in inventory.",
	})

	LiveModels = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "live_models",
		Help:      "Active MaaS models in inventory.",
	})
)

// Splunk forwarder metrics.
var (
	SplunkForwardTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "splunk_forward_total",
		Help:      "Raw events forwarded to Splunk HEC.",
	})

	SplunkForwardErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "splunk_forward_errors_total",
		Help:      "Errors forwarding to Splunk HEC.",
	})

	SplunkForwardDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "splunk_forward_duration_seconds",
		Help:      "Splunk forward sweep duration.",
		Buckets:   prometheus.DefBuckets,
	})
)
