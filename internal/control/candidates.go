package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"scout.local/scout/internal/enrollment"
	"scout.local/scout/internal/store"
)

type candidateListResponse struct {
	Items      []candidateSummaryResponse `json:"items"`
	NextCursor *string                    `json:"nextCursor"`
}

type candidateFilterResult struct {
	items      []store.Candidate
	nextCursor *string
}

type candidateSummaryResponse struct {
	ID              string                        `json:"id"`
	SiteID          string                        `json:"siteId"`
	ScopeID         string                        `json:"scopeId"`
	DeviceID        string                        `json:"deviceId,omitempty"`
	Address         string                        `json:"address"`
	Hostname        string                        `json:"hostname,omitempty"`
	Source          string                        `json:"source,omitempty"`
	DisplayName     string                        `json:"displayName"`
	State           string                        `json:"state"`
	ScopeRevision   int64                         `json:"scopeRevision"`
	CoverageState   string                        `json:"coverageState"`
	EntryPointCount int                           `json:"entryPointCount"`
	EntryPointIDs   []string                      `json:"entryPointIds,omitempty"`
	PreferredAccess string                        `json:"preferredAccessMethod,omitempty"`
	Provenance      []candidateProvenanceResponse `json:"provenance"`
	FirstSeen       time.Time                     `json:"firstSeen"`
	LastSeen        time.Time                     `json:"lastSeen"`
	ExpiresAt       time.Time                     `json:"expiresAt"`
	Excluded        bool                          `json:"excluded"`
	LastScannedAt   *time.Time                    `json:"lastScannedAt"`
	Action          *candidateActionResponse      `json:"action"`
}

type candidateProvenanceResponse struct {
	Scanner        candidateScannerResponse `json:"scanner"`
	LastObservedAt time.Time                `json:"lastObservedAt"`
	Outcome        string                   `json:"outcome"`
}

type candidateScannerResponse struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type candidateActionResponse struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Href  string `json:"href,omitempty"`
}

type candidateDetailResponse struct {
	Candidate      candidateSummaryResponse          `json:"candidate"`
	EntryPoints    entryPointObservationListResponse `json:"entryPoints"`
	AccessRequests []candidateAccessRequestResponse  `json:"accessRequests"`
	Enrollment     *candidateEnrollmentResponse      `json:"enrollment"`
}

type entryPointObservationListResponse struct {
	Items      []entryPointObservationResponse `json:"items"`
	NextCursor *string                         `json:"nextCursor"`
}

type entryPointObservationResponse struct {
	ID                  string                   `json:"id"`
	RunID               string                   `json:"runId"`
	PageOrdinal         int                      `json:"pageOrdinal"`
	ScopeID             string                   `json:"scopeId"`
	ScopeRevision       int64                    `json:"scopeRevision"`
	Scanner             candidateScannerResponse `json:"scanner"`
	Address             string                   `json:"address"`
	Transport           string                   `json:"transport"`
	Port                int                      `json:"port"`
	EntryPointID        string                   `json:"entryPointId"`
	Outcome             string                   `json:"outcome"`
	ReasonCode          *string                  `json:"reasonCode"`
	LatencyMilliseconds *float64                 `json:"latencyMilliseconds"`
	ObservedAt          time.Time                `json:"observedAt"`
	ReceivedAt          time.Time                `json:"receivedAt"`
	ExpiresAt           time.Time                `json:"expiresAt"`
	Actionable          bool                     `json:"actionable"`
}

type candidateAccessRequestResponse struct {
	ID          string            `json:"id"`
	Method      string            `json:"method"`
	Endpoint    string            `json:"endpoint"`
	ReasonCode  string            `json:"reasonCode"`
	State       string            `json:"state"`
	SafeDetails map[string]string `json:"safeDetails,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type candidateEnrollmentResponse struct {
	JobID string `json:"jobId"`
	State string `json:"state"`
}

type candidateJobResponse struct {
	ID            string    `json:"id"`
	DeviceID      string    `json:"deviceId"`
	ScopeID       string    `json:"scopeId,omitempty"`
	ScopeRevision int64     `json:"scopeRevision"`
	State         string    `json:"state"`
	Destination   string    `json:"destination,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type candidateCursor struct {
	LastSeen string `json:"lastSeen"`
	Address  string `json:"address"`
	ID       string `json:"id"`
}

func (a *App) getCandidates(w http.ResponseWriter, r *http.Request, scopeID string) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	items, err := a.Store.ListCandidates(r.Context(), scopeID, "")
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	filtered, err := filterCandidates(items, r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, candidateListResponse{Items: candidateSummaries(filtered.items), NextCursor: filtered.nextCursor})
}

func (a *App) getCandidate(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	candidate, err := a.Store.GetCandidate(r.Context(), r.PathValue("candidateId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	entryPoints, err := a.Store.ListEntryPointObservations(r.Context(), store.EntryPointObservationQuery{ScopeID: candidate.ScopeID, Address: candidate.Address, CurrentOnly: true, Limit: 500})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	requests, err := a.Store.ListAccessRequestsForCandidate(r.Context(), candidate.ID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	enrollment, err := a.candidateEnrollment(r.Context(), candidate)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, candidateDetailResponse{
		Candidate:      candidateSummary(candidate),
		EntryPoints:    entryPointObservationPage(entryPoints),
		AccessRequests: candidateAccessRequests(requests),
		Enrollment:     enrollment,
	})
}

func (a *App) listCandidateEntryPoints(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	candidate, err := a.Store.GetCandidate(r.Context(), r.PathValue("candidateId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	currentOnly, err := queryBool(r, "currentOnly")
	if err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	limit, err := scanQueryLimit(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	observations, err := a.Store.ListEntryPointObservations(r.Context(), store.EntryPointObservationQuery{
		ScopeID: candidate.ScopeID, Address: candidate.Address, ScannerID: r.URL.Query().Get("scannerId"),
		CurrentOnly: currentOnly, Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, entryPointObservationPage(observations))
}

func scanQueryLimit(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultScanAPIPageSize, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maxScanAPIPageSize {
		return 0, store.ErrInvalid
	}
	return value, nil
}

func (a *App) reevaluateCandidate(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	candidate, err := a.Store.GetCandidate(r.Context(), r.PathValue("candidateId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	scope, err := a.Store.GetScope(r.Context(), candidate.ScopeID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if request.ExpectedRevision != scope.Revision {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	access := a.Enrollment
	if access == nil {
		access = &enrollment.Access{Policy: a.Policy, Store: a.Store, Now: a.Store.Now}
	}
	result, err := access.ReevaluateCandidate(r.Context(), candidate.ID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "candidate.reevaluate", candidate.ID, map[string]any{"eligible": result.Eligible, "reason": result.Reason})
	response := map[string]any{
		"candidate": candidateSummary(result.Candidate),
		"eligible":  result.Eligible,
		"reason":    result.Reason,
	}
	if result.Request.ID != "" {
		response["request"] = candidateAccessRequest(result.Request)
	}
	if result.Job.ID != "" {
		response["job"] = candidateJob(result.Job)
	}
	writeJSON(w, http.StatusAccepted, response)
}

func filterCandidates(items []store.Candidate, r *http.Request) (candidateFilterResult, error) {
	stateFilter := strings.TrimSpace(r.URL.Query().Get("state"))
	if stateFilter != "" && !validCandidateAPIState(stateFilter) {
		return candidateFilterResult{}, store.ErrInvalid
	}
	accessMethod := strings.TrimSpace(r.URL.Query().Get("accessMethod"))
	if accessMethod != "" && accessMethod != store.ScanAccessSSH {
		return candidateFilterResult{}, store.ErrInvalid
	}
	freshness := strings.TrimSpace(r.URL.Query().Get("freshness"))
	if freshness != "" && !validCandidateCoverage(freshness) {
		return candidateFilterResult{}, store.ErrInvalid
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	if len(query) > 128 {
		return candidateFilterResult{}, store.ErrInvalid
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("deviceId"))
	if len(deviceID) > 128 {
		return candidateFilterResult{}, store.ErrInvalid
	}
	cursor, err := decodeCandidateCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return candidateFilterResult{}, err
	}
	limit, err := scanQueryLimit(r)
	if err != nil {
		return candidateFilterResult{}, err
	}
	filtered := make([]store.Candidate, 0, len(items))
	for _, candidate := range items {
		if deviceID != "" && candidate.DeviceID != deviceID {
			continue
		}
		if stateFilter != "" && apiCandidateState(candidate.State) != stateFilter {
			continue
		}
		if accessMethod != "" && candidate.PreferredAccessMethod != accessMethod {
			continue
		}
		if freshness != "" && candidateCoverage(candidate) != freshness {
			continue
		}
		if query != "" && !candidateMatches(candidate, query) {
			continue
		}
		if !candidateAfterCursor(candidate, cursor) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].LastSeen.Equal(filtered[j].LastSeen) {
			if filtered[i].Address == filtered[j].Address {
				return filtered[i].ID < filtered[j].ID
			}
			return filtered[i].Address < filtered[j].Address
		}
		return filtered[i].LastSeen.After(filtered[j].LastSeen)
	})
	if len(filtered) > limit {
		last := filtered[limit-1]
		encoded, err := encodeCandidateCursor(last)
		if err != nil {
			return candidateFilterResult{}, err
		}
		return candidateFilterResult{items: filtered[:limit], nextCursor: &encoded}, nil
	}
	return candidateFilterResult{items: filtered}, nil
}

func candidateMatches(candidate store.Candidate, query string) bool {
	return strings.Contains(strings.ToLower(candidate.Address), query) || strings.Contains(strings.ToLower(candidate.Hostname), query) || strings.Contains(strings.ToLower(candidate.ID), query)
}

func candidateAfterCursor(candidate store.Candidate, cursor candidateCursor) bool {
	if cursor.ID == "" {
		return true
	}
	lastSeen, err := time.Parse(time.RFC3339Nano, cursor.LastSeen)
	if err != nil {
		return false
	}
	if candidate.LastSeen.Before(lastSeen) {
		return true
	}
	if candidate.LastSeen.After(lastSeen) {
		return false
	}
	if candidate.Address > cursor.Address {
		return true
	}
	if candidate.Address < cursor.Address {
		return false
	}
	return candidate.ID > cursor.ID
}

func encodeCandidateCursor(candidate store.Candidate) (string, error) {
	data, err := json.Marshal(candidateCursor{LastSeen: candidate.LastSeen.UTC().Format(time.RFC3339Nano), Address: candidate.Address, ID: candidate.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCandidateCursor(value string) (candidateCursor, error) {
	if strings.TrimSpace(value) == "" {
		return candidateCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return candidateCursor{}, store.ErrInvalid
	}
	var cursor candidateCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.ID == "" {
		return candidateCursor{}, store.ErrInvalid
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.LastSeen); err != nil {
		return candidateCursor{}, store.ErrInvalid
	}
	return cursor, nil
}

func queryBool(r *http.Request, name string) (bool, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	return parsed, err
}

func candidateSummaries(items []store.Candidate) []candidateSummaryResponse {
	result := make([]candidateSummaryResponse, 0, len(items))
	for _, item := range items {
		result = append(result, candidateSummary(item))
	}
	return result
}

func candidateSummary(candidate store.Candidate) candidateSummaryResponse {
	provenance := make([]candidateProvenanceResponse, 0, len(candidate.Provenance))
	for _, item := range candidate.Provenance {
		if len(provenance) == 16 {
			break
		}
		provenance = append(provenance, candidateProvenanceResponse{Scanner: candidateScannerResponse{Kind: item.ScannerKind, ID: item.ScannerID}, LastObservedAt: item.LastObservedAt, Outcome: item.Outcome})
	}
	return candidateSummaryResponse{
		ID: candidate.ID, SiteID: candidate.SiteID, ScopeID: candidate.ScopeID, DeviceID: candidate.DeviceID, Address: candidate.Address,
		Hostname: candidate.Hostname, Source: candidate.Source, DisplayName: candidateDisplayName(candidate), State: apiCandidateState(candidate.State), ScopeRevision: candidate.ScopeRevision,
		CoverageState: candidateCoverage(candidate), EntryPointCount: minInt(len(candidate.EntryPointIDs), 64), EntryPointIDs: append([]string(nil), candidate.EntryPointIDs...), PreferredAccess: candidate.PreferredAccessMethod, Provenance: provenance,
		FirstSeen: candidate.FirstSeen, LastSeen: candidate.LastSeen, ExpiresAt: candidate.ExpiresAt, Excluded: candidate.Excluded,
		LastScannedAt: cloneCandidateTime(candidate.LastScannedAt), Action: candidateAction(candidate),
	}
}

func candidateDisplayName(candidate store.Candidate) string {
	if strings.TrimSpace(candidate.Hostname) != "" {
		return candidate.Hostname
	}
	return candidate.Address
}

func candidateAction(candidate store.Candidate) *candidateActionResponse {
	state := apiCandidateState(candidate.State)
	if state == "excluded" {
		return nil
	}
	href := "/?page=Network&candidateId=" + url.QueryEscape(candidate.ID)
	action := &candidateActionResponse{Kind: "view_details", Label: "View details", Href: href}
	switch state {
	case "needs_credentials":
		action.Kind, action.Label, action.Href = "assign_credentials", "Add SSH credentials", "/?page=Access&candidateId="+url.QueryEscape(candidate.ID)
	case "invalid_credentials":
		action.Kind, action.Label, action.Href = "assign_credentials", "Update SSH credentials", "/?page=Access&candidateId="+url.QueryEscape(candidate.ID)
	case "needs_host_trust":
		action.Kind, action.Label, action.Href = "review_trust", "Review host trust", "/?page=Access&candidateId="+url.QueryEscape(candidate.ID)
	case "needs_privilege":
		action.Label = "Fix SSH privilege"
	case "needs_server_connectivity":
		action.Label = "Fix server connectivity"
	case "queued", "enrolling":
		action.Label = "View enrollment"
	case "enrolled":
		action.Label = "View device"
	}
	return action
}

func candidateCoverage(candidate store.Candidate) string {
	if candidate.CoverageState != "" && validCandidateCoverage(candidate.CoverageState) {
		return candidate.CoverageState
	}
	if candidate.State == "stale" || candidate.State == "expired" {
		return "stale"
	}
	if candidate.LastScannedAt != nil {
		return "current"
	}
	return "unknown"
}

func apiCandidateState(value string) string {
	if value == "expired" {
		return "stale"
	}
	if validCandidateAPIState(value) {
		return value
	}
	return "discovered"
}

func validCandidateAPIState(value string) bool {
	switch value {
	case "discovered", "needs_credentials", "invalid_credentials", "needs_privilege", "needs_host_trust", "needs_server_connectivity", "queued", "enrolling", "enrolled", "unsupported", "unreachable", "excluded", "stale":
		return true
	default:
		return false
	}
}

func validCandidateCoverage(value string) bool {
	switch value {
	case "current", "partial", "stale", "contradicted", "unknown":
		return true
	default:
		return false
	}
}

func entryPointObservationPage(page store.EntryPointObservationPage) entryPointObservationListResponse {
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	items := make([]entryPointObservationResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, entryPointObservation(item))
	}
	return entryPointObservationListResponse{Items: items, NextCursor: next}
}

func entryPointObservation(item store.EntryPointObservation) entryPointObservationResponse {
	var reason *string
	if item.ReasonCode != "" {
		value := item.ReasonCode
		reason = &value
	}
	return entryPointObservationResponse{
		ID: item.ID, RunID: item.RunID, PageOrdinal: item.PageOrdinal, ScopeID: item.ScopeID, ScopeRevision: item.ScopeRevision,
		Scanner: candidateScannerResponse{Kind: item.ScannerKind, ID: item.ScannerID}, Address: item.Address, Transport: item.Transport,
		Port: item.Port, EntryPointID: item.EntryPointID, Outcome: item.Outcome, ReasonCode: reason, LatencyMilliseconds: item.LatencyMilliseconds,
		ObservedAt: item.ObservedAt, ReceivedAt: item.ReceivedAt, ExpiresAt: item.ExpiresAt, Actionable: item.Actionable,
	}
}

func candidateAccessRequests(items []store.AccessRequest) []candidateAccessRequestResponse {
	result := make([]candidateAccessRequestResponse, 0, minInt(len(items), 64))
	for _, item := range items {
		if len(result) == 64 {
			break
		}
		result = append(result, candidateAccessRequest(item))
	}
	return result
}

func candidateAccessRequest(item store.AccessRequest) candidateAccessRequestResponse {
	return candidateAccessRequestResponse{ID: item.ID, Method: item.AccessMethod, Endpoint: item.Endpoint, ReasonCode: item.ReasonCode, State: item.State, SafeDetails: cloneStringMap(item.SafeDetails), UpdatedAt: item.LastAttempt}
}

func (a *App) candidateEnrollment(ctx context.Context, candidate store.Candidate) (*candidateEnrollmentResponse, error) {
	if candidate.DeviceID == "" {
		return nil, nil
	}
	jobs, err := a.Store.ListJobs(ctx, "enrollment", "", candidate.DeviceID)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	job := jobs[len(jobs)-1]
	return &candidateEnrollmentResponse{JobID: job.ID, State: candidateJobState(job.State)}, nil
}

func candidateJobState(value string) string {
	switch value {
	case "queued", "retry":
		return "queued"
	case "claimed", "connecting", "installing", "verifying", "pausing":
		return "enrolling"
	case "enrolled", "completed":
		return "enrolled"
	case "failed":
		return "failed"
	case "paused":
		return "paused"
	default:
		return "failed"
	}
}

func candidateJob(job store.Job) candidateJobResponse {
	return candidateJobResponse{ID: job.ID, DeviceID: job.DeviceID, ScopeID: job.ScopeID, ScopeRevision: job.ScopeRevision, State: candidateJobState(job.State), Destination: job.Destination}
}

func cloneCandidateTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
