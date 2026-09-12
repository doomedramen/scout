package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

const (
	DefaultServerVantageID  = "control-server"
	defaultCoordinatorLease = 30 * time.Second
	defaultCoordinatorPoll  = 5 * time.Second
	defaultFenceInterval    = 100 * time.Millisecond
)

// Coordinator materializes and executes the control-server vantage. Agent
// runs are deliberately handed off through desired state and are not dialed
// by this process.
type Coordinator struct {
	Store         *store.Store
	Policy        *policy.Engine
	Scanner       Scanner
	ServerID      string
	LeaseDuration time.Duration
	RunDeadline   time.Duration
	PollInterval  time.Duration
	FenceInterval time.Duration
	Now           func() time.Time
}

func NewCoordinator(repository *store.Store, engine *policy.Engine, scanner Scanner) *Coordinator {
	return &Coordinator{Store: repository, Policy: engine, Scanner: scanner, ServerID: DefaultServerVantageID, LeaseDuration: defaultCoordinatorLease, PollInterval: defaultCoordinatorPoll, FenceInterval: defaultFenceInterval}
}

func (c *Coordinator) clock() time.Time {
	if c != nil && c.Now != nil {
		return c.Now().UTC()
	}
	if c != nil && c.Store != nil {
		return c.Store.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Coordinator) serverID() string {
	if strings.TrimSpace(c.ServerID) != "" {
		return strings.TrimSpace(c.ServerID)
	}
	return DefaultServerVantageID
}

// ScanScheduleJitter returns stable, bounded jitter for one scope/vantage
// pair. It uses identity only, so restarts do not reshuffle the schedule.
func ScanScheduleJitter(scopeID, scannerKind, scannerID string, schedule time.Duration) time.Duration {
	if schedule <= 0 {
		return 0
	}
	maximum := schedule / 10
	if maximum > time.Minute {
		maximum = time.Minute
	}
	if maximum <= 0 {
		return 0
	}
	digest := sha256.Sum256([]byte(scopeID + "\x00" + scannerKind + "\x00" + scannerID))
	return time.Duration(binary.BigEndian.Uint64(digest[:8]) % uint64(maximum+1))
}

func NextScanScheduleAt(base time.Time, scopeID, scannerKind, scannerID string, schedule time.Duration) time.Time {
	return base.Add(schedule).Add(ScanScheduleJitter(scopeID, scannerKind, scannerID, schedule))
}

// ScheduleDue materializes at most one queued run for each enabled,
// server-enabled scope. The first run is immediate; later runs use stable
// identity-derived jitter after the configured interval.
func (c *Coordinator) ScheduleDue(ctx context.Context) ([]store.ScanRun, error) {
	if c == nil || c.Store == nil {
		return nil, store.ErrInvalid
	}
	scopes, err := c.Store.ListScopes(ctx)
	if err != nil {
		return nil, err
	}
	now := c.clock()
	created := []store.ScanRun{}
	for _, scope := range scopes {
		if !scope.Enabled {
			continue
		}
		scanPolicy, policyErr := c.Store.ScanPolicy(ctx, scope.ID)
		if policyErr != nil {
			return nil, policyErr
		}
		if !scanPolicy.Enabled || !scanPolicy.ServerEnabled {
			continue
		}
		vantageID := c.serverID()
		runs, listErr := c.Store.ListScanRuns(ctx, store.ScanRunQuery{ScopeID: scope.ID, ScannerID: vantageID, Limit: 500})
		if listErr != nil {
			return nil, listErr
		}
		var latest *store.ScanRun
		active := false
		for index := range runs.Items {
			run := runs.Items[index]
			if latest == nil {
				copy := run
				latest = &copy
			}
			if isActiveScanRun(run.State) {
				active = true
				break
			}
		}
		if active {
			continue
		}
		schedule := time.Duration(scanPolicy.ScheduleSeconds) * time.Second
		scheduledAt := now
		if latest != nil {
			scheduledAt = NextScanScheduleAt(latest.ScheduledAt, scope.ID, "server", vantageID, schedule)
		}
		if scheduledAt.After(now) {
			continue
		}
		run, materializeErr := c.materializeRun(ctx, scope, scanPolicy, vantageID, scheduledAt)
		if errors.Is(materializeErr, store.ErrConflict) {
			continue
		}
		if materializeErr != nil {
			return nil, materializeErr
		}
		created = append(created, run)
	}
	return created, nil
}

func isActiveScanRun(state string) bool {
	switch state {
	case store.ScanRunQueued, store.ScanRunLeased, store.ScanRunRunning, store.ScanRunUploading:
		return true
	default:
		return false
	}
}

func enabledEntryPoints(entries []store.ScanEntryPoint) []store.ScanEntryPoint {
	result := make([]store.ScanEntryPoint, 0, len(entries))
	for _, entry := range entries {
		if entry.Enabled {
			result = append(result, entry)
		}
	}
	return result
}

func boundedAttemptPlan(targetCount, entryPointCount int) (int, error) {
	if targetCount < 0 || entryPointCount < 0 {
		return 0, store.ErrInvalid
	}
	if entryPointCount != 0 && targetCount > int(^uint(0)>>1)/entryPointCount {
		return 0, store.ErrInvalid
	}
	planned := targetCount * entryPointCount
	if planned > maxProbeAttempts {
		planned = maxProbeAttempts
	}
	return planned, nil
}

func (c *Coordinator) materializeRun(ctx context.Context, scope store.Scope, scanPolicy store.ScanPolicy, scannerID string, scheduledAt time.Time) (store.ScanRun, error) {
	addresses, err := ExpandTargets(scope.Ranges, scope.Exclusions, scanPolicy.Limits.TargetBudget)
	if err != nil {
		return store.ScanRun{}, err
	}
	entryPoints := enabledEntryPoints(scanPolicy.EntryPoints)
	attempts, err := boundedAttemptPlan(len(addresses), len(entryPoints))
	if err != nil {
		return store.ScanRun{}, err
	}
	deadline := time.Duration(scanPolicy.Limits.RunDeadlineSeconds) * time.Second
	if deadline <= 0 {
		return store.ScanRun{}, store.ErrInvalid
	}
	return c.Store.CreateScanRun(ctx, store.ScanRun{
		ScopeID:             scope.ID,
		ScopeRevision:       scanPolicy.Revision,
		ScannerKind:         "server",
		ScannerID:           scannerID,
		Trigger:             "schedule",
		ScheduledAt:         scheduledAt,
		AssignmentExpiresAt: scheduledAt.Add(deadline),
		PolicySnapshot: store.ScanPolicySnapshot{
			Ranges:      append([]string(nil), scope.Ranges...),
			Exclusions:  append([]string(nil), scope.Exclusions...),
			EntryPoints: append([]store.ScanEntryPoint(nil), scanPolicy.EntryPoints...),
			Limits:      scanPolicy.Limits,
		},
		TargetsPlanned:  len(addresses),
		AttemptsPlanned: attempts,
		IdempotencyKey:  fmt.Sprintf("schedule/%s/%s/%d", scope.ID, scannerID, scheduledAt.UnixNano()),
	})
}

// LeaseDue claims queued runs and reclaims expired leases after a coordinator
// restart. A run whose assignment itself expired is left for the recovery
// path rather than being executed with stale authority.
func (c *Coordinator) LeaseDue(ctx context.Context) ([]store.ScanRun, error) {
	if c == nil || c.Store == nil {
		return nil, store.ErrInvalid
	}
	if _, err := c.ScheduleDue(ctx); err != nil {
		return nil, err
	}
	page, err := c.Store.ListScanRuns(ctx, store.ScanRunQuery{ScannerID: c.serverID(), Limit: 500})
	if err != nil {
		return nil, err
	}
	now := c.clock()
	leased := []store.ScanRun{}
	for _, run := range page.Items {
		if run.ScannerKind != "server" || run.ScannerID != c.serverID() || !isActiveScanRun(run.State) || run.ScheduledAt.After(now) || run.AssignmentExpiresAt.IsZero() || !now.Before(run.AssignmentExpiresAt) {
			continue
		}
		if run.LeaseExpiresAt != nil && now.Before(*run.LeaseExpiresAt) {
			continue
		}
		duration := c.LeaseDuration
		if duration <= 0 {
			duration = defaultCoordinatorLease
		}
		if remaining := run.AssignmentExpiresAt.Sub(now); remaining < duration {
			duration = remaining
		}
		if duration <= 0 {
			continue
		}
		claimed, claimErr := c.Store.LeaseScanRun(ctx, run.ID, c.serverID(), duration)
		if errors.Is(claimErr, store.ErrConflict) {
			continue
		}
		if claimErr != nil {
			return nil, claimErr
		}
		leased = append(leased, claimed)
	}
	return leased, nil
}

// RunOnce schedules, leases, and executes due server runs. Scanner failures
// become visible partial runs; only control/storage failures are returned.
func (c *Coordinator) RunOnce(ctx context.Context) ([]store.ScanRun, error) {
	if c == nil || c.Store == nil || c.Policy == nil || c.Scanner == nil {
		return nil, store.ErrInvalid
	}
	leased, err := c.LeaseDue(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]store.ScanRun, 0, len(leased))
	for _, run := range leased {
		if run.ScannerKind != "server" {
			continue
		}
		completed, executeErr := c.executeServerRun(ctx, run)
		if executeErr != nil {
			return nil, executeErr
		}
		result = append(result, completed)
	}
	return result, nil
}

func (c *Coordinator) runDeadline(run store.ScanRun) time.Duration {
	if c.RunDeadline > 0 {
		return c.RunDeadline
	}
	return time.Duration(run.PolicySnapshot.Limits.RunDeadlineSeconds) * time.Second
}

func (c *Coordinator) executeServerRun(parent context.Context, queued store.ScanRun) (store.ScanRun, error) {
	owner := c.serverID()
	run, err := c.Store.StartScanRun(parent, queued.ID, owner, queued.LeaseEpoch)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return queued, nil
		}
		return store.ScanRun{}, err
	}
	addresses, err := ExpandTargets(run.PolicySnapshot.Ranges, run.PolicySnapshot.Exclusions, run.PolicySnapshot.Limits.TargetBudget)
	if err != nil {
		return c.finishFailed(parent, run, "scanner_error")
	}
	ports := make([]int, 0, len(run.PolicySnapshot.EntryPoints))
	for _, entryPoint := range enabledEntryPoints(run.PolicySnapshot.EntryPoints) {
		if entryPoint.Transport != store.ScanTransportTCP {
			return c.finishFailed(parent, run, "scanner_error")
		}
		ports = append(ports, entryPoint.Port)
	}
	attemptBudget := run.PolicySnapshot.Limits.AttemptBudget
	if attemptBudget > run.AttemptsPlanned {
		attemptBudget = run.AttemptsPlanned
	}
	probePolicy := ProbePolicy{Ports: ports, Concurrency: run.PolicySnapshot.Limits.Concurrency, ProbesPerSecond: run.PolicySnapshot.Limits.ProbesPerSecond, TargetBudget: run.PolicySnapshot.Limits.TargetBudget, AttemptBudget: attemptBudget, Timeout: time.Duration(run.PolicySnapshot.Limits.TimeoutMilliseconds) * time.Millisecond}
	deadline := c.runDeadline(run)
	if deadline <= 0 {
		return c.finishFailed(parent, run, "scanner_error")
	}
	scanContext, cancel := context.WithTimeout(parent, deadline)
	watchContext, stopWatching := context.WithCancel(parent)
	fenceReasons := make(chan string, 1)
	go c.watchRunFence(watchContext, run, cancel, fenceReasons)
	results, scanErr := c.Scanner.Scan(scanContext, addresses, probePolicy)
	scanContextErr := scanContext.Err()
	cancel()
	stopWatching()
	fenceReason := ""
	select {
	case fenceReason = <-fenceReasons:
	default:
	}
	if fenceReason == "" {
		fenceReason = c.scanFenceReason(parent, run)
	}
	if fenceReason != "" {
		return c.finishPartial(parent, run, fenceReason)
	}
	partialReason := scanPartialReason(scanErr, scanContextErr, parent.Err())
	if partialReason == "" && len(results) != run.AttemptsPlanned {
		partialReason = "scanner_unavailable"
	}
	if partialReason == "" && hasSkippedResults(results) {
		partialReason = "attempt_budget"
	}
	if partialReason == "" && len(results) == run.AttemptsPlanned && run.AttemptsPlanned < len(addresses)*len(ports) {
		partialReason = "result_limit"
	}
	if _, err := c.Store.BeginScanUpload(parent, run.ID, owner, run.LeaseEpoch); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return c.currentRun(parent, run.ID, run)
		}
		return store.ScanRun{}, err
	}
	if err := c.uploadServerResults(parent, run, results, partialReason); err != nil {
		if reason := c.scanFenceReason(parent, run); reason != "" {
			return c.finishPartial(parent, run, reason)
		}
		return c.finishFailed(parent, run, "scanner_error")
	}
	return c.currentRun(parent, run.ID, run)
}

func (c *Coordinator) watchRunFence(ctx context.Context, run store.ScanRun, cancel context.CancelFunc, reasons chan<- string) {
	interval := c.FenceInterval
	if interval <= 0 {
		interval = defaultFenceInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reason := c.scanFenceReason(ctx, run)
			if reason == "" {
				continue
			}
			cancel()
			reasons <- reason
			return
		}
	}
}

func (c *Coordinator) scanFenceReason(ctx context.Context, run store.ScanRun) string {
	if c == nil || c.Store == nil {
		return "scanner_unavailable"
	}
	currentRun, err := c.Store.GetScanRun(ctx, run.ID)
	if err != nil {
		return ""
	}
	now := c.clock()
	if currentRun.LeaseExpiresAt == nil || !now.Before(*currentRun.LeaseExpiresAt) {
		return "scanner_unavailable"
	}
	if !run.AssignmentExpiresAt.IsZero() && !now.Before(run.AssignmentExpiresAt) {
		return "deadline"
	}
	scope, err := c.Store.GetScope(ctx, run.ScopeID)
	if err != nil {
		return ""
	}
	scanPolicy, err := c.Store.ScanPolicy(ctx, run.ScopeID)
	if err != nil {
		return ""
	}
	if !scope.Enabled || !scanPolicy.Enabled || scanPolicy.Revision != run.ScopeRevision {
		return "policy_changed"
	}
	if currentRun.CancellationRequested {
		return "cancelled"
	}
	workspace, err := c.Store.Workspace(ctx)
	if err != nil {
		return ""
	}
	if workspace.RecoveryMode || workspace.DiscoveryPaused {
		return "cancelled"
	}
	return ""
}

func hasSkippedResults(results []ProbeResult) bool {
	for _, result := range results {
		if result.Outcome == "skipped" {
			return true
		}
	}
	return false
}

func scanPartialReason(scanErr, scanContextErr, parentErr error) string {
	if errors.Is(parentErr, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(scanContextErr, context.DeadlineExceeded) || errors.Is(scanErr, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(scanContextErr, context.Canceled) || errors.Is(scanErr, context.Canceled) {
		return "cancelled"
	}
	if scanErr != nil {
		return "scanner_unavailable"
	}
	return ""
}

func (c *Coordinator) uploadServerResults(ctx context.Context, run store.ScanRun, results []ProbeResult, partialReason string) error {
	pageSize := run.PolicySnapshot.Limits.ResultPageSize
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 1000
	}
	if len(results) > run.AttemptsPlanned {
		return store.ErrBackpressure
	}
	started := run.ScheduledAt
	if run.StartedAt != nil {
		started = *run.StartedAt
	}
	if started.IsZero() {
		started = c.clock()
	}
	if len(results) == 0 {
		return c.uploadServerPage(ctx, run, 0, nil, run.AttemptsCompleted, started, true, partialReason)
	}
	completed := run.AttemptsCompleted
	for ordinal, offset := 0, 0; offset < len(results); ordinal, offset = ordinal+1, offset+pageSize {
		end := offset + pageSize
		if end > len(results) {
			end = len(results)
		}
		pagePartial := ""
		if end == len(results) {
			pagePartial = partialReason
		}
		if err := c.uploadServerPage(ctx, run, ordinal, results[offset:end], completed, started, end == len(results), pagePartial); err != nil {
			return err
		}
		completed += end - offset
	}
	return nil
}

func (c *Coordinator) uploadServerPage(ctx context.Context, run store.ScanRun, ordinal int, results []ProbeResult, completedBefore int, started time.Time, final bool, partialReason string) error {
	observedAt := c.clock()
	if observedAt.Before(started) {
		observedAt = started
	}
	converted := make([]ScanResult, 0, len(results))
	for _, result := range results {
		entryPoint, found := scanEntryPointByPort(run.PolicySnapshot.EntryPoints, result.Port)
		if !found {
			return store.ErrInvalid
		}
		latency := float64(result.Latency) / float64(time.Millisecond)
		converted = append(converted, ScanResult{Address: result.Address, EntryPointID: entryPoint.ID, Transport: entryPoint.Transport, Port: result.Port, Outcome: result.Outcome, ReasonCode: result.ReasonCode, LatencyMilliseconds: &latency, ObservedAt: observedAt})
	}
	page := ScanResultPage{ProtocolVersion: scanProtocolVersion, RunID: run.ID, LeaseEpoch: run.LeaseEpoch, ScopeRevision: run.ScopeRevision, PageOrdinal: ordinal, ObservedFrom: started, ObservedTo: observedAt, Final: final, Results: converted}
	if page.Final {
		page.Summary = &ScanResultSummary{TargetsPlanned: run.TargetsPlanned, AttemptsPlanned: run.AttemptsPlanned, AttemptsCompleted: completedBefore + len(results)}
		if partialReason != "" {
			page.Summary.PartialReason = &partialReason
		}
	}
	_, _, err := c.DiscoveryService().IngestScanResultPage(ctx, policy.ScanVantage{Kind: "server", ID: c.serverID()}, page)
	return err
}

func scanEntryPointByPort(entries []store.ScanEntryPoint, port int) (store.ScanEntryPoint, bool) {
	for _, entry := range entries {
		if entry.Enabled && entry.Transport == store.ScanTransportTCP && entry.Port == port {
			return entry, true
		}
	}
	return store.ScanEntryPoint{}, false
}

func (c *Coordinator) DiscoveryService() *Service {
	return &Service{Store: c.Store, Policy: c.Policy, Now: c.clock}
}

func (c *Coordinator) currentRun(ctx context.Context, runID string, fallback store.ScanRun) (store.ScanRun, error) {
	run, err := c.Store.GetScanRun(ctx, runID)
	if errors.Is(err, store.ErrNotFound) {
		return fallback, nil
	}
	return run, err
}

func (c *Coordinator) finishFailed(ctx context.Context, run store.ScanRun, errorCode string) (store.ScanRun, error) {
	if _, err := c.Store.FinalizeScanRun(ctx, run.ID, c.serverID(), run.LeaseEpoch, store.ScanRunPartial, "scanner_unavailable", errorCode); err != nil {
		return store.ScanRun{}, err
	}
	return c.currentRun(ctx, run.ID, run)
}

func (c *Coordinator) finishPartial(ctx context.Context, run store.ScanRun, reason string) (store.ScanRun, error) {
	if _, err := c.Store.FinalizeScanRun(ctx, run.ID, c.serverID(), run.LeaseEpoch, store.ScanRunPartial, reason, ""); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return c.currentRun(ctx, run.ID, run)
		}
		return store.ScanRun{}, err
	}
	return c.currentRun(ctx, run.ID, run)
}

// Start runs the coordinator independently from host telemetry. The server
// wires its lifetime to the HTTP process in the startup task.
func (c *Coordinator) Start(ctx context.Context) error {
	if c == nil {
		return store.ErrInvalid
	}
	interval := c.PollInterval
	if interval <= 0 {
		interval = defaultCoordinatorPoll
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	if _, err := c.RunOnce(ctx); err != nil && ctx.Err() != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := c.RunOnce(ctx); err != nil && ctx.Err() != nil {
				return err
			}
		}
	}
}

// Keep the compile-time contract explicit for future coordinator adapters.
var _ Scanner = TCPScanner{}
