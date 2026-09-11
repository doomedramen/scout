---
id: "002"
title: "Add container image update visibility"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [containers, updates]
---

## Problem

Scout does not specify an opt-in way to show when a newer container image may be available.

## Context

Beszel performs image update checks separately from metrics collection. A future Scout design should account for registry access, credentials, rate limits, digest-pinned images, isolated networks, check timestamps, and registry errors. Availability must not imply security or compatibility, and this ticket does not propose automatic container updates.
