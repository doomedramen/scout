# Scout Specification Workflow

Scout uses [GitHub Spec Kit](https://github.com/github/spec-kit), initialized with Specify CLI 1.0.1 and the Codex skills integration. Project-local skills, templates, and shell helpers are committed so the workflow is reproducible.

## Current specification

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
