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
	writeJSON(w, http.StatusOK, updated)
}
