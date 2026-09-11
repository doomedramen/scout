---
id: "005"
title: "Add OAuth, OIDC, and trusted-proxy authentication"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [authentication, oidc]
---

## Problem

Scout does not specify OAuth2/OIDC login, trusted authentication headers, automatic identity creation, or disabling password login.

## Context

Beszel supports hosted and self-hosted identity providers and trusted-header authentication. This work depends on a defined Scout multi-user model. Any trusted-header mode must define the trusted proxy boundary and prevent clients from supplying the header directly.
