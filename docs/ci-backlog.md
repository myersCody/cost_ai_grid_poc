# CI Backlog

Improvements to the CI pipeline (`.github/workflows/ci.yml`) beyond what
shipped in the initial version.

## Done

- [x] `go vet` lint step
- [x] `go build` both binaries
- [x] `go test -race` with PostgreSQL service container
- [x] Container image build verification
- [x] Docker layer caching via GHA cache
- [x] `actions/checkout@v7`, `actions/setup-go@v6` (no Node.js deprecation warnings)

## Near-term

- [ ] **Re-enable `golangci-lint`** — blocked on a release built with Go 1.26+
  (v1.64 is built with Go 1.24). Catches unused vars, error handling issues,
  security issues via `gosec`.
- [ ] **Test coverage reporting** — add `-coverprofile=coverage.out` and either
  upload to Codecov or print the summary. Gives visibility on coverage trends.
- [ ] **Branch protection rule** — configure required status checks on `main`
  in the upstream repo so PRs can't merge without CI passing.

## Medium-term

- [ ] **Unpin fulfillment-service in integration test** — currently pinned to
  `b0c0208c` because HEAD has a migration that crashes on fresh DBs
  (go-migrate dirty version 69). See `docs/dev/troubleshooting.md`. Need to
  either wait for OSAC to fix migration 69+, or test with their latest
  tagged release once they cut one.
- [ ] **`govulncheck`** — add `golang/govulncheck-action@v1` to scan
  dependencies for known CVEs. Free and fast.
- [ ] **Test result reporting** — switch to `gotestsum --junitfile results.xml`
  and upload as a GitHub Actions artifact for better failure diagnosis.
- [ ] **Integration test job** — ~~run `snippets/test-inventory-watcher.sh`
  (full pipeline test, ~90s with metering) against a real DB + mock OSAC.~~
  Done — `integration-test/` with full k3s stack.

## Longer-term

- [ ] **Image push on merge** — after merge to `main`, push the container
  image to `quay.io`. Requires a Quay push secret configured as a repo secret.
- [ ] **Release tagging** — tag images with git SHA and semver on release.
- [ ] **Dependabot / Renovate** — automated dependency updates for Go modules
  and GitHub Actions versions.
