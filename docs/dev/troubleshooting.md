# Troubleshooting

## OSAC Fulfillment-Service: Dirty Database Migration

**Symptom:** The OSAC gRPC server crashes in a loop with:
```
Dirty database version 69. Fix and force version.
```

**Root cause:** Migration 69 (`69_add_project_column.up.sql`) calls
`uuidv7()`, which is a **PostgreSQL 18 built-in function**. If you run
OSAC against PostgreSQL 16 or 17, this function doesn't exist and the
migration crashes. The go-migrate library marks `schema_migrations` as
`dirty=true` before running each migration, so the crash leaves the DB
in a dirty state that blocks all subsequent starts.

The OSAC team uses PG 18 (via `quay.io/sclorg/postgresql-18-c10s`) in
their own infrastructure, so they never hit this.

**When it happens:**
- Running OSAC fulfillment-service against PostgreSQL < 18
- Migration 69 calls `uuidv7()` → PG throws "function does not exist"
- go-migrate has already set `dirty=true` on `schema_migrations`
- Every pod restart sees `dirty=true` and exits immediately

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
- The k3s full-stack script ([`snippets/k3s-full-stack.sh`](../../snippets/k3s-full-stack.sh))
  uses an init container to work around this
