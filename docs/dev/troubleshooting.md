# Troubleshooting

## OSAC Fulfillment-Service: Dirty Database Migration

**Symptom:** The OSAC gRPC server crashes in a loop with:
```
Dirty database version 69. Fix and force version.
```

**Root cause:** The go-migrate library used by fulfillment-service marks
the `schema_migrations` table as `dirty=true` before running a migration.
If the process crashes mid-migration (OOM, timeout, network issue), the
dirty flag stays set and all subsequent starts refuse to proceed.

This is a known limitation of go-migrate — it has no automatic recovery
from partial migrations.

**When it happens:**
- The gRPC pod starts before PostgreSQL is fully accepting connections
- A migration at version 69 runs partially and crashes
- Every pod restart sees `dirty=true` and exits immediately
- Kubernetes keeps restarting the pod, but it crashes every time

**Fix (manual / one-time):**
```bash
# Option 1: Reset the dirty flag (preserves existing tables)
kubectl exec -n osac statefulset/osac-db -- \
  psql -U osacuser -d osacdb -c "UPDATE schema_migrations SET dirty = false;"

# Option 2: Drop everything and let migrations re-run from scratch
kubectl exec -n osac statefulset/osac-db -- \
  psql -U osacuser -d osacdb -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

# Then restart gRPC to pick up the clean state
kubectl rollout restart deployment/osac-grpc -n osac
```

**Fix (CI / automation):**
Use an init container that waits for PostgreSQL AND resets the schema
before the gRPC container starts. See `integration-test/deploy-osac.sh`
for the pattern.

**Prevention:**
- Always ensure PostgreSQL is fully ready before deploying gRPC
- Use readiness probes on the PostgreSQL pod
- On CRC/OpenShift: CloudNativePG handles migrations correctly and
  avoids this issue entirely (see `docs/dev/crc-osac-deployment.md`)

**Related:**
- go-migrate issue: https://github.com/golang-migrate/migrate/issues/283
- Our CRC deployment used CloudNativePG which handles this correctly
- The testfarm script (`deploy-full-k3s.sh`) uses an init container
  to work around this
