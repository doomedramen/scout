package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s *Store) PutCredential(ctx context.Context, credential CredentialRef) (CredentialRef, error) {
	if credential.Kind == "" || len(credential.Ciphertext) == 0 || len(credential.Nonce) == 0 || len(credential.WrappedDataKey) == 0 {
		return CredentialRef{}, ErrInvalid
	}
	var result CredentialRef
	err := s.mutate(ctx, func(state *State) error {
		if credential.ID == "" {
			credential.ID = NewID()
		}
		if credential.Revision == 0 {
			credential.Revision = 1
		}
		if credential.Metadata == nil {
			credential.Metadata = map[string]string{}
		}
		credential.AllowedUse = cloneStrings(credential.AllowedUse)
		credential.Targets = cloneStrings(credential.Targets)
		credential.Metadata = cloneMap(credential.Metadata)
		state.Credentials[credential.ID] = credential
		result = credential
		return nil
	})
	return result, err
}

func (s *Store) Credential(ctx context.Context, id string) (CredentialRef, error) {
	var result CredentialRef
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Credentials[id]
		if !ok {
			return ErrNotFound
		}
		item.AllowedUse = cloneStrings(item.AllowedUse)
		item.Targets = cloneStrings(item.Targets)
		item.Metadata = cloneMap(item.Metadata)
		item.Ciphertext = append([]byte(nil), item.Ciphertext...)
		item.Nonce = append([]byte(nil), item.Nonce...)
		item.WrappedDataKey = append([]byte(nil), item.WrappedDataKey...)
		result = item
		return nil
	})
	return result, err
}

func (s *Store) ListCredentials(ctx context.Context) ([]CredentialRef, error) {
	result := []CredentialRef{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Credentials {
			item.Ciphertext = nil
			item.Nonce = nil
			item.WrappedDataKey = nil
			item.AllowedUse = cloneStrings(item.AllowedUse)
			item.Targets = cloneStrings(item.Targets)
			item.Metadata = cloneMap(item.Metadata)
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
		return nil
	})
	return result, err
}

func (s *Store) RotateCredential(ctx context.Context, id string, expected int64, next CredentialRef) (CredentialRef, error) {
	if len(next.Ciphertext) == 0 || len(next.Nonce) == 0 || len(next.WrappedDataKey) == 0 {
		return CredentialRef{}, ErrInvalid
	}
	var result CredentialRef
	err := s.mutate(ctx, func(state *State) error {
		current, ok := state.Credentials[id]
		if !ok {
			return ErrNotFound
		}
		if current.RevokedAt != nil {
			return ErrRevoked
		}
		if current.Revision != expected {
			return ErrConflict
		}
		next.ID = id
		next.Kind = current.Kind
		next.Revision = current.Revision + 1
		if next.KeyVersion < 1 {
			next.KeyVersion = current.KeyVersion
		}
		state.Credentials[id] = next
		for jobID, job := range state.Jobs {
			if job.Kind == "enrollment" && job.CredentialVersion < next.Revision && isActiveJob(job.State) {
				job.State = "retry"
				job.LeaseOwner = ""
				job.LeaseExpiry = nil
				job.NextAttempt = s.now().UTC()
				job.Result = map[string]string{"code": "credential_rotated"}
				state.Jobs[jobID] = job
			}
		}
		result = next
		return nil
	})
	return result, err
}

func (s *Store) RevokeCredential(ctx context.Context, id string) error {
	return s.mutate(ctx, func(state *State) error {
		item, ok := state.Credentials[id]
		if !ok {
			return ErrNotFound
		}
		now := s.now().UTC()
		item.RevokedAt = &now
		state.Credentials[id] = item
		for jobID, job := range state.Jobs {
			if job.Kind == "enrollment" && job.CredentialVersion == item.Revision && isActiveJob(job.State) {
				job.State = "paused"
				job.LeaseOwner = ""
				job.LeaseExpiry = nil
				job.Result = map[string]string{"code": "credential_revoked"}
				state.Jobs[jobID] = job
			}
		}
		return nil
	})
}

func (s *Store) PutTrust(ctx context.Context, trust TrustRecord) (TrustRecord, error) {
	if trust.Host == "" || trust.Fingerprint == "" {
		return TrustRecord{}, ErrInvalid
	}
	var result TrustRecord
	err := s.mutate(ctx, func(state *State) error {
		if trust.ID == "" {
			trust.ID = NewID()
		}
		if trust.Revision == 0 {
			trust.Revision = 1
		}
		if trust.OwnerEstablishedAt.IsZero() {
			trust.OwnerEstablishedAt = s.now().UTC()
		}
		state.Trust[trust.ID] = trust
		result = trust
		return nil
	})
	return result, err
}

func (s *Store) Trust(ctx context.Context, id string) (TrustRecord, error) {
	var result TrustRecord
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Trust[id]
		if !ok {
			return ErrNotFound
		}
		result = item
		return nil
	})
	return result, err
}

func (s *Store) UpdateTrust(ctx context.Context, id string, expected int64, next TrustRecord) (TrustRecord, error) {
	if next.Host == "" || next.Fingerprint == "" {
		return TrustRecord{}, ErrInvalid
	}
	var result TrustRecord
	err := s.mutate(ctx, func(state *State) error {
		current, ok := state.Trust[id]
		if !ok {
			return ErrNotFound
		}
		if current.RevokedAt != nil {
			return ErrRevoked
		}
		if current.Revision != expected {
			return ErrConflict
		}
		next.ID = id
		next.ScopeID = current.ScopeID
		next.Revision = current.Revision + 1
		next.OwnerEstablishedAt = current.OwnerEstablishedAt
		state.Trust[id] = next
		result = next
		return nil
	})
	return result, err
}

func (s *Store) ListTrust(ctx context.Context) ([]TrustRecord, error) {
	result := []TrustRecord{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Trust {
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
		return nil
	})
	return result, err
}

func (s *Store) RevokeTrust(ctx context.Context, id string) error {
	return s.mutate(ctx, func(state *State) error {
		item, ok := state.Trust[id]
		if !ok {
			return ErrNotFound
		}
		now := s.now().UTC()
		item.RevokedAt = &now
		state.Trust[id] = item
		return nil
	})
}

func (s *Store) UpsertAccessRequest(ctx context.Context, request AccessRequest) (AccessRequest, error) {
	if request.DeviceID == "" || request.ReasonCode == "" {
		return AccessRequest{}, ErrInvalid
	}
	var result AccessRequest
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		var err error
		result, err = upsertAccessRequestState(state, request, s.now().UTC())
		return err
	})
	return result, err
}

func upsertAccessRequestState(state *State, request AccessRequest, now time.Time) (AccessRequest, error) {
	if request.DeviceID == "" || request.ReasonCode == "" {
		return AccessRequest{}, ErrInvalid
	}
	if request.CandidateID != "" || request.AccessMethod != "" || request.Endpoint != "" {
		if request.CandidateID == "" || request.AccessMethod == "" || request.Endpoint == "" {
			return AccessRequest{}, ErrInvalid
		}
		request.DeviceID = request.CandidateID
		dedupeKey := scanAccessRequestDedupeKey(request.CandidateID, request.AccessMethod, request.Endpoint)
		if key, ok := state.ScanAccessRequestKeys[dedupeKey]; ok {
			if existing, exists := state.AccessRequests[key.AccessRequestID]; exists && accessRequestIsOpen(existing) {
				return updateAccessRequestState(state, existing, request, key, dedupeKey, now), nil
			}
		}
		for id, existing := range state.AccessRequests {
			if existing.CandidateID == request.CandidateID && existing.AccessMethod == request.AccessMethod && existing.Endpoint == request.Endpoint && accessRequestIsOpen(existing) {
				key := ScanAccessRequestKey{DedupeKey: dedupeKey, CandidateID: request.CandidateID, AccessMethod: request.AccessMethod, Endpoint: request.Endpoint, AccessRequestID: id, State: existing.State, UpdatedAt: now}
				return updateAccessRequestState(state, existing, request, key, dedupeKey, now), nil
			}
		}
		if request.ID == "" {
			request.ID = NewID()
		}
		if request.State == "" {
			request.State = "open"
		}
		if request.LastAttempt.IsZero() {
			request.LastAttempt = now
		}
		request.SafeDetails = cloneMap(request.SafeDetails)
		state.AccessRequests[request.ID] = request
		state.ScanAccessRequestKeys[dedupeKey] = ScanAccessRequestKey{DedupeKey: dedupeKey, CandidateID: request.CandidateID, AccessMethod: request.AccessMethod, Endpoint: request.Endpoint, AccessRequestID: request.ID, State: request.State, UpdatedAt: now}
		return request, nil
	}
	for id, existing := range state.AccessRequests {
		if existing.DeviceID == request.DeviceID && existing.ReasonCode == request.ReasonCode && existing.State == "open" {
			existing.SafeDetails = cloneMap(request.SafeDetails)
			existing.LastAttempt = now
			state.AccessRequests[id] = existing
			return existing, nil
		}
	}
	if request.ID == "" {
		request.ID = NewID()
	}
	if request.State == "" {
		request.State = "open"
	}
	if request.LastAttempt.IsZero() {
		request.LastAttempt = now
	}
	request.SafeDetails = cloneMap(request.SafeDetails)
	state.AccessRequests[request.ID] = request
	return request, nil
}

func accessRequestIsOpen(request AccessRequest) bool {
	return request.State == "open" || request.State == "reevaluating"
}

func updateAccessRequestState(state *State, existing, requested AccessRequest, key ScanAccessRequestKey, dedupeKey string, now time.Time) AccessRequest {
	existing.ScopeID = requested.ScopeID
	existing.CandidateID = requested.CandidateID
	existing.AccessMethod = requested.AccessMethod
	existing.Endpoint = requested.Endpoint
	existing.EntryPointObservationID = requested.EntryPointObservationID
	existing.ReasonCode = requested.ReasonCode
	existing.SafeDetails = cloneMap(requested.SafeDetails)
	existing.LastAttempt = now
	state.AccessRequests[existing.ID] = existing
	key.DedupeKey = dedupeKey
	key.CandidateID = existing.CandidateID
	key.AccessMethod = existing.AccessMethod
	key.Endpoint = existing.Endpoint
	key.AccessRequestID = existing.ID
	key.State = existing.State
	key.UpdatedAt = now
	state.ScanAccessRequestKeys[dedupeKey] = key
	return existing
}

func scanAccessRequestDedupeKey(candidateID, accessMethod, endpoint string) string {
	return candidateID + "\x00" + accessMethod + "\x00" + endpoint
}

func (s *Store) ListAccessRequests(ctx context.Context, stateFilter, reason string) ([]AccessRequest, error) {
	result := []AccessRequest{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.AccessRequests {
			if stateFilter != "" && item.State != stateFilter {
				continue
			}
			if reason != "" && item.ReasonCode != reason {
				continue
			}
			item.SafeDetails = cloneMap(item.SafeDetails)
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].LastAttempt.After(result[j].LastAttempt) })
		return nil
	})
	return result, err
}

func (s *Store) ResolveAccessRequest(ctx context.Context, id string) error {
	return s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		item, ok := state.AccessRequests[id]
		if !ok {
			return ErrNotFound
		}
		item.State = "resolved"
		state.AccessRequests[id] = item
		for key, value := range state.ScanAccessRequestKeys {
			if value.AccessRequestID == id {
				value.State = item.State
				value.UpdatedAt = s.now().UTC()
				state.ScanAccessRequestKeys[key] = value
			}
		}
		return nil
	})
}

func (s *Store) ListAccessRequestsForCandidate(ctx context.Context, candidateID string) ([]AccessRequest, error) {
	if strings.TrimSpace(candidateID) == "" {
		return nil, ErrInvalid
	}
	result := []AccessRequest{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.AccessRequests {
			if item.CandidateID != candidateID && item.DeviceID != candidateID {
				continue
			}
			item.SafeDetails = cloneMap(item.SafeDetails)
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].LastAttempt.After(result[j].LastAttempt) })
		return nil
	})
	return result, err
}

func (s *Store) CreateJob(ctx context.Context, job Job) (Job, error) {
	var result Job
	err := s.mutate(ctx, func(state *State) error {
		if _, ok := state.Devices[job.DeviceID]; !ok {
			return ErrNotFound
		}
		if job.Kind == "enrollment" {
			for _, existing := range state.Jobs {
				if existing.DeviceID == job.DeviceID && existing.Kind == job.Kind && isActiveJob(existing.State) {
					result = existing
					return ErrDuplicate
				}
			}
		}
		if job.ID == "" {
			job.ID = NewID()
		}
		if job.State == "" {
			job.State = "queued"
		}
		if job.NextAttempt.IsZero() {
			job.NextAttempt = s.now().UTC()
		}
		if job.Result == nil {
			job.Result = map[string]string{}
		}
		state.Jobs[job.ID] = job
		result = job
		return nil
	})
	return result, err
}

func isActiveJob(state string) bool {
	switch state {
	case "queued", "claimed", "connecting", "installing", "verifying", "pausing":
		return true
	}
	return false
}

func (s *Store) ClaimJob(ctx context.Context, worker string) (Job, error) {
	var result Job
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		paused := false
		ids := make([]string, 0, len(state.Jobs))
		for id := range state.Jobs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			item := state.Jobs[id]
			if item.State != "queued" && item.State != "retry" {
				continue
			}
			if state.Workspace.RecoveryMode || jobPaused(state.Workspace, item.Kind) {
				paused = true
				continue
			}
			if item.NextAttempt.After(now) {
				continue
			}
			if item.Deadline != nil && !now.Before(*item.Deadline) {
				item.State = "failed"
				item.Result = map[string]string{"code": "deadline_expired"}
				state.Jobs[id] = item
				continue
			}
			item.State = "claimed"
			item.LeaseOwner = worker
			item.Epoch++
			expiry := now.Add(60 * time.Second)
			item.LeaseExpiry = &expiry
			item.Attempts++
			state.Jobs[id] = item
			result = item
			return nil
		}
		if paused {
			return ErrBackpressure
		}
		return ErrNotFound
	})
	return result, err
}

func (s *Store) RenewJob(ctx context.Context, id, worker string, epoch int64) (Job, error) {
	var result Job
	err := s.mutate(ctx, func(state *State) error {
		item, ok := state.Jobs[id]
		if !ok {
			return ErrNotFound
		}
		if item.LeaseOwner != worker || item.Epoch != epoch || item.LeaseExpiry == nil || !s.now().Before(*item.LeaseExpiry) {
			return ErrConflict
		}
		expiry := s.now().UTC().Add(60 * time.Second)
		item.LeaseExpiry = &expiry
		state.Jobs[id] = item
		result = item
		return nil
	})
	return result, err
}

func (s *Store) ReportJob(ctx context.Context, id, worker string, epoch int64, nextState string, result map[string]string) error {
	if result == nil {
		result = map[string]string{}
	}
	return s.mutate(ctx, func(state *State) error {
		item, ok := state.Jobs[id]
		if !ok {
			return ErrNotFound
		}
		if item.LeaseOwner != worker || item.Epoch != epoch {
			return ErrConflict
		}
		if item.LeaseExpiry == nil || !s.now().Before(*item.LeaseExpiry) {
			return ErrConflict
		}
		if !validJobState(nextState) {
			return ErrInvalid
		}
		item.State = nextState
		item.Result = cloneMap(result)
		if jobStateTerminal(nextState) {
			item.LeaseExpiry = nil
		} else {
			expires := s.now().UTC().Add(60 * time.Second)
			item.LeaseExpiry = &expires
		}
		if nextState == "retry" {
			if item.Attempts >= 8 {
				item.State = "failed"
				item.Result = map[string]string{"code": "retry_limit"}
			} else {
				item.NextAttempt = s.now().UTC().Add(backoff(item.Attempts))
			}
		}
		state.Jobs[id] = item
		return nil
	})
}

func jobStateTerminal(state string) bool {
	switch state {
	case "enrolled", "failed", "retry", "paused", "excluded", "unsupported":
		return true
	default:
		return false
	}
}

func jobPaused(workspace WorkspaceState, kind string) bool {
	switch kind {
	case "discovery":
		return workspace.DiscoveryPaused
	case "update", "rollout":
		return workspace.UpdatesPaused
	default:
		return workspace.EnrollmentPaused
	}
}

func validJobState(value string) bool {
	switch value {
	case "queued", "claimed", "connecting", "installing", "verifying", "enrolled", "failed", "retry", "paused", "excluded", "unsupported", "pausing":
		return true
	}
	return false
}
func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(1<<min(attempt, 7)) * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Store) CancelJobsForDevice(ctx context.Context, deviceID, reason string) error {
	return s.mutate(ctx, func(state *State) error {
		for id, item := range state.Jobs {
			if item.DeviceID == deviceID && isActiveJob(item.State) {
				item.State = "paused"
				item.Result = map[string]string{"code": reason}
				item.LeaseExpiry = nil
				state.Jobs[id] = item
			}
		}
		return nil
	})
}

func (s *Store) ListJobs(ctx context.Context, kind, stateFilter, deviceID string) ([]Job, error) {
	result := []Job{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Jobs {
			if kind != "" && item.Kind != kind {
				continue
			}
			if stateFilter != "" && item.State != stateFilter {
				continue
			}
			if deviceID != "" && item.DeviceID != deviceID {
				continue
			}
			item.Result = cloneMap(item.Result)
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].NextAttempt.Before(result[j].NextAttempt) })
		return nil
	})
	return result, err
}

func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	var result Job
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Jobs[id]
		if !ok {
			return ErrNotFound
		}
		item.Result = cloneMap(item.Result)
		result = item
		return nil
	})
	return result, err
}

func (s *Store) ScopeCredentialVersion(ctx context.Context, scopeID string) (int64, error) {
	var result int64
	err := s.read(ctx, func(state *State) error {
		scope, ok := state.Scopes[scopeID]
		if !ok {
			return ErrNotFound
		}
		if scope.CredentialRef == "" {
			return nil
		}
		cred, ok := state.Credentials[scope.CredentialRef]
		if !ok || cred.RevokedAt != nil {
			return ErrRevoked
		}
		result = cred.Revision
		return nil
	})
	return result, err
}

func (s *Store) ScopeTrust(ctx context.Context, scopeID string, endpoint string, fingerprint string) (bool, error) {
	var matched bool
	err := s.read(ctx, func(state *State) error {
		scope, ok := state.Scopes[scopeID]
		if !ok {
			return ErrNotFound
		}
		var trust TrustRecord
		var trustOK bool
		if scope.TrustRef != "" {
			trust, trustOK = state.Trust[scope.TrustRef]
		} else {
			for _, item := range state.Trust {
				if item.ScopeID == scopeID {
					trust, trustOK = item, true
					break
				}
			}
		}
		if !trustOK || trust.RevokedAt != nil {
			return nil
		}
		matched = strings.EqualFold(trust.Endpoint, endpoint) && strings.EqualFold(trust.Fingerprint, fingerprint)
		return nil
	})
	return matched, err
}

// ScopeTrustRecord returns the active owner-established trust record for an
// endpoint. It intentionally does not expose or infer a host key from scan
// evidence; callers use it only to prove that an explicit trust decision
// exists before handing work to the privileged enrollment boundary.
func (s *Store) ScopeTrustRecord(ctx context.Context, scopeID, endpoint string) (TrustRecord, error) {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(endpoint) == "" {
		return TrustRecord{}, ErrInvalid
	}
	var result TrustRecord
	err := s.read(ctx, func(state *State) error {
		scope, ok := state.Scopes[scopeID]
		if !ok {
			return ErrNotFound
		}
		if scope.TrustRef != "" {
			trust, trustOK := state.Trust[scope.TrustRef]
			if !trustOK || trust.RevokedAt != nil || !strings.EqualFold(trust.Endpoint, endpoint) {
				return ErrNotFound
			}
			result = trust
			return nil
		}
		for _, trust := range state.Trust {
			if trust.ScopeID == scopeID && trust.RevokedAt == nil && strings.EqualFold(trust.Endpoint, endpoint) {
				result = trust
				return nil
			}
		}
		return ErrNotFound
	})
	return result, err
}

// ResolveAccessRequestsForCandidate closes open prerequisite requests once a
// guarded enrollment handoff has been queued. Historical request rows remain
// available for audit and troubleshooting.
func (s *Store) ResolveAccessRequestsForCandidate(ctx context.Context, candidateID string) error {
	if strings.TrimSpace(candidateID) == "" {
		return ErrInvalid
	}
	return s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		found := false
		for id, request := range state.AccessRequests {
			if request.CandidateID != candidateID || !accessRequestIsOpen(request) {
				continue
			}
			found = true
			request.State = "resolved"
			state.AccessRequests[id] = request
			for key, value := range state.ScanAccessRequestKeys {
				if value.AccessRequestID == id {
					value.State = request.State
					value.UpdatedAt = s.now().UTC()
					state.ScanAccessRequestKeys[key] = value
				}
			}
		}
		if !found {
			return nil
		}
		return nil
	})
}
