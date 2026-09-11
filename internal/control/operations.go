package control

import (
	"net/http"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

func (a *App) registerOperationsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/control/pause", a.pauseControl)
	mux.HandleFunc("GET /api/v1/control/state", a.controlState)
	mux.HandleFunc("POST /api/v1/devices/{deviceId}/decommission", a.decommission)
	mux.HandleFunc("POST /api/v1/devices/{deviceId}/reenable", a.reenable)
	mux.HandleFunc("GET /api/v1/audit", a.auditEvents)
}

func (a *App) pauseControl(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request struct {
		Discovery  bool `json:"discovery"`
		Enrollment bool `json:"enrollment"`
		Updates    bool `json:"updates"`
	}
	if err := decodeJSON(r, &request, 16<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	state, err := a.Store.SetControlPause(r.Context(), request.Discovery, request.Enrollment, request.Updates)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	status := http.StatusOK
	if state.PausePending {
		status = http.StatusAccepted
	}
	a.recordOwnerAudit(r, "control.pause", "workspace", map[string]any{"discovery": request.Discovery, "enrollment": request.Enrollment, "updates": request.Updates, "pending": state.PausePending})
	writeJSON(w, status, state)
}

func (a *App) controlState(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	state, err := a.Store.Workspace(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *App) decommission(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request struct {
		Uninstall bool   `json:"uninstall"`
		Reason    string `json:"reason"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil || strings.TrimSpace(request.Reason) == "" {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	device, err := a.Store.DecommissionDevice(r.Context(), r.PathValue("deviceId"), request.Reason)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	response := map[string]any{"device": device, "identityRevoked": true, "excluded": true, "uninstall": "not_requested"}
	if request.Uninstall {
		job, jobErr := a.Store.CreateJob(r.Context(), store.Job{Kind: "uninstall", DeviceID: device.ID, State: "queued", Deadline: timePtr(time.Now().UTC().Add(10 * time.Minute))})
		if jobErr != nil {
			writeMappedError(w, r, jobErr)
			return
		}
		response["uninstall"] = map[string]any{"state": job.State, "jobId": job.ID, "confirmed": false}
	}
	a.recordOwnerAudit(r, "device.decommission", device.ID, map[string]any{"uninstallRequested": request.Uninstall, "success": true})
	writeJSON(w, http.StatusAccepted, response)
}

func (a *App) reenable(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if err := decodeJSON(r, &request, 16<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	device, err := a.Store.ReenableDevice(r.Context(), r.PathValue("deviceId"), request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "device.reenable", device.ID, map[string]any{"success": true})
	writeJSON(w, http.StatusOK, device)
}

func (a *App) auditEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	events, err := a.Store.ListAudit(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if len(events) > 500 {
		events = events[:500]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events, "nextCursor": nil})
}

func timePtr(value time.Time) *time.Time { return &value }
