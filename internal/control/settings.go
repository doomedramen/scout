package control

import (
	"net/http"
	"strings"

	"scout.local/scout/internal/store"
	"scout.local/scout/internal/telemetry"
)

func (a *App) registerSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/monitoring/settings", a.monitoringSettings)
	mux.HandleFunc("PATCH /api/v1/monitoring/settings", a.updateMonitoringSettings)
	mux.HandleFunc("POST /api/v1/monitoring/retention-preview", a.createRetentionPreview)
	mux.HandleFunc("GET /api/v1/monitoring/status", a.monitoringStatus)
	mux.HandleFunc("GET /api/v1/settings/telemetry", a.telemetrySettings)
	mux.HandleFunc("PATCH /api/v1/settings/telemetry", a.updateTelemetrySettings)
}

func (a *App) monitoringSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	settings, err := a.Store.GetMonitoringSettings(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (a *App) monitoringStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	status, err := a.Store.MonitoringStatus(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *App) createRetentionPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 128 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	var request store.RetentionPreviewInput
	if err := decodeJSON(r, &request, 16<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	request.IdempotencyKey = idempotencyKey
	preview, err := a.Store.CreateRetentionPreview(r.Context(), request)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (a *App) updateMonitoringSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request store.MonitoringSettingsPatch
	if err := decodeJSON(r, &request, 16<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	settings, err := a.Store.UpdateMonitoringSettings(r.Context(), request)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if a.Telemetry != nil {
		_, err = a.Telemetry.EnforceRetention(r.Context(), telemetry.RetentionPolicy{
			RawDays: settings.Retention.RawDays, FiveMinuteDays: settings.Retention.FiveMinuteDays, HourlyDays: settings.Retention.HourlyDays,
		})
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	a.recordOwnerAudit(r, "monitoring.settings.update", "workspace", map[string]any{
		"revision":        settings.Revision,
		"retention":       settings.Retention,
		"diskBudgetBytes": settings.DiskBudgetBytes,
	})
	writeJSON(w, http.StatusOK, settings)
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
		RetentionHours       *int   `json:"retentionHours"`
		MaxSamples           *int   `json:"maxSamples"`
		TelemetryBudgetBytes *int64 `json:"telemetryBudgetBytes"`
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
	if request.TelemetryBudgetBytes != nil {
		workspace.TelemetryBudgetBytes = *request.TelemetryBudgetBytes
	}
	if workspace.RetentionHours < 1 || workspace.RetentionHours > 24*3650 || workspace.MaxSamples < 0 || workspace.MaxSamples > 100_000_000 || workspace.TelemetryBudgetBytes < 1<<20 || workspace.TelemetryBudgetBytes > 1<<40 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if _, err := a.Store.SetWorkspace(r.Context(), func(state *store.WorkspaceState) error {
		state.RetentionHours = workspace.RetentionHours
		state.MaxSamples = workspace.MaxSamples
		state.TelemetryBudgetBytes = workspace.TelemetryBudgetBytes
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
