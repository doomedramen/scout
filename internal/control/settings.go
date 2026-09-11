package control

import (
	"net/http"

	"scout.local/scout/internal/store"
	"scout.local/scout/internal/telemetry"
)

func (a *App) registerSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings/telemetry", a.telemetrySettings)
	mux.HandleFunc("PATCH /api/v1/settings/telemetry", a.updateTelemetrySettings)
}

func (a *App) telemetrySettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	status, err := a.Store.TelemetryStatus(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *App) updateTelemetrySettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request struct {
		RetentionHours *int `json:"retentionHours"`
		MaxSamples     *int `json:"maxSamples"`
	}
	if err := decodeJSON(r, &request, 16<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	workspace, err := a.Store.Workspace(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if request.RetentionHours != nil {
		workspace.RetentionHours = *request.RetentionHours
	}
	if request.MaxSamples != nil {
		workspace.MaxSamples = *request.MaxSamples
	}
	if workspace.RetentionHours < 1 || workspace.RetentionHours > 24*3650 || workspace.MaxSamples < 1000 || workspace.MaxSamples > 100_000_000 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if _, err := a.Store.SetWorkspace(r.Context(), func(state *store.WorkspaceState) error {
		state.RetentionHours = workspace.RetentionHours
		state.MaxSamples = workspace.MaxSamples
		return nil
	}); err != nil {
		writeMappedError(w, r, err)
		return
	}
	report, err := a.Telemetry.EnforceRetention(r.Context(), telemetry.RetentionPolicy{Hours: workspace.RetentionHours, MaxSamples: workspace.MaxSamples})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report.Status)
}
