# Feature Specification: Scout Monitoring Platform

**Feature Branch**: `main` (specification directory is independent of the branch)

**Created**: 2026-09-11

**Status**: Specification and implementation handoff prepared; implementation incomplete. Planning defaults remain explicitly revisable.

**Input**: The owner wants a self-hosted server, network, and device monitor. One agent runs per device, automatically installs onto reachable eligible devices using supplied access, reports missing access, and contributes to a shared network map. Agents update automatically or on demand through the server, even without internet access. An extensible collector framework supports hypervisors, container runtimes, and other services. Linux comes first, then macOS, then Windows. The initial product has one owner and a polished, compact monitoring interface.

## User Scenarios & Testing _(mandatory)_

### User Story 1 - Establish a private monitoring workspace (Priority: P1)

As the owner, I can deploy Scout on my infrastructure, secure my account, bootstrap the first Linux agent, and see actual host health.

**Why this priority**: Reliable identity and real telemetry are the foundation for every other capability.

**Independent Test**: Deploy a fresh workspace and one Linux test host without any discovery, remote enrollment, or service integrations. Verify real metrics, restart persistence, and loss-of-contact behavior.

**Acceptance Scenarios**:

1. **Given** a fresh installation, **When** I complete protected first-owner setup, **Then** only that owner can access inventory, secrets, policies, and actions; a second setup attempt cannot replace the owner.
2. **Given** a valid one-time bootstrap invitation, **When** the first agent enrolls, **Then** exactly one device appears with an independently revocable identity and timestamped CPU, memory, filesystem, uptime, and interface measurements.
3. **Given** an enrolled device, **When** its connection stops, **Then** its data becomes stale and its agent becomes offline within the configured thresholds; retained values are not represented as current.
4. **Given** stored observations, **When** the server or agent restarts, **Then** identity and history remain associated with the same device.
5. **Given** an expired, reused, or revoked invitation or agent identity, **When** it is presented, **Then** access is denied and the attempt is recorded without disclosing the secret.

### User Story 2 - Grow coverage automatically (Priority: P1)

As the owner, I configure network scopes and access once. Scout discovers eligible machines, installs one agent on each, and continues discovering from those new vantage points without asking me to approve each device.

**Why this priority**: This is Scout's defining behavior and removes repetitive manual installation.

**Independent Test**: Use a controlled lab with one seed agent, two eligible Linux targets, an excluded target, and a target visible only from the second vantage point. Supply scoped access and verify expansion and boundaries.

**Acceptance Scenarios**:

1. **Given** an active scope and valid access and host trust, **When** an eligible target is discovered, **Then** installation begins automatically and the resulting agent contributes observations without per-device confirmation.
2. **Given** the same target observed by multiple agents or retried after interruption, **When** enrollment runs concurrently, **Then** only one active installation and one device agent result.
3. **Given** a new vantage point, **When** it reveals another eligible target inside an existing scope, **Then** coverage continues; discovering another subnet does not authorize that subnet.
4. **Given** an exclusion, paused enrollment, changed scope, expired job, or changed host identity, **When** a queued installation reaches execution, **Then** it is revalidated and blocked if no longer authorized.
5. **Given** an installation in progress, **When** enrollment is paused, **Then** no new installation starts; current operations stop at their next safe boundary and report their state without leaving an unrecoverable host.

### User Story 3 - Resolve missing access without spreading secrets (Priority: P1)

As the owner, I can see why Scout cannot monitor a device, provide the missing access, and let enrollment resume automatically.

**Why this priority**: Automatic installation must handle partial access without losing visibility or exposing credentials.

**Independent Test**: Use a discovered target with missing credentials, incorrect credentials, insufficient elevation, and an untrusted host identity in separate runs. Resolve each issue and verify the targeted retry.

**Acceptance Scenarios**:

1. **Given** a discovered device that cannot be enrolled, **When** Scout classifies the failure, **Then** it shows an actionable state such as needs credentials, needs privilege, needs host trust, unreachable, or unsupported, rather than silently retrying forever.
2. **Given** a missing-access request, **When** I assign valid access to its intended scope, **Then** eligible enrollment resumes automatically without entering credentials again on each device.
3. **Given** a stored credential, **When** I inspect settings or diagnostics, **Then** I see metadata and usage history but cannot retrieve its original value; ordinary agents cannot retrieve it either.
4. **Given** a changed or unknown host identity, **When** enrollment attempts to connect, **Then** it requires previously configured trust or owner resolution; it does not silently accept the identity to preserve automation.
5. **Given** credential revocation, **When** future jobs run, **Then** they cannot use the revoked credential. Existing agent monitoring continues using its own identity.

### User Story 4 - Understand health and relationships (Priority: P1)

As the owner, I can inspect systems quickly, open host charts, and explore a network map that explains where relationships came from.

**Why this priority**: Monitoring is useful only when the owner can trust and act on its presentation.

**Independent Test**: Load a deterministic dataset containing healthy, stale, offline, unmonitored, and ambiguous devices; complete inventory, chart, and topology journeys with a keyboard and pointer.

**Acceptance Scenarios**:

1. **Given** a populated workspace, **When** I filter by name, address, health, or monitoring state, **Then** matching systems remain visible with resource usage, last seen, and agent version, and I can open their history.
2. **Given** a selected time range, **When** observations are missing or a collector was unavailable, **Then** charts show gaps and units rather than invented zeroes or an uninterrupted healthy line.
3. **Given** a graph relationship, **When** I inspect it, **Then** its type, source, observation time, age, and confidence are visible; a logical relationship is not presented as confirmed physical cabling.
4. **Given** conflicting identity evidence or overlapping addresses in different sites, **When** observations are reconciled, **Then** unrelated devices are not silently merged, and manual corrections remain auditable.
5. **Given** loading, an empty workspace, a failed request, or demo mode, **When** the view renders, **Then** the state and appropriate next action are clear; demo observations never enter operational history or trigger actions.

### User Story 5 - Maintain agents across connected and isolated networks (Priority: P1)

As the owner, I can schedule automatic updates or assign a release manually, including when agents cannot reach the internet.

**Why this priority**: An automatically growing fleet needs a trustworthy, recoverable lifecycle from its first production release.

**Independent Test**: Block agent internet access, host a verified release on the server, and exercise successful upgrade, tampering, interrupted installation, startup failure, and rollback.

**Acceptance Scenarios**:

1. **Given** an agent that can reach only its Scout server, **When** I assign a compatible release, **Then** it obtains and independently verifies that release through the server, upgrades, and preserves identity and configuration.
2. **Given** an isolated server, **When** I import a valid signed release bundle, **Then** it can distribute the release without public internet access; unknown trust keys and invalid bundles are rejected.
3. **Given** automatic policy, **When** a release becomes eligible, **Then** canaries update first, subsequent batches respect maintenance windows and concurrency, and unhealthy rollout results pause expansion.
4. **Given** a pinned device, paused rollout, or revoked assignment, **When** an offline agent reconnects, **Then** it rechecks current policy instead of executing stale work.
5. **Given** a replacement that fails local startup checks or an interrupted switch, **When** recovery runs, **Then** a previously verified agent remains recoverable and rollback attempts are bounded. A server outage alone does not cause a rollback loop.
6. **Given** an unsigned, altered, incompatible, wrong-platform, or unauthorized older release, **When** an update is attempted, **Then** execution is rejected and the reason is visible without changing the running agent.

### User Story 6 - Understand services through extensible collectors (Priority: P2)

As the owner, I can see host services and their resources, including workloads managed by hypervisors and container runtimes. Additional service types can be supported later without redesigning Scout.

**Why this priority**: Device metrics alone cannot explain workload health or virtualized topology. P2 indicates sequencing, not exclusion from the target release.

**Independent Test**: Exercise two service types with representative fixtures or lab installations, then add a small test adapter for a third type without modifying common agent lifecycle behavior.

**Acceptance Scenarios**:

1. **Given** a supported local service, **When** it is detected, **Then** its collector reports availability and required access; detection does not grant privileges automatically.
2. **Given** permitted service access, **When** collection runs, **Then** workloads, status, available resource metrics, and host-to-workload relationships appear alongside host observations.
3. **Given** an unavailable, denied, malformed, or slow service, **When** its collector fails, **Then** its own status becomes actionable while host monitoring and unrelated collectors continue.
4. **Given** a hypervisor-reported guest and a guest agent, **When** evidence establishes that they are the same device, **Then** Scout associates both sources without double-counting the guest or installing a second agent.
5. **Given** a new service adapter, **When** it is added and upgraded, **Then** it uses the existing configuration, permission, health, and lifecycle mechanisms. Docker and Proxmox are not special cases required by every agent.

### User Story 7 - Operate and recover the self-hosted system (Priority: P1)

As the owner, I can back up Scout, restore it, manage retention, and understand outages without depending on a cloud service.

**Why this priority**: Loss of the control plane must not destroy monitoring identity or expose secrets.

**Independent Test**: Back up a populated test workspace, restore to a clean host using the documented key recovery procedure, and reconnect existing agents.

**Acceptance Scenarios**:

1. **Given** a fresh host meeting documented prerequisites, **When** I follow the deployment guide, **Then** persistent storage and secure access are established without default credentials or a cloud account.
2. **Given** a backup and separately protected recovery material, **When** I restore Scout, **Then** device identities, configured policy, retained history, and usable credential references are restored; missing keys produce an explicit recovery failure.
3. **Given** a control-plane outage, **When** agents cannot submit observations, **Then** bounded buffering and backoff apply and any discarded observations are reported after reconnection.
4. **Given** a stale backup, **When** the server restarts after restoration, **Then** enrollment and update execution remain paused until the owner reconciles potentially outdated policies, credentials, and revocations.

### User Story 8 - Retire or exclude a device (Priority: P2)

As the owner, I can stop managing a machine without Scout immediately reinstalling its agent.

**Why this priority**: Automatic enrollment needs an equally clear way to remove authority.

**Independent Test**: Decommission a device, rediscover it, then explicitly re-enable it and verify a new authorized enrollment.

**Acceptance Scenarios**:

1. **Given** a managed device, **When** I decommission it, **Then** its identity is revoked, new jobs are stopped, and an exclusion prevents automatic re-enrollment; retained history follows retention policy.
2. **Given** an offline decommissioned device, **When** it reconnects, **Then** it cannot submit as its revoked identity. The UI does not falsely report that remote software removal succeeded.
3. **Given** owner-requested uninstall with valid access, **When** removal completes, **Then** the service and Scout binaries are removed and the outcome is recorded. Re-enabling monitoring is an explicit owner action.

### Edge Cases

- Cloned machine identities, DHCP changes, multiple interfaces, NAT, IPv6, and overlapping site ranges require conservative reconciliation, not IP-only identity.
- Unsupported devices remain visible as unmonitored candidates; Scout does not attempt arbitrary installers, password guessing, exploits, or host privilege escalation beyond supplied authorization.
- Credential changes, exclusions, DNS changes, and pauses between scheduling and execution invalidate affected work before connection or installation.
- A newly discovered target may be reachable from an agent but unable to reach the server; report a connectivity prerequisite, not successful enrollment.
- Low disk space, read-only filesystems, interrupted writes, reboot, and incompatible service managers must leave explicit failure states and bounded recovery.
- Duplicate, delayed, out-of-order, or future-dated samples must not create false freshness; report clock skew and preserve observation versus receipt time.
- Discovery storms, rapid workload churn, and hostile payloads must be limited without losing base host health reporting.
- A denied Docker connection is not “no containers”; a read-only filesystem mount does not establish read-only service permissions.
- A Proxmox or other hypervisor observation does not authorize guest installation. Cluster observations require coordinated ownership to avoid duplicate collection.
- Certificate expiry, lost recovery keys, restored old secrets, and release-key rotation must have documented recovery paths that do not disable identity verification.

## Requirements _(mandatory)_

### Functional Requirements

#### Workspace and host monitoring

- **FR-001**: Scout MUST support one authenticated owner with protected one-time setup, sign-in, sign-out, session expiry, second-factor protection for credential management and remote enrollment, and a documented owner recovery process.
- **FR-002**: Scout MUST bootstrap a first Linux agent using expiring, single-use authorization and issue a unique, renewable, revocable identity; invitations and identities MUST NOT be interchangeable between devices.
- **FR-003**: Scout MUST maintain one active agent per device across retries, reboot, reconnection, upgrades, and identity reconciliation.
- **FR-004**: Agents MUST report available CPU utilization, CPU count, memory usage/capacity, filesystem usage/capacity, uptime, interface addresses, and per-interface traffic rates with units and observation times. Unsupported measurements MUST be marked unavailable.
- **FR-005**: Scout MUST retain history, distinguish freshness from health, expose last seen and collector availability, and transition to stale/offline according to configurable thresholds.
- **FR-006**: All non-public monitoring data and control actions MUST require authentication and authorization. Transport MUST verify both endpoints' identities where applicable. A device MUST NOT impersonate another device or owner.

#### Discovery, access, and automatic enrollment

- **FR-007**: The owner MUST be able to define site-scoped address ranges, exclusions, permitted discovery methods, rate/concurrency limits, assigned access, and active/paused state.
- **FR-008**: Agents MUST contribute local interface, route, and neighbor observations where permitted and perform only policy-authorized active discovery. New observations MUST NOT expand authorization.
- **FR-009**: Scout MUST automatically install an agent on each eligible target with sufficient assigned access and trusted host identity, then continue coverage from new agents without routine per-device approval.
- **FR-010**: Scout MUST deduplicate and serialize installation for a target, revalidate policy and resolved destination at execution, bound retries, and record progress and recoverable failures.
- **FR-011**: Scout MUST retain inaccessible and unsupported candidates and show a specific missing-access or connectivity reason; assigning valid access MUST resume eligible work automatically.
- **FR-012**: Credentials MUST be scoped, encrypted in storage, masked after entry, rotatable, revocable, and audited. Their original values MUST NOT appear in ordinary agent data, browser read responses, logs, telemetry, or process arguments. Storage keys MUST be protected separately from stored secrets.
- **FR-013**: Enrollment MUST validate host identity against owner-established trust and verify the installation artifact. Unknown or changed identities MUST block installation until trust is resolved; supplied authorization MUST bound elevated operations.
- **FR-014**: The owner MUST be able to pause discovery and enrollment globally or per scope, exclude a target, and revoke access; queued and reconnecting work MUST honor the current state.

#### Inventory, history, and topology

- **FR-015**: Inventory MUST support filtering by identity, address, health, and monitoring state; host detail MUST show current measurements, history with selectable time ranges and units, agent version, last seen, and service collector state.
- **FR-016**: Each topology relationship MUST expose its type, source, observation time, expiry, and confidence. Logical, inferred, and confirmed physical relationships MUST be distinguishable.
- **FR-017**: Device reconciliation MUST use contextual evidence beyond address alone, preserve multiple observation sources, flag ambiguous identities, and audit owner corrections.
- **FR-018**: The interface MUST provide readable compact system rows and charts, a separate network view, keyboard navigation, visible focus, non-color status labels, reduced-motion support, and usable layouts from 360 to 1440 CSS pixels.
- **FR-019**: Loading, empty, failed, stale, offline, unsupported, and missing-access states MUST be distinguishable. Demo mode MUST be visibly labeled and isolated from operational storage and actions.

#### Agent updates

- **FR-020**: Every supported native agent MUST support updates delivered from its server without direct public internet access or new inbound connectivity to the device.
- **FR-021**: The owner MUST be able to import verified release bundles into an isolated server and select eligible releases for distribution; importing MUST NOT automatically trust included signing keys.
- **FR-022**: Agents MUST independently verify publisher authorization, artifact integrity, intended platform, compatibility, and downgrade policy before execution. A compromised distribution server alone MUST NOT be sufficient to forge a trusted release.
- **FR-023**: Updates MUST support manual assignment, opt-in automatic policy, device pins, maintenance windows, canary groups, concurrency limits, pause, and failure-based rollout stops. Reconnecting agents MUST revalidate assignments.
- **FR-024**: Updates MUST preserve identity/configuration, stage recoverably, check local startup, and support bounded rollback to a previously verified version. A missing server connection alone MUST NOT create restart or rollback loops.
- **FR-025**: The owner MUST see current and desired version, policy, last check, rollout state, failure/rollback reasons, and actor or policy responsible for each assignment. Release trust rotation and revocation MUST have documented online and isolated recovery procedures.

#### Extensible service knowledge

- **FR-026**: Agents MUST provide a common extension contract for service detection, permission declaration, configuration, scheduling, health, and bounded collection across hypervisors, container runtimes, and other services.
- **FR-027**: Service collectors MUST report supported entities, available metrics, lifecycle/health state, and relationships using versioned, provider-identifiable observations; new service types MUST NOT require replacement of agent identity, enrollment, or update mechanisms.
- **FR-028**: Collectors MUST distinguish absent, detected, needs access, enabled, degraded, and disabled states. Slow, failing, malformed, or incompatible service responses MUST NOT stop unrelated collection.
- **FR-029**: Service access MUST be least-privilege and separate from enrollment access. Detection MUST NOT grant privileges. Collection MUST exclude service credentials, workload environment secrets, and unrequested logs or arbitrary inspection payloads.
- **FR-030**: Scout MUST reconcile guest/container observations with device agents when evidence permits, preserve source provenance, and coordinate cluster-level collectors to avoid duplicate work and double-counting.

#### Operations and retirement

- **FR-031**: Core monitoring MUST run entirely on owner-controlled infrastructure without a hosted account. The initial release MUST have a documented persistent container deployment and Linux VM deployment path.
- **FR-032**: Backup and restore MUST cover inventory, retained observations, owner identity, policies, and credential references, with separately protected key recovery. Restored installations MUST pause enrollment and updates until stale authorization state is reconciled.
- **FR-033**: Scout MUST bound queues, buffering, ingestion size/rate, collector execution, discovery, retention, and label/entity growth. Drops and partial collection MUST be observable rather than silently treated as success.
- **FR-034**: Security and control-plane events MUST record time, actor, target, action, and outcome without secret values, including setup, credential access, scope changes, enrollment, release assignment, revocation, and reconciliation.
- **FR-035**: Decommissioning MUST revoke the device identity, prevent new jobs and automatic re-enrollment, retain history according to policy, and distinguish identity revocation from confirmed uninstall.
- **FR-036**: Scout MUST publish supported host/platform versions, upgrade compatibility, privileges, retention behavior, and recovery instructions for each release. Unavailable platforms and integrations MUST NOT be advertised as implemented.

### Key Entities _(include if feature involves data)_

- **Owner**: The initial workspace's sole administrator, authentication factors, active sessions, and recovery status.
- **Site and scope**: Address context, permitted operations, exclusions, limits, assigned access, and policy revision.
- **Device**: Stable inventory identity, contextual addresses, evidence, lifecycle state, and any active agent association.
- **Agent identity**: A device-bound credential, capabilities, validity, revocation status, and installed version.
- **Observation**: Source, observed/received times, availability, measurement or relationship, and expiry.
- **Relationship**: Typed association between devices, interfaces, networks, or service entities, backed by observations.
- **Access reference and request**: Protected credential reference, allowed target/use, trust status, missing prerequisite, and resolution history.
- **Enrollment job**: Target, authorization revision, release, execution lease, progress, attempts, and outcome.
- **Release and rollout**: Verified artifact identity, compatibility, trust, desired versions, targets, policy, and health decisions.
- **Collector and service entity**: Provider identity/version, required permission, collection health, workload identity, and owning host/cluster.
- **Audit event**: Actor, target, action, time, and redacted outcome.
- **Recovery snapshot**: Backup scope, creation time, version, separate key requirements, and restored authorization reconciliation state.

### State and terminology rules

- **Eligible** means a supported device inside an active authorized scope, not excluded or already managed, with sufficient assigned access, trusted host identity, and required connectivity. Being reachable alone is insufficient.
- **Coverage** distinguishes discovered devices, enrolled devices, devices needing access, unsupported devices, and excluded devices. A container or VM observation is not automatically a separate installed agent.
- **Availability** describes agent contact: connecting, online, offline, or revoked. **Freshness** describes each measurement: current, stale, missing, or unsupported. A fresh heartbeat does not refresh an old CPU sample.
- **Health** describes reported conditions and collector failures. “Healthy” requires current supported observations and no reported fault in that displayed scope; missing or unsupported checks cannot be counted as passed. An online host does not imply healthy guests or services.
- **Enrollment** progresses through discovered, needs access/trust/connectivity, queued, connecting, installing, verifying, and enrolled, with explicit failed, paused, excluded, and unsupported outcomes. Successful file transfer alone is not successful enrollment; the agent must authenticate and report.
- **Updates** progress through queued, downloading, verifying, installing, checking startup, and healthy, with explicit failed, rolled back, paused, and pinned states. Desired version is never presented as installed before confirmation.
- **Decommissioned** devices retain an exclusion independent of historical metric retention. Expiring old measurements must not remove the exclusion or resurrect revoked authority.

## Success Criteria _(mandatory)_

### Measurable Outcomes

These are proposed acceptance targets for the target release, not claims about the scaffold. Use the reference conditions in Assumptions and record the actual hardware and versions with results.

- **SC-001**: An owner following the guide can deploy a clean workspace and see the first host's real measurements within 15 minutes once prerequisites and host access are available.
- **SC-002**: With a 15-second reporting interval, 95% of host observations appear within 30 seconds of collection. Missing data is marked stale by 45 seconds and the agent offline by 90 seconds after its last accepted heartbeat under the default policy.
- **SC-003**: In a 10-target lab, every eligible, reachable target with sufficient trusted access enrolls within 10 minutes of discovery, including at least one target discovered by a newly enrolled agent. Excluded targets receive zero installation attempts.
- **SC-004**: Repeating enrollment with 20 concurrent duplicate observations and an interrupted installation produces one active agent per device, with bounded retry and no secret leakage in captured outputs.
- **SC-005**: Resolving missing access resumes eligible work within one minute. Pausing enrollment prevents any new installation from starting after the pause is acknowledged.
- **SC-006**: In a reference workspace with 100 devices and 24 hours of measurements, 95% of inventory filters, host-history views, and relationship inspections complete within two seconds on the local network.
- **SC-007**: All displayed relationship fixtures expose provenance and age; all missing-data fixtures show gaps/unavailability. Keyboard-only users can filter, open a host, inspect chart values, and navigate topology without a pointer.
- **SC-008**: An internet-blocked agent upgrades successfully through its server, and an isolated server distributes an imported bundle. Every tampered, wrong-platform, incompatible, unknown-key, or unauthorized downgrade case is rejected before execution.
- **SC-009**: Failed startup restores a verified working agent within two minutes on the reference host; process termination at each update stage leaves a documented recovery path without identity loss.
- **SC-010**: A third test service adapter can be added without changing common identity, enrollment, transport, or update behavior. Failure and permission-denial fixtures for each reference adapter leave host collection running on schedule.
- **SC-011**: A reference backup restores retained identity, policy, and measurements to a clean installation within 30 minutes. Agents reconnect without duplication; enrollment and update execution remain paused until owner reconciliation.
- **SC-012**: Unauthenticated access, cross-device impersonation, replay, revoked access, escaped scope, changed host trust, and credential-exposure tests all fail closed. Decommissioned devices remain excluded through rediscovery and reconnect attempts.

## Assumptions

### Confirmed scope

- One agent per eligible device; automatic enrollment within configured scopes; inaccessible targets return to the owner. The owner manually bootstraps only the first agent or another required network vantage point.
- Linux first, macOS second, Windows third. One owner initially. Automatic and manual server-delivered updates and extensible service knowledge are core requirements.
- The supplied interface references define compact dark inventory and host-chart direction. Technology choices are recorded in the constitution and existing decision notes rather than prescribed here.

### Proposed defaults for planning

These defaults close gaps without pretending the owner supplied capacity or timing figures. They are revisable in technical planning; changes must update the corresponding acceptance criteria.

- Reference lab: one owner, up to 100 devices for load tests, ordinary wired local-network latency, Linux AMD64 and ARM64 agents. Provision a dedicated 4-vCPU/8-GiB control host and SSD storage; document actual load and storage sizing in the plan. This is not a supported fleet-size ceiling.
- Initial native host support: systemd-based Linux distributions selected and versioned during planning. Devices outside the published matrix remain visible but unmonitored.
- Default host reporting: 15 seconds; stale after 45 seconds; offline after 90 seconds. Measurements and heartbeat freshness remain separate.
- Default raw measurement retention: 30 days; redacted audit history: 90 days; bounded agent buffer: at most one hour or 64 MiB, whichever is reached first. Report dropped samples. The plan must size these policies and describe disk-pressure behavior.
- Native-agent updates default to manual until the owner enables automatic rollout. Automatic enrollment starts as soon as a scope, sufficient access, and host trust are configured; it is not a per-device approval workflow.
- Remote installation initially uses owner-provided SSH access. A trusted execution location must reach the target, and the target must reach the control server. An ordinary agent does not automatically become a privileged credential relay.
- Owner-established host trust can be imported or resolved as a missing-access condition. Automatic unknown-host acceptance is not a default.
- Docker and Proxmox are proposed reference collectors to validate two service categories, not a fixed provider list or a restriction on future adapters. Initial service collection is read-only in purpose; the owner sees any access that also permits administration.
- The server may acquire releases online or through offline import. Isolated agents still need server connectivity; completely disconnected devices cannot receive a pushed update.

### Release boundaries and dependencies

All eight stories describe the target Linux release; independently testable slices may ship as explicitly incomplete development milestones. The current scaffold satisfies neither the production security baseline nor these end-to-end outcomes.

Later scope: macOS and Windows agents, multi-user/customer isolation, additional providers, external notification delivery, advanced alert rules, automatic remediation, high-availability control planes, arbitrary executable plugins, and physical-topology guarantees without supporting device evidence. Container-packaged agent updates belong to their orchestrator rather than self-modifying a running container.

Operational dependencies include owner-authorized network scopes, reachable endpoints, sufficient host privileges, trusted release artifacts, protected recovery keys, and permission to access each service. The owner need not grant all of these for basic host monitoring to work.
