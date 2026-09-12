package control

import (
	"context"
	"net/http"
	"strings"
	"time"

	"scout.local/scout/internal/audit"
	"scout.local/scout/internal/store"
)

func (a *App) registerAccessRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/sites", a.listSites)
	mux.HandleFunc("POST /api/v1/sites", a.createSite)
	mux.HandleFunc("GET /api/v1/scopes", a.listScopes)
	mux.HandleFunc("POST /api/v1/scopes", a.createScope)
	mux.HandleFunc("GET /api/v1/scopes/{scopeId}", a.getScope)
	mux.HandleFunc("PATCH /api/v1/scopes/{scopeId}", a.updateScope)
	mux.HandleFunc("GET /api/v1/credentials", a.listCredentials)
	mux.HandleFunc("POST /api/v1/credentials", a.createCredential)
	mux.HandleFunc("POST /api/v1/credentials/{credentialId}/rotate", a.rotateCredential)
	mux.HandleFunc("DELETE /api/v1/credentials/{credentialId}", a.revokeCredential)
	mux.HandleFunc("GET /api/v1/trust", a.listTrust)
	mux.HandleFunc("POST /api/v1/trust", a.createTrust)
	mux.HandleFunc("PATCH /api/v1/trust/{trustId}", a.updateTrust)
	mux.HandleFunc("DELETE /api/v1/trust/{trustId}", a.revokeTrust)
	mux.HandleFunc("GET /api/v1/access-requests", a.listAccessRequests)
	mux.HandleFunc("GET /api/v1/jobs", a.listJobs)
	mux.HandleFunc("POST /api/v1/bootstrap-invitations", a.createBootstrapInvitation)
	mux.HandleFunc("POST /api/v1/devices/{deviceId}/bootstrap", a.createDeviceInvitation)
}

func (a *App) listSites(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListSites(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}
func (a *App) createSite(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireOwner(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Name           string `json:"name"`
		AddressContext string `json:"addressContext"`
	}
	if err := decodeJSON(r, &req, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	site, err := a.Store.CreateSite(r.Context(), store.Site{Name: req.Name, AddressContext: req.AddressContext})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "site.create", site.ID, map[string]any{"success": true})
	writeJSON(w, http.StatusCreated, site)
}

func (a *App) listScopes(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListScopes(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	views := make([]scopeResponse, 0, len(items))
	for _, item := range items {
		view, viewErr := a.scopeView(r.Context(), item)
		if viewErr != nil {
			writeMappedError(w, r, viewErr)
			return
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views, "nextCursor": nil})
}
func (a *App) createScope(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var req scopeRequest
	if err := decodeJSON(r, &req, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	scopeInput := req.scope()
	var scope store.Scope
	var err error
	if req.ScanPolicy != nil {
		scopeInput.ID = store.NewID()
		normalized, policyErr := a.normalizeOwnerScanPolicy(r.Context(), scopeInput, req.ScanPolicy.policy(scopeInput.ID))
		if policyErr != nil {
			writeScanError(w, r, policyErr)
			return
		}
		scope, err = a.Store.CreateScopeWithScanPolicy(r.Context(), scopeInput, normalized)
	} else {
		scope, err = a.Store.CreateScope(r.Context(), scopeInput)
	}
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "scope.create", scope.ID, map[string]any{"enabled": scope.Enabled})
	view, err := a.scopeView(r.Context(), scope)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}
func (a *App) getScope(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	scope, err := a.Store.GetScope(r.Context(), r.PathValue("scopeId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	view, err := a.scopeView(r.Context(), scope)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (a *App) updateScope(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var req scopePatch
	if err := decodeJSON(r, &req, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	current, err := a.Store.GetScope(r.Context(), r.PathValue("scopeId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if req.ExpectedRevision != current.Revision {
		writeScanError(w, r, scanConflictError("scope revision is superseded"))
		return
	}
	updated := req.scope(current)
	var nextPolicy *store.ScanPolicy
	if req.ScanPolicy != nil {
		currentPolicy, policyErr := a.Store.ScanPolicy(r.Context(), current.ID)
		if policyErr != nil {
			writeMappedError(w, r, policyErr)
			return
		}
		if req.ScanPolicy.ExpectedRevision != currentPolicy.Revision {
			writeScanError(w, r, scanConflictError("scan policy revision is superseded"))
			return
		}
		normalized, policyErr := a.normalizeOwnerScanPolicy(r.Context(), updated, req.ScanPolicy.apply(currentPolicy))
		if policyErr != nil {
			writeScanError(w, r, policyErr)
			return
		}
		nextPolicy = &normalized
	}
	scope := current
	if req.hasBaseFields() {
		scope, err = a.Store.UpdateScope(r.Context(), current.ID, req.ExpectedRevision, updated)
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	if nextPolicy != nil {
		if _, err := a.Store.UpdateScanPolicy(r.Context(), current.ID, req.ScanPolicy.ExpectedRevision, *nextPolicy); err != nil {
			if err == store.ErrConflict {
				writeScanError(w, r, scanConflictError("scan policy revision is superseded"))
			} else {
				writeScanError(w, r, scanPolicyStoreError(err))
			}
			return
		}
	}
	a.recordOwnerAudit(r, "scope.update", scope.ID, map[string]any{"revision": scope.Revision})
	view, err := a.scopeView(r.Context(), scope)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type scopeRequest struct {
	SiteID         string            `json:"siteId"`
	Ranges         []string          `json:"ranges"`
	Exclusions     []string          `json:"exclusions"`
	AllowedMethods []string          `json:"methods"`
	Ports          []int             `json:"ports"`
	CredentialRef  string            `json:"credentialRef"`
	TrustRef       string            `json:"trustRef"`
	Limits         store.ScopeLimits `json:"limits"`
	Enabled        bool              `json:"enabled"`
	ScanPolicy     *scanPolicyInput  `json:"scanPolicy"`
}

func (r scopeRequest) scope() store.Scope {
	return store.Scope{SiteID: r.SiteID, Ranges: r.Ranges, Exclusions: r.Exclusions, AllowedMethods: r.AllowedMethods, Ports: r.Ports, CredentialRef: r.CredentialRef, TrustRef: r.TrustRef, Limits: r.Limits, Enabled: r.Enabled}
}

type scopePatch struct {
	ExpectedRevision int64              `json:"expectedRevision"`
	Ranges           *[]string          `json:"ranges"`
	Exclusions       *[]string          `json:"exclusions"`
	AllowedMethods   *[]string          `json:"methods"`
	Ports            *[]int             `json:"ports"`
	CredentialRef    *string            `json:"credentialRef"`
	TrustRef         *string            `json:"trustRef"`
	Limits           *store.ScopeLimits `json:"limits"`
	Enabled          *bool              `json:"enabled"`
	ScanPolicy       *scanPolicyPatch   `json:"scanPolicy"`
}

func (r scopePatch) scope(current store.Scope) store.Scope {
	updated := current
	if r.Ranges != nil {
		updated.Ranges = *r.Ranges
	}
	if r.Exclusions != nil {
		updated.Exclusions = *r.Exclusions
	}
	if r.AllowedMethods != nil {
		updated.AllowedMethods = *r.AllowedMethods
	}
	if r.Ports != nil {
		updated.Ports = *r.Ports
	}
	if r.CredentialRef != nil {
		updated.CredentialRef = *r.CredentialRef
	}
	if r.TrustRef != nil {
		updated.TrustRef = *r.TrustRef
	}
	if r.Limits != nil {
		updated.Limits = *r.Limits
	}
	if r.Enabled != nil {
		updated.Enabled = *r.Enabled
	}
	return updated
}

func (r scopePatch) hasBaseFields() bool {
	return r.Ranges != nil || r.Exclusions != nil || r.AllowedMethods != nil || r.Ports != nil || r.CredentialRef != nil || r.TrustRef != nil || r.Limits != nil || r.Enabled != nil
}

func (a *App) listCredentials(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListCredentials(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	safe := make([]map[string]any, 0, len(items))
	for _, item := range items {
		safe = append(safe, map[string]any{"id": item.ID, "kind": item.Kind, "endpoint": item.Endpoint, "allowedUse": item.AllowedUse, "targets": item.Targets, "metadata": item.Metadata, "revision": item.Revision, "revokedAt": item.RevokedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": safe, "nextCursor": nil})
}
func (a *App) createCredential(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var req struct {
		Kind                  string   `json:"kind"`
		Secret                string   `json:"secret"`
		AllowedUse            []string `json:"allowedUse"`
		Targets               []string `json:"targets"`
		Endpoint              string   `json:"endpoint"`
		ScopeID               string   `json:"scopeId"`
		ExpectedScopeRevision int64    `json:"expectedScopeRevision"`
	}
	if err := decodeJSON(r, &req, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if strings.TrimSpace(req.Secret) == "" || len(req.Secret) > 8192 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	id := store.NewID()
	envelope, err := a.Secrets.EncryptSecret(id, req.Kind, []byte(req.Secret))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	credential, err := a.Store.PutCredential(r.Context(), store.CredentialRef{ID: id, Kind: req.Kind, Endpoint: req.Endpoint, AllowedUse: req.AllowedUse, Targets: req.Targets, Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, WrappedDataKey: envelope.WrappedDataKey, KeyVersion: envelope.KeyVersion, Metadata: map[string]string{"created": "owner"}})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if req.ScopeID != "" {
		if err := a.bindCredentialToScope(r.Context(), req.ScopeID, credential.ID, req.ExpectedScopeRevision); err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	affected := 0
	if req.ScopeID != "" && a.Enrollment != nil {
		affected, err = a.Enrollment.ReevaluateScope(r.Context(), req.ScopeID)
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	a.recordOwnerAudit(r, "credential.create", id, map[string]any{"kind": req.Kind, "success": true})
	response := safeCredential(credential)
	response["affectedCandidateCount"] = affected
	writeJSON(w, http.StatusCreated, response)
}
func (a *App) rotateCredential(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var req struct {
		Secret           string `json:"secret"`
		ExpectedRevision int64  `json:"expectedRevision"`
	}
	if err := decodeJSON(r, &req, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	current, err := a.Store.Credential(r.Context(), r.PathValue("credentialId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	envelope, err := a.Secrets.EncryptSecret(current.ID, current.Kind, []byte(req.Secret))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	current.Ciphertext = envelope.Ciphertext
	current.Nonce = envelope.Nonce
	current.WrappedDataKey = envelope.WrappedDataKey
	current.KeyVersion = envelope.KeyVersion
	updated, err := a.Store.RotateCredential(r.Context(), current.ID, req.ExpectedRevision, current)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	affected := 0
	if a.Enrollment != nil {
		affected, err = a.reevaluateCredentialScopes(r.Context(), updated.ID)
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	a.recordOwnerAudit(r, "credential.rotate", updated.ID, map[string]any{"revision": updated.Revision})
	response := safeCredential(updated)
	response["affectedCandidateCount"] = affected
	writeJSON(w, http.StatusOK, response)
}
func (a *App) revokeCredential(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	credentialID := r.PathValue("credentialId")
	if _, err := a.Store.Credential(r.Context(), credentialID); err != nil {
		writeMappedError(w, r, err)
		return
	}
	if err := a.Store.RevokeCredential(r.Context(), credentialID); err != nil {
		writeMappedError(w, r, err)
		return
	}
	affected := 0
	if a.Enrollment != nil {
		var err error
		affected, err = a.reevaluateCredentialScopes(r.Context(), credentialID)
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	a.recordOwnerAudit(r, "credential.revoke", credentialID, map[string]any{"success": true})
	writeJSON(w, http.StatusOK, map[string]any{"affectedCandidateCount": affected})
}
func safeCredential(item store.CredentialRef) map[string]any {
	return map[string]any{"id": item.ID, "kind": item.Kind, "endpoint": item.Endpoint, "allowedUse": item.AllowedUse, "targets": item.Targets, "metadata": item.Metadata, "revision": item.Revision, "revokedAt": item.RevokedAt}
}

func (a *App) listTrust(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListTrust(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}
func (a *App) createTrust(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var req struct {
		ScopeID     string `json:"scopeId"`
		Host        string `json:"host"`
		Endpoint    string `json:"endpoint"`
		Fingerprint string `json:"fingerprint"`
		PublicKey   string `json:"publicKey"`
	}
	if err := decodeJSON(r, &req, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if req.Endpoint == "" {
		req.Endpoint = req.Host
	}
	trust, err := a.Store.PutTrust(r.Context(), store.TrustRecord{ScopeID: req.ScopeID, Host: req.Host, Endpoint: req.Endpoint, Fingerprint: req.Fingerprint, PublicKey: req.PublicKey})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if req.ScopeID != "" {
		if err := a.bindTrustToScope(r.Context(), req.ScopeID, trust.ID); err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	affected := 0
	if req.ScopeID != "" && a.Enrollment != nil {
		affected, err = a.Enrollment.ReevaluateScope(r.Context(), req.ScopeID)
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	a.recordOwnerAudit(r, "trust.create", trust.ID, map[string]any{"host": trust.Host, "fingerprint": trust.Fingerprint})
	writeJSON(w, http.StatusCreated, map[string]any{"id": trust.ID, "scopeId": trust.ScopeID, "endpoint": trust.Endpoint, "host": trust.Host, "fingerprint": trust.Fingerprint, "publicKey": trust.PublicKey, "revision": trust.Revision, "revokedAt": trust.RevokedAt, "affectedCandidateCount": affected})
}

func (a *App) bindCredentialToScope(ctx context.Context, scopeID, credentialID string, expectedRevision int64) error {
	scope, err := a.Store.GetScope(ctx, scopeID)
	if err != nil {
		return err
	}
	if expectedRevision > 0 && expectedRevision != scope.Revision {
		return store.ErrConflict
	}
	scope.CredentialRef = credentialID
	_, err = a.Store.UpdateScope(ctx, scope.ID, scope.Revision, scope)
	return err
}

func (a *App) bindTrustToScope(ctx context.Context, scopeID, trustID string) error {
	scope, err := a.Store.GetScope(ctx, scopeID)
	if err != nil {
		return err
	}
	scope.TrustRef = trustID
	_, err = a.Store.UpdateScope(ctx, scope.ID, scope.Revision, scope)
	return err
}

func (a *App) reevaluateCredentialScopes(ctx context.Context, credentialID string) (int, error) {
	if a.Enrollment == nil {
		return 0, nil
	}
	scopes, err := a.Store.ListScopes(ctx)
	if err != nil {
		return 0, err
	}
	affected := 0
	for _, scope := range scopes {
		if scope.CredentialRef != credentialID {
			continue
		}
		count, err := a.Enrollment.ReevaluateScope(ctx, scope.ID)
		if err != nil {
			return affected, err
		}
		affected += count
	}
	return affected, nil
}
func (a *App) listAccessRequests(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListAccessRequests(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("reason"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}
func (a *App) listJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListJobs(r.Context(), r.URL.Query().Get("kind"), r.URL.Query().Get("state"), r.URL.Query().Get("deviceId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (a *App) createBootstrapInvitation(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var req struct {
		DisplayName string `json:"displayName"`
		SiteID      string `json:"siteId"`
	}
	if err := decodeJSON(r, &req, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	device, err := a.Store.CreateDevice(r.Context(), store.Device{DisplayName: req.DisplayName, SiteID: req.SiteID, Platform: "linux", Architecture: "unknown"})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.returnInvitation(w, r, device)
}
func (a *App) createDeviceInvitation(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	device, err := a.Store.GetDevice(r.Context(), r.PathValue("deviceId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if device.Excluded || device.Lifecycle == "decommissioned" {
		writeMappedError(w, r, store.ErrForbidden)
		return
	}
	a.returnInvitation(w, r, device)
}
func (a *App) returnInvitation(w http.ResponseWriter, r *http.Request, device store.Device) {
	token := randomToken(32)
	expires := time.Now().UTC().Add(5 * time.Minute)
	if err := a.Store.ReserveInvitation(r.Context(), store.BootstrapInvitation{TokenHash: store.HashToken(token), DeviceID: device.ID, ExpiresAt: expires}); err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "agent.invitation.create", device.ID, map[string]any{"expiresAt": expires, "success": true})
	writeJSON(w, http.StatusCreated, map[string]any{"deviceId": device.ID, "invitation": token, "expiresAt": expires, "instructions": "Write invitation to a private file or stdin; do not put it in process arguments."})
}

func (a *App) recordOwnerAudit(r *http.Request, action, target string, outcome map[string]any) {
	token := cookieValue(r, a.Auth.SessionCookieName())
	session, err := a.Auth.ValidateSession(r.Context(), token)
	actor := "owner"
	if err == nil {
		actor = session.OwnerID
	}
	_ = a.Audit.Record(r.Context(), audit.Event{ActorKind: "owner", ActorID: actor, Action: action, Target: target, Outcome: outcome, RequestID: r.Header.Get("X-Request-ID")})
}
