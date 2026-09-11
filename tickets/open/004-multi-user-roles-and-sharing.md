---
id: "004"
title: "Add multi-user roles and resource sharing"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [authentication, authorization]
---

## Problem

Scout is designed for one owner and does not specify multiple users, read-only access, delegated administration, or sharing selected sites and devices.

## Context

Beszel supports admin, user, and read-only roles plus system sharing. Scout needs its own authorization model rather than inheriting PocketBase behavior. Credential access, enrollment authority, updates, recovery, audit visibility, and monitoring configuration require explicit role boundaries.
