# Evidence Ledger: Active Network Scanning

## Specification delivery

2026-09-11: specification and implementation package prepared from user direction, Scout constitution, 001 discovery/enrollment contracts, and current code inspection. Existing active implementation changes in the working tree were not modified or treated as feature evidence.

Document validation establishes artifact consistency only. It does not prove active scanning, packet boundaries, enrollment, performance, or compatibility.

## Implementation evidence

No implementation or live network scan is claimed by this package.

For every completed task or release gate append:

- task, requirement, story, and success-criterion IDs;
- date and commit;
- exact command and environment, with secrets redacted;
- authorized ranges, exclusions, scanner identity, topology, and capture point for live tests;
- expected outcome and actual outcome;
- logs, packet capture, screenshots, timing, and storage artifacts where applicable;
- remaining limits and unverified platforms.

Never attach credential values, private keys, host-key private material, banners, packet payloads, or production addressing.

## T001 — 001 prerequisite gate

- Requirements: FR-003, FR-004, FR-013, FR-014, FR-022.
- Date: 2026-09-12 (Europe/London); implementation commit: pending.
- Baseline: repository was clean at commit `1fc68ec` with the configured
  `origin` remote `https://github.com/doomedramen/scout.git`. The 001 handoff,
  evidence ledger, and task ledger were inspected before this gate.
- Exact verification commands and outcomes: `go test ./internal/... -count=1`
  passed; `scripts/test-first-agent.sh` passed its fixture coverage;
  `scripts/test-enrollment.sh` passed finite target, exclusion, method,
  bounds, duplicate, and restart fixtures; and `scripts/test-restore.sh`
  passed its fixture restore/recovery boundary. No live device, production
  database, or credential was contacted.
- Inherited boundaries confirmed for this feature: owner authentication and
  CSRF/MFA mutation fencing; host/site/scope and exclusion policy; encrypted,
  write-only credential storage and normalized host trust; target-bound
  enrollment jobs with current revision/epoch/destination/release checks;
  authenticated desired state; audit events; global pause/recovery fencing;
  PostgreSQL backup/restore checks; and authenticated telemetry heartbeat and
  batch ingestion. Active scanning must call these boundaries rather than
  create parallel authority paths.
- Unmet prerequisites intentionally remain open in 001 and are not duplicated
  here: native Linux/systemd first-agent acceptance (T017), live clean-host
  restore (T022), power-loss updater lab (T030), live second-vantage placement
  (T044), full browser viewport/accessibility acceptance (T049), live
  collector/provider compatibility (T055), full offline decommission lab
  (T059), and live capacity/disk-pressure measurement (T061). In particular,
  no 001 evidence authorizes scanning the owner's LAN or installing an agent
  on a real device.
