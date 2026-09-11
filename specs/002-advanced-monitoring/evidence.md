# Evidence Ledger

## Specification delivery

2026-09-11: documentation package authored from the approved plan, Beszel revision f204dc17e64bba444478c60210d018e43a777193, 001 artifacts, and current code inspection. Current 001 checkout contained unrelated implementation changes; those were excluded from this work.

Document checks are recorded in analysis.md after validation. They establish artifact coverage and consistency only.

## T001 — 001 prerequisite gate

- Date: 2026-09-11 (Europe/London); commit: `93ae5be`
- The working tree was clean before this gate. The active `.specify/feature.json`
  pointer remains `001-scout-platform`; the 002 prerequisite was selected only
  per-command with `SPECIFY_FEATURE_DIRECTORY`.
- `SPECIFY_FEATURE_DIRECTORY="$PWD/specs/002-advanced-monitoring" .specify/scripts/bash/check-prerequisites.sh --json --require-tasks --include-tasks` passed and found the 002 plan, research, data model, contracts, quickstart, and tasks.
- The 001 prerequisite code was verified directly, not inferred from task
  checkboxes: `go test ./internal/identity ./internal/collector/... ./internal/store ./internal/control ./internal/updates ./internal/enrollment -count=1` passed. This covers the existing identity authority and TLS paths, collector registry/adapters, recovery/store, authenticated control handlers, signed release verification, and scoped enrollment boundaries.
- 001 remains an implementation-complete but acceptance-incomplete dependency.
  T017, T022, T030, T044, T049, T055, T059, T061, and T063 remain open for
  owner-authorized Linux/provider/browser/capacity evidence; this 002 work does
  not mark those gates complete or duplicate their foundations.

## Implementation evidence

No 002 feature implementation, runtime tests, migration, benchmark, ntfy
publication, host installation, or hardware validation was performed before
the T001 prerequisite gate. Future entries must include task and requirement
IDs, commit, date, exact command/environment (secrets redacted), expected and
actual outcomes, output/artifact links, and remaining limits.

For future results append: task and requirement IDs, commit, date, exact command/environment (secrets redacted), expected outcome, actual outcome, output/artifact link, and remaining limits. Record prerequisite 001 evidence during T001; do not copy its checkbox state as proof.

## T002 — contracts, bounds, and compatibility targets

- Requirements: FR-003, FR-008, FR-009, FR-014, FR-022, FR-032, FR-034.
- Date: 2026-09-11 (Europe/London); implementation checkpoint pending commit.
- Contract outputs: `api/openapi.yaml` now covers the additive rule, incident,
  notification, suppression, monitoring-status, and retention-preview routes;
  `api/schemas/` contains strict bounded input/output schemas; and
  `tests/contracts/fixtures/` contains positive rule, destination, quiet-window,
  and history examples plus a rejected executable-expression example.
- Security/bounds evidence: destination `topic` and `token` are write-only with
  limits of 128 and 4096 bytes; destination responses are redacted; recent MFA
  is declared for protected destination/window/settings actions; history is
  bounded to 16 series and 600 points; numeric/state durations are bounded to
  86400 seconds; arbitrary expressions and service/GPU control routes are
  rejected by the contract test.
- Dependency evidence: `go.mod` pins
  `github.com/coreos/go-systemd/v22 v22.7.0`; `go.sum` matches the module and
  go.mod checksums; `research.md` records smartmontools/OpenZFS/hwmon/NVIDIA/
  AMD/Intel compatibility targets as unvalidated fixture/lab targets.
- Exact verification commands (no credentials or external device access):
  `jq -e empty api/schemas/*.json tests/contracts/fixtures/*.json`;
  `go test ./tests/contracts -count=1`; `go test ./... -count=1`;
  `go vet ./...`; `npm run format:check`; `git diff --check`; and
  `go mod verify`.
- Expected and actual outcome: all commands passed. OpenAPI schema references
  and JSON schema references were checked for existing files. The contract
  test passed both positive fixtures and the negative executable-expression /
  redaction assertions.
- Remaining limits: these are executable contract and fixture checks only. No
  ntfy publication, D-Bus/systemd host, smartctl device, ZFS pool, sensor/GPU
  hardware, or live API acceptance is claimed here; those belong to later
  implementation and release-gate tasks.
