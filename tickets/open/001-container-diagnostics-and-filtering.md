---
id: "001"
title: "Add container diagnostics and filtering"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [containers, monitoring]
---

## Problem

Scout does not specify operator-facing container inspection, bounded log access, configurable container inclusion or exclusion, or the richer health details documented by Beszel.

## Context

Scout's existing collector specification covers container identity, lifecycle state, health, and resource metrics. This ticket records the additional diagnostics and filtering surface only. Any log or inspect support needs a separate security boundary that excludes secrets and arbitrary inspection payloads by default.
