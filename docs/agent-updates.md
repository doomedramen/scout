# Agent updates

Agent updates are a core capability of Scout. Every supported agent must be updatable without direct internet access, provided it can reach its Scout control server. Automatic and owner-triggered updates use the same verified delivery mechanism.

## Delivery

The control server stores approved releases and serves their artifacts over authenticated TLS. An agent receives its desired version through its existing outbound connection, then downloads the corresponding artifact from the server. “Push an update” means assigning a desired version and notifying the agent; it does not require inbound connectivity to the machine.

An internet-connected server may synchronize releases from an explicitly configured release source. An isolated server supports importing a signed release bundle. Import must pass the same verification as online synchronization. Agents do not need to contact a public release service.

Initially support Linux artifacts for explicitly tested architectures. Each release includes a signed manifest binding the version, platform, architecture, artifact digest, size, and protocol compatibility. Publish a new immutable release when any content changes.

## Trust

Agents independently verify release signatures using trusted release-signing public keys, then verify the downloaded artifact against the signed manifest. TLS and a server-provided checksum alone are insufficient: a compromised server must not be able to invent executable releases.

Keep release-signing private keys outside the deployed control server. Design key rotation and revocation before shipping the updater. Record trusted key identifiers and define a recovery procedure for isolated deployments. Importing an artifact must not automatically trust a public key bundled with it.

A valid signature proves publisher authorization, not that a release is newer or safe to deploy. Track release sequence and compatibility, reject arbitrary downgrades and replayed update assignments, and permit only an explicitly authorized rollback to a locally recorded, previously verified release.

## Policy

Provide per-fleet policy with optional group and device overrides:

- Manual: an owner selects a release and assigns it to selected agents.
- Automatic: eligible approved releases deploy during a configured maintenance window.
- Pinned: the device remains on its selected version until the owner changes policy.

Use manual policy initially. Automatic policy is opt-in. Owners can pause a rollout globally, cap concurrency, and exclude individual devices. Offline devices remain queued and recheck current policy, assignment expiry, compatibility, and maintenance windows when they reconnect.

## Safe installation

1. Check disk capacity, platform, compatibility, current policy, and assignment validity.
2. Download into a restricted staging directory with size limits and bounded retries.
3. Verify signature and content before execution. Reject unexpected files, paths, links, or permissions if using an archive format.
4. Stage alongside the running binary. Preserve identity, configuration, and a verified previous binary.
5. Ask a narrowly privileged local updater to switch versions atomically and restart the service. Do not grant the ordinary monitoring agent general shell execution authority.
6. Require a local startup health check within a fixed deadline. Report version and update outcome to the server when connectivity permits.
7. Restore the previous verified binary on failed startup and report rollback. Bound rollback attempts to avoid restart loops.

Power loss at each stage must leave either the old or new verified installation recoverable. Failure to contact the server alone must not create a rollback loop. Keep configuration compatible across the supported rollback window; defer irreversible local migrations until rollback is no longer needed.

The updater itself needs a verified upgrade path. Define its privileges, ownership, installation layout, and recovery behavior as part of implementation. Container deployments update through their container orchestrator rather than mutating binaries inside running containers.

## Rollout and visibility

Roll out to a small canary group first, observe a defined health interval, and expand only when health criteria pass. Pause on failures above the configured threshold. Server and agent releases need a documented compatibility window so agents can update gradually.

Display current version, desired version, policy, last check, and update state. States include queued, downloading, verifying, installing, healthy, failed, rolled back, and paused. Preserve an audit trail showing who or which policy assigned a release and why an update failed.

## Acceptance criteria

- An agent with public internet access blocked updates successfully from its Scout server.
- A fully isolated server can import and distribute a valid signed bundle.
- Tampered artifacts, unknown signing keys, wrong architectures, incompatible releases, and unauthorized downgrades are rejected before execution.
- Interrupting download or installation preserves a recoverable installation.
- A release that fails its local startup check rolls back to the previous verified version.
- An offline agent reconnecting after a rollout is paused does not execute the stale assignment.
- Automatic updates respect maintenance windows, pins, concurrency limits, and rollout pause.
- Successful updates preserve agent identity and monitoring history.
