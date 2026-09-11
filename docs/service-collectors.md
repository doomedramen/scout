# Service-aware collectors

Scout agents understand host services as well as host resources. The framework
supports additional hypervisors, container runtimes, storage systems, and other
services through independent adapters. Docker and Proxmox VE are reference
adapters, not an exhaustive list. The host collector remains independent from
these adapters, and the control UI exposes collector descriptors, health, and
service entities.

## Extension model

Keep one agent process per device. Compile first-party collectors into that agent behind a common lifecycle: detect, describe required access, collect with a deadline, report health, and stop. Do not load arbitrary executable plugins received from peers or the control server.

Detection and permission are separate. A collector reports one of: unavailable, detected, needs access, enabled, degraded, or disabled. Detecting a service must not silently grant its collector additional privileges. A failure in a Docker or Proxmox collector must not stop host monitoring.

Each collector declares its identifier and schema version, supported platforms, required permissions, collection interval, timeout, and resource limits. Its output contains source identity, observation time, typed metrics, relationships, and a sanitized diagnostic. Limit entity counts, payload sizes, retries, and metric cardinality. Prefer service APIs to parsing command output.

Keep agent core independent of vendor-specific API types. Use common resource concepts (host, virtual machine, container, storage pool, service) with namespaced provider extensions. A registry selects adapters and a common scheduler runs them. Adding an adapter must not require changes to enrollment, agent identity, update delivery, or base telemetry transport. Version collector schemas so agents and servers can evolve independently; unknown extensions must not break core ingestion. Package initial adapters with signed agent releases rather than introducing a separate executable plugin distribution path prematurely.

## Docker

Detect a configured local Docker endpoint and collect container identity, image reference, lifecycle state, health, CPU, memory, network I/O, and block I/O where available. Report host-to-container relationships and distinguish absent counters from zero activity. Avoid copying container environment variables, secret labels, logs, or arbitrary inspection payloads by default.

Docker daemon access can grant host administration authority. Mounting its Unix socket read-only does not make its API read-only. Prefer a restricted local API proxy with an explicit allowlist of necessary read endpoints; document direct-socket access as privileged. Do not expose the daemon over an unauthenticated network endpoint.

## Proxmox VE

Collect node health, VM and LXC status, resource usage, storage capacity, and node-to-guest relationships through the Proxmox API. Use a dedicated API token with the minimum tested read permissions and validated TLS. Do not request administration privileges merely to read monitoring data.

An agent installed on a Proxmox host can report its local host metrics and permitted platform observations. Reading VM state is not authority to install software inside guests. Automatic guest enrollment still needs a configured installation scope and appropriate guest access.

Coordinate cluster-level collection through one assigned collector with failover, avoiding duplicate polling by every node. Preserve both platform guest identity and guest agent identity so Scout can reconcile them without counting each VM twice.

## Credential boundary

Remote enrollment secrets remain in the enrollment service. A service collector may require its own narrow API credential; treat this as a separate credential class, scoped to the collector, endpoint, and operations. Prefer local provisioning or short-lived, revocable delivery over sharing a fleet-wide secret. Protect any necessary local credential file with restrictive ownership and permissions and exclude it from diagnostics.

## Current verification boundary

- Fixture coverage verifies that a host without Docker or Proxmox continues
  normal monitoring; inaccessible, malformed, slow, and over-limit adapters
  become degraded without blocking other collectors.
- Docker and Proxmox adapter fixtures verify bounded, typed, redacted entity
  translation and TLS/permission boundaries. A live provider version and ACL
  claim requires the owner-authorized lab recorded in docs/support-matrix.md.
- The lease and service-association fixtures verify epoch fencing, exact
  site-local address association, and an unassociated outcome when evidence is
  ambiguous.
- Removing collector access is represented by configuration and health state;
  live agent provisioning remains an acceptance gap.
