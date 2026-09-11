# Release and Collector Contracts

## Signed native release envelope

The publisher signs the exact UTF-8 manifest byte sequence with Ed25519. Reject duplicate JSON keys, unsupported manifest versions, trailing data, non-finite values and ambiguous fields after signature verification. Verification must use the raw bytes, not a reserialized object.

Required manifest fields: `manifestVersion=1`, `releaseId`, display `version`, monotonic `generation`, `platform`, `architecture`, `sha256`, `sizeBytes`, `protocolMin`, `protocolMax`, `configReadMin`, `configReadMax`, `signingKeyId`, `issuedAt`, and `component` (agent or updater). A detached signature accompanies the manifest. Trust comes from the installed trust store, not a key in the bundle. Maximum manifest 64 KiB; default artifact cap 256 MiB. An offline bundle holds manifest, signature and one raw artifact per declared component/platform; server import must reject traversal, links, duplicate names, decompression bombs, and undeclared content if archived.

Distribution/rollout policy and publisher authenticity are separate checks. A current server assignment binds device, release digest, generation and expiry. The local updater tracks highest trusted generation, current slot, previous verified slot, attempt and recovery state. Only its recorded previous slot is eligible for automatic rollback; a signed older artifact from a server is not equivalent to that rollback authorization.

Trust transitions have a monotonically increasing trust generation and signatures from already trusted keys, declaring replacement/revoked key IDs. A device unable to obtain fresh revocation information cannot know about an unseen revocation; surface last trust refresh and do not claim immediate global revocation for disconnected agents. Total loss of signing authority requires an owner-run local recovery package/process, never automatic trust of an unsigned replacement.

## Local installation/update boundary

Persistent agent identity and config: `/var/lib/scout/agent` and `/etc/scout/agent`; private files 0600, owned by the dedicated agent account. Versioned executables and guardian: `/opt/scout`, root-owned and not writable by the agent. Staging, journal, slot switching, directory fsync, service restart and recovery belong to the guardian. Use an authenticated local socket with peer-credential checks for structured update requests; no arbitrary path or shell command arguments. Re-verify staged digest/ownership immediately before switching to prevent time-of-check/time-of-use replacement.

Expose local health readiness to the guardian without granting remote management authority. A successful readiness check must verify the process/version/config loaded, not merely that a PID exists. Guardian startup reconciles unfinished journal entries. The guardian's own update must have a separately executable previous launcher so a broken guardian does not remove recovery.

## Collector extension boundary

The common descriptor contains `id`, `schemaVersion`, supported platforms, declared entity kinds and metrics/units, configuration schema, permission descriptors, default interval, timeout, and entity/label limits.

Lifecycle:

1. Detect using local/configured endpoints within the permitted budget; return unavailable, detected, or needs access with safe diagnostics.
2. Configure only schema-valid non-secret settings and narrow credential references; never generic executable paths or remote code.
3. Collect with context cancellation and a deadline, no concurrent invocation of the same collector, and bounded output. Return versioned entities, samples and relationships with provenance.
4. Report enabled/degraded/disabled state independently of host collection. Close releases resources and revokes any transient grants.

Core owns scheduling, credential mediation, telemetry transport, logging/redaction and updates. Adapters own provider detection, API translation and permission descriptions. A provider field cannot redefine device identity, job authorization, or release trust. Built-in adapters ship through signed agent releases; untrusted third-party executable plugins are out of scope.

Service IDs are `(provider, cluster-or-host namespace, external ID)`. Container runtime metrics include workload state, CPU/memory/network/block counters where supported; do not collect environment variables or logs. Hypervisors include nodes, guests, storage and relationships; cluster collector leases prevent duplicate polling. Required privileges must be tested per declared provider version and listed in the UI and documentation.
