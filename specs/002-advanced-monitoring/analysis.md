# Specification Consistency Analysis

**Date**: 2026-09-11
**Scope**: Author review of specification, plan, contracts, tasks and acceptance mapping. This is document validation, not a code review or runtime acceptance.

## Findings and disposition

| Finding | Disposition |
| --- | --- |
| Current SQL store serializes full workspace telemetry; multi-entity current values collide by metric name | Explicit migration and full-series identity tasks T003–T005 precede evaluator/rollups. |
| Spec Kit normal setup/check helpers persist an explicit feature override | Original 001 pointer restored; handoff documents save/restore and read-only --paths-only checks. |
| Year-long hourly source still exceeds 600 query points | Contract requires integer-multiple query grouping with full-span coverage and partial buckets. |
| Hardware fault interpretation could imply arbitrary wear thresholds | Collector contract enumerates explicit SMART/ZFS fault predicates; generic thresholds remain owner-configured. |
| External acceptance can be uncertain after a lost response | Outbox contract states possible remote duplicates and never claims exactly-once delivery or subscriber receipt. |
| Pending messages can become stale before delivery | Execution-time relevance checks cancel obsolete triggers; recovery requires recorded prior acceptance. |
| Reducing retention needs concrete preview semantics | API binds an expiring preview to revision and proposed values before destructive reduction. |

No unresolved critical/high document finding remains after these corrections. Actual scale and hardware behavior remain mandatory implementation evidence, not resolved by author review.

## Coverage summary

- 35 functional requirements; every requirement has tasks and a planned acceptance row.
- 11 success criteria; every criterion has validation tasks and a planned acceptance row.
- 42 sequential unchecked tasks, each with an implementation path and requirement references.
- Story tasks: US1 6, US2 6, US3 4, US4 3, US5 5, US6 5, US7 4; 5 foundation tasks and 4 final integration tasks.
- No unmapped tasks, duplicate requirement IDs, unresolved template markers, or constitution exception identified.
- Detailed requirement-to-task mapping is [acceptance-matrix.md](acceptance-matrix.md).

## Validation method

Check file existence and relative Markdown links, sequential task IDs/format, unique requirement IDs, all FR/SC references in tasks and matrix, unresolved placeholders, whitespace errors, and the preserved active feature pointer. Run the explicit 002 prerequisite helper and restore the pointer afterward. No extension hooks are configured in this checkout.

## Next action

Use [handoff.md](handoff.md) to begin implementation when 001 prerequisites are verified. Keep every runtime acceptance row planned until its evidence exists. Missing hardware evidence remains a release limitation even when fixture tests pass.
