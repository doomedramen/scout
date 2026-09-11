# Architecture

## Control plane

The Go control plane owns inventory, agent identities, discovery policy, enrollment authorization, metric retention, alert rules, and the topology graph in PostgreSQL. The React/TypeScript browser application uses shadcn/ui and talks to this service; Go agents establish outbound authenticated connections to it. Turborepo coordinates build and development tasks.

Start with a modular server and a separate agent rather than many independently deployed services. Keep credential handling and enrollment behind a narrow interface so an isolated worker can execute privileged operations later.

The first release has one owner. Enrollment runs automatically within the owner's configured scopes using supplied access; no routine per-device approval is required. The initial agent is bootstrapped by the owner, then every newly enrolled agent contributes observations that extend coverage. Preserve exactly one agent identity per device through idempotent installation and reconciliation.

## Agent

An agent collects host CPU, memory, filesystem, uptime, and network-interface metrics. It reports its own interfaces, routes, and locally visible neighbors where permitted. Active discovery is a separate, disabled-by-default capability requiring an explicit policy.

Agents do not hold the owner's reusable SSH credentials, authorize other agents, or select arbitrary installation targets. A relay worker may run near an isolated network, but it needs its own explicitly assigned role and receives only approved enrollment jobs.

Each agent has a distinct identity and capability set. Collection runs without root where possible; privileged collectors are optional and separately documented. Containerized collectors must describe their host visibility limits rather than silently reporting container metrics as host metrics.

Service-aware collectors extend the same agent through an adapter registry and common scheduling contract. Hypervisors, container runtimes, and other services share transport, health reporting, and entity relationships without baking vendor-specific APIs into the agent core. See [service collectors](service-collectors.md).

## Discovery and enrollment

1. The owner defines allowed CIDRs, exclusions, discovery methods, rate limits, and installation policy.
2. Agents submit observations with timestamps and provenance.
3. The server creates or updates candidate devices. An IP address alone is not a stable device identity.
4. A candidate becomes an access request when approved monitoring needs credentials or another installation method.
5. The owner supplies an existing secret reference or creates a narrowly scoped credential for the configured scope. Enrollment proceeds automatically when scope and access permit it.
6. The enrollment worker validates target scope and SSH host identity, installs a verified agent artifact, and supplies a short-lived, single-use bootstrap token bound to the intended enrollment.
7. The agent exchanges its bootstrap token for its own renewable identity. The worker releases the credential and records the outcome.

Enrollment states: discovered, awaiting approval, awaiting access, queued, connecting, installing, verifying, enrolled, failed, excluded. Failures preserve a safe explanation and retry state. Jobs need idempotency, concurrency limits, and bounded retries to avoid repeated installation attempts.

## Topology

Store observations separately from the graph derived from them. Every observation records its reporting agent, collection method, timestamp, expiry, and confidence. Every displayed relationship can explain its evidence.

Represent devices, interfaces, networks, and their relationships. Distinguish interface membership, neighbor visibility, routing next hops, and confirmed physical links. A route or ARP entry does not establish physical cabling. Do not promise a complete physical network map from host agents alone; switch and hypervisor integrations may add that evidence later.

Merge identity evidence conservatively. DHCP reassignment, multiple interfaces, cloned machines, NAT, and overlapping private networks must not collapse unrelated devices. Scope addresses to their site/network context. Permit audited manual corrections.

## Data and connectivity

- Inventory and policy: transactional records with migrations and backups.
- Metrics: timestamped samples with explicit retention and bounded cardinality.
- Topology: expiring observations plus derived relationships.
- Secrets: encrypted records or external secret references, with separate key management.
- Audit: append-oriented security events without secret values.

Offline agents buffer a bounded amount of data, retry with backoff, and expose stale status. Ingestion handles duplicates and out-of-order samples. Server receipt time and agent observation time remain distinct.

## Agent lifecycle

The control server acts as the artifact source for agent upgrades. Agents receive desired-version assignments over their outbound control connection and fetch signed artifacts from the server, so they do not need internet access or inbound ports. Release acquisition supports both online synchronization and signed offline imports.

Agent identity survives upgrades. A constrained local updater verifies releases, stages replacements, and supports recovery and rollback. Manual, automatic, and pinned policies share this mechanism. See [agent updates](agent-updates.md) for the trust model and acceptance criteria.

## Self-hosting

The first deployment target is Docker Compose with persistent data, TLS setup, health checks, backup/restore instructions, and an explicit first-owner setup flow. A Linux VM on Proxmox VE can run the same deployment. Native Proxmox integration and LXC packaging are separate later decisions.

Specify sizing and supported operating systems only after measuring the implementation. No cloud account should be required for core monitoring.
