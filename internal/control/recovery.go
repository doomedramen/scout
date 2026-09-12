package control

import (
	"net/http"

	"scout.local/scout/internal/alerts"
	"scout.local/scout/internal/audit"
	"scout.local/scout/internal/store"
)

func (a *App) registerRecoveryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/recovery/status", a.recoveryStatus)
	mux.HandleFunc("POST /api/v1/recovery/start", a.startRecovery)
	mux.HandleFunc("POST /api/v1/recovery/reconcile", a.reconcileRecovery)
	mux.HandleFunc("POST /api/v1/monitoring/notifications/resume", a.resumeNotifications)
}

func (a *App) recoveryStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	workspace, err := a.Store.Workspace(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	telemetryStatus, err := a.Store.TelemetryStatus(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspace, "telemetry": telemetryStatus})
}

func (a *App) startRecovery(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	state, err := a.Store.StartRecovery(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	_ = a.Audit.Record(r.Context(), audit.Event{ActorKind: "owner", Action: "recovery.start", Target: "workspace", Outcome: map[string]any{"success": true}, RequestID: r.Header.Get("X-Request-ID")})
	writeJSON(w, http.StatusAccepted, state)
}

func (a *App) reconcileRecovery(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	if a.Secrets == nil {
		writeMappedError(w, r, store.ErrForbidden)
		return
	}
	state, err := a.Store.ReconcileRecovery(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	_ = a.Audit.Record(r.Context(), audit.Event{ActorKind: "owner", Action: "recovery.reconcile", Target: "workspace", Outcome: map[string]any{"success": true, "enrollmentPaused": state.EnrollmentPaused, "updatesPaused": state.UpdatesPaused}, RequestID: r.Header.Get("X-Request-ID")})
	writeJSON(w, http.StatusOK, state)
}

func (a *App) resumeNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request expectedRevisionInput
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	state, result, err := alerts.ResumeNotifications(r.Context(), a.Store, request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "monitoring.notifications.resume", "workspace", map[string]any{
		"revision": state.PolicyRevision,
		"queued":   result.Enqueued,
		"dropped":  result.Dropped,
	})
	settings, err := a.Store.GetMonitoringSettings(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
