# Scout Product Specification

**Authority**: This is Scout's sole product specification and supersedes every previous feature specification, plan, task list, evidence ledger, and ticket.

**Created**: 2026-09-13

**Status**: Definitive v1 goal

**Input**: "You install Scout, you log in, and you immediately start seeing all servers with SSH, and other supported ways to get an agent into a server. You see potential places to install agents and servers already running agents. Providing access causes Scout to install the agent. Configuration should be minimal and setup should be easy."

## Product promise

Scout is a self-hosted server monitor that grows its own coverage. An owner installs Scout, signs in, sees useful systems discovered on the local network, supplies access where needed, and watches those systems become monitored without manually installing an agent on each host.

Scout's activation moment is not account creation or defining a scope. It is the first discovered host becoming a monitored system with real health data.

## Product principles

1. **Automation is the default**: Discovery starts automatically, valid access leads to installation automatically, and enrolled agents begin reporting automatically.
2. **Systems are the primary object**: Discovery candidates, installation state, access needs, and monitoring data are different states of one system—not separate products or queues.
3. **One agent per device**: Repeated scans, retries, restarts, and observations from several vantage points must converge on one stable system and one active agent identity.
4. **Progressive disclosure**: The normal journey exposes only the next useful action. Network boundaries, credentials, trust, jobs, updates, and recovery remain available without dominating the interface.
5. **Truth before decoration**: Scout shows real evidence, timestamps, gaps, errors, and stale states. It never passes demo data or an address-space size off as monitored or actionable systems.
6. **Secure automation**: Discovery does not grant installation authority. Credentials remain encrypted and scoped, host identity is verified, and every agent has an independently revocable identity.
7. **Recoverable operations**: Installation, restart, update, outage, and decommission paths must preserve or deliberately revoke identity without creating duplicates or unrecoverable hosts.
8. **Evolve the existing product**: This reset simplifies and completes the current application. It does not authorize re-scaffolding, a stack migration, or replacement with another prototype.

## Minimum path to value

The first session has one goal: monitor a real system.

1. Install Scout using the documented self-hosted deployment.
2. Create the first owner account, or sign in when an owner already exists.
3. Land on Systems while bounded local discovery starts automatically.
4. See only hosts with a supported, currently observed access method as potential systems.
5. Open a system marked **Needs access**.
6. Enter the fields required by the detected method and confirm the observed host identity in the same flow.
7. Submit once. Scout stores the credential, installs and enrolls the agent, and shows progress in place.
8. See the same system become **Online** with real health data.

If Scout cannot safely infer one bounded local network, step 3 becomes one inline network-range field. No separate setup wizard or settings detour is permitted.

## Information architecture

### Primary surfaces

- **Systems**: The default signed-in surface. It combines a compact fleet summary, automatic discovery status, potential systems, installing systems, monitored systems, and systems needing attention.
- **System detail**: Identity, addresses, detected access methods, credential/trust action when needed, installation progress, current health, history, services, and system-specific operations.
- **Network**: An evidence-backed topology view available after systems exist. It is not required for setup or enrollment.
- **Settings**: Consolidated owner, network-boundary, credential, update, backup/recovery, and diagnostic controls.
- **Add system**: A `+` action containing manual agent installation and explicit target fallbacks. It is not the default onboarding path.

### Concepts that are not primary destinations

Access requests, trust records, enrollment jobs, scan scopes, hardware, storage, and service collectors must not appear as separate top-level destinations. Their relevant state belongs to a system; fleet-wide administration belongs in Settings.

There is no separate overview page whose only purpose is to link to Systems. Systems is the overview.

## User scenarios and acceptance

### User Story 1 - Install and enter Scout (Priority: P1)

As the owner, I can start Scout with a copyable deployment, create my account once, and return to a normal sign-in screen thereafter.

**Independent Test**: Deploy a clean instance with persistent storage, complete owner setup, restart it, and sign in again without editing application files.

**Acceptance Scenarios**:

1. **Given** a clean installation with no owner, **When** the local setup path is opened, **Then** Scout asks for the one-time setup token and a valid owner password.
2. **Given** an owner already exists, **When** Scout is opened, **Then** the default authentication screen is **Sign in to Scout**, not owner setup.
3. **Given** a valid password of at least eight characters containing upper-case and lower-case letters and a number, **When** the owner creates the account or signs in, **Then** Scout accepts it.
4. **Given** the owner restarts or updates Scout, **When** the service returns, **Then** account, configuration, identity, history, and encrypted credentials remain intact.
5. **Given** the control service cannot start, **When** the web process or container is checked, **Then** the deployment reports unhealthy rather than serving a shell that fails every action.

### User Story 2 - See useful local systems automatically (Priority: P1)

As the owner, I sign in and immediately see Scout discovering hosts that expose SSH or another supported agent-installation method.

**Independent Test**: In a controlled 256-address network containing a few reachable hosts and one SSH target, sign in to a fresh Scout instance and observe the target appear without manually creating a site, scope, or scan.

**Acceptance Scenarios**:

1. **Given** one safely inferable local private network, **When** the owner first signs in, **Then** bounded discovery starts automatically and its progress is visible on Systems.
2. **Given** no safely inferable boundary, **When** the owner first signs in, **Then** Scout asks for one network range inline and starts discovery after it is supplied.
3. **Given** a host with a supported open access method, **When** it is observed, **Then** one potential system appears with address, detected method, identity evidence, and last-seen time.
4. **Given** an address with no supported open access method, **When** it is scanned, **Then** it is not shown or counted as needing access.
5. **Given** the same host is seen repeatedly or Scout restarts, **When** discovery reconciles the evidence, **Then** one stable system remains.
6. **Given** the owner pauses discovery, changes the boundary, or excludes a host, **When** work is next evaluated, **Then** no new out-of-policy probe or installation starts.

### User Story 3 - Turn a discovered host into a monitored system (Priority: P1)

As the owner, I open a potential system, provide access, and Scout installs and enrolls the agent without requiring me to run a command on that host.

**Independent Test**: Discover an owner-authorized disposable Linux SSH host, submit valid username and password access from that system, and verify automatic installation, enrollment, and first telemetry.

**Acceptance Scenarios**:

1. **Given** SSH was detected, **When** the system is opened, **Then** Scout asks for username and password by default and offers private-key authentication without showing unrelated fields.
2. **Given** the SSH host identity is not yet trusted, **When** access is supplied, **Then** Scout presents the observed fingerprint for confirmation inside the same flow.
3. **Given** valid scoped access and confirmed identity, **When** the owner submits once, **Then** Scout stores the secret securely, starts installation, and displays understandable progress on that system.
4. **Given** installation succeeds, **When** the agent enrolls, **Then** the candidate becomes the same monitored system with exactly one active agent identity.
5. **Given** invalid credentials, insufficient privilege, changed host identity, unsupported architecture, unreachable server, or an installation failure, **When** enrollment stops, **Then** Scout names the blocker and provides one targeted retry or resolution action.
6. **Given** any access submission, **When** it succeeds, fails, or times out, **Then** its busy state ends within a bounded time and an accessible success or error message is shown.
7. **Given** credentials were accepted but a follow-up refresh fails, **When** the UI recovers, **Then** it says the credential was stored and allows a state refresh; it never asks the owner to submit the secret blindly again.

### User Story 4 - Monitor real host health (Priority: P1)

As the owner, I can tell which systems are healthy, offline, stale, installing, or blocked and inspect current and historical host data from each system.

**Independent Test**: Enroll one disposable Linux agent, observe real metrics, interrupt reporting, recover it, and verify truthful list and detail states throughout.

**Acceptance Scenarios**:

1. **Given** an enrolled agent sends its first observations, **When** Systems refreshes, **Then** the existing system becomes Online and shows useful current health within seconds.
2. **Given** a monitored system, **When** it is opened, **Then** CPU, memory, filesystem, uptime, and interface data are available with units, source time, and history where supported.
3. **Given** data is loading, absent, stale, unsupported, or interrupted, **When** the interface renders, **Then** it names that state and does not display fabricated values or misleading zeroes.
4. **Given** an agent loses contact, **When** the offline threshold passes, **Then** the same system becomes Offline and preserves its last trustworthy observation time.
5. **Given** a local service collector fails, **When** base host collection continues, **Then** host monitoring remains available and only that service reports a bounded, actionable failure.

### User Story 5 - Grow and maintain coverage safely (Priority: P2)

As the owner, I can reuse suitable access deliberately, discover from enrolled agents, update agents through Scout, and decommission systems without losing control of the fleet.

**Independent Test**: In an isolated two-network lab, enroll one host, use it as a bounded discovery vantage point, deliver a signed update without agent internet access, and revoke the agent.

**Acceptance Scenarios**:

1. **Given** the owner explicitly allows a credential for a bounded network, **When** another eligible host is found there, **Then** Scout can enroll it automatically without asking for the same secret per host.
2. **Given** an enrolled agent can reach an approved network the server cannot, **When** scanning is assigned, **Then** it reports supported entry-point evidence without receiving reusable enrollment credentials.
3. **Given** an agent cannot access the public internet, **When** a compatible signed release is assigned, **Then** it receives and independently verifies that release through Scout.
4. **Given** an update is interrupted, invalid, incompatible, or unhealthy, **When** recovery runs, **Then** the previous verified agent remains recoverable and rollout does not continue blindly.
5. **Given** a device is decommissioned, **When** the old identity reconnects, **Then** it is rejected and automatic discovery does not immediately reinstall it unless the owner explicitly re-enables that system.

### User Story 6 - Operate Scout without specialist knowledge (Priority: P2)

As the owner, I can update, back up, restore, diagnose, and change Scout's exposed port using documented commands that preserve my data and configuration.

**Independent Test**: Run clean deployment, alternate-port, update, backup, restore, and failed-control-service exercises using disposable data.

**Acceptance Scenarios**:

1. **Given** the default host port is unavailable, **When** the owner selects another exposed port in one place, **Then** Scout remains internally consistent without several coordinated edits.
2. **Given** a supported existing deployment, **When** the documented update command runs, **Then** it obtains the current deployment definition and images while preserving owner configuration and persistent data.
3. **Given** a valid backup and separately protected recovery material, **When** Scout is restored cleanly, **Then** systems, identities, history, policies, and usable encrypted credentials return in a safe recovery state.
4. **Given** an operation fails, **When** the owner inspects status and logs, **Then** the failure identifies the unavailable component and a concrete recovery action without exposing secrets.

## Functional requirements

### Deployment and authentication

- **FR-001**: Scout MUST provide one documented, copyable self-hosted deployment with persistent database and server data.
- **FR-002**: Changing the externally exposed web port MUST require changing one owner-facing value only; internal listeners and generated public links MUST remain correct.
- **FR-003**: The normal update path MUST refresh both the deployment definition and application images without deleting owner data or silently replacing owner configuration.
- **FR-004**: A fresh instance MUST allow exactly one owner to be created with a local, expiring setup secret; later visits MUST default to sign-in.
- **FR-005**: Owner passwords MUST be at least eight characters and include at least one upper-case letter, one lower-case letter, and one number.
- **FR-006**: Development mode MAY bypass recent multi-factor confirmation for sensitive actions while preserving authentication and request-forgery protection. Production mode MUST retain the configured sensitive-action protections.
- **FR-007**: Deployment health MUST include both the user interface and control service so a partially started application cannot appear healthy.

### Automatic discovery and system identity

- **FR-008**: Scout MUST automatically derive one bounded private-network discovery boundary when it can do so safely.
- **FR-009**: When automatic derivation is ambiguous or unsafe, Scout MUST request one bounded network range inline and MUST NOT guess a broad scope.
- **FR-010**: Initial discovery MUST detect SSH by default and support adding other bounded agent-installation methods through the same model.
- **FR-011**: Discovery MUST perform bounded reachability and known-entry-point checks only; it MUST NOT guess passwords, exploit services, run arbitrary commands, or treat a port as trusted identity.
- **FR-012**: Only unique hosts with a currently observed supported access method and enrolled agents MAY appear in the primary Systems fleet. Other scan outcomes belong in diagnostics, not the Needs access count.
- **FR-013**: Each potential system MUST show useful identity evidence, including current addresses, detected methods, last observation, hostname or hardware address when known, and the limits of that evidence.
- **FR-014**: Reconciliation MUST prefer durable agent identity and verified host identity over addresses; IP and hardware addresses are supporting evidence and must not cause unsafe merges on their own.
- **FR-015**: Scout MUST deduplicate observations, access needs, installation work, systems, and agent identities while retaining evidence provenance.
- **FR-016**: Discovery MUST obey current network boundaries, exclusions, pause state, rate, concurrency, attempt, and duration limits before every new unit of work.

### Access and automatic enrollment

- **FR-017**: Access fields MUST be specific to the detected method. SSH password access requires username and password; SSH key access requires username and private key.
- **FR-018**: Credentials MUST be encrypted, write-only, auditable without their value, and bound to exact targets by default. Broader reuse requires an explicit owner choice and a bounded network.
- **FR-019**: Unknown host identity confirmation MUST be integrated into the system access flow. A changed previously trusted identity MUST block automatically and require explicit resolution.
- **FR-020**: Valid access and host identity MUST trigger installation and enrollment automatically without a manual target-host command.
- **FR-021**: The control server MUST provide the appropriate installer and agent artifact to enrolled hosts; a separate public agent-container workflow MUST NOT be required for ordinary installation.
- **FR-022**: Installation MUST be idempotent, revalidate authority before privileged steps, and converge on one active agent identity per system.
- **FR-023**: The owner MUST see installation stages and a specific recoverable reason for invalid credentials, missing privilege, trust change, connectivity failure, incompatibility, or bounded retry exhaustion.
- **FR-024**: Every mutation control MUST show an accessible pending state, prevent accidental duplicate submission, and return to an actionable state after success, failure, or a bounded timeout.
- **FR-025**: Secrets MUST be cleared from browser state after submission, excluded from responses and logs, and never distributed to ordinary monitoring agents.

### Monitoring, collectors, and lifecycle

- **FR-026**: Each agent MUST have a durable, independently revocable identity and continue as the same agent across valid service, server, and host restarts.
- **FR-027**: Linux agents MUST report real timestamped CPU, memory, filesystem, uptime, and interface observations with explicit availability and units.
- **FR-028**: Scout MUST distinguish loading, current, stale, missing, unsupported, offline, and revoked states in storage and presentation.
- **FR-029**: Agents MUST buffer and retry within explicit bounds during server loss and report discarded data rather than implying complete history.
- **FR-030**: Service collectors MUST be independently extensible, permission-aware, bounded, and unable to stop base host monitoring when one collector fails.
- **FR-031**: Host services and workloads MUST appear in their system context. Provider-specific fleet configuration belongs in Settings, not separate routine navigation.
- **FR-032**: Agent updates MUST be server-delivered, publisher-signed, independently verified by the agent, usable without agent internet access, compatible, recoverable, and protected from stale assignments.
- **FR-033**: Decommissioning MUST revoke agent identity, retain an explicit exclusion by default, and make uninstall outcome truthful rather than assumed.

### Interface and operations

- **FR-034**: Systems MUST be the default signed-in view and combine fleet summary, discovery status, potential systems, monitored systems, and attention states.
- **FR-035**: The primary navigation MUST NOT include separate pages for access requests, trust records, enrollment jobs, scan scopes, hardware, storage, or service collectors.
- **FR-036**: A system detail MUST combine identity, access, installation, health, history, services, and relevant system operations with progressive disclosure.
- **FR-037**: Manual agent installation MUST remain available behind the top-level add-system action and MUST NOT be the default onboarding instruction.
- **FR-038**: Demo systems, demo metrics, and unlabeled fixtures MUST NOT appear in operational builds.
- **FR-039**: Empty, loading, success, warning, and error states MUST identify what happened and the next useful action without exposing internal-only identifiers as the primary explanation.
- **FR-040**: The primary journey MUST support keyboard-only operation, visible focus, semantic status announcements, browser back navigation, reduced motion, and supported narrow and wide viewports without horizontal page overflow.
- **FR-041**: Scout MUST retain auditable discovery, credential, trust, enrollment, update, and decommission events without credential values or unsafe remote output.
- **FR-042**: Backup and restore MUST preserve identity, configuration, retained history, and encrypted credential usability when correct recovery material is supplied, and fail explicitly when it is not.

## v1 boundaries

### Included

- One self-hosted owner.
- Linux server deployment and Linux agents.
- Automatic bounded local discovery with SSH as the first installation method.
- Username/password and private-key SSH access.
- Automatic server-driven agent installation and enrollment.
- Real host telemetry, history, availability, and basic service collection.
- One-agent-per-device reconciliation.
- Signed server-delivered agent updates, including isolated agents.
- Backup, restore, update, decommission, and operational diagnostics.
- Responsive and keyboard-accessible web interface.

### Not included

- Multi-user roles, customer tenancy, or hosted accounts.
- macOS, Windows, FreeBSD, or mobile agents in v1.
- Vulnerability scanning, password guessing, exploit execution, arbitrary remote shell, or intrusive fingerprinting.
- Silent trust-on-first-use for changed host identities.
- Requiring public internet access from monitored agents.
- A separate agent image as the normal enrollment path.
- Provider-specific UI architecture that prevents adding new collectors.

## Success criteria

- **SC-001**: A new owner can complete the documented deployment and reach owner setup in under ten minutes on a supported clean Linux host.
- **SC-002**: After owner sign-in in a controlled 256-address private network, a reachable SSH host appears as one actionable potential system within 60 seconds without a settings visit or manual scan.
- **SC-003**: Zero closed, filtered, unreachable, or unsupported addresses are included in the Needs access count.
- **SC-004**: Three Scout restarts and overlapping scan observations produce one system record and at most one active agent identity for the same test host.
- **SC-005**: From the Systems view, a first-time owner can start installation with one system selection, required credential entry, integrated host confirmation, and one final submit action; no target-host command is required.
- **SC-006**: Valid SSH access to a disposable Linux host produces an enrolled online agent and first real host observations within two minutes.
- **SC-007**: Invalid credentials, insufficient privilege, changed host identity, control-service loss, installation failure, and request timeout all restore an actionable UI state without exposing the submitted secret.
- **SC-008**: Every access submission leaves its busy state within 15 seconds and presents an accessible success or specific error message.
- **SC-009**: New agent observations update the same system from Installing to Online within five seconds of receipt.
- **SC-010**: The normal discovery-to-monitoring journey uses one Systems surface and one system-detail context; no internal workflow page is required.
- **SC-011**: At 360px, 768px, 1024px, and 1440px widths, the primary journey has no horizontal page overflow, hidden primary action, clipped content, invalid date, or unreadable chart state.
- **SC-012**: A keyboard-only owner can complete setup, discovery inspection, access submission, installation tracking, and monitoring inspection with visible focus and announced status.
- **SC-013**: Clean-install and existing-install update tests prove that one update command refreshes deployment definitions and images while preserving configuration and data.
- **SC-014**: A signed update succeeds for an agent denied public internet access; tampered, unsigned, incompatible, and stale releases make zero running-agent changes.
- **SC-015**: A backup from a populated instance restores into a clean instance with the same systems and identities and with authority paused until owner review.
- **SC-016**: All acceptance screenshots and operational values come from live disposable test systems or are explicitly labeled test fixtures; no demo data is shipped.

## Required acceptance environment

Before v1 is called complete, an automated local acceptance environment must exercise:

- a clean Scout deployment with persistent data;
- a bounded 256-address network;
- at least one disposable Linux host with a real SSH service and system service manager;
- valid and invalid SSH credentials;
- host identity first-contact and changed-key cases;
- installation, restart, certificate renewal, loss of contact, recovery, update, and revocation;
- current, stale, missing, unsupported, and offline monitoring states;
- narrow and wide browser rendering plus keyboard-only interaction;
- control-service startup failure and browser request timeout;
- an existing deployment update and a clean restore.

Tests must not probe or install on real devices, use production credentials, or contact production endpoints without explicit owner authorization.

## Definition of done

Scout v1 is done only when:

1. Every P1 scenario and success criterion passes in the required disposable acceptance environment.
2. The complete install-to-monitor journey works from the product interface without manual target-host commands.
3. The repository's formatter, linter, type checks, unit/integration tests, browser tests, and pre-commit/pre-push hooks pass.
4. Continuous integration is green for the exact pushed commit.
5. The public README contains the verified copyable deployment, alternate-port, update, backup, and recovery commands.
6. The published server image contains the tested UI, control service, installer, and agent artifacts for supported platforms.
7. No obsolete spec, task ledger, ticket, demo workflow, duplicate navigation path, or known indefinite-loading action remains.

## Assumptions

- The owner controls the networks and hosts they authorize Scout to inspect and enroll.
- A safely inferred network means a bounded directly reachable private network; ambiguous, public, VPN, container-only, or proxy-derived evidence is insufficient on its own.
- Advanced controls are necessary but are secondary to the minimum path to value.
- The existing application and implementation stack remain the starting point and are simplified in place.
- Future platforms and providers must fit the same System, Access method, Agent identity, Observation, and Collector concepts rather than create parallel products.
