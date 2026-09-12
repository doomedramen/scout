package control

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

const (
	defaultScanAPIPageSize = 100
	maxScanAPIPageSize     = 500
	maxScanIdempotencyKey  = 128
)

type scopeResponse struct {
	store.Scope
	ScanPolicy scanPolicyResponse `json:"scanPolicy"`
}

type scanPolicyResponse struct {
	Revision        int64                  `json:"revision"`
	Enabled         bool                   `json:"enabled"`
	ServerEnabled   bool                   `json:"serverEnabled"`
	AgentIDs        []string               `json:"agentIds"`
	ScheduleSeconds int                    `json:"scheduleSeconds"`
	EntryPoints     []store.ScanEntryPoint `json:"entryPoints"`
	Limits          store.ScanLimits       `json:"limits"`
}

type scanPolicyInput struct {
	ServerEnabled   bool                   `json:"serverEnabled"`
	AgentIDs        []string               `json:"agentIds"`
	ScheduleSeconds int                    `json:"scheduleSeconds"`
	EntryPoints     []store.ScanEntryPoint `json:"entryPoints"`
	Limits          store.ScanLimits       `json:"limits"`
}

func (input scanPolicyInput) policy(scopeID string) store.ScanPolicy {
	return store.ScanPolicy{
		ScopeID:         scopeID,
		ServerEnabled:   input.ServerEnabled,
		AgentIDs:        append([]string(nil), input.AgentIDs...),
		ScheduleSeconds: input.ScheduleSeconds,
		EntryPoints:     append([]store.ScanEntryPoint(nil), input.EntryPoints...),
		Limits:          input.Limits,
	}
}

type scanPolicyPatch struct {
	ExpectedRevision int64                   `json:"expectedRevision"`
	ServerEnabled    *bool                   `json:"serverEnabled"`
	AgentIDs         *[]string               `json:"agentIds"`
	ScheduleSeconds  *int                    `json:"scheduleSeconds"`
	EntryPoints      *[]store.ScanEntryPoint `json:"entryPoints"`
	Limits           *store.ScanLimits       `json:"limits"`
}

func (patch scanPolicyPatch) apply(current store.ScanPolicy) store.ScanPolicy {
	next := current
	if patch.ServerEnabled != nil {
		next.ServerEnabled = *patch.ServerEnabled
	}
	if patch.AgentIDs != nil {
		next.AgentIDs = append([]string(nil), (*patch.AgentIDs)...)
	}
	if patch.ScheduleSeconds != nil {
		next.ScheduleSeconds = *patch.ScheduleSeconds
	}
	if patch.EntryPoints != nil {
		next.EntryPoints = append([]store.ScanEntryPoint(nil), (*patch.EntryPoints)...)
	}
	if patch.Limits != nil {
		next.Limits = *patch.Limits
	}
	return next
}

func (a *App) scopeView(ctx context.Context, scope store.Scope) (scopeResponse, error) {
	scanPolicy, err := a.Store.ScanPolicy(ctx, scope.ID)
	if err != nil {
		return scopeResponse{}, err
	}
	return scopeResponse{Scope: scope, ScanPolicy: scanPolicyResponse{
		Revision:        scanPolicy.Revision,
		Enabled:         scanPolicy.Enabled,
		ServerEnabled:   scanPolicy.ServerEnabled,
		AgentIDs:        append([]string{}, scanPolicy.AgentIDs...),
		ScheduleSeconds: scanPolicy.ScheduleSeconds,
		EntryPoints:     append([]store.ScanEntryPoint{}, scanPolicy.EntryPoints...),
		Limits:          scanPolicy.Limits,
	}}, nil
}

type scanRouteError struct {
	status    int
	code      string
	message   string
	retryable bool
	cause     error
}

func (e *scanRouteError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *scanRouteError) Unwrap() error { return e.cause }

func scanError(status int, code, message string, retryable bool, cause error) error {
	return &scanRouteError{status: status, code: code, message: message, retryable: retryable, cause: cause}
}

func scanPolicyInvalidError(cause error) error {
	return scanError(http.StatusBadRequest, "scan_policy_invalid", "Scan policy failed validation", false, cause)
}

func scanConflictError(message string) error {
	return scanError(http.StatusConflict, "scan_revision_superseded", message, false, store.ErrConflict)
}

func scanPolicyStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrConflict):
		return scanConflictError("Scan policy changed; refresh and retry")
	case errors.Is(err, store.ErrInvalid):
		return scanPolicyInvalidError(err)
	default:
		return err
	}
}

func writeScanError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	var routeErr *scanRouteError
	if errors.As(err, &routeErr) {
		writeError(w, r, routeErr.status, routeErr.code, routeErr.message, routeErr.retryable)
		return
	}
	writeMappedError(w, r, err)
}

func (a *App) normalizeOwnerScanPolicy(ctx context.Context, scope store.Scope, requested store.ScanPolicy) (store.ScanPolicy, error) {
	normalized, err := policy.NormalizeScanPolicy(requested, scope)
	if err != nil {
		return store.ScanPolicy{}, scanPolicyInvalidError(err)
	}
	for _, agentID := range normalized.AgentIDs {
		agent, agentErr := a.Store.Agent(ctx, agentID)
		if errors.Is(agentErr, store.ErrNotFound) {
			return store.ScanPolicy{}, scanError(http.StatusConflict, "scan_vantage_not_assigned", "The selected agent is not available for this scope", false, agentErr)
		}
		if agentErr != nil {
			return store.ScanPolicy{}, agentErr
		}
		if agent.RevokedAt != nil || !agent.ExpiresAt.IsZero() && !agent.ExpiresAt.After(a.Store.Now()) {
			return store.ScanPolicy{}, scanError(http.StatusConflict, "scan_vantage_unavailable", "The selected agent is not available", false, store.ErrRevoked)
		}
		device, deviceErr := a.Store.GetDevice(ctx, agent.DeviceID)
		if deviceErr != nil {
			return store.ScanPolicy{}, deviceErr
		}
		if device.SiteID != scope.SiteID || device.Excluded || device.DecommissionedAt != nil || device.Lifecycle == "decommissioned" {
			return store.ScanPolicy{}, scanError(http.StatusConflict, "scan_vantage_not_assigned", "The selected agent is not assigned to this scope", false, store.ErrConflict)
		}
		if !supportsScanCapabilities(agent.Capabilities) {
			return store.ScanPolicy{}, scanError(http.StatusConflict, "scan_capability_unsupported", "The selected agent does not support bounded scanning", false, store.ErrConflict)
		}
	}
	return normalized, nil
}

func (a *App) registerScanRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/scopes/{scopeId}/scan-runs", a.createScanRun)
	mux.HandleFunc("GET /api/v1/scopes/{scopeId}/scan-status", a.scanStatus)
	mux.HandleFunc("GET /api/v1/scan-runs", a.listScanRuns)
	mux.HandleFunc("GET /api/v1/scan-runs/{runId}", a.getScanRun)
	mux.HandleFunc("POST /api/v1/scan-runs/{runId}/cancel", a.cancelScanRun)
}

type scanRunCreateRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
	Scanner          struct {
		Kind     string `json:"kind"`
		ID       string `json:"id"`
		DeviceID string `json:"deviceId"`
	} `json:"scanner"`
}

func (a *App) createScanRun(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request scanRunCreateRequest
	if err := decodeJSON(r, &request, 32<<10); err != nil || request.ExpectedRevision < 1 || strings.TrimSpace(request.Scanner.Kind) == "" || strings.TrimSpace(request.Scanner.ID) == "" {
		writeScanError(w, r, scanPolicyInvalidError(store.ErrInvalid))
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); len(key) > maxScanIdempotencyKey {
		writeScanError(w, r, scanPolicyInvalidError(store.ErrInvalid))
		return
	}
	scope, err := a.Store.GetScope(r.Context(), r.PathValue("scopeId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	scanPolicy, err := a.Store.ScanPolicy(r.Context(), scope.ID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if request.ExpectedRevision != scanPolicy.Revision {
		writeScanError(w, r, scanConflictError("Scan policy changed; refresh and retry"))
		return
	}
	workspace, err := a.Store.Workspace(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if workspace.RecoveryMode || workspace.DiscoveryPaused {
		code := "scan_globally_paused"
		if workspace.RecoveryMode {
			code = "scan_scope_paused"
		}
		writeScanError(w, r, scanError(http.StatusConflict, code, "Scanning is paused", true, store.ErrConflict))
		return
	}
	if !scope.Enabled || !scanPolicy.Enabled {
		writeScanError(w, r, scanError(http.StatusConflict, "scan_scope_paused", "This scope is disabled for scanning", false, store.ErrConflict))
		return
	}
	scannerKind := strings.TrimSpace(request.Scanner.Kind)
	scannerID := strings.TrimSpace(request.Scanner.ID)
	scanner := policy.ScanVantage{Kind: scannerKind, ID: scannerID, DeviceID: strings.TrimSpace(request.Scanner.DeviceID)}
	switch scannerKind {
	case "server":
		if scannerID != a.CoordinatorServerID() || scanner.DeviceID != "" {
			writeScanError(w, r, scanError(http.StatusConflict, "scan_vantage_not_assigned", "The selected scanner is not assigned to this scope", false, store.ErrConflict))
			return
		}
		if !scanPolicy.ServerEnabled {
			writeScanError(w, r, scanError(http.StatusConflict, "scan_vantage_not_assigned", "The server scanner is disabled for this scope", false, store.ErrConflict))
			return
		}
	case "agent":
		if scanner.DeviceID == "" || !containsScanString(scanPolicy.AgentIDs, scannerID) {
			writeScanError(w, r, scanError(http.StatusConflict, "scan_vantage_not_assigned", "The selected agent is not assigned to this scope", false, store.ErrConflict))
			return
		}
		agent, agentErr := a.Store.Agent(r.Context(), scannerID)
		if errors.Is(agentErr, store.ErrNotFound) {
			writeScanError(w, r, scanError(http.StatusConflict, "scan_vantage_unavailable", "The selected agent is not available", true, agentErr))
			return
		}
		if agentErr != nil {
			writeMappedError(w, r, agentErr)
			return
		}
		if !supportsScanCapabilities(agent.Capabilities) {
			writeScanError(w, r, scanError(http.StatusConflict, "scan_capability_unsupported", "The selected agent does not support bounded scanning", false, store.ErrConflict))
			return
		}
		if scanner.DeviceID != agent.DeviceID {
			writeScanError(w, r, scanError(http.StatusConflict, "scan_vantage_unavailable", "The selected agent identity is unavailable", false, store.ErrConflict))
			return
		}
	default:
		writeScanError(w, r, scanPolicyInvalidError(store.ErrInvalid))
		return
	}
	if scannerKind == "agent" {
		decision, decisionErr := a.Policy.ValidateScanVantage(r.Context(), scope.ID, scanner)
		if decisionErr != nil {
			writeMappedError(w, r, decisionErr)
			return
		}
		if !decision.Allowed {
			writeScanError(w, r, scanVantageDecisionError(decision.Reason))
			return
		}
	}
	targets, err := discovery.ExpandTargets(scope.Ranges, scope.Exclusions, scanPolicy.Limits.TargetBudget)
	if err != nil {
		writeScanError(w, r, scanError(http.StatusBadRequest, "scan_bound_exceeded", "The scan target bound could not be planned", false, err))
		return
	}
	entryPoints := enabledScanEntryPoints(scanPolicy.EntryPoints)
	attempts, err := scanAttemptPlan(len(targets), len(entryPoints))
	if err != nil || attempts > scanPolicy.Limits.AttemptBudget {
		writeScanError(w, r, scanError(http.StatusBadRequest, "scan_bound_exceeded", "The scan attempt bound could not be planned", false, err))
		return
	}
	now := a.Store.Now()
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	run, err := a.Store.CreateScanRun(r.Context(), store.ScanRun{
		ID:                  store.NewID(),
		ScopeID:             scope.ID,
		ScopeRevision:       scanPolicy.Revision,
		ScannerKind:         scannerKind,
		ScannerID:           scannerID,
		Trigger:             "owner",
		ScheduledAt:         now,
		AssignmentExpiresAt: now.Add(time.Duration(scanPolicy.Limits.RunDeadlineSeconds) * time.Second),
		PolicySnapshot: store.ScanPolicySnapshot{
			Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: scanPolicy.EntryPoints, Limits: scanPolicy.Limits,
		},
		TargetsPlanned: targetsCount(targets), AttemptsPlanned: attempts, IdempotencyKey: key,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			if key != "" {
				if existing, lookupErr := a.Store.ListScanRuns(r.Context(), store.ScanRunQuery{ScopeID: scope.ID, Limit: maxScanAPIPageSize}); lookupErr == nil {
					for _, item := range existing.Items {
						if item.IdempotencyKey == key {
							run = item
							goto created
						}
					}
				}
			}
			writeScanError(w, r, scanError(http.StatusConflict, "scan_already_active", "A scan is already active for this scanner", true, err))
			return
		}
		if errors.Is(err, store.ErrBackpressure) {
			writeScanError(w, r, scanError(http.StatusServiceUnavailable, "scan_backpressure", "Scanning is temporarily at capacity", true, err))
			return
		}
		writeScanError(w, r, scanPolicyStoreError(err))
		return
	}
created:
	a.recordOwnerAudit(r, "scan.run.create", run.ID, map[string]any{"scopeId": scope.ID, "scannerKind": scannerKind, "scannerId": scannerID, "trigger": "owner"})
	writeJSON(w, http.StatusAccepted, a.scanRunResponse(r.Context(), run))
}

func (a *App) CoordinatorServerID() string {
	if a != nil && a.Coordinator != nil {
		if value := strings.TrimSpace(a.Coordinator.ServerID); value != "" {
			return value
		}
	}
	return discovery.DefaultServerVantageID
}

func containsScanString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func enabledScanEntryPoints(items []store.ScanEntryPoint) []store.ScanEntryPoint {
	result := make([]store.ScanEntryPoint, 0, len(items))
	for _, item := range items {
		if item.Enabled {
			result = append(result, item)
		}
	}
	return result
}

func scanAttemptPlan(targets, entryPoints int) (int, error) {
	if targets < 0 || entryPoints < 0 || entryPoints != 0 && targets > math.MaxInt/entryPoints {
		return 0, store.ErrInvalid
	}
	return targets * entryPoints, nil
}

func targetsCount(targets []string) int { return len(targets) }

func scanVantageDecisionError(reason string) error {
	switch reason {
	case "vantage_not_assigned":
		return scanError(http.StatusConflict, "scan_vantage_not_assigned", "The selected agent is not assigned to this scope", false, store.ErrConflict)
	case "vantage_unavailable":
		return scanError(http.StatusConflict, "scan_vantage_unavailable", "The selected agent is not available", true, store.ErrConflict)
	default:
		return scanError(http.StatusConflict, "scan_vantage_unavailable", "The selected scanner is not available", true, store.ErrConflict)
	}
}

type scanRunResponse struct {
	ID                  string                  `json:"id"`
	ScopeID             string                  `json:"scopeId"`
	ScopeRevision       int64                   `json:"scopeRevision"`
	Scanner             scanScannerResponse     `json:"scanner"`
	Trigger             string                  `json:"trigger"`
	State               string                  `json:"state"`
	ScheduledAt         time.Time               `json:"scheduledAt"`
	StartedAt           *time.Time              `json:"startedAt"`
	FinishedAt          *time.Time              `json:"finishedAt"`
	AssignmentExpiresAt time.Time               `json:"assignmentExpiresAt"`
	TargetsPlanned      int                     `json:"targetsPlanned"`
	AttemptsPlanned     int                     `json:"attemptsPlanned"`
	AttemptsCompleted   int                     `json:"attemptsCompleted"`
	OutcomeCounts       store.ScanOutcomeCounts `json:"outcomeCounts"`
	PartialReason       *string                 `json:"partialReason"`
	ErrorCode           *string                 `json:"errorCode"`
	PageCount           int                     `json:"pageCount"`
	FinalPageOrdinal    *int                    `json:"finalPageOrdinal"`
}

type scanScannerResponse struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	DeviceID string `json:"deviceId,omitempty"`
}

func scanRunView(run store.ScanRun) scanRunResponse {
	partialReason := optionalString(run.PartialReason)
	errorCode := optionalString(run.ErrorCode)
	return scanRunResponse{
		ID: run.ID, ScopeID: run.ScopeID, ScopeRevision: run.ScopeRevision,
		Scanner: scanScannerResponse{Kind: run.ScannerKind, ID: run.ScannerID}, Trigger: run.Trigger, State: run.State,
		ScheduledAt: run.ScheduledAt, StartedAt: cloneTime(run.StartedAt), FinishedAt: cloneTime(run.FinishedAt),
		AssignmentExpiresAt: run.AssignmentExpiresAt, TargetsPlanned: run.TargetsPlanned, AttemptsPlanned: run.AttemptsPlanned,
		AttemptsCompleted: run.AttemptsCompleted, OutcomeCounts: run.OutcomeCounts, PartialReason: partialReason,
		ErrorCode: errorCode, PageCount: run.PageCount, FinalPageOrdinal: cloneInt(run.FinalPageOrdinal),
	}
}

func (a *App) scanRunResponse(ctx context.Context, run store.ScanRun) scanRunResponse {
	view := scanRunView(run)
	if run.ScannerKind == "agent" {
		if agent, err := a.Store.Agent(ctx, run.ScannerID); err == nil {
			view.Scanner.DeviceID = agent.DeviceID
		}
	}
	return view
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func (a *App) listScanRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	query, err := scanRunQuery(r)
	if err != nil {
		writeScanError(w, r, scanPolicyInvalidError(err))
		return
	}
	page, err := a.Store.ListScanRuns(r.Context(), query)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items := make([]scanRunResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, a.scanRunResponse(r.Context(), item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nullableScanCursor(page.NextCursor)})
}

func scanRunQuery(r *http.Request) (store.ScanRunQuery, error) {
	limit := defaultScanAPIPageSize
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxScanAPIPageSize {
			return store.ScanRunQuery{}, store.ErrInvalid
		}
		limit = parsed
	}
	query := store.ScanRunQuery{ScopeID: strings.TrimSpace(r.URL.Query().Get("scopeId")), ScannerID: strings.TrimSpace(r.URL.Query().Get("scannerId")), State: strings.TrimSpace(r.URL.Query().Get("state")), Cursor: r.URL.Query().Get("cursor"), Limit: limit}
	for _, state := range []string{store.ScanRunQueued, store.ScanRunLeased, store.ScanRunRunning, store.ScanRunUploading, store.ScanRunCompleted, store.ScanRunPartial, store.ScanRunFailed, store.ScanRunCancelled, store.ScanRunRejected, store.ScanRunExpired} {
		if query.State == state {
			goto validState
		}
	}
	if query.State != "" {
		return store.ScanRunQuery{}, store.ErrInvalid
	}
validState:
	if raw := strings.TrimSpace(r.URL.Query().Get("startedAfter")); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return store.ScanRunQuery{}, store.ErrInvalid
		}
		query.StartedAfter = &value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("startedBefore")); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return store.ScanRunQuery{}, store.ErrInvalid
		}
		query.StartedBefore = &value
	}
	return query, nil
}

func nullableScanCursor(cursor string) any {
	if cursor == "" {
		return nil
	}
	return cursor
}

func (a *App) getScanRun(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	run, err := a.Store.GetScanRun(r.Context(), r.PathValue("runId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.scanRunResponse(r.Context(), run))
}

func (a *App) cancelScanRun(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireSensitive(w, r)
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeScanError(w, r, scanPolicyInvalidError(store.ErrInvalid))
		return
	}
	run, err := a.Store.GetScanRun(r.Context(), r.PathValue("runId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if request.ExpectedRevision != run.ScopeRevision {
		writeScanError(w, r, scanConflictError("Scan run revision is superseded"))
		return
	}
	if !isScanRunTerminal(run.State) {
		run, err = a.Store.RequestScanCancellation(r.Context(), run.ID)
		if err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	a.recordOwnerAudit(r, "scan.run.cancel", run.ID, map[string]any{"scopeId": run.ScopeID})
	writeJSON(w, http.StatusAccepted, a.scanRunResponse(r.Context(), run))
}

func isScanRunTerminal(state string) bool {
	switch state {
	case store.ScanRunCompleted, store.ScanRunPartial, store.ScanRunFailed, store.ScanRunCancelled, store.ScanRunRejected, store.ScanRunExpired:
		return true
	default:
		return false
	}
}

type scanStatusResponse struct {
	ScopeID                string                `json:"scopeId"`
	PolicyRevision         int64                 `json:"policyRevision"`
	Enabled                bool                  `json:"enabled"`
	LastCompletedAt        *time.Time            `json:"lastCompletedAt"`
	NextScheduledAt        *time.Time            `json:"nextScheduledAt"`
	ActiveRun              *scanRunResponse      `json:"activeRun"`
	Vantages               []scanVantageResponse `json:"vantages"`
	CoverageState          string                `json:"coverageState"`
	PartialReason          *string               `json:"partialReason"`
	CandidateOutcomeCounts map[string]int        `json:"candidateOutcomeCounts"`
	Retention              scanRetentionResponse `json:"retention"`
	Queue                  scanQueueResponse     `json:"queue"`
}

type scanVantageResponse struct {
	Scanner      scanScannerResponse `json:"scanner"`
	State        string              `json:"state"`
	Assigned     bool                `json:"assigned"`
	Capabilities []string            `json:"capabilities"`
	LastSeen     *time.Time          `json:"lastSeen"`
}

type scanRetentionResponse struct {
	EvidenceBefore *time.Time `json:"evidenceBefore"`
	RunsBefore     *time.Time `json:"runsBefore"`
	LagSeconds     int64      `json:"lagSeconds"`
	Blocked        bool       `json:"blocked"`
}

type scanQueueResponse struct {
	ActiveRuns   int  `json:"activeRuns"`
	PendingPages int  `json:"pendingPages"`
	Backpressure bool `json:"backpressure"`
}

func (a *App) scanStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	scope, err := a.Store.GetScope(r.Context(), r.PathValue("scopeId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	scanPolicy, err := a.Store.ScanPolicy(r.Context(), scope.ID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	runs, err := a.Store.ListScanRuns(r.Context(), store.ScanRunQuery{ScopeID: scope.ID, Limit: maxScanAPIPageSize})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	status := scanStatusResponse{ScopeID: scope.ID, PolicyRevision: scanPolicy.Revision, Enabled: scope.Enabled && scanPolicy.Enabled, Vantages: []scanVantageResponse{}, CandidateOutcomeCounts: map[string]int{}}
	now := a.Store.Now()
	var latestTerminal *store.ScanRun
	var latestScheduled *store.ScanRun
	for index := range runs.Items {
		run := runs.Items[index]
		if latestScheduled == nil || run.ScheduledAt.After(latestScheduled.ScheduledAt) {
			copy := run
			latestScheduled = &copy
		}
		if isScanRunActive(run.State) {
			if status.ActiveRun == nil {
				view := a.scanRunResponse(r.Context(), run)
				status.ActiveRun = &view
			}
		}
		if isScanRunTerminal(run.State) && run.FinishedAt != nil && (latestTerminal == nil || run.FinishedAt.After(*latestTerminal.FinishedAt)) {
			copy := run
			latestTerminal = &copy
		}
	}
	if latestTerminal != nil {
		status.LastCompletedAt = cloneTime(latestTerminal.FinishedAt)
		status.PartialReason = optionalString(latestTerminal.PartialReason)
		switch latestTerminal.State {
		case store.ScanRunCompleted:
			status.CoverageState = "current"
		case store.ScanRunPartial:
			status.CoverageState = "partial"
		default:
			status.CoverageState = "stale"
		}
	} else {
		status.CoverageState = "unknown"
	}
	if scanPolicy.ServerEnabled && status.Enabled {
		next := now
		if latestScheduled != nil {
			next = discovery.NextScanScheduleAt(latestScheduled.ScheduledAt, scope.ID, "server", a.CoordinatorServerID(), time.Duration(scanPolicy.ScheduleSeconds)*time.Second)
			if next.Before(now) {
				next = now
			}
		}
		status.NextScheduledAt = &next
	}
	status.Vantages = append(status.Vantages, a.serverVantageStatus(scanPolicy, status.Enabled))
	for _, agentID := range scanPolicy.AgentIDs {
		vantage := a.agentVantageStatus(r.Context(), scope, agentID, now)
		status.Vantages = append(status.Vantages, vantage)
	}
	candidates, err := a.Store.ListCandidates(r.Context(), scope.ID, "")
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	for _, candidate := range candidates {
		status.CandidateOutcomeCounts[candidate.State]++
	}
	workspace, err := a.Store.Workspace(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	activeCount := 0
	for _, run := range runs.Items {
		if isScanRunActive(run.State) {
			activeCount++
		}
	}
	evidenceBefore := now.Add(-30 * 24 * time.Hour)
	runsBefore := now.Add(-90 * 24 * time.Hour)
	status.Retention = scanRetentionResponse{EvidenceBefore: &evidenceBefore, RunsBefore: &runsBefore, LagSeconds: 0, Blocked: workspace.TelemetryBackpressure}
	status.Queue = scanQueueResponse{ActiveRuns: activeCount, PendingPages: 0, Backpressure: workspace.TelemetryBackpressure}
	writeJSON(w, http.StatusOK, status)
}

func isScanRunActive(state string) bool {
	switch state {
	case store.ScanRunQueued, store.ScanRunLeased, store.ScanRunRunning, store.ScanRunUploading:
		return true
	default:
		return false
	}
}

func (a *App) serverVantageStatus(scanPolicy store.ScanPolicy, enabled bool) scanVantageResponse {
	capabilities := []string{}
	state := "unsupported"
	if scanPolicy.ServerEnabled && enabled {
		capabilities = []string{"scan-protocol:1", "tcp"}
		state = "available"
	}
	return scanVantageResponse{Scanner: scanScannerResponse{Kind: "server", ID: a.CoordinatorServerID()}, State: state, Assigned: scanPolicy.ServerEnabled, Capabilities: capabilities, LastSeen: nil}
}

func (a *App) agentVantageStatus(ctx context.Context, scope store.Scope, agentID string, now time.Time) scanVantageResponse {
	response := scanVantageResponse{Scanner: scanScannerResponse{Kind: "agent", ID: agentID}, Assigned: true, Capabilities: []string{}, LastSeen: nil, State: "stale"}
	agent, err := a.Store.Agent(ctx, agentID)
	if errors.Is(err, store.ErrNotFound) {
		response.State = "stale"
		return response
	}
	if err != nil {
		return response
	}
	response.Scanner.DeviceID = agent.DeviceID
	if agent.RevokedAt != nil {
		response.State = "revoked"
		return response
	}
	device, err := a.Store.GetDevice(ctx, agent.DeviceID)
	if err != nil {
		return response
	}
	if device.SiteID != scope.SiteID || device.DecommissionedAt != nil || device.Lifecycle == "decommissioned" {
		response.State = "decommissioned"
		return response
	}
	if !supportsScanCapabilities(agent.Capabilities) {
		response.State = "unsupported"
		return response
	}
	response.Capabilities = []string{"scan-protocol:1", "tcp"}
	response.LastSeen = cloneTime(device.LastHeartbeat)
	if device.LastHeartbeat != nil && now.Sub(*device.LastHeartbeat) <= 90*time.Second {
		response.State = "available"
	}
	return response
}
