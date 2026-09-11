# Implementation and Validation Guide

## Establish the current baseline

Read AGENTS.md and handoff.md first. Run these from the repository root with Node/npm/Go installed:

```sh
npm ci
npm run check
npm run build
npm test
npm run snapshot -w @scout/agent
```

The last command currently prints local identity/interface metadata only. Existing tests cover the health handler, not monitoring security or the target platform. A successful baseline is not acceptance of any unfinished story.

Optional local development:

```sh
npm run setup
npm run db:up
npm run dev
```

Setup preserves an existing private .env. Do not print, commit, copy into a handoff, or repurpose its secrets. The existing Compose file starts only PostgreSQL, and the UI's populated screens are demo data. The project supports both docker compose and docker-compose. If Docker's configured credential helper is unavailable, repair the local runtime configuration or use a documented disposable configuration for a public image; do not alter the user's global Docker settings silently.

## Select the Spec Kit feature

```sh
export SPECIFY_FEATURE_DIRECTORY="$PWD/specs/001-scout-platform"
.specify/scripts/bash/check-prerequisites.sh --json --require-tasks --include-tasks
```

The check locates documents; it does not execute tests or establish implementation completeness. The installed `$speckit-implement` skill may drive tasks.md. No new task or agent is automatically authorized by that command; obey current project/user constraints.

## Target validation runs to implement

The commands below are **future interfaces**, created by the indicated tasks. Do not report them as runnable until those tasks exist. Each runner must create an isolated test environment, print a redacted report, return nonzero on failure, and clean only its own fixtures. No runner may discover or install onto the user's real network by default.

| Runner to create                 | Task        | Required proof                                                                                                                                            |
| -------------------------------- | ----------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `scripts/test-integration.sh`    | T002        | Dedicated PostgreSQL, migrations, contract/security tests and cleanup; no use of personal .env database.                                                  |
| `scripts/test-first-agent.sh`    | T017        | Protected owner setup, expiring invitation, mTLS, real host metrics, restart persistence, stale/offline transitions.                                      |
| `scripts/test-restore.sh`        | T022        | Backup a populated fixture, restore with correct/missing keys, revoke old sessions, pause stale authorization, reconnect agents without duplication.      |
| `scripts/test-updates.sh`        | T030        | Verified online/server-only/offline-import upgrades, invalid manifest/key/platform, interruption, startup failure, bounded rollback and trust changes.    |
| `scripts/test-enrollment.sh`     | T044        | Ten isolated Linux targets, second vantage point, exclusions, access resolution, trust failure, duplicates, pause and expired work.                       |
| `npm run test:e2e -w @scout/web` | T002 / T049 | Live-backed inventory, history, chart tooltips/gaps, accessible topology/list, demo isolation and 360/1440-pixel layouts.                                 |
| `scripts/test-collectors.sh`     | T055        | Two reference provider majors plus fake third adapter, missing privilege, timeout/malformed responses, redaction, cluster failover and guest association. |
| `scripts/test-decommission.sh`   | T059        | Revoke, exclude, rediscover, offline reconnect, verified uninstall and explicit re-enable.                                                                |
| `scripts/test-load.sh`           | T061        | 100-device/24-hour dataset and timed reads/ingestion, queue/buffer limits, disk pressure, retention and sample-loss reports.                              |

## Lab prerequisites

Use disposable systemd Linux VMs for SSH/elevation/updater tests and separate seeded known_hosts. Include a target reachable only from a second authorized worker/vantage point. Keep a target outside the scope and inspect traffic to prove no probing/installation there. Block public internet independently from server reachability. Run update power/interruption cases at download, staging, fsync, switch, restart, guardian upgrade and readiness stages.

Provider tests need owner-supplied access to an isolated Docker/PVE test environment; fixture tests are available without it. If no real hypervisor lab is available, complete the adapter/fixture work and explicitly leave live compatibility acceptance incomplete. Do not invent live results or request production credentials just to close a task.

## Completion evidence

Store redacted acceptance results in `specs/001-scout-platform/evidence.md` with command, date, commit, environment, relevant FR/SC IDs, result, and material limits. Reference the acceptance matrix. A story is complete only when its independent test passes, not when its UI or endpoint merely exists. The target Linux release includes all eight stories, including P2 collectors and decommissioning. Run build/check plus relevant tests after changes; avoid repeatedly running unrelated suites without cause.
