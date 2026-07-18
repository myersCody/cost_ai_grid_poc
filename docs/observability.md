# Observability Plan — Cost Event Consumer

> A Go service running in OpenShift, part of the Red Hat Cost Management
> product family. This plan uses Go-native libraries and patterns,
> fits the Kubernetes/OpenShift observability ecosystem, and aligns with
> existing RHT services (Koku, chrome-service-backend) where appropriate.
>
> **Go ecosystem libraries:**
> - `log/slog` — structured logging (stdlib, Go 1.21+)
> - `github.com/prometheus/client_golang` — Prometheus metrics
> - `github.com/getsentry/sentry-go` — crash reporting (Sentry/GlitchTip)
> - `go.opentelemetry.io/otel` — distributed tracing (post-PoC)
>
> **RHT references (for ecosystem alignment, not code patterns):**
> - [Koku sentry.py](https://gitlab.cee.redhat.com/koku/koku/-/blob/main/koku/koku/sentry.py) — Sentry config: enable toggle, sampling, blocklist
> - [Koku clowdapp.yaml](https://gitlab.cee.redhat.com/koku/koku/-/blob/main/deploy/clowdapp.yaml) — probe config, Sentry env vars, ClowdApp patterns
> - [chrome-service-backend](https://github.com/RedHatInsights/chrome-service-backend/blob/main/main.go) — Go service with separate metrics port, promhttp

## Current State

| Area | Status | Notes |
|------|--------|-------|
| Structured logging | **Done** | `log/slog`, `LOG_LEVEL` wired, `LOG_FORMAT=json` for OpenShift |
| Kubernetes probes | **Done** | `/healthz` (liveness), `/readyz` (DB ping), auth-exempt |
| Graceful shutdown | **Done** | `srv.Shutdown()` with 30s drain, errgroup cancellation |
| Panic recovery | **Done** | HTTP middleware + `defer/recover` on all goroutines |
| Prometheus metrics | **Done** | Separate `:9000/metrics` port, pipeline counters/histograms/gauges |
| Request logging | **Done** | Method, path, status, duration_ms, request_id on every request |
| Debug dashboard | **Done** | `/debug/dashboard`, `/api/v1/debug/config` |
| Crash reporting | Not needed | GlitchTip/Sentry deferred — not required for PoC |
| Distributed tracing | Not needed | OpenTelemetry deferred — post-GA |

---

## 1. Prometheus Metrics

**Priority:** HIGH — core observability, required for production.

### Metrics to expose

**HTTP request metrics** (middleware on all handlers):

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | Counter | method, path, status | Total HTTP requests |
| `http_request_duration_seconds` | Histogram | method, path | Request latency |
| `http_request_size_bytes` | Histogram | method, path | Request body size |

**Pipeline metrics** (emitted from sweep/handler code):

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `events_processed_total` | Counter | type, status | CloudEvents ingested (accepted/duplicate/error) |
| `metering_entries_created_total` | Counter | resource_type, meter_name | Metering entries inserted |
| `cost_entries_created_total` | Counter | resource_type, cost_type | Cost entries produced by rating |
| `metering_sweep_duration_seconds` | Histogram | — | Time spent in 60s metering sweep |
| `rating_sweep_duration_seconds` | Histogram | — | Time spent in 30s rating sweep |
| `rating_sweep_entries_rated` | Counter | — | Entries rated per sweep |
| `rating_sweep_entries_skipped` | Counter | — | Entries skipped (no matching rate) |
| `reconcile_duration_seconds` | Histogram | resource_type | Reconciliation sweep time |
| `reconcile_drift_created` | Counter | resource_type | Resources found in OSAC but not local |
| `reconcile_drift_deleted` | Counter | resource_type | Resources missing from OSAC |
| `alerts_fired_total` | Counter | tenant_id, threshold | Quota threshold alerts |

**Resource gauges** (updated each sweep):

| Metric | Type | Description |
|--------|------|-------------|
| `live_compute_instances` | Gauge | Active VMs in inventory |
| `live_clusters` | Gauge | Active clusters |
| `live_models` | Gauge | Active MaaS models |
| `rates_total` | Gauge | Number of rate definitions |

**Go runtime** (automatic with `promhttp`):

- `go_goroutines`, `go_gc_duration_seconds`, `go_memstats_*`
- `process_cpu_seconds_total`, `process_open_fds`

### Implementation

```go
// go get github.com/prometheus/client_golang

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

// Register in ServeMux:
mux.Handle("GET /metrics", promhttp.Handler())

// Middleware for HTTP metrics:
func metricsMiddleware(next http.Handler) http.Handler { ... }

// Pipeline metrics emitted inline:
eventsProcessed.WithLabelValues(ce.Type, "accepted").Inc()
```

### RHT pattern: Separate metrics port

The chrome-service-backend pattern uses a **separate port** for metrics,
so Prometheus scraping doesn't go through auth middleware:

```go
metricsRouter := http.NewServeMux()
metricsRouter.Handle("/metrics", promhttp.Handler())
go http.ListenAndServe(":9000", metricsRouter)  // separate from :8020
```

Koku uses the same approach via ClowdApp's `metricsPath` and `metricsPort`
configuration. We should follow this pattern.

### Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `METRICS_ENABLED` | `true` | Enable `/metrics` endpoint |
| `METRICS_PORT` | `9000` | Separate port for metrics scraping |

### Kubernetes ServiceMonitor

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: cost-event-consumer
spec:
  selector:
    matchLabels:
      app: cost-event-consumer
  endpoints:
    - port: metrics
      path: /metrics
      interval: 15s
```

---

## 2. Structured Logging

**Priority:** HIGH — needed for log aggregation in OpenShift.

### Current state

Uses `log/slog` with `TextHandler` outputting `key=value` format to stderr.
Logger injected into all components. `LOG_LEVEL` env var controls verbosity.

### Target state

Add JSON format option for production (compatible with OpenShift log
aggregation, Loki, CloudWatch, Splunk).

### Implementation

```go
var handler slog.Handler
switch cfg.LogFormat {
case "json":
    handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
default:
    handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
}
logger := slog.New(handler)
```

### Request logging middleware

The chrome-service-backend uses `chi/middleware.RequestLogger` and
`middleware.RequestID`. We use stdlib `net/http`, so we implement the
equivalent:

```go
func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        requestID := r.Header.Get("X-Request-ID")
        if requestID == "" {
            requestID = uuid.New().String()
        }
        sw := &statusWriter{ResponseWriter: w}
        next.ServeHTTP(sw, r)
        logger.Info("http request",
            "method", r.Method,
            "path", r.URL.Path,
            "status", sw.status,
            "duration_ms", time.Since(start).Milliseconds(),
            "request_id", requestID,
        )
    })
}
```

### Correlation IDs

Generate a request ID for each ingest event and propagate through the
pipeline. The CloudEvent `id` field serves as a natural correlation ID
for event processing — it's already logged as `event_id` in most places.

For HTTP requests without a CloudEvent, generate a UUID or use the
`X-Request-ID` header if present.

### Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `LOG_LEVEL` | `info` | Log verbosity: debug, info, warn, error |
| `LOG_FORMAT` | `text` | Output format: `text` or `json` |

---

## 3. Kubernetes Probes

**Priority:** HIGH — required for correct pod lifecycle management.

### Endpoints

#### `GET /healthz` — Liveness Probe

Indicates the process is alive and not deadlocked. Should be lightweight
and not check external dependencies.

```go
mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    writeJSON(w, map[string]string{"status": "ok"})
})
```

Kubernetes config:
```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: http
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 3
```

#### `GET /readyz` — Readiness Probe

Indicates the service can accept traffic. Checks:
- Database connection (pool ping)
- OSAC reachable (if Watch stream is the primary source)
- Initial reconciliation completed

```go
type ReadinessChecker struct {
    dbPool       *pgxpool.Pool
    reconciled   atomic.Bool  // set to true after first reconcileAll()
}

func (rc *ReadinessChecker) Check(ctx context.Context) error {
    if err := rc.dbPool.Ping(ctx); err != nil {
        return fmt.Errorf("database: %w", err)
    }
    if !rc.reconciled.Load() {
        return fmt.Errorf("initial reconciliation not complete")
    }
    return nil
}
```

Kubernetes config:
```yaml
readinessProbe:
  httpGet:
    path: /readyz
    port: http
  initialDelaySeconds: 10
  periodSeconds: 5
  failureThreshold: 3
```

#### `GET /startupz` — Startup Probe

Indicates initialization is complete (migrations run, rates seeded).
Once startup succeeds, Kubernetes switches to liveness/readiness probes.

```yaml
startupProbe:
  httpGet:
    path: /readyz
    port: http
  initialDelaySeconds: 2
  periodSeconds: 5
  failureThreshold: 30  # allow up to 2.5 minutes for migrations
```

### RHT pattern: Koku probe configuration

Koku's ClowdApp uses the same path for both liveness and readiness
(`/api/cost-management/v1/status/`) with generous timeouts:
- `initialDelaySeconds: 30` — allow time for Django startup
- `periodSeconds: 20` — not too aggressive
- `timeoutSeconds: 10` — accommodate slow DB queries
- `failureThreshold: 5` — tolerate temporary issues

The Celery workers use a separate `WorkerProbeServer` with a readiness
state that tracks whether the worker has completed initialization.
Our Go service starts faster, so we can use shorter delays (5-10s).

### Pre-shutdown draining

When SIGTERM is received, mark `/readyz` as not-ready immediately. This
tells the Kubernetes load balancer to stop sending traffic before the
pod terminates. Then drain in-flight requests.

```go
// On SIGTERM:
readiness.SetNotReady()
time.Sleep(5 * time.Second)  // allow LB to deregister
srv.Shutdown(ctx)
```

---

## 4. Crash Reporting (GlitchTip / Sentry)

**Priority:** MEDIUM — important for production, not needed for PoC demo.

### Panic recovery

#### HTTP handler middleware

The chrome-service-backend uses `chi/middleware.Recoverer` for this.
Since we use stdlib, we implement the equivalent:

```go
func panicRecovery(logger *slog.Logger, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                logger.Error("panic recovered", "error", err,
                    "stack", string(debug.Stack()))
                sentry.CurrentHub().Recover(err)
                http.Error(w, "internal server error", 500)
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

#### Goroutine wrapper

All long-running goroutines (watcher, reconciler, metering, rating,
summarizer) should be wrapped:

```go
func safeGo(logger *slog.Logger, name string, fn func() error) func() error {
    return func() error {
        defer func() {
            if err := recover(); err != nil {
                logger.Error("goroutine panic", "component", name,
                    "error", err, "stack", string(debug.Stack()))
                sentry.CurrentHub().Recover(err)
            }
        }()
        return fn()
    }
}

// Usage:
g.Go(safeGo(logger, "watcher", func() error { return w.Run(ctx) }))
```

### GlitchTip integration

```go
import "github.com/getsentry/sentry-go"

if cfg.SentryDSN != "" {
    sentry.Init(sentry.ClientOptions{
        Dsn:         cfg.SentryDSN,
        Environment: cfg.Environment,  // "development", "staging", "production"
        Release:     version,
    })
    defer sentry.Flush(2 * time.Second)
}
```

GlitchTip is Sentry-compatible — same SDK, different DSN.

### RHT pattern: Koku Sentry configuration

Koku uses three env vars with an explicit enable toggle
([sentry.py](https://gitlab.cee.redhat.com/koku/koku/-/blob/main/koku/koku/sentry.py)):
- `KOKU_ENABLE_SENTRY` — boolean toggle (default: false)
- `KOKU_SENTRY_DSN` — DSN string
- `KOKU_SENTRY_ENVIRONMENT` — environment tag

Plus production hardening:
- **Traces sampler** at 5% sample rate, with blocklist for health/status endpoints
- **before_send hook** for grouping worker timeout/OOM errors by PID
- **Fingerprinting** to avoid alert spam from recurring process kills

We should follow the same pattern: explicit enable toggle, low default
sample rate, blocklist for probe endpoints.

### Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `SENTRY_ENABLED` | `false` | Enable crash reporting (explicit opt-in like Koku) |
| `SENTRY_DSN` | (empty) | GlitchTip/Sentry DSN |
| `SENTRY_ENVIRONMENT` | `development` | Environment tag for crash reports |
| `SENTRY_TRACES_SAMPLE_RATE` | `0.05` | Trace sampling rate (5% default, matching Koku) |

---

## 5. Distributed Tracing (OpenTelemetry)

**Priority:** LOW — post-PoC. Document approach but don't implement yet.

### Approach

Use OpenTelemetry SDK for Go with OTLP exporter. Traces propagate
through the pipeline:

```
HTTP Request → Event Ingest → Raw Event Store → Metering → Rating → Cost Entry
     span 1         span 2         span 3        span 4     span 5     span 6
```

The CloudEvent `id` already serves as a natural trace root for event
processing flows. For the sweep-based metering/rating path, each sweep
invocation is a trace root.

### Libraries

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)
```

### Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (empty) | OTLP collector endpoint. Empty = disabled |
| `OTEL_SERVICE_NAME` | `cost-event-consumer` | Service name in traces |

### OpenShift integration

OpenShift includes a Tempo-based tracing stack. Configure the OTLP
endpoint to point at the in-cluster collector:

```yaml
env:
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://tempo-distributor.openshift-tempo:4318"
```

---

## 6. Graceful Shutdown

**Priority:** HIGH — needed for zero-downtime deployments.

### Current state

Signal handling exists via `signal.NotifyContext`. HTTP server closes on
context cancel. Database pool deferred close. No shutdown timeout or
drain period.

### Target state

```go
// 1. SIGTERM received → context cancelled
// 2. Mark /readyz as not-ready (LB stops sending traffic)
// 3. Wait 5s for LB deregistration
// 4. Shutdown HTTP server with 30s timeout (drains in-flight requests)
// 5. Stop background goroutines (watcher, sweeps)
// 6. Close database pool
// 7. Flush Sentry/metrics
// 8. Exit

shutdownTimeout := 30 * time.Second

go func() {
    <-ctx.Done()
    readiness.SetNotReady()
    time.Sleep(5 * time.Second)

    shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
    defer cancel()
    srv.Shutdown(shutdownCtx)
}()
```

### Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `SHUTDOWN_TIMEOUT` | `30s` | Max time to drain in-flight requests |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Delay after marking not-ready before shutdown |

---

## Implementation Priority

| Phase | Items | Effort | Status |
|-------|-------|--------|--------|
| **P1 — PoC** | Kubernetes probes (healthz/readyz), JSON logging, panic recovery | Small | **Done** |
| **P2 — Pre-production** | Prometheus metrics, request logging middleware, graceful shutdown | Medium | **Done** |
| **P3 — Production** | GlitchTip/Sentry, ServiceMonitor, alerting rules | Medium | Not needed for PoC |
| **P4 — Post-GA** | OpenTelemetry tracing, log sampling, custom dashboards | Large | Not needed for PoC |

---

## Environment Variables Summary

| Var | Default | Phase | Description |
|-----|---------|-------|-------------|
| `LOG_LEVEL` | `info` | **Done** | debug, info, warn, error |
| `LOG_FORMAT` | `text` | **Done** | text or json |
| `METRICS_PORT` | `9000` | **Done** | Separate port for Prometheus scraping |
| `DEBUG_DASHBOARD` | `true` | **Done** | Enable `/debug/dashboard` |
| `SENTRY_DSN` | (empty) | P3 | GlitchTip/Sentry DSN (not needed for PoC) |
| `ENVIRONMENT` | `development` | P3 | Environment tag (not needed for PoC) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (empty) | P4 | OTLP collector (not needed for PoC) |

---

## OpenShift Deployment Snippet

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cost-event-consumer
spec:
  template:
    spec:
      containers:
        - name: consumer
          image: quay.io/cost-mgmt/cost-event-consumer:latest
          ports:
            - name: http
              containerPort: 8020
          env:
            - name: LOG_FORMAT
              value: "json"
            - name: SENTRY_DSN
              valueFrom:
                secretKeyRef:
                  name: cost-consumer-secrets
                  key: sentry-dsn
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 5
          startupProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 2
            periodSeconds: 5
            failureThreshold: 30
          resources:
            requests:
              memory: "64Mi"
              cpu: "100m"
            limits:
              memory: "256Mi"
              cpu: "500m"
```
