# Load Test Procedure — Cost Event Consumer

The `osac-request-generator` sends resource requests to an OSAC API and then
exercises the resulting event path. It is a request generator, not an OSAC
emulator.

How to run a reproducible performance experiment on CRC or any k8s cluster.

## Prerequisites

- CRC running with the full stack deployed (`docs/dev/crc-full-deployment.md`)
- Token refreshed: `./scripts/refresh-token.sh`
- Port-forwards: cost-db on 5434, consumer on 8020 (for local analysis)
- `psql` and `python3` installed locally

---

## 1. Start generators

```bash
eval $(crc oc-env)

# Deploy load test manifests (one-time)
kubectl apply -f deploy/loadtest/

# Start at 1 replica each
kubectl scale deployment maas-traffic-gen osac-traffic-gen -n cost-mgmt --replicas=1

# Watch generator output
kubectl logs -f -n cost-mgmt -l app=maas-traffic-gen
kubectl logs -f -n cost-mgmt -l app=osac-traffic-gen
```

Tune event rate via ConfigMap:
```bash
kubectl edit configmap loadtest-config -n cost-mgmt
# MAAS_RATE: events/sec per pod   (default 50)
# OSAC_RATE: VM lifecycle ops/sec (default 1)
# OSAC_VM_COUNT: live VM pool size (default 10)
```

Scale for higher load:
```bash
kubectl scale deployment maas-traffic-gen -n cost-mgmt --replicas=3
```

---

## 2. Monitor pipeline health

Key metrics to watch during the test:

```bash
# Port-forward metrics
kubectl port-forward -n cost-mgmt svc/cost-event-consumer 9000:9000 &

# Queue depth — should stay near 0; spikes indicate rating bottleneck
watch 'curl -sf localhost:9000/metrics | grep unrated_metering'

# Pipeline lag — should be < 30s (well within 90s SLA)
curl -sf localhost:9000/metrics | grep pipeline_lag
```

Or open Grafana (`http://localhost:3000`) and watch the "Unrated Queue Depth"
and "Pipeline Lag" panels in the cost-consumer-overview dashboard.

---

## 3. Run sizing analysis

After at least 60 seconds of sustained load:

```bash
# Port-forward DB if not already open
kubectl port-forward -n cost-mgmt svc/cost-db 5434:5432 &

./scripts/analyze-sizing.sh
```

The script observes growth over 60 seconds and projects to 1d/1w/1m/1y
at 100/1k/10k events/min. See `docs/operations/table-sizing.md` for
the reference numbers and methodology.

---

## 4. Stop generators

```bash
kubectl scale deployment maas-traffic-gen osac-traffic-gen -n cost-mgmt --replicas=0
```

---

## 5. Generate synthetic historical data (lifecycle testing)

To test retention policies, monthly roll-ups, and quota calculations across
billing periods without running generators for days:

```bash
# Find a dense 1-hour window from the load test
psql postgres://user:pass@localhost:5434/costdb -c "
  SELECT date_trunc('hour', received_at) AS hour, count(*)
  FROM raw_events GROUP BY 1 ORDER BY 2 DESC LIMIT 5;"

# Replicate 1 day (24 copies of the base hour, ~30 min to run)
./scripts/replicate-data.sh \
  --base-start "2026-07-25 11:00:00+00" \
  --base-end   "2026-07-25 12:00:00+00" \
  --copies 24 --shift "1 hour" --yes

# Replicate 1 week (168 copies, ~3.5 hours)
./scripts/replicate-data.sh \
  --base-start "2026-07-25 11:00:00+00" \
  --base-end   "2026-07-25 12:00:00+00" \
  --copies 168 --shift "1 hour" --yes
```

Adjust `--base-start`/`--base-end` to the window you found above.

---

## 6. Teardown

```bash
eval $(crc oc-env)

# Remove load test deployments
kubectl delete -f deploy/loadtest/

# Full CRC stack teardown (optional)
kubectl delete namespace osac cost-mgmt --ignore-not-found --wait
kubectl delete cluster osac -n postgres --ignore-not-found
kubectl delete namespace postgres --ignore-not-found --wait
helm uninstall cnpg -n postgres 2>/dev/null || true
helm uninstall trust-manager cert-manager -n cert-manager 2>/dev/null || true
kubectl delete namespace cert-manager --ignore-not-found --wait
kubectl delete crd -l app.kubernetes.io/name=cert-manager --ignore-not-found
kubectl delete crd clusters.postgresql.cnpg.io poolers.postgresql.cnpg.io \
  scheduledbackups.postgresql.cnpg.io backups.postgresql.cnpg.io \
  bundles.trust.cert-manager.io --ignore-not-found
```

---

## Key findings from July 2026 experiment

**Setup:** 1 MaaS generator pod (50 events/s) + 1 OSAC request-generator pod (10 VMs)

**Bottleneck found:** Rating sweep was capped at 25 entries/s (500 batch / 20s tick).
Built a 264k-entry backlog (45-minute lag) within minutes. Fixed by loop-until-empty.

**Sizing at ~50 events/s:**
| Metric | Value |
|--------|-------|
| Observed rate | ~2,940 events/min |
| 1-day data | 10.4 GB (4.5M + 9.4M + 9.5M rows) |
| Projected 1 month | ~300 GB |
| Projected 3 months | ~1 TB |

**Row sizes (measured):**
| Table | Total bytes/row (data + indexes) |
|-------|----------------------------------|
| raw_events | 1,146 B |
| metering_entries | 442 B |
| cost_entries | 286 B |

See `docs/operations/table-sizing.md` for full fleet/MaaS combination tables.

**Remaining work:** `docs/operations/performance-next-steps.md`
