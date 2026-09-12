package enrollment

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

type Reason string

const (
	ReasonMissingCredentials    Reason = "missing_credentials"
	ReasonHostTrust             Reason = "host_trust_required"
	ReasonOutOfScope            Reason = "outside_scope"
	ReasonExcluded              Reason = "excluded"
	ReasonMethod                Reason = "method_not_allowed"
	ReasonPort                  Reason = "port_not_allowed"
	ReasonUnsupported           Reason = "unsupported_platform"
	ReasonConnectivity          Reason = "no_connectivity"
	ReasonInvalidCredentials    Reason = "invalid_credentials"
	ReasonInsufficientPrivilege Reason = "insufficient_privilege"
	ReasonServerConnectivity    Reason = "server_connectivity_required"
	ReasonStale                 Reason = "stale"
)

type Access struct {
	Policy   *policy.Engine
	Store    *store.Store
	Now      func() time.Time
	Verifier AccessVerifier
}

// AccessCheckRequest contains only references and safe target metadata. A
// verifier runs inside the enrollment boundary and never receives the
// encrypted credential material through this interface.
type AccessCheckRequest struct {
	Scope             store.Scope
	Candidate         store.Candidate
	Endpoint          string
	Method            string
	Port              int
	CredentialID      string
	CredentialVersion int64
	TrustID           string
}

type AccessCheckResult struct {
	Authenticated   bool
	Privileged      bool
	HostTrusted     bool
	ServerReachable bool
}

type AccessVerifier interface {
	Verify(context.Context, AccessCheckRequest) (AccessCheckResult, error)
}

type AccessVerifierFunc func(context.Context, AccessCheckRequest) (AccessCheckResult, error)

func (f AccessVerifierFunc) Verify(ctx context.Context, request AccessCheckRequest) (AccessCheckResult, error) {
	return f(ctx, request)
}

type Result struct {
	Eligible  bool
	Reason    Reason
	Job       store.Job
	Request   store.AccessRequest
	Candidate store.Candidate
}

func (a *Access) clock() time.Time {
	if a != nil && a.Now != nil {
		return a.Now().UTC()
	}
	if a != nil && a.Store != nil {
		return a.Store.Now()
	}
	return time.Now().UTC()
}

func (a *Access) Evaluate(ctx context.Context, scopeID, target, method string, port int, deviceID string) (Result, error) {
	if a == nil || a.Policy == nil || a.Store == nil {
		return Result{}, store.ErrInvalid
	}
	decision, err := a.Policy.Evaluate(ctx, scopeID, target, method, port)
	if err != nil {
		return Result{}, err
	}
	if !decision.Allowed {
		reason := Reason(decision.Reason)
		if reason == "needs_credentials" {
			reason = ReasonMissingCredentials
		}
		request, requestErr := a.Store.UpsertAccessRequest(ctx, store.AccessRequest{DeviceID: deviceID, ScopeID: scopeID, ReasonCode: string(reason), SafeDetails: map[string]string{"target": target, "method": method}, State: "open", LastAttempt: time.Now().UTC()})
		return Result{Reason: reason, Request: request}, requestErr
	}
	scope, err := a.Store.GetScope(ctx, scopeID)
	if err != nil {
		return Result{}, err
	}
	if scope.TrustRef != "" {
		matched, trustErr := a.Store.ScopeTrust(ctx, scopeID, secrets.Target(target, port), "")
		if trustErr != nil {
			return Result{}, trustErr
		}
		if !matched {
			request, requestErr := a.Store.UpsertAccessRequest(ctx, store.AccessRequest{DeviceID: deviceID, ScopeID: scopeID, ReasonCode: string(ReasonHostTrust), SafeDetails: map[string]string{"target": secrets.Target(target, port), "method": method}, State: "open", LastAttempt: time.Now().UTC()})
			return Result{Reason: ReasonHostTrust, Request: request}, requestErr
		}
	}
	if deviceID == "" {
		return Result{}, store.ErrInvalid
	}
	job, jobErr := a.Store.CreateJob(ctx, store.Job{Kind: "enrollment", DeviceID: deviceID, ScopeID: scopeID, ScopeRevision: decision.ScopeRevision, CredentialVersion: decision.CredentialVersion, Destination: secrets.Target(target, port), TrustRef: scope.TrustRef, State: "queued", Deadline: timePtr(time.Now().UTC().Add(10 * time.Minute))})
	if jobErr != nil && jobErr != store.ErrDuplicate {
		return Result{}, jobErr
	}
	return Result{Eligible: true, Reason: "eligible", Job: job}, nil
}

// ReevaluateCandidate applies current policy and the owner-controlled access
// boundary to one scan candidate. It is deliberately separate from scanning:
// changing credentials or trust can call it without repeating network probes.
func (a *Access) ReevaluateCandidate(ctx context.Context, candidateID string) (Result, error) {
	if a == nil || a.Policy == nil || a.Store == nil || strings.TrimSpace(candidateID) == "" {
		return Result{}, store.ErrInvalid
	}
	candidate, err := a.Store.GetCandidate(ctx, candidateID)
	if err != nil {
		return Result{}, err
	}
	if candidate.State == "queued" || candidate.State == "enrolling" || candidate.State == "enrolled" {
		if candidate.DeviceID != "" {
			jobs, jobsErr := a.Store.ListJobs(ctx, "enrollment", "", candidate.DeviceID)
			if jobsErr != nil {
				return Result{}, jobsErr
			}
			for _, job := range jobs {
				if job.Kind == "enrollment" && enrollmentJobActive(job.State) {
					return Result{Eligible: true, Reason: "eligible", Job: job, Candidate: candidate}, nil
				}
			}
		}
		if candidate.State == "enrolled" {
			return Result{Eligible: true, Reason: "eligible", Candidate: candidate}, nil
		}
	}
	if candidate.Excluded {
		return a.ineligible(ctx, candidate, nil, ReasonExcluded, "excluded")
	}
	now := a.clock()
	if candidate.State == "expired" || !candidate.ExpiresAt.IsZero() && !now.Before(candidate.ExpiresAt) {
		return a.ineligible(ctx, candidate, nil, ReasonStale, "stale")
	}
	if candidate.CoverageState != "" && candidate.CoverageState != "current" {
		state := "stale"
		reason := ReasonStale
		if candidate.CoverageState == "unreachable" {
			state = "unreachable"
			reason = ReasonConnectivity
		}
		return a.ineligible(ctx, candidate, nil, reason, state)
	}
	scope, err := a.Store.GetScope(ctx, candidate.ScopeID)
	if err != nil {
		return Result{}, err
	}
	request, err := a.candidateAccessRequest(ctx, candidate)
	if err != nil {
		return Result{}, err
	}
	endpoint, method, port, err := candidateEndpoint(candidate, request)
	if err != nil {
		return a.ineligible(ctx, candidate, request, ReasonUnsupported, "unsupported")
	}
	if method != store.ScanAccessSSH {
		return a.ineligible(ctx, candidate, request, ReasonUnsupported, "unsupported")
	}
	if !scanEntryPointEnabled(ctx, a.Store, candidate.ScopeID, candidate.EntryPointIDs, port) {
		return a.ineligible(ctx, candidate, request, ReasonStale, "stale")
	}
	host, _, splitErr := net.SplitHostPort(endpoint)
	if splitErr != nil {
		return a.ineligible(ctx, candidate, request, ReasonUnsupported, "unsupported")
	}
	decision, err := a.Policy.Evaluate(ctx, scope.ID, strings.Trim(host, "[]"), "tcp", port)
	if err != nil {
		return Result{}, err
	}
	if !decision.Allowed {
		reason, state := accessDecisionState(decision.Reason)
		return a.ineligible(ctx, candidate, request, reason, state)
	}
	if scope.CredentialRef == "" {
		return a.ineligible(ctx, candidate, request, ReasonMissingCredentials, "needs_credentials")
	}
	credential, credentialErr := a.Store.Credential(ctx, scope.CredentialRef)
	if credentialErr != nil || credential.RevokedAt != nil || credential.Kind != "ssh" || !containsString(credential.AllowedUse, "enrollment") || !secrets.TargetAllowed(credential.Targets, endpoint) {
		return a.ineligible(ctx, candidate, request, ReasonInvalidCredentials, "invalid_credentials")
	}
	trust, trustErr := a.Store.ScopeTrustRecord(ctx, scope.ID, endpoint)
	if errors.Is(trustErr, store.ErrNotFound) {
		return a.ineligible(ctx, candidate, request, ReasonHostTrust, "needs_host_trust")
	}
	if trustErr != nil {
		return Result{}, trustErr
	}
	check := AccessCheckResult{Authenticated: true, Privileged: true, HostTrusted: true, ServerReachable: true}
	if a.Verifier != nil {
		check, err = a.Verifier.Verify(ctx, AccessCheckRequest{Scope: scope, Candidate: candidate, Endpoint: endpoint, Method: method, Port: port, CredentialID: credential.ID, CredentialVersion: credential.Revision, TrustID: trust.ID})
		if err != nil {
			return Result{}, err
		}
	}
	if !check.Authenticated {
		return a.ineligible(ctx, candidate, request, ReasonInvalidCredentials, "invalid_credentials")
	}
	if !check.Privileged {
		return a.ineligible(ctx, candidate, request, ReasonInsufficientPrivilege, "needs_privilege")
	}
	if !check.HostTrusted {
		return a.ineligible(ctx, candidate, request, ReasonHostTrust, "needs_host_trust")
	}
	if !check.ServerReachable {
		return a.ineligible(ctx, candidate, request, ReasonServerConnectivity, "needs_server_connectivity")
	}

	queuedCandidate, job, queueErr := a.Store.QueueCandidateEnrollment(ctx, candidate.ID, store.Job{
		Kind: "enrollment", ScopeID: scope.ID, ScopeRevision: scope.Revision, CredentialVersion: credential.Revision,
		Destination: endpoint, TrustRef: trust.ID, State: "queued", Deadline: timePtr(now.Add(10 * time.Minute)),
	})
	if queueErr == store.ErrDuplicate {
		if job.ID == "" {
			return Result{}, queueErr
		}
		return Result{Eligible: true, Reason: "eligible", Job: job, Candidate: candidate}, nil
	}
	if queueErr != nil {
		return Result{}, queueErr
	}
	if err := a.Store.ResolveAccessRequestsForCandidate(ctx, candidate.ID); err != nil {
		return Result{}, err
	}
	return Result{Eligible: true, Reason: "eligible", Job: job, Candidate: queuedCandidate}, nil
}

// ReevaluateScope is intentionally bounded by the candidate store's normal
// list limit. It is used after a protected access mutation and returns the
// number of supported scan candidates that were considered.
func (a *Access) ReevaluateScope(ctx context.Context, scopeID string) (int, error) {
	if a == nil || a.Store == nil || strings.TrimSpace(scopeID) == "" {
		return 0, store.ErrInvalid
	}
	candidates, err := a.Store.ListCandidates(ctx, scopeID, "")
	if err != nil {
		return 0, err
	}
	affected := 0
	for _, candidate := range candidates {
		if candidate.PreferredAccessMethod != store.ScanAccessSSH && !hasAccessCandidateRequest(ctx, a.Store, candidate.ID) {
			continue
		}
		affected++
		if _, err := a.ReevaluateCandidate(ctx, candidate.ID); err != nil {
			return affected, err
		}
	}
	return affected, nil
}

// RecordEnrollmentOutcome projects a safe worker result into the same
// candidate/request state machine used by scan reconciliation.
func (a *Access) RecordEnrollmentOutcome(ctx context.Context, candidateID, code string) error {
	if a == nil || a.Store == nil || strings.TrimSpace(candidateID) == "" {
		return store.ErrInvalid
	}
	candidate, err := a.Store.GetCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	request, requestErr := a.candidateAccessRequest(ctx, candidate)
	if requestErr != nil {
		return requestErr
	}
	code = strings.ToLower(strings.TrimSpace(code))
	reason, state := ReasonServerConnectivity, "needs_server_connectivity"
	switch code {
	case "invalid_credentials", "authentication", "authentication_failed", "credential_unavailable":
		reason, state = ReasonInvalidCredentials, "invalid_credentials"
	case "insufficient_privilege", "privilege", "permission_denied", "installation":
		reason, state = ReasonInsufficientPrivilege, "needs_privilege"
	case "host_key_mismatch", "host_trust_required", "host_key":
		reason, state = ReasonHostTrust, "needs_host_trust"
	}
	_, err = a.ineligible(ctx, candidate, request, reason, state)
	return err
}

func (a *Access) candidateAccessRequest(ctx context.Context, candidate store.Candidate) (*store.AccessRequest, error) {
	requests, err := a.Store.ListAccessRequestsForCandidate(ctx, candidate.ID)
	if err != nil {
		return nil, err
	}
	for index := range requests {
		if requests[index].AccessMethod == store.ScanAccessSSH && requests[index].Endpoint != "" {
			request := requests[index]
			return &request, nil
		}
	}
	return nil, nil
}

func (a *Access) ineligible(ctx context.Context, candidate store.Candidate, request *store.AccessRequest, reason Reason, state string) (Result, error) {
	updated, err := a.Store.UpdateCandidateState(ctx, candidate.ID, state)
	if err != nil {
		return Result{}, err
	}
	if request == nil && reason != ReasonStale && reason != ReasonExcluded {
		request = &store.AccessRequest{CandidateID: candidate.ID, DeviceID: candidate.ID, ScopeID: candidate.ScopeID, AccessMethod: candidate.PreferredAccessMethod, ReasonCode: string(reason), State: "open"}
	}
	if request != nil && reason != ReasonStale && reason != ReasonExcluded {
		request.DeviceID = candidate.ID
		request.CandidateID = candidate.ID
		request.ReasonCode = string(reason)
		request.State = "open"
		if request.SafeDetails == nil {
			request.SafeDetails = map[string]string{}
		}
		if request.Endpoint != "" {
			request.SafeDetails["target"] = request.Endpoint
		}
		if request.AccessMethod != "" {
			request.SafeDetails["method"] = request.AccessMethod
		}
		request.LastAttempt = a.clock()
		stored, requestErr := a.Store.UpsertAccessRequest(ctx, *request)
		if requestErr != nil {
			return Result{}, requestErr
		}
		request = &stored
	}
	result := Result{Eligible: false, Reason: reason, Candidate: updated}
	if request != nil {
		result.Request = *request
	}
	return result, nil
}

func candidateEndpoint(candidate store.Candidate, request *store.AccessRequest) (string, string, int, error) {
	if request == nil || strings.TrimSpace(request.Endpoint) == "" {
		return "", "", 0, store.ErrNotFound
	}
	method := strings.TrimSpace(request.AccessMethod)
	host, portText, err := net.SplitHostPort(request.Endpoint)
	if err != nil || host == "" {
		return "", method, 0, store.ErrInvalid
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || strings.TrimSpace(host) != candidate.Address && !strings.EqualFold(strings.Trim(host, "[]"), candidate.Address) {
		return "", method, 0, store.ErrInvalid
	}
	return request.Endpoint, method, port, nil
}

func scanEntryPointEnabled(ctx context.Context, repository *store.Store, scopeID string, entryPointIDs []string, port int) bool {
	policy, err := repository.ScanPolicy(ctx, scopeID)
	if err != nil {
		return false
	}
	for _, entryPoint := range policy.EntryPoints {
		if entryPoint.Enabled && entryPoint.AccessMethod == store.ScanAccessSSH && entryPoint.Port == port && (len(entryPointIDs) == 0 || containsString(entryPointIDs, entryPoint.ID)) {
			return true
		}
	}
	return false
}

func accessDecisionState(reason string) (Reason, string) {
	switch reason {
	case "excluded":
		return ReasonExcluded, "excluded"
	case "outside_scope", "scope_paused":
		return ReasonStale, "stale"
	case "method_not_allowed":
		return ReasonMethod, "unsupported"
	case "port_not_allowed":
		return ReasonPort, "unsupported"
	default:
		return ReasonStale, "stale"
	}
}

func hasAccessCandidateRequest(ctx context.Context, repository *store.Store, candidateID string) bool {
	requests, err := repository.ListAccessRequestsForCandidate(ctx, candidateID)
	if err != nil {
		return false
	}
	for _, request := range requests {
		if request.AccessMethod == store.ScanAccessSSH && request.Endpoint != "" {
			return true
		}
	}
	return false
}

func enrollmentJobActive(state string) bool {
	switch state {
	case "queued", "claimed", "connecting", "installing", "verifying", "pausing":
		return true
	default:
		return false
	}
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func timePtr(value time.Time) *time.Time { return &value }
