package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	ScanTransportTCP   = "tcp"
	ScanAccessSSH      = "ssh"
	maxScanRunAttempts = 16384

	ScanRunQueued    = "queued"
	ScanRunLeased    = "leased"
	ScanRunRunning   = "running"
	ScanRunUploading = "uploading"

	ScanRunCompleted = "completed"
	ScanRunPartial   = "partial"
	ScanRunFailed    = "failed"
	ScanRunCancelled = "cancelled"
	ScanRunRejected  = "rejected"
	ScanRunExpired   = "expired"
)

var scanRunTerminalStates = map[string]bool{
	ScanRunCompleted: true,
	ScanRunPartial:   true,
	ScanRunFailed:    true,
	ScanRunCancelled: true,
	ScanRunRejected:  true,
	ScanRunExpired:   true,
}

type ScanRunQuery struct {
	ScopeID       string
	ScannerID     string
	State         string
	StartedAfter  *time.Time
	StartedBefore *time.Time
	Cursor        string
	Limit         int
}

type ScanRunPage struct {
	Items      []ScanRun `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type EntryPointObservationQuery struct {
	ScopeID   string
	RunID     string
	ScannerID string
	Address   string
	Cursor    string
	Limit     int
}

type EntryPointObservationPage struct {
	Items      []EntryPointObservation `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type scanCursor struct {
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

func defaultScanPolicy(scope Scope, now time.Time) ScanPolicy {
	return ScanPolicy{
		ScopeID:         scope.ID,
		Revision:        1,
		Enabled:         scope.Enabled,
		ServerEnabled:   false,
		AgentIDs:        []string{},
		ScheduleSeconds: 300,
		EntryPoints: []ScanEntryPoint{{
			ID:           "ssh-default",
			Name:         "SSH",
			Transport:    ScanTransportTCP,
			Port:         22,
			AccessMethod: ScanAccessSSH,
			Enabled:      true,
		}},
		Limits: ScanLimits{
			ProbesPerSecond:     10,
			Concurrency:         16,
			TargetBudget:        256,
			AttemptBudget:       256,
			TimeoutMilliseconds: 2000,
			RunDeadlineSeconds:  600,
			ResultPageSize:      1000,
		},
		UpdatedAt: now.UTC(),
	}
}

func cloneScanEntryPoints(items []ScanEntryPoint) []ScanEntryPoint {
	return append([]ScanEntryPoint(nil), items...)
}

func cloneScanPolicy(policy ScanPolicy) ScanPolicy {
	policy.AgentIDs = cloneStrings(policy.AgentIDs)
	policy.EntryPoints = cloneScanEntryPoints(policy.EntryPoints)
	return policy
}

func cloneScanSnapshot(snapshot ScanPolicySnapshot) ScanPolicySnapshot {
	snapshot.Ranges = cloneStrings(snapshot.Ranges)
	snapshot.Exclusions = cloneStrings(snapshot.Exclusions)
	snapshot.EntryPoints = cloneScanEntryPoints(snapshot.EntryPoints)
	return snapshot
}

func cloneScanRun(run ScanRun) ScanRun {
	run.PolicySnapshot = cloneScanSnapshot(run.PolicySnapshot)
	if run.StartedAt != nil {
		value := *run.StartedAt
		run.StartedAt = &value
	}
	if run.FinishedAt != nil {
		value := *run.FinishedAt
		run.FinishedAt = &value
	}
	if run.LeaseExpiresAt != nil {
		value := *run.LeaseExpiresAt
		run.LeaseExpiresAt = &value
	}
	if run.FinalPageOrdinal != nil {
		value := *run.FinalPageOrdinal
		run.FinalPageOrdinal = &value
	}
	return run
}

func cloneObservation(observation EntryPointObservation) EntryPointObservation {
	if observation.LatencyMilliseconds != nil {
		value := *observation.LatencyMilliseconds
		observation.LatencyMilliseconds = &value
	}
	return observation
}

func scanPolicyForState(state *State, scopeID string, now time.Time) (ScanPolicy, error) {
	scope, ok := state.Scopes[scopeID]
	if !ok {
		return ScanPolicy{}, ErrNotFound
	}
	if policy, ok := state.ScanPolicies[scopeID]; ok {
		return cloneScanPolicy(policy), nil
	}
	return defaultScanPolicy(scope, now), nil
}

func (s *Store) ScanPolicy(ctx context.Context, scopeID string) (ScanPolicy, error) {
	var result ScanPolicy
	err := s.read(ctx, func(state *State) error {
		policy, err := scanPolicyForState(state, scopeID, s.now().UTC())
		if err != nil {
			return err
		}
		result = policy
		return nil
	})
	return result, err
}

func validateScanPolicy(policy ScanPolicy, scope Scope) error {
	if policy.ScopeID != scope.ID || len(policy.AgentIDs) > 16 || policy.ScheduleSeconds < 60 || policy.ScheduleSeconds > 86400 {
		return ErrInvalid
	}
	if len(policy.EntryPoints) == 0 || len(policy.EntryPoints) > 64 {
		return ErrInvalid
	}
	seenIDs := map[string]bool{}
	seenEndpoints := map[string]bool{}
	enabledEntryPoints := 0
	for _, entryPoint := range policy.EntryPoints {
		if strings.TrimSpace(entryPoint.ID) == "" || len(entryPoint.ID) > 64 || seenIDs[entryPoint.ID] || strings.TrimSpace(entryPoint.Name) == "" || len(entryPoint.Name) > 64 {
			return ErrInvalid
		}
		seenIDs[entryPoint.ID] = true
		if entryPoint.Transport != ScanTransportTCP || entryPoint.Port < 1 || entryPoint.Port > 65535 {
			return ErrInvalid
		}
		if entryPoint.AccessMethod != "" && entryPoint.AccessMethod != ScanAccessSSH {
			return ErrInvalid
		}
		endpoint := fmt.Sprintf("%s/%d", entryPoint.Transport, entryPoint.Port)
		if seenEndpoints[endpoint] {
			return ErrInvalid
		}
		seenEndpoints[endpoint] = true
		if entryPoint.Enabled {
			enabledEntryPoints++
		}
	}
	limits := policy.Limits
	if limits.ProbesPerSecond < 1 || limits.ProbesPerSecond > 1000 || limits.Concurrency < 1 || limits.Concurrency > 16 || limits.TargetBudget < 1 || limits.TargetBudget > 4096 || limits.AttemptBudget < 1 || limits.AttemptBudget > 16384 || limits.TimeoutMilliseconds < 100 || limits.TimeoutMilliseconds > 10000 || limits.RunDeadlineSeconds < 30 || limits.RunDeadlineSeconds > 900 {
		return ErrInvalid
	}
	if limits.ResultPageSize != 0 && (limits.ResultPageSize < 1 || limits.ResultPageSize > 1000) {
		return ErrInvalid
	}
	if enabledEntryPoints == 0 || limits.AttemptBudget > limits.TargetBudget*enabledEntryPoints {
		return ErrInvalid
	}
	return nil
}

func (s *Store) UpdateScanPolicy(ctx context.Context, scopeID string, expected int64, next ScanPolicy) (ScanPolicy, error) {
	var result ScanPolicy
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		scope, ok := state.Scopes[scopeID]
		if !ok {
			return ErrNotFound
		}
		current, err := scanPolicyForState(state, scopeID, s.now().UTC())
		if err != nil {
			return err
		}
		if current.Revision != expected {
			return ErrConflict
		}
		next = cloneScanPolicy(next)
		next.ScopeID = scopeID
		next.Revision = current.Revision + 1
		next.Enabled = scope.Enabled
		next.UpdatedAt = s.now().UTC()
		if err := validateScanPolicy(next, scope); err != nil {
			return err
		}
		state.ScanPolicies[scopeID] = next
		for id, run := range state.ScanRuns {
			if run.ScopeID == scopeID && !scanRunTerminalStates[run.State] && run.ScopeRevision < next.Revision {
				run.CancellationRequested = true
				state.ScanRuns[id] = run
			}
		}
		result = cloneScanPolicy(next)
		return nil
	})
	return result, err
}

func encodeScanCursor(item scanCursor) string {
	data, err := json.Marshal(item)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeScanCursor(raw string) (scanCursor, error) {
	if raw == "" {
		return scanCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return scanCursor{}, ErrInvalid
	}
	var cursor scanCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.ID == "" || cursor.Time.IsZero() {
		return scanCursor{}, ErrInvalid
	}
	return cursor, nil
}

func (s *Store) CreateScanRun(ctx context.Context, run ScanRun) (ScanRun, error) {
	var result ScanRun
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		scope, ok := state.Scopes[run.ScopeID]
		if !ok {
			return ErrNotFound
		}
		policy, err := scanPolicyForState(state, run.ScopeID, s.now().UTC())
		if err != nil {
			return err
		}
		if !scope.Enabled || !policy.Enabled || run.ScopeRevision != policy.Revision {
			return ErrConflict
		}
		if run.ScannerKind != "server" && run.ScannerKind != "agent" || strings.TrimSpace(run.ScannerID) == "" {
			return ErrInvalid
		}
		if run.Trigger == "" {
			run.Trigger = "owner"
		}
		if run.Trigger != "owner" && run.Trigger != "schedule" {
			return ErrInvalid
		}
		if run.PolicySnapshot.Limits.TargetBudget == 0 {
			run.PolicySnapshot = ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: policy.EntryPoints, Limits: policy.Limits}
		}
		if len(run.PolicySnapshot.Ranges) == 0 || len(run.PolicySnapshot.EntryPoints) == 0 {
			return ErrInvalid
		}
		if run.PolicySnapshot.Limits.AttemptBudget < 1 || run.PolicySnapshot.Limits.AttemptBudget > 16384 || run.PolicySnapshot.Limits.TargetBudget < 1 || run.PolicySnapshot.Limits.TargetBudget > 4096 {
			return ErrInvalid
		}
		if run.ScheduledAt.IsZero() {
			run.ScheduledAt = s.now().UTC()
		}
		if run.AssignmentExpiresAt.IsZero() {
			run.AssignmentExpiresAt = run.ScheduledAt.Add(time.Duration(policy.Limits.RunDeadlineSeconds) * time.Second)
		}
		if run.AssignmentExpiresAt.Before(run.ScheduledAt) {
			return ErrInvalid
		}
		if run.TargetsPlanned < 0 || run.TargetsPlanned > run.PolicySnapshot.Limits.TargetBudget || run.AttemptsPlanned < 0 || run.AttemptsPlanned > maxScanRunAttempts || run.AttemptsCompleted < 0 || run.AttemptsCompleted > run.AttemptsPlanned {
			return ErrInvalid
		}
		planLimit, planErr := scanRunPlanLimit(run.PolicySnapshot)
		if planErr != nil || run.AttemptsPlanned > planLimit {
			return ErrInvalid
		}
		if run.State == "" {
			run.State = ScanRunQueued
		}
		if run.State != ScanRunQueued {
			return ErrInvalid
		}
		if run.IdempotencyKey != "" {
			for _, existing := range state.ScanRuns {
				if existing.ScopeID == run.ScopeID && existing.IdempotencyKey == run.IdempotencyKey {
					result = cloneScanRun(existing)
					return nil
				}
			}
		}
		for _, existing := range state.ScanRuns {
			if existing.ScopeID == run.ScopeID && existing.ScannerKind == run.ScannerKind && existing.ScannerID == run.ScannerID && !scanRunTerminalStates[existing.State] {
				return ErrConflict
			}
		}
		if run.ID == "" {
			run.ID = NewID()
		}
		if run.CreatedAt.IsZero() {
			run.CreatedAt = s.now().UTC()
		}
		if run.UpdatedAt.IsZero() {
			run.UpdatedAt = run.CreatedAt
		}
		if run.OutcomeCounts == (ScanOutcomeCounts{}) {
			run.OutcomeCounts = ScanOutcomeCounts{}
		}
		run.PolicySnapshot = cloneScanSnapshot(run.PolicySnapshot)
		state.ScanRuns[run.ID] = run
		result = cloneScanRun(run)
		return nil
	})
	return result, err
}

func scanRunPlanLimit(snapshot ScanPolicySnapshot) (int, error) {
	enabledEntryPoints := 0
	for _, entryPoint := range snapshot.EntryPoints {
		if entryPoint.Enabled {
			enabledEntryPoints++
		}
	}
	if enabledEntryPoints == 0 {
		return 0, ErrInvalid
	}
	if enabledEntryPoints > 0 && snapshot.Limits.TargetBudget > int(^uint(0)>>1)/enabledEntryPoints {
		return 0, ErrInvalid
	}
	limit := snapshot.Limits.TargetBudget * enabledEntryPoints
	if limit > maxScanRunAttempts {
		limit = maxScanRunAttempts
	}
	return limit, nil
}

func (s *Store) GetScanRun(ctx context.Context, id string) (ScanRun, error) {
	var result ScanRun
	err := s.read(ctx, func(state *State) error {
		run, ok := state.ScanRuns[id]
		if !ok {
			return ErrNotFound
		}
		result = cloneScanRun(run)
		return nil
	})
	return result, err
}

func (s *Store) ListScanRuns(ctx context.Context, query ScanRunQuery) (ScanRunPage, error) {
	var result ScanRunPage
	err := s.read(ctx, func(state *State) error {
		cursor, err := decodeScanCursor(query.Cursor)
		if err != nil {
			return err
		}
		limit := query.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 500 {
			limit = 500
		}
		items := make([]ScanRun, 0, len(state.ScanRuns))
		for _, run := range state.ScanRuns {
			if query.ScopeID != "" && run.ScopeID != query.ScopeID || query.ScannerID != "" && run.ScannerID != query.ScannerID || query.State != "" && run.State != query.State {
				continue
			}
			if query.StartedAfter != nil && (run.StartedAt == nil || !run.StartedAt.After(*query.StartedAfter)) || query.StartedBefore != nil && (run.StartedAt == nil || !run.StartedAt.Before(*query.StartedBefore)) {
				continue
			}
			if !cursor.Time.IsZero() && !scanCursorAfter(run.ScheduledAt, run.ID, cursor) {
				continue
			}
			items = append(items, cloneScanRun(run))
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].ScheduledAt.Equal(items[j].ScheduledAt) {
				return items[i].ID > items[j].ID
			}
			return items[i].ScheduledAt.After(items[j].ScheduledAt)
		})
		if len(items) > limit {
			last := items[limit-1]
			result.NextCursor = encodeScanCursor(scanCursor{Time: last.ScheduledAt, ID: last.ID})
			items = items[:limit]
		}
		result.Items = items
		return nil
	})
	return result, err
}

func scanCursorAfter(itemTime time.Time, itemID string, cursor scanCursor) bool {
	return itemTime.Before(cursor.Time) || itemTime.Equal(cursor.Time) && itemID < cursor.ID
}

func scanLeaseMatches(run ScanRun, owner string, epoch int64, now time.Time) bool {
	return run.LeaseOwner == owner && run.LeaseEpoch == epoch && run.LeaseExpiresAt != nil && now.Before(*run.LeaseExpiresAt)
}

func (s *Store) LeaseScanRun(ctx context.Context, runID, owner string, duration time.Duration) (ScanRun, error) {
	var result ScanRun
	if strings.TrimSpace(owner) == "" || duration <= 0 {
		return result, ErrInvalid
	}
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		run, ok := state.ScanRuns[runID]
		if !ok {
			return ErrNotFound
		}
		now := s.now().UTC()
		if scanRunTerminalStates[run.State] || run.CancellationRequested {
			return ErrConflict
		}
		if run.LeaseExpiresAt != nil && now.Before(*run.LeaseExpiresAt) {
			return ErrConflict
		}
		run.State = ScanRunLeased
		run.LeaseOwner = owner
		run.LeaseEpoch++
		if run.LeaseEpoch < 1 {
			run.LeaseEpoch = 1
		}
		leaseUntil := now.Add(duration)
		run.LeaseExpiresAt = &leaseUntil
		run.UpdatedAt = now
		state.ScanRuns[runID] = run
		state.ScanRunLeases[runID] = ScanRunLease{RunID: runID, LeaseOwner: owner, LeaseEpoch: run.LeaseEpoch, LeaseUntil: leaseUntil, State: run.State}
		result = cloneScanRun(run)
		return nil
	})
	return result, err
}

func (s *Store) RenewScanLease(ctx context.Context, runID, owner string, epoch int64, duration time.Duration) (ScanRun, error) {
	var result ScanRun
	if strings.TrimSpace(owner) == "" || epoch < 1 || duration <= 0 {
		return result, ErrInvalid
	}
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		run, ok := state.ScanRuns[runID]
		if !ok {
			return ErrNotFound
		}
		now := s.now().UTC()
		if !scanLeaseMatches(run, owner, epoch, now) || scanRunTerminalStates[run.State] {
			return ErrConflict
		}
		leaseUntil := now.Add(duration)
		run.LeaseExpiresAt = &leaseUntil
		run.UpdatedAt = now
		state.ScanRuns[runID] = run
		lease := state.ScanRunLeases[runID]
		lease.LeaseUntil = leaseUntil
		lease.State = run.State
		state.ScanRunLeases[runID] = lease
		result = cloneScanRun(run)
		return nil
	})
	return result, err
}

func (s *Store) StartScanRun(ctx context.Context, runID, owner string, epoch int64) (ScanRun, error) {
	return s.transitionScanRun(ctx, runID, owner, epoch, ScanRunRunning, "")
}

func (s *Store) BeginScanUpload(ctx context.Context, runID, owner string, epoch int64) (ScanRun, error) {
	return s.transitionScanRun(ctx, runID, owner, epoch, ScanRunUploading, "")
}

func (s *Store) transitionScanRun(ctx context.Context, runID, owner string, epoch int64, nextState, reason string) (ScanRun, error) {
	var result ScanRun
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		run, ok := state.ScanRuns[runID]
		if !ok {
			return ErrNotFound
		}
		if nextState != ScanRunRunning && nextState != ScanRunUploading || run.State == nextState {
			return ErrInvalid
		}
		if !scanLeaseMatches(run, owner, epoch, s.now().UTC()) {
			return ErrConflict
		}
		if nextState == ScanRunRunning && run.State != ScanRunLeased || nextState == ScanRunUploading && run.State != ScanRunRunning {
			return ErrConflict
		}
		run.State = nextState
		if run.StartedAt == nil {
			now := s.now().UTC()
			run.StartedAt = &now
		}
		run.UpdatedAt = s.now().UTC()
		state.ScanRuns[runID] = run
		lease := state.ScanRunLeases[runID]
		lease.State = nextState
		state.ScanRunLeases[runID] = lease
		result = cloneScanRun(run)
		return nil
	})
	return result, err
}

func (s *Store) RequestScanCancellation(ctx context.Context, runID string) (ScanRun, error) {
	var result ScanRun
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		run, ok := state.ScanRuns[runID]
		if !ok {
			return ErrNotFound
		}
		if scanRunTerminalStates[run.State] {
			result = cloneScanRun(run)
			return nil
		}
		run.CancellationRequested = true
		run.UpdatedAt = s.now().UTC()
		if run.State == ScanRunQueued {
			run.State = ScanRunCancelled
			now := s.now().UTC()
			run.FinishedAt = &now
		}
		state.ScanRuns[runID] = run
		if scanRunTerminalStates[run.State] {
			delete(state.ScanRunLeases, runID)
		}
		result = cloneScanRun(run)
		return nil
	})
	return result, err
}

func (s *Store) FinalizeScanRun(ctx context.Context, runID, owner string, epoch int64, terminalState, partialReason, errorCode string) (ScanRun, error) {
	var result ScanRun
	if !scanRunTerminalStates[terminalState] {
		return result, ErrInvalid
	}
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		run, ok := state.ScanRuns[runID]
		if !ok {
			return ErrNotFound
		}
		if scanRunTerminalStates[run.State] {
			result = cloneScanRun(run)
			return nil
		}
		if !scanLeaseMatches(run, owner, epoch, s.now().UTC()) {
			return ErrConflict
		}
		now := s.now().UTC()
		run.State = terminalState
		run.PartialReason = partialReason
		run.ErrorCode = errorCode
		run.FinishedAt = &now
		run.LeaseExpiresAt = nil
		run.LeaseOwner = ""
		run.UpdatedAt = now
		state.ScanRuns[runID] = run
		delete(state.ScanRunLeases, runID)
		result = cloneScanRun(run)
		return nil
	})
	return result, err
}

func (s *Store) AcceptScanResultPage(ctx context.Context, receipt ScanResultReceipt, observations []EntryPointObservation) (ScanResultReceipt, bool, error) {
	var result ScanResultReceipt
	duplicate := false
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		run, ok := state.ScanRuns[receipt.RunID]
		if !ok {
			return ErrNotFound
		}
		if scanRunTerminalStates[run.State] || run.State == ScanRunQueued || run.State == ScanRunLeased {
			return ErrConflict
		}
		if existing, ok := state.ScanResultReceipts[scanReceiptKey(receipt.RunID, receipt.PageOrdinal)]; ok {
			if existing.ContentHash != receipt.ContentHash {
				return ErrConflict
			}
			result = existing
			duplicate = true
			return nil
		}
		if receipt.PageOrdinal < 0 || receipt.PageOrdinal > 16383 || len(observations) > 1000 || receipt.ResultCount != len(observations) || receipt.ContentHash == "" {
			return ErrInvalid
		}
		if run.AttemptsCompleted+len(observations) > run.AttemptsPlanned {
			return ErrBackpressure
		}
		seen := map[string]bool{}
		for index, observation := range observations {
			if observation.ID == "" {
				observation.ID = NewID()
			}
			if observation.RunID != receipt.RunID || observation.PageOrdinal != receipt.PageOrdinal || observation.ScopeID != run.ScopeID || observation.ScopeRevision != run.ScopeRevision || observation.ScannerKind != run.ScannerKind || observation.ScannerID != run.ScannerID {
				return ErrConflict
			}
			if _, err := netip.ParseAddr(observation.Address); err != nil || observation.Transport != ScanTransportTCP || observation.Port < 1 || observation.Port > 65535 || observation.EntryPointID == "" || !validScanOutcome(observation.Outcome) {
				return ErrInvalid
			}
			if observation.ObservedAt.IsZero() {
				return ErrInvalid
			}
			key := fmt.Sprintf("%s/%s/%d/%s", observation.Address, observation.Transport, observation.Port, observation.EntryPointID)
			if seen[key] {
				return ErrDuplicate
			}
			seen[key] = true
			for _, existing := range state.EntryPointObservations {
				if existing.RunID == observation.RunID && scanObservationKey(existing.Address, existing.Transport, existing.Port, existing.EntryPointID) == key {
					return ErrDuplicate
				}
			}
			if observation.ReceivedAt.IsZero() {
				observation.ReceivedAt = s.now().UTC()
			}
			if observation.ExpiresAt.IsZero() {
				observation.ExpiresAt = observation.ReceivedAt.Add(30 * 24 * time.Hour)
			}
			if observation.ExpiresAt.Before(observation.ReceivedAt) {
				return ErrInvalid
			}
			observations[index] = observation
		}
		receipt.AcceptedAt = receipt.AcceptedAt.UTC()
		if receipt.AcceptedAt.IsZero() {
			receipt.AcceptedAt = s.now().UTC()
		}
		receipt.ResultCount = len(observations)
		state.ScanResultReceipts[scanReceiptKey(receipt.RunID, receipt.PageOrdinal)] = receipt
		for _, observation := range observations {
			state.EntryPointObservations[observation.ID] = cloneObservation(observation)
			updateCurrentObservation(state, observation)
			incrementScanOutcome(&run.OutcomeCounts, observation.Outcome)
		}
		run.PageCount++
		run.AttemptsCompleted += len(observations)
		if receipt.IsFinal {
			ordinal := receipt.PageOrdinal
			run.FinalPageOrdinal = &ordinal
		}
		run.UpdatedAt = s.now().UTC()
		state.ScanRuns[run.ID] = run
		result = receipt
		return nil
	})
	return result, duplicate, err
}

func scanReceiptKey(runID string, ordinal int) string {
	return fmt.Sprintf("%s/%d", runID, ordinal)
}

func scanObservationKey(address, transport string, port int, entryPointID string) string {
	return fmt.Sprintf("%s/%s/%d/%s", address, transport, port, entryPointID)
}

func validScanOutcome(outcome string) bool {
	switch outcome {
	case "open", "closed", "filtered", "unreachable", "skipped", "scanner_error":
		return true
	default:
		return false
	}
}

func incrementScanOutcome(counts *ScanOutcomeCounts, outcome string) {
	switch outcome {
	case "open":
		counts.Open++
	case "closed":
		counts.Closed++
	case "filtered":
		counts.Filtered++
	case "unreachable":
		counts.Unreachable++
	case "skipped":
		counts.Skipped++
	case "scanner_error":
		counts.ScannerError++
	}
}

func entryPointCurrentKey(scopeID, address, transport string, port int, scannerKind, scannerID string) string {
	return fmt.Sprintf("%s/%s/%s/%d/%s/%s", scopeID, address, transport, port, scannerKind, scannerID)
}

func updateCurrentObservation(state *State, observation EntryPointObservation) {
	key := entryPointCurrentKey(observation.ScopeID, observation.Address, observation.Transport, observation.Port, observation.ScannerKind, observation.ScannerID)
	current, exists := state.EntryPointCurrent[key]
	if exists && (observation.ObservedAt.Before(current.ObservedAt) || observation.ObservedAt.Equal(current.ObservedAt) && observation.ReceivedAt.Before(current.ReceivedAt)) {
		return
	}
	freshness := "current"
	if observation.ExpiresAt.Before(observation.ReceivedAt) {
		freshness = "stale"
	}
	state.EntryPointCurrent[key] = EntryPointCurrent{
		Key:           key,
		ScopeID:       observation.ScopeID,
		Address:       observation.Address,
		Transport:     observation.Transport,
		Port:          observation.Port,
		ScannerKind:   observation.ScannerKind,
		ScannerID:     observation.ScannerID,
		ObservationID: observation.ID,
		Outcome:       observation.Outcome,
		Freshness:     freshness,
		ObservedAt:    observation.ObservedAt,
		ReceivedAt:    observation.ReceivedAt,
		ExpiresAt:     observation.ExpiresAt,
	}
}

func (s *Store) ScanResultReceipt(ctx context.Context, runID string, pageOrdinal int) (ScanResultReceipt, error) {
	var result ScanResultReceipt
	err := s.read(ctx, func(state *State) error {
		item, ok := state.ScanResultReceipts[scanReceiptKey(runID, pageOrdinal)]
		if !ok {
			return ErrNotFound
		}
		result = item
		return nil
	})
	return result, err
}

func (s *Store) ApplyScanCandidate(ctx context.Context, update ScanCandidateUpdate) (Candidate, *AccessRequest, error) {
	address, err := normalizeAddress(update.Candidate.Address)
	if err != nil || update.Candidate.ScopeID == "" {
		return Candidate{}, nil, ErrInvalid
	}
	var result Candidate
	var requestResult *AccessRequest
	err = s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		scope, ok := state.Scopes[update.Candidate.ScopeID]
		if !ok {
			return ErrNotFound
		}
		candidate := cloneCandidate(update.Candidate)
		candidate.Address = address
		candidate.SiteID = scope.SiteID
		if candidate.Source == "" {
			candidate.Source = "active-scan"
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
		key := candidateKey(candidate.ScopeID, candidate.Address)
		if old, exists := state.Candidates[key]; exists {
			candidate.ID = old.ID
			candidate.FirstSeen = old.FirstSeen
			candidate.EvidenceIDs = appendUniqueStrings(old.EvidenceIDs, candidate.EvidenceIDs...)
			candidate.Excluded = old.Excluded || candidate.Excluded
			if old.State == "queued" || old.State == "enrolling" || old.State == "enrolled" {
				candidate.State = old.State
			}
		}
		if candidate.ID == "" {
			candidate.ID = NewID()
		}
		if candidate.Excluded || scopeExcludes(scope, candidate.Address) {
			candidate.Excluded = true
			candidate.State = "excluded"
		}
		extension := mergeScanCandidateExtension(state.ScanCandidateExtensions[candidate.ID], update.Extension, candidate.ID, now)
		candidate.CoverageState = extension.CoverageState
		candidate.EntryPointIDs = append([]string(nil), extension.EntryPointIDs...)
		candidate.PreferredAccessMethod = extension.PreferredAccessMethod
		candidate.LastScannedAt = cloneTimePointer(extension.LastScannedAt)
		candidate.Provenance = cloneScanVantageEvidence(extension.Provenance)
		if candidate.ScopeRevision == 0 {
			candidate.ScopeRevision = scope.Revision
		}
		state.Candidates[key] = candidate
		state.ScanCandidateExtensions[candidate.ID] = extension
		result = cloneCandidate(candidate)
		if update.AccessRequest != nil {
			request := *update.AccessRequest
			if request.CandidateID == "" {
				request.CandidateID = candidate.ID
			}
			if request.DeviceID == "" {
				request.DeviceID = candidate.ID
			}
			request, requestErr := upsertAccessRequestState(state, request, now)
			if requestErr != nil {
				return requestErr
			}
			requestResult = &request
		}
		return nil
	})
	return result, requestResult, err
}

func (s *Store) CandidateByScopeAddress(ctx context.Context, scopeID, rawAddress string) (Candidate, error) {
	address, err := normalizeAddress(rawAddress)
	if err != nil || scopeID == "" {
		return Candidate{}, ErrInvalid
	}
	var result Candidate
	err = s.read(ctx, func(state *State) error {
		candidate, ok := state.Candidates[candidateKey(scopeID, address)]
		if !ok {
			return ErrNotFound
		}
		result = cloneCandidate(candidate)
		return nil
	})
	return result, err
}

func mergeScanCandidateExtension(existing, incoming ScanCandidateExtension, candidateID string, now time.Time) ScanCandidateExtension {
	merged := existing
	merged.CandidateID = candidateID
	if incoming.CoverageState != "" {
		merged.CoverageState = incoming.CoverageState
	}
	if merged.CoverageState == "" {
		merged.CoverageState = "current"
	}
	merged.EntryPointIDs = appendUniqueStrings(merged.EntryPointIDs, incoming.EntryPointIDs...)
	if len(merged.EntryPointIDs) > 64 {
		merged.EntryPointIDs = merged.EntryPointIDs[:64]
	}
	if incoming.PreferredAccessMethod != "" {
		merged.PreferredAccessMethod = incoming.PreferredAccessMethod
	}
	if incoming.LastScannedAt != nil && (merged.LastScannedAt == nil || incoming.LastScannedAt.After(*merged.LastScannedAt)) {
		value := *incoming.LastScannedAt
		merged.LastScannedAt = &value
	}
	merged.Provenance = mergeScanVantageEvidence(merged.Provenance, incoming.Provenance)
	merged.UpdatedAt = now
	return merged
}

func mergeScanVantageEvidence(existing, incoming []ScanVantageEvidence) []ScanVantageEvidence {
	merged := cloneScanVantageEvidence(existing)
	for _, item := range incoming {
		found := false
		for index := range merged {
			if merged[index].ScannerKind == item.ScannerKind && merged[index].ScannerID == item.ScannerID {
				if item.LastObservedAt.After(merged[index].LastObservedAt) {
					merged[index] = item
				}
				found = true
				break
			}
		}
		if !found && len(merged) < 16 {
			merged = append(merged, item)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].ScannerKind == merged[j].ScannerKind {
			return merged[i].ScannerID < merged[j].ScannerID
		}
		return merged[i].ScannerKind < merged[j].ScannerKind
	})
	return merged
}

func cloneScanVantageEvidence(items []ScanVantageEvidence) []ScanVantageEvidence {
	return append([]ScanVantageEvidence(nil), items...)
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func appendUniqueStrings(existing []string, additions ...string) []string {
	result := append([]string(nil), existing...)
	seen := make(map[string]bool, len(result)+len(additions))
	for _, value := range result {
		seen[value] = true
	}
	for _, value := range additions {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (s *Store) ListEntryPointObservations(ctx context.Context, query EntryPointObservationQuery) (EntryPointObservationPage, error) {
	var result EntryPointObservationPage
	err := s.read(ctx, func(state *State) error {
		cursor, err := decodeScanCursor(query.Cursor)
		if err != nil {
			return err
		}
		limit := query.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 500 {
			limit = 500
		}
		items := make([]EntryPointObservation, 0, len(state.EntryPointObservations))
		for _, observation := range state.EntryPointObservations {
			if query.ScopeID != "" && observation.ScopeID != query.ScopeID || query.RunID != "" && observation.RunID != query.RunID || query.ScannerID != "" && observation.ScannerID != query.ScannerID || query.Address != "" && observation.Address != query.Address {
				continue
			}
			if !cursor.Time.IsZero() && !scanCursorAfter(observation.ObservedAt, observation.ID, cursor) {
				continue
			}
			items = append(items, cloneObservation(observation))
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].ObservedAt.Equal(items[j].ObservedAt) {
				return items[i].ID > items[j].ID
			}
			return items[i].ObservedAt.After(items[j].ObservedAt)
		})
		if len(items) > limit {
			last := items[limit-1]
			result.NextCursor = encodeScanCursor(scanCursor{Time: last.ObservedAt, ID: last.ID})
			items = items[:limit]
		}
		result.Items = items
		return nil
	})
	return result, err
}

func (s *Store) GetEntryPointObservation(ctx context.Context, id string) (EntryPointObservation, error) {
	var result EntryPointObservation
	err := s.read(ctx, func(state *State) error {
		item, ok := state.EntryPointObservations[id]
		if !ok {
			return ErrNotFound
		}
		result = cloneObservation(item)
		return nil
	})
	return result, err
}

func (s *Store) MarkScanObservationActionable(ctx context.Context, id string, actionable bool) error {
	if strings.TrimSpace(id) == "" {
		return ErrInvalid
	}
	return s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		observation, ok := state.EntryPointObservations[id]
		if !ok {
			return ErrNotFound
		}
		observation.Actionable = actionable
		state.EntryPointObservations[id] = observation
		return nil
	})
}
