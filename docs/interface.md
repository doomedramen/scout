# Interface direction

Scout should show infrastructure state clearly and let an owner trace every health or topology claim to evidence.

## Main surfaces

The default landing page is **Fleet overview**. It shows current availability, telemetry freshness, active incidents, access prerequisites, and systems with offline, stale, or access state. Counts link to the full inventory or queue; no historical uptime or health percentage is inferred when evidence is unavailable.

- **Overview:** current incidents, coverage, stale agents, and infrastructure health. Prioritize actionable changes over decorative counters.
- **Network:** an explorable graph with site/network grouping, search, filters, and a synchronized inventory list. Selecting a relationship shows source, age, and confidence.
- **Systems:** searchable inventory with health, monitoring method, last seen, addresses, and enrollment status.
- **Incidents:** an evidence-first queue with acknowledgement, history, and rule management.
- **Device detail:** metric history, interfaces, observed neighbors, events, and access state.
- **Enrollment:** a queue of discovered candidates, missing access, approvals, progress, and recoverable failures.
- **Updates:** fleet version coverage, approved releases, rollout progress, maintenance windows, pins, and rollback results. Allow owners to import signed bundles and assign releases to machines without internet access.
- **Administration:** discovery scopes, identities, credentials, users, audit history, and retention.
- **Settings:** the entry point for administration and diagnostics that do not belong in the primary monitoring flow.

## Visual direction

Follow the supplied UniFi reference: compact system rows, inline CPU/memory/disk readings, restrained dark panels, and paired host charts. Use charcoal (#141619), panel (#191c20), border (#30343b), text (#e7e9ed), muted green (#74ad90), and amber (#cbb074). Use Avenir Next with Segoe UI and sans-serif fallbacks; tabular numbers align metrics. Keep the header compact, use a labeled sidebar on wide screens, and use a top-bar menu sheet on phones. Topology occupies its own view rather than displacing the practical system table. Reserve semantic colors for health and action; always pair color with text or symbols. Dark theme is the default and only theme. Motion should explain graph changes and preserve spatial context without making routine monitoring distracting.

The graph complements a usable list; it must not become the only navigation method. Keyboard access, visible focus, reduced motion, and meaningful loading, empty, failure, and stale states are part of the initial design.

## Core journey

On a fresh install, the owner creates their account, enrolls the first agent, sees real telemetry, and learns what the agent can observe. Discovery is then enabled for a defined scope. New candidates show why Scout found them, what monitoring is possible, and what access is missing.

Credential entry must state which targets can use the credential and what operations it permits. Enrollment shows a concrete target and expected installation changes. Never imply that finding a device proves authority to administer it.

Use demo data only in an explicitly marked demo mode. An empty production install must never show fabricated healthy devices or topology.

## Navigation and responsive layout

Scout uses four primary destinations, Overview, Systems, Incidents, and Network, with administration and diagnostics grouped under **Settings**. The top-bar bell opens recent delivery activity and links to full notification settings. On phones and narrow tablets the primary destinations open from a burger menu; on wide screens they live in a 208px sidebar.

Systems and Network use list-first layouts on small screens. A visible List/Map control switches Network’s observed-device presentation. Device detail opens as a full page with Summary, Metrics, and Manage sections, while filters and short editors use accessible shadcn sheets. Browser hash routes preserve page, device, candidate, and incident selection across refresh and Back navigation.
