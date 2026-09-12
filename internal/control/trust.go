package control

import (
	"net/http"

	"scout.local/scout/internal/store"
)

func (a *App) updateTrust(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request struct {
		ExpectedRevision int64  `json:"expectedRevision"`
		Host             string `json:"host"`
		Endpoint         string `json:"endpoint"`
		Fingerprint      string `json:"fingerprint"`
		PublicKey        string `json:"publicKey"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	current, err := a.Store.Trust(r.Context(), r.PathValue("trustId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	next := current
	if request.Host != "" {
		next.Host = request.Host
	}
	if request.Endpoint != "" {
		next.Endpoint = request.Endpoint
	}
	if request.Fingerprint != "" {
		next.Fingerprint = request.Fingerprint
	}
	if request.PublicKey != "" {
		next.PublicKey = request.PublicKey
	}
	changedKey := next.Fingerprint != current.Fingerprint || next.PublicKey != current.PublicKey
	updated, err := a.Store.UpdateTrust(r.Context(), current.ID, request.ExpectedRevision, next)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	action := "trust.update"
	if changedKey {
		action = "trust.key_change"
	}
	a.recordOwnerAudit(r, action, updated.ID, map[string]any{"revision": updated.Revision, "changed": changedKey})
	affected := 0
	if a.Enrollment != nil && updated.ScopeID != "" {
		affected, err = a.Enrollment.ReevaluateScope(r.Context(), updated.ScopeID)
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": updated.ID, "scopeId": updated.ScopeID, "endpoint": updated.Endpoint, "host": updated.Host, "fingerprint": updated.Fingerprint, "publicKey": updated.PublicKey, "revision": updated.Revision, "revokedAt": updated.RevokedAt, "affectedCandidateCount": affected})
}

func (a *App) revokeTrust(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	trust, err := a.Store.Trust(r.Context(), r.PathValue("trustId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if err := a.Store.RevokeTrust(r.Context(), trust.ID); err != nil {
		writeMappedError(w, r, err)
		return
	}
	affected := 0
	if a.Enrollment != nil && trust.ScopeID != "" {
		affected, err = a.Enrollment.ReevaluateScope(r.Context(), trust.ScopeID)
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	a.recordOwnerAudit(r, "trust.revoke", trust.ID, map[string]any{"success": true})
	writeJSON(w, http.StatusOK, map[string]any{"affectedCandidateCount": affected})
}
