---
id: "003"
title: "Add automatic local and S3-compatible backups"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [backup, operations]
---

## Problem

Scout specifies manual backup and restore but not scheduled backups, retention management, or S3-compatible backup storage.

## Context

Beszel exposes automatic backup and restore for local disk and S3-compatible storage. Scout's existing recovery fencing, separate wrapping-key requirement, schema validation, and notification pause after restore must remain intact if automation is added.
