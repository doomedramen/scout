package control

import (
	"net/http"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

func (a *App) getDeviceUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	policy, err := a.Store.GetDeviceUpdatePolicy(r.Context(), r.PathValue("deviceId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (a *App) updateDeviceUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request struct {
		ExpectedRevision int64      `json:"expectedRevision"`
		Mode             string     `json:"mode"`
		ReleaseID        string     `json:"releaseId"`
		Version          string     `json:"version"`
		WindowStart      *time.Time `json:"windowStart"`
		WindowEnd        *time.Time `json:"windowEnd"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	policy, err := a.Store.PutDeviceUpdatePolicy(r.Context(), store.DeviceUpdatePolicy{DeviceID: r.PathValue("deviceId"), Mode: request.Mode, ReleaseID: request.ReleaseID, Version: request.Version, WindowStart: request.WindowStart, WindowEnd: request.WindowEnd, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if policy.ReleaseID != "" && policy.Mode != "manual" {
		release, releaseErr := a.Store.GetRelease(r.Context(), policy.ReleaseID)
		if releaseErr != nil {
			writeMappedError(w, r, releaseErr)
			return
		}
		_, assignmentErr := a.Store.PutAssignment(r.Context(), store.Assignment{DeviceID: policy.DeviceID, DesiredRelease: release.ID, Generation: release.Generation, ExpiresAt: a.Store.Now().Add(24 * time.Hour), State: "queued"})
		if assignmentErr != nil {
			writeMappedError(w, r, assignmentErr)
			return
		}
	}
	a.recordOwnerAudit(r, "device.update_policy", policy.DeviceID, map[string]any{"mode": policy.Mode, "releaseId": policy.ReleaseID, "revision": policy.Revision})
	writeJSON(w, http.StatusOK, policy)
}

func (a *App) listCollectorConfigs(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListCollectorConfigs(r.Context(), r.PathValue("deviceId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if len(items) == 0 {
		states, stateErr := a.Store.ListCollectors(r.Context(), r.PathValue("deviceId"))
		if stateErr != nil {
			writeMappedError(w, r, stateErr)
			return
		}
		for _, state := range states {
			items = append(items, store.CollectorConfig{DeviceID: r.PathValue("deviceId"), CollectorID: state.ID, Provider: state.Provider, Enabled: state.State != store.CollectorDisabled, Health: state.State, Diagnostic: state.Diagnostic, LastSuccess: state.LastSuccess})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (a *App) getCollectorConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	item, err := a.Store.GetCollectorConfig(r.Context(), r.PathValue("deviceId"), r.PathValue("collectorId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *App) updateCollectorConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request struct {
		ExpectedRevision int64             `json:"expectedRevision"`
		Provider         string            `json:"provider"`
		Enabled          bool              `json:"enabled"`
		Config           map[string]string `json:"config"`
		CredentialRef    string            `json:"credentialRef"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil || strings.TrimSpace(request.Provider) == "" {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if request.CredentialRef != "" {
		credential, credentialErr := a.Store.Credential(r.Context(), request.CredentialRef)
		if credentialErr != nil || credential.RevokedAt != nil {
			writeMappedError(w, r, store.ErrForbidden)
			return
		}
	}
	item, err := a.Store.PutCollectorConfig(r.Context(), store.CollectorConfig{DeviceID: r.PathValue("deviceId"), CollectorID: r.PathValue("collectorId"), Provider: request.Provider, Enabled: request.Enabled, Config: request.Config, CredentialRef: request.CredentialRef}, request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "collector.update", item.DeviceID+"/"+item.CollectorID, map[string]any{"enabled": item.Enabled, "revision": item.Revision})
	writeJSON(w, http.StatusOK, item)
}

func (a *App) listServiceEntities(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListServiceEntities(r.Context(), r.URL.Query().Get("provider"), a.Store.Now())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}
