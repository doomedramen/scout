package control

import (
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"time"

	"scout.local/scout/internal/audit"
	"scout.local/scout/internal/identity"
	"scout.local/scout/internal/store"
	"scout.local/scout/internal/telemetry"
	"scout.local/scout/internal/updates"
)

func (a *App) registerAgentRoutes(mux *http.ServeMux) {
	for _, prefix := range []string{"/api/v1/agent/v1", "/agent/v1"} {
		mux.HandleFunc("POST "+prefix+"/enroll", a.agentEnroll)
		mux.HandleFunc("POST "+prefix+"/renew", a.agentRenew)
		mux.HandleFunc("POST "+prefix+"/batches", a.agentBatch)
		mux.HandleFunc("POST "+prefix+"/heartbeat", a.agentHeartbeat)
		mux.HandleFunc("GET "+prefix+"/desired-state", a.agentDesiredState)
		mux.HandleFunc("POST "+prefix+"/update-results", a.agentUpdateResult)
	}
}

func (a *App) agentEnroll(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Invitation   string `json:"invitation"`
		CSRPEM       string `json:"csrPem"`
		AgentVersion string `json:"agentVersion"`
		Platform     string `json:"platform"`
		Architecture string `json:"architecture"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	result, err := a.Identity.Enroll(r.Context(), identity.EnrollmentRequest{Invitation: request.Invitation, CSRPEM: request.CSRPEM, AgentVersion: request.AgentVersion, Platform: request.Platform, Architecture: request.Architecture})
	if err != nil {
		_ = a.Audit.Record(r.Context(), audit.Event{ActorKind: "anonymous", Action: "agent.enroll", Target: "invitation", Outcome: map[string]any{"success": false, "code": errorCode(err)}, RequestID: r.Header.Get("X-Request-ID")})
		writeMappedError(w, r, err)
		return
	}
	_ = a.Audit.Record(r.Context(), audit.Event{ActorKind: "agent", ActorID: result.AgentID, Action: "agent.enroll", Target: result.DeviceID, Outcome: map[string]any{"success": true}, RequestID: r.Header.Get("X-Request-ID")})
	writeJSON(w, http.StatusOK, result)
}

func (a *App) agentRenew(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	var request struct {
		CSRPEM string `json:"csrPem"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	result, err := a.Identity.Renew(r.Context(), agent.ID, request.CSRPEM)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *App) agentBatch(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a.Config.MaxBodyBytes))
	if err != nil {
		writeMappedError(w, r, store.ErrBackpressure)
		return
	}
	batch, err := telemetry.DecodeBatch(data)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	result, err := a.Telemetry.Ingest(r.Context(), agent.ID, batch, data)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batchId": batch.BatchID, "acceptedAt": result.AcceptedAt, "duplicate": result.Duplicate})
}

func (a *App) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	var request struct {
		BootID           string                           `json:"bootId"`
		InstalledVersion string                           `json:"installedVersion"`
		UptimeSeconds    int64                            `json:"uptimeSeconds"`
		CollectorStates  []store.CollectorDescriptorState `json:"collectorStates"`
		UpdateState      map[string]string                `json:"updateState"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	serverTime, revision, err := a.Telemetry.Heartbeat(r.Context(), agent.ID, telemetry.Heartbeat{BootID: request.BootID, InstalledVersion: request.InstalledVersion, UptimeSeconds: request.UptimeSeconds, CollectorStates: request.CollectorStates, UpdateState: request.UpdateState})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"serverTime": serverTime, "policyRevision": revision})
}

func (a *App) agentDesiredState(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	scopes, err := a.Store.ListScopes(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	visible := make([]store.Scope, 0, len(scopes))
	for _, scope := range scopes {
		if scope.Enabled {
			visible = append(visible, scope)
		}
	}
	assignment, assignmentErr := a.Store.Assignment(r.Context(), agent.DeviceID)
	update := any(nil)
	workspace, workspaceErr := a.Store.Workspace(r.Context())
	if assignmentErr == nil && workspaceErr == nil && !workspace.RecoveryMode && !workspace.UpdatesPaused && assignment.ExpiresAt.After(a.Store.Now()) && assignment.State != "paused" && assignment.State != "failed" {
		if release, releaseErr := a.Store.GetRelease(r.Context(), assignment.DesiredRelease); releaseErr == nil && release.RevokedAt == nil {
			update = map[string]any{"assignment": assignment, "release": releaseManifest(release), "artifactPath": "/api/v1/agent/v1/releases/" + release.Digest}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": maxScopeRevision(visible), "expiresAt": time.Now().UTC().Add(5 * time.Minute), "discoveryPolicy": visible, "collectorConfig": []any{}, "updateAssignment": update})
}

func releaseManifest(release store.Release) map[string]any {
	if release.Manifest != nil {
		return release.Manifest
	}
	return map[string]any{"version": release.Version, "generation": release.Generation, "platform": release.Platform, "architecture": release.Architecture, "digest": release.Digest, "bytes": release.Bytes, "trustKeyId": release.TrustKeyID}
}

func maxScopeRevision(scopes []store.Scope) int64 {
	var result int64
	for _, scope := range scopes {
		if scope.Revision > result {
			result = scope.Revision
		}
	}
	return result
}

func (a *App) agentUpdateResult(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	var request struct {
		AssignmentID     string `json:"assignmentId"`
		Generation       int64  `json:"generation"`
		State            string `json:"state"`
		InstalledVersion string `json:"installedVersion"`
		ErrorCode        string `json:"errorCode"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	assignment, err := a.Store.Assignment(r.Context(), agent.DeviceID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if assignment.ID != request.AssignmentID || assignment.Generation != request.Generation {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	if assignment.ExpiresAt.Before(a.Store.Now()) {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	if !validUpdateState(request.State) {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	_, err = a.Store.UpdateAssignment(r.Context(), agent.DeviceID, func(item *store.Assignment) error { item.State = request.State; return nil })
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if request.InstalledVersion != "" {
		_, _ = a.Store.UpdateAgent(r.Context(), agent.ID, func(item *store.AgentIdentity) error { item.InstalledVersion = request.InstalledVersion; return nil })
	}
	if request.State == "failed" && assignment.RolloutID != "" {
		_, _ = a.Store.UpdateRollout(r.Context(), assignment.RolloutID, func(item *store.Rollout) error { updates.RecordRolloutFailure(item); return nil })
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func validUpdateState(value string) bool {
	switch value {
	case "queued", "downloading", "verifying", "staged", "installing", "healthy", "failed", "rolled_back", "rejected", "paused":
		return true
	default:
		return false
	}
}

func (a *App) requireAgent(w http.ResponseWriter, r *http.Request) (store.AgentIdentity, bool) {
	if a.Config.AgentRequireMTLS || a.Config.Production {
		if r.TLS == nil {
			writeError(w, r, http.StatusUnauthorized, "agent_mtls_required", "Authenticated agent transport required", false)
			return store.AgentIdentity{}, false
		}
	}
	token := bearerToken(r)
	var state *tls.ConnectionState
	if r.TLS != nil {
		state = r.TLS
	}
	agent, err := a.Identity.AgentFromRequest(r.Context(), token, state)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Authenticated agent identity required", false)
		return store.AgentIdentity{}, false
	}
	if agent.RevokedAt != nil || !time.Now().UTC().Before(agent.ExpiresAt) {
		writeError(w, r, http.StatusUnauthorized, "revoked", "Agent identity is no longer valid", false)
		return store.AgentIdentity{}, false
	}
	return agent, true
}
func bearerToken(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
}
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if err == store.ErrExpired {
		return "expired"
	}
	if err == store.ErrRevoked {
		return "revoked"
	}
	if err == store.ErrConflict {
		return "conflict"
	}
	if err == store.ErrInvalid {
		return "invalid"
	}
	return "failed"
}
