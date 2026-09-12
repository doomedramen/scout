package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scout.local/scout/internal/audit"
	"scout.local/scout/internal/store"
	"scout.local/scout/internal/updates"
)

func (a *App) registerUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/releases", a.listReleases)
	mux.HandleFunc("POST /api/v1/releases/import", a.importRelease)
	mux.HandleFunc("DELETE /api/v1/releases/{releaseId}", a.revokeRelease)
	mux.HandleFunc("GET /api/v1/rollouts", a.listRollouts)
	mux.HandleFunc("POST /api/v1/rollouts", a.createRollout)
	mux.HandleFunc("POST /api/v1/rollouts/{rolloutId}/pause", a.pauseRollout)
	mux.HandleFunc("GET /api/v1/bootstrap/agent/{architecture}", a.agentBootstrap)
	mux.HandleFunc("GET /api/v1/bootstrap/agent/install.sh", a.agentInstaller)
	mux.HandleFunc("GET /api/v1/agent/v1/releases/{digest}", a.agentRelease)
	mux.HandleFunc("GET /agent/v1/releases/{digest}", a.agentRelease)
}

var supportedAgentArchitectures = map[string]struct{}{
	"amd64": {},
	"arm64": {},
}

const agentServerURLPlaceholder = "__SCOUT_SERVER_URL__"

func (a *App) agentBootstrap(w http.ResponseWriter, r *http.Request) {
	architecture := r.PathValue("architecture")
	if _, ok := supportedAgentArchitectures[architecture]; !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "Agent binary is not available for this architecture", false)
		return
	}
	directory := strings.TrimSpace(a.Config.AgentBootstrapDir)
	if directory == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "Agent bootstrap is not configured", false)
		return
	}
	path := filepath.Join(directory, "scout-agent-linux-"+architecture)
	a.serveBootstrapFile(w, r, path, "scout-agent-linux-"+architecture, "application/octet-stream", "X-Scout-Agent-SHA256")
}

func (a *App) agentInstaller(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(a.Config.AgentInstallerFile)
	if path == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "Agent installer is not configured", false)
		return
	}
	artifact, ok := readBootstrapFile(w, r, path)
	if !ok {
		return
	}
	if bytes.Contains(artifact, []byte(agentServerURLPlaceholder)) {
		origin, valid := bootstrapRequestOrigin(r)
		if !valid {
			writeError(w, r, http.StatusInternalServerError, "internal_error", "Agent installer URL could not be derived from this request", false)
			return
		}
		artifact = bytes.ReplaceAll(artifact, []byte(agentServerURLPlaceholder), []byte(origin))
	}
	serveBootstrapArtifact(w, r, artifact, "install-agent.sh", "text/plain; charset=utf-8", "X-Scout-Agent-Installer-SHA256", "no-store")
}

func (a *App) serveBootstrapFile(w http.ResponseWriter, r *http.Request, path, filename, contentType, checksumHeader string) {
	artifact, ok := readBootstrapFile(w, r, path)
	if !ok {
		return
	}
	serveBootstrapArtifact(w, r, artifact, filename, contentType, checksumHeader, "public, max-age=300")
}

func readBootstrapFile(w http.ResponseWriter, r *http.Request, path string) ([]byte, bool) {
	artifact, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, r, http.StatusNotFound, "not_found", "Agent bootstrap artifact is not available", false)
		return nil, false
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "Agent bootstrap artifact could not be read", false)
		return nil, false
	}
	if len(artifact) == 0 {
		writeError(w, r, http.StatusNotFound, "not_found", "Agent bootstrap artifact is empty", false)
		return nil, false
	}
	return artifact, true
}

func serveBootstrapArtifact(w http.ResponseWriter, r *http.Request, artifact []byte, filename, contentType, checksumHeader, cacheControl string) {
	digest := sha256.Sum256(artifact)
	digestHex := hex.EncodeToString(digest[:])
	w.Header().Set(checksumHeader, digestHex)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	serveArtifactWithOptions(w, r, artifact, "sha256:"+digestHex, contentType, cacheControl)
}

func bootstrapRequestOrigin(r *http.Request) (string, bool) {
	host := strings.TrimSpace(r.Host)
	if host == "" || strings.ContainsAny(host, "/?#%\\\"' \t\r\n@") {
		return "", false
	}
	scheme := bootstrapRequestScheme(r)
	origin := scheme + "://" + host
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != scheme || parsed.Host != host || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	return origin, true
}

func bootstrapRequestScheme(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("Forwarded")); forwarded != "" {
		first := strings.TrimSpace(strings.SplitN(forwarded, ",", 2)[0])
		for _, part := range strings.Split(first, ";") {
			key, value, found := strings.Cut(strings.TrimSpace(part), "=")
			if !found || !strings.EqualFold(key, "proto") {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), "\"")
			if value == "http" || value == "https" {
				return value
			}
		}
	}
	if forwarded := strings.TrimSpace(strings.SplitN(r.Header.Get("X-Forwarded-Proto"), ",", 2)[0]); forwarded == "http" || forwarded == "https" {
		return forwarded
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func (a *App) listReleases(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListReleases(r.Context(), r.URL.Query().Get("platform"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	safe := make([]map[string]any, 0, len(items))
	for _, item := range items {
		safe = append(safe, safeRelease(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": safe, "nextCursor": nil})
}

func safeRelease(item store.Release) map[string]any {
	return map[string]any{"id": item.ID, "version": item.Version, "generation": item.Generation, "platform": item.Platform, "architecture": item.Architecture, "digest": item.Digest, "bytes": item.Bytes, "trustKeyId": item.TrustKeyID, "manifest": item.Manifest, "revokedAt": item.RevokedAt}
}

func (a *App) importRelease(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	if a.Updates == nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a.Updates.MaxBundleBytes))
	if err != nil {
		writeMappedError(w, r, store.ErrBackpressure)
		return
	}
	release, err := a.Updates.ImportBundle(r.Context(), data)
	if err != nil {
		a.recordOwnerAudit(r, "release.import", "release", map[string]any{"success": false, "errorCode": updateErrorCode(err)})
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "release.import", release.ID, map[string]any{"success": true, "generation": release.Generation})
	writeJSON(w, http.StatusCreated, safeRelease(release))
}

func (a *App) revokeRelease(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	if err := a.Updates.Revoke(r.Context(), r.PathValue("releaseId")); err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "release.revoke", r.PathValue("releaseId"), map[string]any{"success": true})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) listRollouts(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	rollouts, err := a.Store.ListRollouts(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	assignments, err := a.Store.ListAssignments(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rollouts, "assignments": assignments, "nextCursor": nil})
}

func (a *App) createRollout(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request struct {
		ReleaseID        string    `json:"releaseId"`
		Mode             string    `json:"mode"`
		Targets          []string  `json:"targets"`
		Concurrency      int       `json:"concurrency"`
		Canaries         int       `json:"canaries"`
		FailureThreshold int       `json:"failureThreshold"`
		WindowStart      time.Time `json:"windowStart"`
		WindowEnd        time.Time `json:"windowEnd"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	release, err := a.Store.GetRelease(r.Context(), request.ReleaseID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if release.RevokedAt != nil {
		writeMappedError(w, r, store.ErrRevoked)
		return
	}
	if request.Mode == "" {
		request.Mode = "manual"
	}
	if request.Concurrency == 0 {
		request.Concurrency = 1
	}
	if request.FailureThreshold == 0 {
		request.FailureThreshold = 3
	}
	rollout := store.Rollout{ReleaseID: release.ID, Mode: request.Mode, Targets: request.Targets, Concurrency: request.Concurrency, Canaries: request.Canaries, FailureThreshold: request.FailureThreshold}
	if !request.WindowStart.IsZero() {
		rollout.WindowStart = &request.WindowStart
	}
	if !request.WindowEnd.IsZero() {
		rollout.WindowEnd = &request.WindowEnd
	}
	if err := updates.ValidateRollout(rollout, release); err != nil {
		writeMappedError(w, r, err)
		return
	}
	rollout, err = a.Store.PutRollout(r.Context(), rollout)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	devices, err := a.Store.ListDevices(r.Context(), store.DeviceFilter{})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	planned, err := updates.PlanAssignments(rollout, release, devices, time.Now().UTC())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	assignments := make([]store.Assignment, 0, len(planned))
	for _, plannedAssignment := range planned {
		assignment, putErr := a.Store.PutAssignment(r.Context(), plannedAssignment)
		if putErr != nil {
			writeMappedError(w, r, putErr)
			return
		}
		assignments = append(assignments, assignment)
	}
	a.recordOwnerAudit(r, "rollout.create", rollout.ID, map[string]any{"releaseId": release.ID, "targetCount": len(assignments), "mode": rollout.Mode})
	writeJSON(w, http.StatusAccepted, map[string]any{"rollout": rollout, "assignments": assignments})
}

func (a *App) pauseRollout(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	rollout, err := a.Store.UpdateRollout(r.Context(), r.PathValue("rolloutId"), func(value *store.Rollout) error { value.Paused = true; return nil })
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	_ = a.Audit.Record(r.Context(), audit.Event{ActorKind: "owner", Action: "rollout.pause", Target: rollout.ID, Outcome: map[string]any{"success": true}, RequestID: r.Header.Get("X-Request-ID")})
	writeJSON(w, http.StatusOK, rollout)
}

func (a *App) agentRelease(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	workspace, err := a.Store.Workspace(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if workspace.RecoveryMode || workspace.UpdatesPaused {
		writeMappedError(w, r, store.ErrBackpressure)
		return
	}
	assignment, err := a.Store.Assignment(r.Context(), agent.DeviceID)
	if err != nil {
		writeMappedError(w, r, store.ErrNotFound)
		return
	}
	if assignment.ExpiresAt.Before(time.Now().UTC()) || assignment.State == "paused" || assignment.State == "failed" {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	release, err := a.Store.GetRelease(r.Context(), assignment.DesiredRelease)
	if err != nil || release.RevokedAt != nil || release.Digest != r.PathValue("digest") {
		writeMappedError(w, r, store.ErrForbidden)
		return
	}
	artifact, err := a.Updates.Artifact(r.Context(), release.ID, r.PathValue("digest"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	serveArtifact(w, r, artifact, release.Digest)
}

func serveArtifact(w http.ResponseWriter, r *http.Request, artifact []byte, digest string) {
	serveArtifactWithOptions(w, r, artifact, digest, "application/octet-stream", "private, immutable")
}

func serveArtifactWithOptions(w http.ResponseWriter, r *http.Request, artifact []byte, digest, contentType, cacheControl string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", strconv.Quote(digest))
	start, end, partial, err := artifactRange(r.Header.Get("Range"), int64(len(artifact)))
	if err != nil {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(int64(len(artifact)), 10))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if partial {
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(int64(len(artifact)), 10))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(artifact[start : end+1])
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(artifact)))
	_, _ = w.Write(artifact)
}

func artifactRange(raw string, size int64) (int64, int64, bool, error) {
	if raw == "" {
		return 0, size - 1, false, nil
	}
	if !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return 0, 0, false, errors.New("invalid range")
	}
	parts := strings.Split(strings.TrimPrefix(raw, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false, errors.New("invalid range")
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false, errors.New("invalid range")
	}
	end := size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start || end >= size {
			return 0, 0, false, errors.New("invalid range")
		}
	}
	return start, end, true, nil
}

func updateErrorCode(err error) string {
	switch {
	case errors.Is(err, updates.ErrTampered):
		return "tampered"
	case errors.Is(err, store.ErrUnauthorized):
		return "unknown_signing_key"
	case errors.Is(err, store.ErrConflict):
		return "conflict"
	default:
		return "invalid"
	}
}
