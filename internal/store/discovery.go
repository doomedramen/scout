package store

import (
	"context"
	"net/netip"
	"sort"
	"strings"
	"time"
)

func normalizeAddress(raw string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", ErrInvalid
	}
	return address.String(), nil
}

func candidateKey(scopeID, address string) string {
	return safeCompositeKey(scopeID, address)
}

func (s *Store) UpsertCandidate(ctx context.Context, candidate Candidate) (Candidate, error) {
	address, err := normalizeAddress(candidate.Address)
	if err != nil || candidate.ScopeID == "" {
		return Candidate{}, ErrInvalid
	}
	var result Candidate
	err = s.mutate(ctx, func(state *State) error {
		scope, ok := state.Scopes[candidate.ScopeID]
		if !ok {
			return ErrNotFound
		}
		if candidate.SiteID != "" && candidate.SiteID != scope.SiteID {
			return ErrConflict
		}
		candidate.SiteID = scope.SiteID
		candidate.Address = address
		if candidate.Source == "" {
			candidate.Source = "local-observation"
		}
		if candidate.State == "" {
			candidate.State = "discovered"
		}
		now := s.now().UTC()
		if candidate.LastSeen.IsZero() {
			candidate.LastSeen = now
		}
		if candidate.FirstSeen.IsZero() {
			candidate.FirstSeen = candidate.LastSeen
		}
		if candidate.ExpiresAt.IsZero() {
			candidate.ExpiresAt = candidate.LastSeen.Add(15 * time.Minute)
		}
		candidate.ScopeRevision = scope.Revision
		key := candidateKey(candidate.ScopeID, candidate.Address)
		if old, exists := state.Candidates[key]; exists {
			candidate.ID = old.ID
			candidate.FirstSeen = old.FirstSeen
			candidate.EvidenceIDs = append(append([]string(nil), old.EvidenceIDs...), candidate.EvidenceIDs...)
			candidate.Excluded = old.Excluded || candidate.Excluded
		}
		if candidate.ID == "" {
			candidate.ID = NewID()
		}
		if candidate.Excluded || scopeExcludes(scope, candidate.Address) {
			candidate.Excluded = true
			candidate.State = "excluded"
		}
		state.Candidates[key] = candidate
		result = cloneCandidate(candidate)
		return nil
	})
	return result, err
}

func scopeExcludes(scope Scope, address string) bool {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return true
	}
	for _, excluded := range scope.Exclusions {
		if excluded == address {
			return true
		}
		prefix, parseErr := netip.ParsePrefix(strings.TrimSpace(excluded))
		if parseErr == nil && prefix.Contains(parsed) {
			return true
		}
	}
	return false
}

func (s *Store) GetCandidate(ctx context.Context, id string) (Candidate, error) {
	var result Candidate
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Candidates {
			if item.ID == id {
				result = cloneCandidate(item)
				return nil
			}
		}
		return ErrNotFound
	})
	return result, err
}

func (s *Store) ListCandidates(ctx context.Context, scopeID, stateFilter string) ([]Candidate, error) {
	result := []Candidate{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Candidates {
			if scopeID != "" && item.ScopeID != scopeID {
				continue
			}
			if stateFilter != "" && item.State != stateFilter {
				continue
			}
			result = append(result, cloneCandidate(item))
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].LastSeen.Equal(result[j].LastSeen) {
				return result[i].Address < result[j].Address
			}
			return result[i].LastSeen.After(result[j].LastSeen)
		})
		return nil
	})
	return result, err
}

func (s *Store) ExpireCandidates(ctx context.Context, now time.Time) (int, error) {
	changed := 0
	err := s.mutate(ctx, func(state *State) error {
		for key, item := range state.Candidates {
			if item.Excluded || item.State == "expired" || item.ExpiresAt.IsZero() || now.Before(item.ExpiresAt) {
				continue
			}
			item.State = "expired"
			state.Candidates[key] = item
			changed++
		}
		return nil
	})
	return changed, err
}

func cloneCandidate(candidate Candidate) Candidate {
	candidate.EvidenceIDs = append([]string(nil), candidate.EvidenceIDs...)
	return candidate
}

func (s *Store) CreateWorker(ctx context.Context, worker WorkerIdentity) error {
	if worker.Kind != "enroller" || worker.AuthTokenHash == "" || worker.Name == "" {
		return ErrInvalid
	}
	return s.mutate(ctx, func(state *State) error {
		if _, exists := state.Workers[worker.ID]; exists && worker.ID != "" {
			return ErrConflict
		}
		for _, existing := range state.Workers {
			if existing.AuthTokenHash == worker.AuthTokenHash {
				return ErrConflict
			}
		}
		for _, siteID := range worker.SiteIDs {
			if _, ok := state.Sites[siteID]; !ok {
				return ErrNotFound
			}
		}
		if worker.ID == "" {
			worker.ID = NewID()
		}
		if worker.CreatedAt.IsZero() {
			worker.CreatedAt = s.now().UTC()
		}
		worker.SiteIDs = cloneStrings(worker.SiteIDs)
		state.Workers[worker.ID] = worker
		return nil
	})
}

func (s *Store) WorkerByTokenHash(ctx context.Context, tokenHash string) (WorkerIdentity, error) {
	var result WorkerIdentity
	err := s.read(ctx, func(state *State) error {
		for _, worker := range state.Workers {
			if worker.AuthTokenHash == tokenHash && worker.RevokedAt == nil {
				result = worker
				result.SiteIDs = cloneStrings(worker.SiteIDs)
				return nil
			}
		}
		return ErrUnauthorized
	})
	return result, err
}

func (s *Store) RevokeWorker(ctx context.Context, id string) error {
	return s.mutate(ctx, func(state *State) error {
		worker, ok := state.Workers[id]
		if !ok {
			return ErrNotFound
		}
		now := s.now().UTC()
		worker.RevokedAt = &now
		state.Workers[id] = worker
		return nil
	})
}

func (s *Store) ListWorkers(ctx context.Context) ([]WorkerIdentity, error) {
	result := []WorkerIdentity{}
	err := s.read(ctx, func(state *State) error {
		for _, worker := range state.Workers {
			worker.AuthTokenHash = ""
			worker.SiteIDs = cloneStrings(worker.SiteIDs)
			result = append(result, worker)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
		return nil
	})
	return result, err
}

func allowedWorkerSite(worker WorkerIdentity, siteID string) bool {
	for _, allowed := range worker.SiteIDs {
		if allowed == siteID {
			return true
		}
	}
	return false
}

func (s *Store) ClaimWorkerJob(ctx context.Context, worker WorkerIdentity) (Job, error) {
	var result Job
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		ids := make([]string, 0, len(state.Jobs))
		for id := range state.Jobs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			item := state.Jobs[id]
			if item.Kind != "enrollment" || (item.State != "queued" && item.State != "retry") || item.NextAttempt.After(now) {
				continue
			}
			if state.Workspace.RecoveryMode || state.Workspace.EnrollmentPaused {
				return ErrConflict
			}
			device, ok := state.Devices[item.DeviceID]
			if !ok || device.Excluded || device.Lifecycle == "decommissioned" {
				continue
			}
			if !allowedWorkerSite(worker, device.SiteID) {
				continue
			}
			if item.ScopeRevision > 0 {
				validScope := false
				for _, scope := range state.Scopes {
					if scope.SiteID == device.SiteID && scope.Enabled && scope.Revision == item.ScopeRevision {
						validScope = true
						break
					}
				}
				if !validScope {
					item.State = "retry"
					item.Result = map[string]string{"code": "scope_revision_changed"}
					state.Jobs[id] = item
					continue
				}
			}
			item.State = "claimed"
			item.LeaseOwner = worker.ID
			item.Epoch++
			item.Attempts++
			if item.CredentialGrantID == "" && item.ScopeID != "" {
				item.CredentialGrantID = item.ID
			}
			expires := now.Add(60 * time.Second)
			item.LeaseExpiry = &expires
			state.Jobs[id] = item
			result = item
			return nil
		}
		return ErrNotFound
	})
	return result, err
}

func (s *Store) AcknowledgePause(ctx context.Context, holder string) (WorkspaceState, error) {
	var result WorkspaceState
	if strings.TrimSpace(holder) == "" {
		return result, ErrInvalid
	}
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		if state.Workspace.DiscoveryPaused && activeScanHeldBy(state, holder) {
			result = state.Workspace
			return nil
		}
		remaining := state.Workspace.ExecutionHolders[:0]
		for _, item := range state.Workspace.ExecutionHolders {
			if item != holder {
				remaining = append(remaining, item)
			}
		}
		state.Workspace.ExecutionHolders = remaining
		state.Workspace.PausePending = len(remaining) > 0
		result = state.Workspace
		return nil
	})
	return result, err
}

func activeScanHeldBy(state *State, holder string) bool {
	for _, run := range state.ScanRuns {
		if run.LeaseOwner == holder && !scanRunTerminalStates[run.State] {
			return true
		}
	}
	return false
}

func (s *Store) SetControlPause(ctx context.Context, discovery, enrollment, updates bool) (WorkspaceState, error) {
	var result WorkspaceState
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		now := s.now().UTC()
		state.Workspace.DiscoveryPaused = discovery
		state.Workspace.EnrollmentPaused = enrollment
		state.Workspace.UpdatesPaused = updates
		state.Workspace.PauseRequested = discovery || enrollment || updates
		state.Workspace.ExecutionHolders = state.Workspace.ExecutionHolders[:0]
		holders := map[string]bool{}
		for _, job := range state.Jobs {
			if isActiveJob(job.State) && job.LeaseOwner != "" && jobPaused(state.Workspace, job.Kind) {
				holders[job.LeaseOwner] = true
			}
		}
		if discovery {
			for id, run := range state.ScanRuns {
				if scanRunTerminalStates[run.State] {
					continue
				}
				run = markScanRunCancellation(run, now)
				if scanRunTerminalStates[run.State] {
					delete(state.ScanRunLeases, id)
				} else if run.LeaseOwner != "" {
					holders[run.LeaseOwner] = true
				}
				state.ScanRuns[id] = run
			}
		}
		for holder := range holders {
			state.Workspace.ExecutionHolders = append(state.Workspace.ExecutionHolders, holder)
		}
		sort.Strings(state.Workspace.ExecutionHolders)
		state.Workspace.PausePending = len(state.Workspace.ExecutionHolders) > 0
		state.Workspace.PolicyRevision++
		result = state.Workspace
		return nil
	})
	return result, err
}
