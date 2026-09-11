package control

import (
	"net/http"
	"strings"

	"scout.local/scout/internal/store"
)

func (a *App) registerTopologyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/devices/{deviceId}/reconcile", a.reconcileDevices)
}

func (a *App) reconcileDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request struct {
		OtherDeviceID    string `json:"otherDeviceId"`
		Action           string `json:"action"`
		Reason           string `json:"reason"`
		ExpectedRevision int64  `json:"expectedRevision"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil || request.OtherDeviceID == "" || strings.TrimSpace(request.Reason) == "" {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if request.Action != "associate" && request.Action != "separate" {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	left, err := a.Store.GetDevice(r.Context(), r.PathValue("deviceId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	right, err := a.Store.GetDevice(r.Context(), request.OtherDeviceID)
	if err != nil || left.ID == right.ID {
		if err == nil {
			err = store.ErrConflict
		}
		writeMappedError(w, r, err)
		return
	}
	if request.ExpectedRevision > 0 && left.Revision != request.ExpectedRevision {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	relationshipType := "manual-separation"
	if request.Action == "associate" {
		relationshipType = "manual-association"
	}
	relationship := store.Relationship{FromEntity: left.ID, ToEntity: right.ID, Type: relationshipType, Confidence: 1, ProjectionRevision: left.Revision + 1, Source: "owner-correction", EvidenceIDs: []string{request.Reason}}
	if err := a.Store.PutRelationship(r.Context(), relationship); err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "topology.reconcile", left.ID, map[string]any{"otherDeviceId": right.ID, "action": request.Action, "reason": request.Reason})
	writeJSON(w, http.StatusOK, map[string]any{"relationship": relationship, "action": request.Action})
}
