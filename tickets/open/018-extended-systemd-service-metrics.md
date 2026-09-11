---
id: "018"
title: "Add extended systemd service metrics"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [linux, systemd, monitoring]
---

## Problem

Scout's advanced-monitoring specification covers systemd inventory and state but explicitly omits per-service CPU and memory. It also does not specify restart counts, descriptions, unit-file state, or lifecycle timestamps.

## Context

Beszel documents these service details. Future Scout work must retain read-only collection, avoid service environments and journal contents, account for cgroup accounting availability, and keep peak-versus-current memory semantics explicit.
