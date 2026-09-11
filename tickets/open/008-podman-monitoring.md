---
id: "008"
title: "Add Podman monitoring"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [containers, podman]
---

## Problem

Scout's current container adapter supports Docker only. Podman discovery, inventory, metrics, socket permissions, and compatibility are not specified.

## Context

Beszel uses the Podman API through a compatible socket configuration. Scout should decide whether Docker and Podman may run together, how rootless identities map to hosts, and how read-only proxy access is documented and tested.
