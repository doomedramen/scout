---
id: "007"
title: "Add an external dead-man heartbeat"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [availability, operations]
---

## Problem

Scout cannot periodically report its own health to an external dead-man endpoint such as Healthchecks.io, Better Stack, or Uptime Kuma.

## Context

Beszel supports GET, HEAD, and POST heartbeats, with POST carrying system and alert summaries. A Scout design must bound payloads, protect private inventory, validate destinations, and distinguish control-server health from monitored-device health.
