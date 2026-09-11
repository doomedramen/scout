# Scout Specification Workflow

Scout uses [GitHub Spec Kit](https://github.com/github/spec-kit), initialized with Specify CLI 1.0.1 and the Codex skills integration. Project-local skills, templates, and shell helpers are committed so the workflow is reproducible.

## Spec 001 — current implementation

- [Scout platform](001-scout-platform/spec.md): user journeys, requirements, states, success criteria, assumptions, and release boundaries.
- [Constitution](../.specify/memory/constitution.md): project-wide principles and agreed implementation constraints, version 1.0.0.
- [Requirements checklist](001-scout-platform/checklists/requirements.md): author-reviewed specification quality.
- [Acceptance matrix](001-scout-platform/acceptance-matrix.md): coverage for every functional requirement, all planned rather than passed.
- [Implementation handoff](001-scout-platform/handoff.md): reading order, current state, execution rules and a copyable prompt.
- [Technical plan](001-scout-platform/plan.md), [data model](001-scout-platform/data-model.md), [contracts](001-scout-platform/contracts/control-api.md), and [64 tasks](001-scout-platform/tasks.md): the implementation package.
- [Consistency analysis](001-scout-platform/analysis.md): coverage and remaining execution-time validation.

The platform specification governs target behavior. Existing `docs/` files remain supporting design notes; when they diverge, update them to match the specification and current user decisions. Application code remains a development scaffold.

## Next phase

The constitution, specification, technical plan, contracts and task breakdown are written. Start with [handoff.md](001-scout-platform/handoff.md) and use the installed `$speckit-implement` skill against tasks.md. Use `$speckit-clarify` if requirements change, `$speckit-plan` for material design revisions, and `$speckit-analyze` to review cross-artifact consistency. Do not rerun `$speckit-specify` merely to continue this same feature; that creates a new feature by default.

This checkout's active feature pointer is `.specify/feature.json`. It is machine-local and ignored. On a fresh checkout, select the feature explicitly before running helpers or a Spec Kit skill:

```sh
export SPECIFY_FEATURE_DIRECTORY="$PWD/specs/001-scout-platform"
.specify/scripts/bash/check-prerequisites.sh --paths-only --json
```

The spec directory does not depend on a Git branch name. With the supplied plan and tasks present, run the normal prerequisite check with `--json --require-tasks --include-tasks` before implementation.

The implementing agent must validate the plan's support targets and sizing assumptions, record exact provider versions and permissions, and execute the eight journeys in the supplied dependency order. Demo UI is never proof of real telemetry or automatic installation.

## Spec 002 — Advanced monitoring and alerting

The [002 specification](002-advanced-monitoring/spec.md) defines the next Linux phase: automatic incidents, ntfy notifications and quiet hours, systemd health, diagnostic metrics, year-long rollups, SMART/ZFS, and sensors/GPUs. This is a documentation package, not implemented behavior.

- [Implementation handoff](002-advanced-monitoring/handoff.md), [technical plan](002-advanced-monitoring/plan.md), and [42 tasks](002-advanced-monitoring/tasks.md).
- [Data model](002-advanced-monitoring/data-model.md), [API contract](002-advanced-monitoring/contracts/control-api.md), [collector contract](002-advanced-monitoring/contracts/collectors.md), and [rollup contract](002-advanced-monitoring/contracts/rollups.md).
- [Research](002-advanced-monitoring/research.md), [acceptance matrix](002-advanced-monitoring/acceptance-matrix.md), [quickstart](002-advanced-monitoring/quickstart.md), [quality checklist](002-advanced-monitoring/checklists/requirements.md), and [analysis](002-advanced-monitoring/analysis.md).
- [Runtime evidence](002-advanced-monitoring/evidence.md) and [required hardware matrix](002-advanced-monitoring/support-matrix.md) remain unvalidated.

Keep the active feature pointer on 001 while implementation continues. Select 002 explicitly for its helpers:

```sh
SPECIFY_FEATURE_DIRECTORY="$PWD/specs/002-advanced-monitoring" .specify/scripts/bash/check-prerequisites.sh --paths-only --json
```

002 depends on verified 001 foundations. Its preparation does not mark 001 complete or change its current task order.

Normal setup/full-check helpers persist feature selection; save and restore `.specify/feature.json` around 002 calls while 001 remains active. The path-only command above is read-only.
