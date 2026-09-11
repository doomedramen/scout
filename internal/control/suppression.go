package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

type suppressionWindowInput struct {
	Name       string     `json:"name"`
	TargetKind string     `json:"targetKind"`
	TargetID   *string    `json:"targetId"`
	Enabled    *bool      `json:"enabled"`
	Mode       string     `json:"mode"`
	Timezone   string     `json:"timezone"`
	Weekdays   []int      `json:"weekdays"`
	StartLocal string     `json:"startLocal"`
	EndLocal   string     `json:"endLocal"`
	StartsAt   *time.Time `json:"startsAt"`
	EndsAt     *time.Time `json:"endsAt"`
}

type suppressionWindowPatch struct {
	ExpectedRevision int64           `json:"expectedRevision"`
	Name             json.RawMessage `json:"name"`
	TargetKind       json.RawMessage `json:"targetKind"`
	TargetID         json.RawMessage `json:"targetId"`
	Enabled          json.RawMessage `json:"enabled"`
	Mode             json.RawMessage `json:"mode"`
	Timezone         json.RawMessage `json:"timezone"`
	Weekdays         json.RawMessage `json:"weekdays"`
	StartLocal       json.RawMessage `json:"startLocal"`
	EndLocal         json.RawMessage `json:"endLocal"`
	StartsAt         json.RawMessage `json:"startsAt"`
	EndsAt           json.RawMessage `json:"endsAt"`
}

func (a *App) registerSuppressionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/suppression-windows", a.listSuppressionWindows)
	mux.HandleFunc("POST /api/v1/suppression-windows", a.createSuppressionWindow)
	mux.HandleFunc("GET /api/v1/suppression-windows/{windowId}", a.getSuppressionWindow)
	mux.HandleFunc("PATCH /api/v1/suppression-windows/{windowId}", a.updateSuppressionWindow)
	mux.HandleFunc("DELETE /api/v1/suppression-windows/{windowId}", a.deleteSuppressionWindow)
}

func (a *App) listSuppressionWindows(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	limit, cursor, err := alertPageParams(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	page, err := a.Store.ListSuppressionWindows(r.Context(), store.SuppressionWindowQuery{Limit: limit, Cursor: cursor})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, suppressionWindowResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nullableCursor(page.NextCursor)})
}

func (a *App) createSuppressionWindow(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request suppressionWindowInput
	if err := decodeJSON(r, &request, 64<<10); err != nil || request.Enabled == nil || strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.TargetKind) == "" || strings.TrimSpace(request.Mode) == "" {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	window := request.window()
	created, err := a.Store.PutSuppressionWindow(r.Context(), window, 0)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "suppression_window.create", created.ID, map[string]any{"revision": created.Revision, "mode": created.Mode, "targetKind": created.TargetKind})
	writeJSON(w, http.StatusCreated, suppressionWindowResponse(created))
}

func (a *App) getSuppressionWindow(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	window, err := a.Store.GetSuppressionWindow(r.Context(), r.PathValue("windowId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if window.RetiredAt != nil {
		writeMappedError(w, r, store.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, suppressionWindowResponse(window))
}

func (a *App) updateSuppressionWindow(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request suppressionWindowPatch
	if err := decodeJSON(r, &request, 64<<10); err != nil || request.ExpectedRevision < 1 || !suppressionWindowPatchHasChanges(request) {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	current, err := a.Store.GetSuppressionWindow(r.Context(), r.PathValue("windowId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if current.RetiredAt != nil {
		writeMappedError(w, r, store.ErrNotFound)
		return
	}
	updated, err := applySuppressionWindowPatch(current, request)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	stored, err := a.Store.PutSuppressionWindow(r.Context(), updated, request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "suppression_window.update", stored.ID, map[string]any{"revision": stored.Revision})
	writeJSON(w, http.StatusOK, suppressionWindowResponse(stored))
}

func (a *App) deleteSuppressionWindow(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request expectedRevisionInput
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	window, err := a.Store.RetireSuppressionWindow(r.Context(), r.PathValue("windowId"), request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "suppression_window.retire", window.ID, map[string]any{"revision": window.Revision})
	w.WriteHeader(http.StatusNoContent)
}

func (request suppressionWindowInput) window() store.SuppressionWindow {
	targetID := ""
	if request.TargetID != nil {
		targetID = strings.TrimSpace(*request.TargetID)
	}
	return store.SuppressionWindow{
		Name: request.Name, TargetKind: request.TargetKind, TargetID: targetID, Enabled: *request.Enabled, Mode: request.Mode,
		Timezone: request.Timezone, Weekdays: request.Weekdays, StartLocal: request.StartLocal, EndLocal: request.EndLocal,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt,
	}
}

func suppressionWindowPatchHasChanges(request suppressionWindowPatch) bool {
	return len(request.Name) > 0 || len(request.TargetKind) > 0 || len(request.TargetID) > 0 || len(request.Enabled) > 0 || len(request.Mode) > 0 || len(request.Timezone) > 0 || len(request.Weekdays) > 0 || len(request.StartLocal) > 0 || len(request.EndLocal) > 0 || len(request.StartsAt) > 0 || len(request.EndsAt) > 0
}

func applySuppressionWindowPatch(current store.SuppressionWindow, request suppressionWindowPatch) (store.SuppressionWindow, error) {
	updated := current
	if value, present, err := optionalSuppressionString(request.Name); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.Name = value
	}
	if value, present, err := optionalSuppressionString(request.TargetKind); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.TargetKind = value
		if value == "fleet" && len(request.TargetID) == 0 {
			updated.TargetID = ""
		}
	}
	if value, present, err := optionalSuppressionString(request.TargetID); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.TargetID = value
	}
	if value, present, err := optionalSuppressionBool(request.Enabled); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.Enabled = value
	}
	if value, present, err := optionalSuppressionString(request.Mode); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.Mode = value
		if value == store.SuppressionWindowRecurring {
			updated.StartsAt = nil
			updated.EndsAt = nil
		} else if value == store.SuppressionWindowOneTime {
			updated.Timezone = ""
			updated.Weekdays = nil
			updated.StartLocal = ""
			updated.EndLocal = ""
		}
	}
	if value, present, err := optionalSuppressionString(request.Timezone); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.Timezone = value
	}
	if value, present, err := optionalSuppressionInts(request.Weekdays); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.Weekdays = value
	}
	if value, present, err := optionalSuppressionString(request.StartLocal); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.StartLocal = value
	}
	if value, present, err := optionalSuppressionString(request.EndLocal); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.EndLocal = value
	}
	if value, present, err := optionalSuppressionTime(request.StartsAt); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.StartsAt = &value
	}
	if value, present, err := optionalSuppressionTime(request.EndsAt); err != nil {
		return store.SuppressionWindow{}, err
	} else if present {
		updated.EndsAt = &value
	}
	return updated, nil
}

func optionalSuppressionString(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	var value string
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return "", false, store.ErrInvalid
	}
	return strings.TrimSpace(value), true, nil
}

func optionalSuppressionBool(raw json.RawMessage) (bool, bool, error) {
	if len(raw) == 0 {
		return false, false, nil
	}
	var value bool
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return false, false, store.ErrInvalid
	}
	return value, true, nil
}

func optionalSuppressionInts(raw json.RawMessage) ([]int, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	var value []int
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return nil, false, store.ErrInvalid
	}
	return value, true, nil
}

func optionalSuppressionTime(raw json.RawMessage) (time.Time, bool, error) {
	if len(raw) == 0 {
		return time.Time{}, false, nil
	}
	var value time.Time
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil || value.IsZero() {
		return time.Time{}, false, store.ErrInvalid
	}
	return value.UTC(), true, nil
}

func suppressionWindowResponse(window store.SuppressionWindow) map[string]any {
	response := map[string]any{
		"id": window.ID, "name": window.Name, "targetKind": window.TargetKind, "enabled": window.Enabled, "mode": window.Mode,
		"revision": window.Revision, "createdAt": window.CreatedAt.UTC(), "updatedAt": window.UpdatedAt.UTC(),
	}
	if window.TargetID != "" {
		response["targetId"] = window.TargetID
	}
	if window.Mode == store.SuppressionWindowRecurring {
		response["timezone"] = window.Timezone
		response["weekdays"] = window.Weekdays
		response["startLocal"] = window.StartLocal
		response["endLocal"] = window.EndLocal
	} else {
		response["startsAt"] = window.StartsAt
		response["endsAt"] = window.EndsAt
	}
	return response
}
