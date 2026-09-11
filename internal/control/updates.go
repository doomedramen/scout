package control

import (
	"net/http"
	"time"

	"scout.local/scout/internal/store"
)

func (a *App) registerUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/releases", a.listReleases)
	mux.HandleFunc("POST /api/v1/releases/import", a.importRelease)
	mux.HandleFunc("GET /api/v1/rollouts", a.listAssignments)
	mux.HandleFunc("POST /api/v1/rollouts", a.createRollout)
	mux.HandleFunc("GET /api/v1/agent/v1/releases/{digest}", a.agentRelease)
	mux.HandleFunc("GET /agent/v1/releases/{digest}", a.agentRelease)
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
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (a *App) importRelease(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	writeMappedError(w, r, store.ErrInvalid)
}

func (a *App) listAssignments(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListAssignments(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (a *App) createRollout(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request struct {
		ReleaseID   string   `json:"releaseId"`
		Targets     []string `json:"targets"`
		Concurrency int      `json:"concurrency"`
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
	if request.Concurrency < 1 {
		request.Concurrency = 1
	}
	assignments := []store.Assignment{}
	for _, deviceID := range request.Targets {
		assignment, putErr := a.Store.PutAssignment(r.Context(), store.Assignment{DeviceID: deviceID, DesiredRelease: release.ID, Generation: release.Generation, ExpiresAt: time.Now().UTC().Add(24 * time.Hour), State: "queued"})
		if putErr != nil {
			writeMappedError(w, r, putErr)
			return
		}
		assignments = append(assignments, assignment)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"items": assignments, "concurrency": request.Concurrency})
}

func (a *App) agentRelease(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	assignment, err := a.Store.Assignment(r.Context(), agent.DeviceID)
	if err != nil {
		writeMappedError(w, r, store.ErrNotFound)
		return
	}
	release, err := a.Store.GetRelease(r.Context(), assignment.DesiredRelease)
	if err != nil || release.RevokedAt != nil || release.Digest != r.PathValue("digest") {
		writeMappedError(w, r, store.ErrForbidden)
		return
	}
	writeMappedError(w, r, store.ErrNotFound)
}
