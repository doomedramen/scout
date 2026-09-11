---
id: "016"
title: "Add native hub installation and self-update"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [distribution, server, updates]
---

## Problem

Scout documents containerized server deployment but not a supported native hub binary, operating-system service installation, or hub self-update path.

## Context

Beszel provides binary installation, service setup, and update commands. Scout needs a design compatible with PostgreSQL, web asset packaging, configuration and key locations, release verification, rollback, backups, and externally managed production deployments.
