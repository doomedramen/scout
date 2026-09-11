package control

import (
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
	mux.HandleFunc("PATCH /api/v1/scopes/{scopeId}", a.updateScope)
	mux.HandleFunc("GET /api/v1/credentials", a.listCredentials)
	mux.HandleFunc("POST /api/v1/credentials", a.createCredential)
	mux.HandleFunc("POST /api/v1/credentials/{credentialId}/rotate", a.rotateCredential)
	mux.HandleFunc("DELETE /api/v1/credentials/{credentialId}", a.revokeCredential)
	mux.HandleFunc("GET /api/v1/trust", a.listTrust)
	mux.HandleFunc("POST /api/v1/trust", a.createTrust)
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
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
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
	scope, err := a.Store.CreateScope(r.Context(), req.scope())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "scope.create", scope.ID, map[string]any{"enabled": scope.Enabled})
	writeJSON(w, http.StatusCreated, scope)
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
	updated := req.scope(current)
	scope, err := a.Store.UpdateScope(r.Context(), current.ID, req.ExpectedRevision, updated)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "scope.update", scope.ID, map[string]any{"revision": scope.Revision})
	writeJSON(w, http.StatusOK, scope)
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
}

func (r scopeRequest) scope() store.Scope {
	return store.Scope{SiteID: r.SiteID, Ranges: r.Ranges, Exclusions: r.Exclusions, AllowedMethods: r.AllowedMethods, Ports: r.Ports, CredentialRef: r.CredentialRef, TrustRef: r.TrustRef, Limits: r.Limits, Enabled: r.Enabled}
}

type scopePatch struct {
	ExpectedRevision int64             `json:"expectedRevision"`
	Ranges           []string          `json:"ranges"`
	Exclusions       []string          `json:"exclusions"`
	AllowedMethods   []string          `json:"methods"`
	Ports            []int             `json:"ports"`
	CredentialRef    string            `json:"credentialRef"`
	TrustRef         string            `json:"trustRef"`
	Limits           store.ScopeLimits `json:"limits"`
	Enabled          bool              `json:"enabled"`
}

func (r scopePatch) scope(current store.Scope) store.Scope {
	updated := current
	updated.Ranges = r.Ranges
	updated.Exclusions = r.Exclusions
	updated.AllowedMethods = r.AllowedMethods
	updated.Ports = r.Ports
	updated.CredentialRef = r.CredentialRef
	updated.TrustRef = r.TrustRef
	updated.Limits = r.Limits
	updated.Enabled = r.Enabled
	return updated
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
		Kind       string   `json:"kind"`
		Secret     string   `json:"secret"`
		AllowedUse []string `json:"allowedUse"`
		Targets    []string `json:"targets"`
		Endpoint   string   `json:"endpoint"`
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
	a.recordOwnerAudit(r, "credential.create", id, map[string]any{"kind": req.Kind, "success": true})
	writeJSON(w, http.StatusCreated, safeCredential(credential))
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
	a.recordOwnerAudit(r, "credential.rotate", updated.ID, map[string]any{"revision": updated.Revision})
	writeJSON(w, http.StatusOK, safeCredential(updated))
}
func (a *App) revokeCredential(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	if err := a.Store.RevokeCredential(r.Context(), r.PathValue("credentialId")); err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "credential.revoke", r.PathValue("credentialId"), map[string]any{"success": true})
	w.WriteHeader(http.StatusNoContent)
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
	a.recordOwnerAudit(r, "trust.create", trust.ID, map[string]any{"host": trust.Host, "fingerprint": trust.Fingerprint})
	writeJSON(w, http.StatusCreated, trust)
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
