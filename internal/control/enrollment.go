package control

import (
	"context"
	"net/http"
	"strings"
	"time"

	"scout.local/scout/internal/audit"
	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/enrollment"
	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

func (a *App) registerEnrollmentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/candidates", a.listCandidates)
	mux.HandleFunc("POST /api/v1/candidates", a.createCandidate)
	mux.HandleFunc("GET /api/v1/scopes/{scopeId}/candidates", a.listScopeCandidates)
	mux.HandleFunc("POST /api/v1/scopes/{scopeId}/discover", a.discoverScope)
	mux.HandleFunc("POST /api/v1/candidates/{candidateId}/enroll", a.enqueueCandidate)
	mux.HandleFunc("POST /api/v1/control/state/ack", a.acknowledgePause)
	mux.HandleFunc("GET /api/v1/workers", a.listWorkers)
	mux.HandleFunc("POST /api/v1/workers", a.createWorker)
	mux.HandleFunc("GET /api/v1/devices/{deviceId}/update-policy", a.getDeviceUpdatePolicy)
	mux.HandleFunc("PATCH /api/v1/devices/{deviceId}/update-policy", a.updateDeviceUpdatePolicy)
	mux.HandleFunc("GET /api/v1/services", a.listServiceEntities)
	mux.HandleFunc("GET /api/v1/devices/{deviceId}/collectors", a.listCollectorConfigs)
	mux.HandleFunc("GET /api/v1/devices/{deviceId}/collectors/{collectorId}", a.getCollectorConfig)
	mux.HandleFunc("PATCH /api/v1/devices/{deviceId}/collectors/{collectorId}", a.updateCollectorConfig)

	for _, prefix := range []string{"/api/v1/worker/v1", "/worker/v1"} {
		mux.HandleFunc("POST "+prefix+"/claim", a.workerClaim)
		mux.HandleFunc("POST "+prefix+"/jobs/{jobId}/renew", a.workerRenew)
		mux.HandleFunc("POST "+prefix+"/jobs/{jobId}/progress", a.workerProgress)
		mux.HandleFunc("GET "+prefix+"/jobs/{jobId}/confirmation", a.workerConfirmation)
		mux.HandleFunc("POST "+prefix+"/grants/{grantId}/redeem", a.workerRedeem)
	}
}

func (a *App) discoverScope(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	workspace, err := a.Store.Workspace(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if workspace.DiscoveryPaused || workspace.RecoveryMode {
		writeMappedError(w, r, store.ErrBackpressure)
		return
	}
	var request struct {
		Source          string            `json:"source"`
		TargetBudget    int               `json:"targetBudget"`
		ProbesPerSecond int               `json:"probesPerSecond"`
		Concurrency     int               `json:"concurrency"`
		TimeoutMs       int               `json:"timeoutMs"`
		Sightings       []discovery.Sight `json:"sightings"`
	}
	if err := decodeJSON(r, &request, 256<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if request.Source == "" {
		request.Source = "control-local"
	}
	service := &discovery.Service{Store: a.Store, Policy: &policy.Engine{Store: a.Store}}
	var items []store.Candidate
	if len(request.Sightings) > 0 {
		items, err = service.ReconcileSightings(r.Context(), r.PathValue("scopeId"), request.Sightings)
	} else {
		timeout := time.Duration(request.TimeoutMs) * time.Millisecond
		items, err = service.DiscoverScope(r.Context(), r.PathValue("scopeId"), request.Source, discovery.ProbePolicy{TargetBudget: request.TargetBudget, ProbesPerSecond: request.ProbesPerSecond, Concurrency: request.Concurrency, Timeout: timeout})
	}
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "discovery.run", r.PathValue("scopeId"), map[string]any{"candidates": len(items), "source": request.Source})
	writeJSON(w, http.StatusAccepted, map[string]any{"items": items, "nextCursor": nil, "source": request.Source})
}

func (a *App) listCandidates(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListCandidates(r.Context(), r.URL.Query().Get("scopeId"), r.URL.Query().Get("state"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (a *App) listScopeCandidates(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListCandidates(r.Context(), r.PathValue("scopeId"), r.URL.Query().Get("state"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (a *App) createCandidate(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request struct {
		ScopeID   string `json:"scopeId"`
		Address   string `json:"address"`
		Hostname  string `json:"hostname"`
		Source    string `json:"source"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	var expires time.Time
	if request.ExpiresAt != "" {
		var err error
		expires, err = time.Parse(time.RFC3339, request.ExpiresAt)
		if err != nil {
			writeMappedError(w, r, store.ErrInvalid)
			return
		}
	}
	item, err := a.Store.UpsertCandidate(r.Context(), store.Candidate{ScopeID: request.ScopeID, Address: request.Address, Hostname: request.Hostname, Source: request.Source, ExpiresAt: expires})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (a *App) enqueueCandidate(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	candidate, err := a.Store.GetCandidate(r.Context(), r.PathValue("candidateId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if candidate.Excluded || candidate.State == "expired" {
		writeMappedError(w, r, store.ErrForbidden)
		return
	}
	deviceID := ""
	devices, listErr := a.Store.ListDevices(r.Context(), store.DeviceFilter{SiteID: candidate.SiteID, Query: candidate.Address})
	if listErr != nil {
		writeMappedError(w, r, listErr)
		return
	}
	if len(devices) > 0 {
		deviceID = devices[0].ID
	} else {
		device, createErr := a.Store.CreateDevice(r.Context(), store.Device{DisplayName: candidate.Address, SiteID: candidate.SiteID, Platform: "linux", Architecture: "unknown", Addresses: []string{candidate.Address}})
		if createErr != nil {
			writeMappedError(w, r, createErr)
			return
		}
		deviceID = device.ID
	}
	result, err := (&enrollment.Access{Policy: &policy.Engine{Store: a.Store}, Store: a.Store}).Evaluate(r.Context(), candidate.ScopeID, candidate.Address, "tcp", 22, deviceID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if !result.Eligible {
		writeJSON(w, http.StatusAccepted, map[string]any{"eligible": false, "reason": result.Reason, "request": result.Request})
		return
	}
	a.recordOwnerAudit(r, "enrollment.enqueue", candidate.ID, map[string]any{"jobId": result.Job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"eligible": true, "job": result.Job})
}

func (a *App) acknowledgePause(w http.ResponseWriter, r *http.Request) {
	worker, ok := a.requireWorker(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Worker authentication required", false)
		return
	}
	state, err := a.Store.AcknowledgePause(r.Context(), worker.ID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *App) listWorkers(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListWorkers(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (a *App) createWorker(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request struct {
		Name    string   `json:"name"`
		SiteIDs []string `json:"siteIds"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil || strings.TrimSpace(request.Name) == "" || len(request.SiteIDs) == 0 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	token := randomToken(32)
	worker := store.WorkerIdentity{Name: strings.TrimSpace(request.Name), Kind: "enroller", AuthTokenHash: store.HashToken(token), SiteIDs: request.SiteIDs}
	if err := a.Store.CreateWorker(r.Context(), worker); err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "worker.create", worker.Name, map[string]any{"sites": len(worker.SiteIDs)})
	writeJSON(w, http.StatusCreated, map[string]any{"id": worker.ID, "name": worker.Name, "kind": worker.Kind, "siteIds": worker.SiteIDs, "token": token})
}

func (a *App) requireWorker(r *http.Request) (store.WorkerIdentity, bool) {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(value, "Bearer ") {
		return store.WorkerIdentity{}, false
	}
	worker, err := a.Store.WorkerByTokenHash(r.Context(), store.HashToken(strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))))
	return worker, err == nil && worker.Kind == "enroller"
}

func (a *App) workerClaim(w http.ResponseWriter, r *http.Request) {
	worker, ok := a.requireWorker(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Worker authentication required", false)
		return
	}
	job, err := a.Store.ClaimWorkerJob(r.Context(), worker)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobId": job.ID, "deviceId": job.DeviceID, "scopeId": job.ScopeID, "scopeRevision": job.ScopeRevision, "epoch": job.Epoch, "deadline": job.Deadline, "destination": job.Destination, "trustRef": job.TrustRef, "credentialGrantId": job.CredentialGrantID, "releaseId": job.ReleaseID})
}

func (a *App) workerJob(w http.ResponseWriter, r *http.Request) (store.WorkerIdentity, store.Job, bool) {
	worker, ok := a.requireWorker(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Worker authentication required", false)
		return store.WorkerIdentity{}, store.Job{}, false
	}
	job, err := a.Store.Job(r.Context(), r.PathValue("jobId"))
	if err != nil || job.LeaseOwner != worker.ID {
		if err == nil {
			err = store.ErrConflict
		}
		writeMappedError(w, r, err)
		return store.WorkerIdentity{}, store.Job{}, false
	}
	return worker, job, true
}

func (a *App) workerRenew(w http.ResponseWriter, r *http.Request) {
	_, job, ok := a.workerJob(w, r)
	if !ok {
		return
	}
	var request struct {
		Epoch int64 `json:"epoch"`
	}
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.Epoch != job.Epoch {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	renewed, err := a.Store.RenewJob(r.Context(), job.ID, job.LeaseOwner, request.Epoch)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, renewed)
}

func (a *App) workerProgress(w http.ResponseWriter, r *http.Request) {
	worker, job, ok := a.workerJob(w, r)
	if !ok {
		return
	}
	var request struct {
		Epoch  int64             `json:"epoch"`
		State  string            `json:"state"`
		Result map[string]string `json:"result"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil || request.Epoch != job.Epoch {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	if request.State == "enrolled" {
		device, deviceErr := a.Store.GetDevice(r.Context(), job.DeviceID)
		if deviceErr != nil || device.AgentID == "" || device.LastHeartbeat == nil {
			writeMappedError(w, r, store.ErrConflict)
			return
		}
	}
	if err := a.Store.ReportJob(r.Context(), job.ID, worker.ID, request.Epoch, request.State, request.Result); err != nil {
		writeMappedError(w, r, err)
		return
	}
	if request.State == "paused" || request.State == "failed" || request.State == "enrolled" {
		_, _ = a.Store.AcknowledgePause(r.Context(), worker.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobId": job.ID, "state": request.State})
}

func (a *App) workerConfirmation(w http.ResponseWriter, r *http.Request) {
	_, job, ok := a.workerJob(w, r)
	if !ok {
		return
	}
	device, err := a.Store.GetDevice(r.Context(), job.DeviceID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	confirmed := device.AgentID != "" && device.LastHeartbeat != nil && device.Availability == store.AvailabilityOnline
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": confirmed, "deviceId": device.ID, "availability": device.Availability, "lastHeartbeat": device.LastHeartbeat})
}

func (a *App) workerRedeem(w http.ResponseWriter, r *http.Request) {
	worker, ok := a.requireWorker(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Worker authentication required", false)
		return
	}
	job, err := a.Store.Job(r.Context(), r.PathValue("grantId"))
	if err != nil || job.LeaseOwner != worker.ID || job.LeaseExpiry == nil || !a.Store.Now().Before(*job.LeaseExpiry) || job.Kind != "enrollment" {
		if err == nil {
			err = store.ErrForbidden
		}
		writeMappedError(w, r, err)
		return
	}
	if job.ScopeID == "" || job.Destination == "" {
		writeMappedError(w, r, store.ErrForbidden)
		return
	}
	scope, err := a.Store.GetScope(r.Context(), job.ScopeID)
	if err != nil || scope.CredentialRef == "" {
		writeMappedError(w, r, store.ErrForbidden)
		return
	}
	if job.ScopeRevision != scope.Revision || !scope.Enabled {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	secret, err := (&secrets.Broker{Store: a.Store, KeyRing: a.Secrets}).RedeemForTarget(r.Context(), scope.CredentialRef, "enrollment", job.Destination)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	_ = a.Audit.Record(context.Background(), audit.Event{ActorKind: "worker", ActorID: worker.ID, Action: "credential.redeem", Target: job.Destination, Outcome: map[string]any{"jobId": job.ID, "success": true}, RequestID: r.Header.Get("X-Request-ID")})
	writeJSON(w, http.StatusOK, map[string]any{"credential": string(secret), "target": job.Destination, "expiresAt": a.Store.Now().Add(60 * time.Second)})
}
