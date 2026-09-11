# Implementation Handoff

## Objective and reading order

Implement the approved 002 monitoring extension, not Beszel parity beyond the six chosen areas. Read [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md), [data-model.md](data-model.md), [control API](contracts/control-api.md), [collectors](contracts/collectors.md), [rollups](contracts/rollups.md), then [tasks.md](tasks.md). Use [acceptance-matrix.md](acceptance-matrix.md) and [quickstart.md](quickstart.md) throughout.

## Baseline and scope

001 is actively being developed. Its task checkboxes and uncommitted changes are not release evidence. Re-check T001 dependencies; reuse its auth, identity, collector registry, recovery and signed releases. The known snapshot-store and single-metric current-state issues have explicit 002 migration tasks. Do not invent a parallel registry or overwrite ongoing work.

This package contains documentation only. All 42 implementation tasks start unchecked. The requirements checklist evaluates writing quality, not implemented behavior. evidence.md and support-matrix.md intentionally contain unvalidated runtime gates.

## Execution rules

- Use only gpt-5.6-luna unless the owner explicitly authorizes another model. Do not spawn agents without applicable authorization. Commit and push owned work regularly without co-author trailers.
- Keep `.specify/feature.json` on 001 while its work continues. For each helper use `SPECIFY_FEATURE_DIRECTORY="$PWD/specs/002-advanced-monitoring"` as a per-command override. The normal setup/check helpers persist the override; save and restore the pointer around those calls, and avoid running them concurrently with another planning session. Use check-prerequisites.sh --paths-only --json for read-only path inspection. Do not change branches or leave the active feature changed implicitly.
- Follow tasks in order and update evidence alongside completion. Migrations, delivery, secret handling and restore require SQL/negative tests. Passing in-memory tests alone is insufficient.
- Do not silently reduce hardware families, change ntfy to another provider, drop P2 stories, or replace missing metrics with zero. Missing lab access blocks support claims; keep the evidence gate open while completing independent work.
- Sending real test notifications or installing software on hosts requires the owner's actual destination/lab authorization. The specification-writing request does not authorize production deployment.

## Copyable execution prompt

> Implement specs/002-advanced-monitoring/tasks.md using the current repository and the complete 002 contracts. First verify the 001 prerequisites and record actual evidence. Preserve other work and the active 001 pointer. Follow the documented migration and collector boundaries, execute required tests, keep unsupported hardware honest, and commit/push owned changes without co-author trailers. Complete each task only when its behavior and evidence are present.
