package enrollment

import (
	"context"
	"time"

	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

type Reason string

const (
	ReasonMissingCredentials Reason = "missing_credentials"
	ReasonHostTrust          Reason = "host_trust_required"
	ReasonOutOfScope         Reason = "outside_scope"
	ReasonExcluded           Reason = "excluded"
	ReasonMethod             Reason = "method_not_allowed"
	ReasonPort               Reason = "port_not_allowed"
	ReasonUnsupported        Reason = "unsupported_platform"
	ReasonConnectivity       Reason = "no_connectivity"
)

type Access struct {
	Policy *policy.Engine
	Store  *store.Store
}

type Result struct {
	Eligible bool
	Reason   Reason
	Job      store.Job
	Request  store.AccessRequest
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

func timePtr(value time.Time) *time.Time { return &value }
