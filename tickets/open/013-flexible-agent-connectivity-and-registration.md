---
id: "013"
title: "Add flexible agent connectivity and registration"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [agent, networking, enrollment]
---

## Problem

Scout does not specify agent-initiated persistent WebSocket connections, SOCKS proxy support, local Unix-socket transport, or reusable universal registration tokens for elastic deployments.

## Context

Beszel documents these options for NAT, clustered, local-only, and proxy-constrained deployments. Scout already uses authenticated outbound reporting and single-use enrollment invitations; any new registration mode must preserve device-bound identity, scope policy, revocation, auditability, and replay resistance.
