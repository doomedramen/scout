---
id: "017"
title: "Add battery, eMMC, and mdraid health monitoring"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [hardware, monitoring, storage]
---

## Problem

Scout's advanced-monitoring specification does not cover host battery state, Linux eMMC wear and end-of-life indicators, or mdraid array health.

## Context

These capabilities appear in Beszel's documented metrics and SMART-related behavior but fall outside Scout's current SMART, ZFS, sensor, and GPU scope. Each source needs explicit availability semantics, stable entity identity, permissions, alert policy, and hardware evidence.
