# Interface direction

Scout should make infrastructure understandable at a glance and let an owner trace every health or topology claim to evidence.

## Main surfaces

- **Overview:** current incidents, coverage, stale agents, and infrastructure health. Prioritize actionable changes over decorative counters.
- **Network:** an explorable graph with site/network grouping, search, filters, and a synchronized inventory list. Selecting a relationship shows source, age, and confidence.
- **Devices:** searchable inventory with health, monitoring method, last seen, addresses, and enrollment status.
- **Device detail:** metric history, interfaces, observed neighbors, events, and access state.
- **Enrollment:** a queue of discovered candidates, missing access, approvals, progress, and recoverable failures.
- **Updates:** fleet version coverage, approved releases, rollout progress, maintenance windows, pins, and rollback results. Allow owners to import signed bundles and assign releases to machines without internet access.
- **Administration:** discovery scopes, identities, credentials, users, audit history, and retention.

## Visual direction

Follow the supplied Beszel references: compact system rows, inline CPU/memory/disk bars, restrained dark panels, and paired host charts. Use charcoal (#141619), panel (#191c20), border (#30343b), text (#e7e9ed), muted green (#74ad90), and amber (#cbb074). Use Avenir Next with Segoe UI and sans-serif fallbacks; tabular numbers align metrics. Keep the header and navigation horizontal, inventory full-width, and host charts in two columns. Topology occupies its own view rather than displacing the practical system table. Reserve semantic colors for health and action; always pair color with text or symbols. Start with dark theme, adding light theme later. Motion should explain graph changes and preserve spatial context without making routine monitoring distracting.

The graph complements a usable list; it must not become the only navigation method. Keyboard access, visible focus, reduced motion, and meaningful loading, empty, failure, and stale states are part of the initial design.

## Core journey

On a fresh install, the owner creates their account, enrolls the first agent, sees real telemetry, and learns what the agent can observe. Discovery is then enabled for a defined scope. New candidates show why Scout found them, what monitoring is possible, and what access is missing.

Credential entry must state which targets can use the credential and what operations it permits. Enrollment shows a concrete target and expected installation changes. Never imply that finding a device proves authority to administer it.

Use demo data only in an explicitly marked demo mode. An empty production install must never show fabricated healthy devices or topology.
