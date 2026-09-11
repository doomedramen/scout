package control

import (
	"net/http"
	"strconv"
	"time"

	"scout.local/scout/internal/store"
)

func (a *App) registerInventoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devices", a.listDevices)
	mux.HandleFunc("GET /api/v1/devices/{deviceId}", a.getDevice)
	mux.HandleFunc("GET /api/v1/devices/{deviceId}/metrics", a.getMetrics)
	mux.HandleFunc("GET /api/v1/topology", a.topology)
	mux.HandleFunc("GET /api/v1/observations/{observationId}", a.getObservation)
}

func (a *App) listDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	limit, err := queryLimit(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items, err := a.Store.ListDevices(r.Context(), store.DeviceFilter{Query: r.URL.Query().Get("query"), Health: r.URL.Query().Get("health"), MonitoringState: r.URL.Query().Get("monitoringState"), SiteID: r.URL.Query().Get("siteId")})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (a *App) getDevice(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	device, err := a.Store.GetDevice(r.Context(), r.PathValue("deviceId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (a *App) getMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	maxPoints, queryErr := queryInt(r, "maxPoints", 600)
	if queryErr != nil {
		writeMappedError(w, r, queryErr)
		return
	}
	query := store.MetricQuery{DeviceID: r.PathValue("deviceId"), Metric: r.URL.Query().Get("metric"), EntityID: r.URL.Query().Get("entityId"), MaxPoints: maxPoints}
	var err error
	if raw := r.URL.Query().Get("from"); raw != "" {
		query.From, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			writeMappedError(w, r, store.ErrInvalid)
			return
		}
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		query.To, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			writeMappedError(w, r, store.ErrInvalid)
			return
		}
	}
	if !query.From.IsZero() && !query.To.IsZero() && query.From.After(query.To) {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	series, err := a.Store.QueryMetrics(r.Context(), query)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series})
}

func (a *App) topology(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	devices, err := a.Store.ListDevices(r.Context(), store.DeviceFilter{SiteID: r.URL.Query().Get("siteId")})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	relationships, err := a.Store.ListRelationships(r.Context(), r.URL.Query().Get("siteId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	nodes := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		nodes = append(nodes, map[string]any{"id": device.ID, "label": device.DisplayName, "addresses": device.Addresses, "availability": device.Availability, "lifecycle": device.Lifecycle})
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "relationships": relationships, "nextCursor": nil})
}

func (a *App) getObservation(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	observations, err := a.Store.ListObservations(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	for _, item := range observations {
		if item.ID == r.PathValue("observationId") {
			writeJSON(w, http.StatusOK, map[string]any{"id": item.ID, "reporterId": item.ReporterID, "collectorId": item.CollectorID, "subjectId": item.SubjectID, "kind": item.Kind, "observedAt": item.ObservedAt, "receivedAt": item.ReceivedAt, "expiresAt": item.ExpiresAt, "confidence": item.Confidence})
			return
		}
	}
	writeMappedError(w, r, store.ErrNotFound)
}

func queryLimit(r *http.Request) (int, error) { return queryInt(r, "limit", 100) }
func queryInt(r *http.Request, name string, defaultValue int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, store.ErrInvalid
	}
	if name == "limit" && value > 500 {
		return 500, nil
	}
	if name == "maxPoints" && value > 600 {
		return 0, store.ErrInvalid
	}
	return value, nil
}
