# Feature Specification: Active Network Scanning

**Feature Branch**: `main` (directory independent of branch)

**Created**: 2026-09-11

**Status**: Draft

**Input**: User description: "The server currently does not do a network scan. Ideally it and the agents should perform nmap-style scanning for known points of entry such as SSH, flag devices needing credentials in the UI, and continue putting an agent on every eligible device on the network."

## User Scenarios & Testing

### User Story 1 - Discover reachable management entry points (Priority: P1)

As the owner, I enable scanning for an approved network scope and see devices and known management entry points discovered from the control server and eligible agents.

**Why this priority**: Active scanning is required to find devices that are not already present in neighbor tables and determine whether Scout has a viable enrollment path.

**Independent Test**: Configure a controlled scope containing hosts with open, closed, filtered, and unreachable management ports. Run scans from the server and one agent, then verify bounded results, provenance, and exclusions.

**Acceptance Scenarios**:

1. **Given** an active owner-approved scope, **When** the control server can route to its addresses, **Then** it scans only permitted targets and records each observed management entry point with source and time.
2. **Given** an agent assigned as a scanning vantage point, **When** a scheduled scan runs, **Then** its authenticated results appear with that agent as their source and cannot expand the authorized scope.
3. **Given** an excluded address or range, **When** any scanner processes the containing scope, **Then** it sends no active probe to the excluded target.
4. **Given** multiple scanners observing the same address, **When** results arrive, **Then** Scout preserves their separate evidence while presenting one conservative candidate rather than duplicate devices.

---

### User Story 2 - Supply credentials for a discovered device (Priority: P1)

As the owner, I can see that a discovered device exposes SSH or another recognized entry point but lacks usable credentials, assign access at the correct scope, and have enrollment continue automatically.

**Why this priority**: Discovery without an actionable access state does not advance Scout's goal of monitoring every eligible device.

**Independent Test**: Discover an SSH endpoint without credentials, add invalid and then valid scoped credentials, and verify clear state changes and one successful enrollment.

**Acceptance Scenarios**:

1. **Given** a candidate with reachable SSH and no matching access, **When** its scan result is reconciled, **Then** the UI shows a needs-credentials state linked to the address, entry point, scope, evidence source, and last observation.
2. **Given** a needs-credentials candidate, **When** the owner assigns matching credentials, **Then** Scout re-evaluates it and queues eligible enrollment without waiting for another full scan.
3. **Given** matching credentials that fail authentication or lack installation privilege, **When** enrollment attempts access, **Then** the UI distinguishes invalid credentials from insufficient privilege without exposing secret values.
4. **Given** a reachable but unrecognized or unsupported management service, **When** it is discovered, **Then** Scout records it as observed but does not request an irrelevant credential or attempt an arbitrary installer.

---

### User Story 3 - Grow coverage from every safe vantage point (Priority: P1)

As the owner, I can use the control server and enrolled agents as bounded scanning vantage points so devices visible only from another subnet can still be found and enrolled.

**Why this priority**: A server-only scan misses segmented networks, while unbounded agent scans could escape owner authorization.

**Independent Test**: Use two isolated lab networks where the server reaches the first and an enrolled agent reaches the second. Verify discovery and enrollment in both without probing an adjacent unauthorized range.

**Acceptance Scenarios**:

1. **Given** a scope reachable only from an assigned agent, **When** its scheduled scan runs, **Then** candidates are discovered and processed through the same access and enrollment states as server discoveries.
2. **Given** a newly enrolled agent that is eligible to scan an existing scope, **When** it becomes healthy, **Then** it can join future scan cycles without granting itself new ranges.
3. **Given** a scanner that becomes stale, revoked, or decommissioned, **When** work is assigned or results arrive, **Then** new work is withheld and unauthorized or late results cannot trigger enrollment.
4. **Given** scanning or enrollment paused globally or for a scope, **When** a cycle becomes due, **Then** no new probe or installation begins until the applicable control is resumed.

---

### User Story 4 - Understand scan progress and limits (Priority: P2)

As the owner, I can see when and where scans ran, whether coverage was complete, and why targets were skipped or inconclusive.

**Why this priority**: Operators must not mistake a partial, blocked, or stale scan for proof that no devices exist.

**Independent Test**: Exercise target limits, timeouts, scanner loss, overlapping runs, and a partially unreachable scope, then inspect status and audit information using keyboard navigation.

**Acceptance Scenarios**:

1. **Given** an active scan, **When** the owner opens scope status, **Then** the UI shows vantage point, start time, progress, configured limits, and current outcome counts.
2. **Given** a target, rate, duration, or result limit is reached, **When** the scan ends, **Then** the scope is marked partial with the limiting reason rather than complete.
3. **Given** a previously open entry point that is not confirmed by a later scan, **When** evidence ages, **Then** it becomes stale or contradicted without deleting history or claiming the device is absent.
4. **Given** scan configuration or execution, **When** audit history is inspected, **Then** it identifies the actor, scope, scanner, timing, and outcome without credentials or captured sensitive data.

### Edge Cases

- DHCP reassignment, duplicate IP use, NAT, multiple interfaces, IPv4 and IPv6, and cloned hosts must not cause automatic identity merging based only on an address.
- Closed, refused, filtered, timed-out, unreachable, and scanner-error outcomes must remain distinguishable. None alone proves that a device does not exist.
- A service on a non-default port, a misleading banner, port forwarding, or an SSH-compatible decoy must not establish trusted device identity.
- Scope edits, exclusions, pause, credential revocation, host-key changes, or scanner revocation between scheduling and execution must invalidate affected work.
- Overlapping scans and duplicate, delayed, replayed, or out-of-order results must not create duplicate candidates, access requests, or enrollment jobs.
- Large address ranges, IPv6 ranges, hostile endpoints, slow connections, and result floods must remain bounded and must not starve telemetry or control operations.
- Scanning must not perform password guessing, exploit checks, vulnerability tests, UDP sweeps, OS fingerprinting, or arbitrary script execution in this feature.
- A newly enrolled target that cannot reach the control server must remain in a connectivity-needed state rather than being reported as successfully managed.

## Requirements

### Functional Requirements

- **FR-001**: The owner MUST explicitly configure address ranges, exclusions, permitted scan methods, recognized entry points, schedule, and active or paused state for every scanned scope.
- **FR-002**: The control server MUST be available as a scanning vantage point for scopes it can route to, and the owner MUST be able to enable or disable it per scope.
- **FR-003**: Eligible enrolled agents MUST be available as scanning vantage points only for scopes explicitly assigned to them; agent observations MUST NOT create or enlarge authorization.
- **FR-004**: Before every probe, scanners MUST apply the latest available scope, exclusion, pause, scanner-eligibility, target, rate, concurrency, and duration controls.
- **FR-005**: Initial active scanning MUST support bounded host and TCP entry-point discovery for an owner-configured catalog that includes SSH by default.
- **FR-006**: Scanning MUST NOT perform authentication attempts, password guessing, exploit or vulnerability tests, arbitrary scripts, intrusive OS fingerprinting, or installation activity.
- **FR-007**: Every result MUST identify its scope, scanning vantage point, target address, entry point, outcome, observation time, receipt time, and applicable policy revision.
- **FR-008**: Scan outcomes MUST distinguish open or reachable, closed or refused, filtered or timed out, unreachable, skipped, and scanner error without treating missing evidence as a successful check.
- **FR-009**: Scout MUST reconcile evidence from multiple vantage points conservatively and MUST NOT merge device identity solely because observations share an address.
- **FR-010**: A recognized reachable entry point with no matching usable access MUST create or update one deduplicated actionable access request for the candidate and access method.
- **FR-011**: The UI MUST show candidates as discovered, needs credentials, invalid credentials, needs privilege, needs host trust, needs server connectivity, queued, enrolling, enrolled, unsupported, unreachable, excluded, or stale as applicable.
- **FR-012**: Assigning or correcting scoped access MUST promptly re-evaluate affected candidates and queue enrollment when all eligibility, trust, connectivity, and policy conditions pass.
- **FR-013**: Scan evidence alone MUST NOT establish host trust. Enrollment through SSH MUST continue to require an owner-established or explicitly resolved host identity.
- **FR-014**: Ordinary agents MUST NOT receive reusable enrollment credentials or credential values as part of scanning; privileged access remains confined to the existing enrollment boundary.
- **FR-015**: Scout MUST deduplicate overlapping scans, observations, candidates, access requests, and enrollment work while preserving provenance from each vantage point.
- **FR-016**: The owner MUST be able to start an on-demand scan, inspect scheduled and active scans, cancel pending work, and pause scanning globally or per scope.
- **FR-017**: Scan scheduling MUST avoid synchronized fleet bursts and prevent a failed or slow scan from interrupting host telemetry, heartbeats, updates, or other collector work.
- **FR-018**: Every scan MUST have explicit target, probe, rate, concurrency, duration, payload, and retained-result bounds; reaching a bound MUST produce visible partial status.
- **FR-019**: Entry-point evidence MUST age independently by source. Later failure to confirm an entry point MUST make it stale or contradicted, not silently delete it or prove device absence.
- **FR-020**: Scan configuration changes, execution, cancellation, policy rejection, and enrollment handoff MUST be auditable without storing credentials, sensitive banners, or arbitrary remote responses.
- **FR-021**: The UI MUST expose scan coverage, last completed and next scheduled run, vantage points, partial or failed reasons, outcome counts, and actionable candidate states without relying on color alone.
- **FR-022**: Scanner and server upgrades MUST preserve scope policy, exclusions, candidate identity evidence, access requests, and unfinished-work safety without replaying stale enrollment authority.

### Key Entities

- **Scan policy**: Owner-approved scope, exclusions, methods, entry-point catalog, schedule, limits, eligible vantage points, state, and revision.
- **Scanning vantage point**: Control server or enrolled agent identity, reachability context, eligibility, freshness, capabilities, and revocation state.
- **Scan run**: Policy revision, vantage point, schedule or owner trigger, timing, progress, bounds, completion status, and aggregate outcomes.
- **Entry-point observation**: Target address and port, recognized access method, outcome, source, observation and receipt times, latency, freshness, and policy revision.
- **Candidate device**: Conservatively reconciled inventory subject with addresses, evidence, lifecycle state, exclusions, and possible agent association.
- **Access request**: Deduplicated candidate and access-method need, safe diagnostic reason, applicable scope, state, attempts, and resolution.
- **Enrollment handoff**: Revalidated candidate, trusted host identity, scoped access reference, chosen vantage point, policy revision, and resulting job.

## Success Criteria

### Measurable Outcomes

- **SC-001**: In a controlled 256-address scope with default limits, at least 95% of reachable SSH entry points appear in the UI within five minutes of a scheduled scan beginning.
- **SC-002**: Across server and agent vantage points, 100% of excluded lab targets receive zero active probes, including after mid-scan policy changes.
- **SC-003**: Twenty duplicate observations of one target from overlapping runs produce one candidate, one active access request per access method, and at most one enrollment job.
- **SC-004**: A candidate with reachable SSH and no credentials shows an actionable needs-credentials state within 30 seconds of accepted scan evidence.
- **SC-005**: Adding valid matching credentials causes eligible enrollment evaluation to resume within one minute without requiring another full network scan.
- **SC-006**: Closed, filtered, unreachable, stale, skipped, and partial-scan fixtures are represented accurately; none is displayed as confirmed absence or healthy monitoring.
- **SC-007**: Rate, concurrency, target, duration, and result-limit tests remain within configured bounds while ordinary host telemetry continues on its expected schedule.
- **SC-008**: Revoked scanners, changed policy revisions, replayed results, escaped scopes, and untrusted host identities produce zero unauthorized enrollment attempts.
- **SC-009**: Keyboard-only users can inspect scan status, filter candidates needing action, open an access request, and reach the credential-assignment flow at supported viewport sizes.
- **SC-010**: In a segmented two-network lab, a server discovers the reachable first network and an assigned agent discovers the otherwise unreachable second network, while neither probes an adjacent unauthorized range.

## Assumptions

- Existing single-owner authentication, sites, scopes, exclusions, credential storage, trust records, candidate reconciliation, enrollment jobs, and audit history remain dependencies rather than being redesigned here.
- "Nmap-style" means safe, bounded reachability and known TCP entry-point discovery. It does not require the nmap program and excludes vulnerability scanning, aggressive fingerprinting, UDP scanning, banner storage, and arbitrary scripts.
- SSH on TCP port 22 is enabled in the default entry-point catalog. Owners may configure alternate SSH ports and additional recognized management entry points during planning, but only supported enrollment methods create credential requests.
- The control server scans only when the owner enables it for a scope and network routing permits it. Agents scan only assigned scopes and do not receive reusable credentials.
- Scanning identifies candidate addresses and entry-point evidence, not authoritative device identity. Existing trust and enrollment verification remain mandatory.
- Default schedules and numeric safety limits will be selected during planning and validated against the existing 100-device reference workspace.
- Initial delivery remains Linux-first and uses active scanning only inside owner-controlled or owner-authorized networks.
