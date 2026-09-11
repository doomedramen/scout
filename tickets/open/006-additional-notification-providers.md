---
id: "006"
title: "Add notification providers beyond ntfy"
status: open
priority: medium
created: "2026-09-11T14:12:18Z"
updated: "2026-09-11T14:12:18Z"
tags: [alerts, notifications]
---

## Problem

Scout's advanced-monitoring specification limits external notifications to ntfy. It does not specify email, generic webhooks, chat platforms, paging systems, MQTT, SMS, or other providers documented by Beszel.

## Context

Future providers should reuse Scout's durable delivery, retry, deduplication, redaction, suppression, restore, and destination-revocation semantics. Provider breadth and dependency choice remain undecided.
