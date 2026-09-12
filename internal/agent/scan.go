package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"time"

	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/store"
)

const (
	maxScanAssignmentRanges  = 128
	maxScanAssignmentEntries = 64
	maxScanAttempts          = 16384
	maxScanPageBytes         = 1 << 20
	maxScanPageResults       = 1000
)

// ScanAssignment is the credential-free immutable work handed to an agent by
// desired state. It intentionally contains no scanner identity; the
// authenticated agent transport supplies that identity.
type ScanAssignment struct {
	RunID               string                 `json:"runId"`
	LeaseEpoch          int64                  `json:"leaseEpoch"`
	ScopeID             string                 `json:"scopeId"`
	ScopeRevision       int64                  `json:"scopeRevision"`
	AssignmentExpiresAt time.Time              `json:"assignmentExpiresAt"`
	Ranges              []string               `json:"ranges"`
	Exclusions          []string               `json:"exclusions"`
	EntryPoints         []store.ScanEntryPoint `json:"entryPoints"`
	Limits              store.ScanLimits       `json:"limits"`
}

var safeAgentScanReasonCodes = map[string]bool{
	"connection_refused":   true,
	"timeout":              true,
	"network_unreachable":  true,
	"bound_attempt_budget": true,
	"bound_deadline":       true,
	"cancelled":            true,
	"scanner_error":        true,
	"source_unavailable":   true,
}

var safeAgentScanPartialReasons = map[string]bool{
	"attempt_budget":      true,
	"deadline":            true,
	"cancelled":           true,
	"scanner_unavailable": true,
	"result_limit":        true,
	"spool_overflow":      true,
}

// ValidateScanAssignment rejects work that could escape the bounded agent
// scanner. The assignment remains valid only strictly before its expiry.
func ValidateScanAssignment(assignment ScanAssignment, now time.Time) error {
	if assignment.RunID == "" || assignment.LeaseEpoch < 1 || assignment.ScopeID == "" || assignment.ScopeRevision < 1 || assignment.AssignmentExpiresAt.IsZero() || !assignment.AssignmentExpiresAt.After(now) {
		return errors.New("scan assignment is expired or incomplete")
	}
	if len(assignment.Ranges) == 0 || len(assignment.Ranges) > maxScanAssignmentRanges || len(assignment.Exclusions) > maxScanAssignmentRanges || len(assignment.EntryPoints) == 0 || len(assignment.EntryPoints) > maxScanAssignmentEntries {
		return errors.New("scan assignment bounds are invalid")
	}
	if err := validateScanAddresses(assignment.Ranges, assignment.Exclusions); err != nil {
		return err
	}
	seenIDs := map[string]bool{}
	seenEndpoints := map[string]bool{}
	enabled := 0
	for _, entryPoint := range assignment.EntryPoints {
		id := strings.TrimSpace(entryPoint.ID)
		name := strings.TrimSpace(entryPoint.Name)
		if id == "" || len(id) > 64 || name == "" || len(name) > 64 || seenIDs[id] || entryPoint.Transport != store.ScanTransportTCP || entryPoint.Port < 1 || entryPoint.Port > 65535 || entryPoint.AccessMethod != "" && entryPoint.AccessMethod != store.ScanAccessSSH {
			return errors.New("scan entry point is invalid")
		}
		endpoint := fmt.Sprintf("%s/%d", entryPoint.Transport, entryPoint.Port)
		if seenEndpoints[endpoint] {
			return errors.New("scan entry points overlap")
		}
		seenIDs[id] = true
		seenEndpoints[endpoint] = true
		if entryPoint.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return errors.New("scan assignment has no enabled entry point")
	}
	limits := assignment.Limits
	if limits.ProbesPerSecond < 1 || limits.ProbesPerSecond > 1000 || limits.Concurrency < 1 || limits.Concurrency > 16 || limits.TargetBudget < 1 || limits.TargetBudget > 4096 || limits.AttemptBudget < 1 || limits.AttemptBudget > maxScanAttempts || limits.TimeoutMilliseconds < 100 || limits.TimeoutMilliseconds > 10000 || limits.RunDeadlineSeconds < 30 || limits.RunDeadlineSeconds > 900 || limits.ResultPageSize < 1 || limits.ResultPageSize > maxScanPageResults {
		return errors.New("scan assignment limits are invalid")
	}
	if enabled > 0 && limits.TargetBudget > int(^uint(0)>>1)/enabled || limits.AttemptBudget > limits.TargetBudget*enabled {
		return errors.New("scan assignment attempt bound is invalid")
	}
	return nil
}

func validateScanAddresses(ranges, exclusions []string) error {
	for _, raw := range ranges {
		if _, err := netip.ParseAddr(strings.TrimSpace(raw)); err != nil {
			prefix, prefixErr := netip.ParsePrefix(strings.TrimSpace(raw))
			if prefixErr != nil || prefix.Addr().BitLen() == 128 && prefix.Bits() < 116 {
				return errors.New("scan range is invalid or unbounded")
			}
		}
	}
	for _, raw := range exclusions {
		if _, err := netip.ParseAddr(strings.TrimSpace(raw)); err != nil {
			if _, prefixErr := netip.ParsePrefix(strings.TrimSpace(raw)); prefixErr != nil {
				return errors.New("scan exclusion is invalid")
			}
		}
	}
	return nil
}

// BuildScanResultPages converts scanner outcomes into bounded, deterministic
// agent pages. It is pure so page sizing and final-summary rules are testable
// without an HTTP server or an enrolled device.
func BuildScanResultPages(assignment ScanAssignment, agentID string, results []discovery.ProbeResult, observedAt time.Time, partialReason string) ([]discovery.ScanResultPage, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, errors.New("agent identity is required")
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if err := ValidateScanAssignment(assignment, observedAt); err != nil {
		return nil, err
	}
	if partialReason != "" && !safeAgentScanPartialReasons[partialReason] {
		return nil, errors.New("scan partial reason is invalid")
	}
	addresses, err := discovery.ExpandTargets(assignment.Ranges, assignment.Exclusions, assignment.Limits.TargetBudget)
	if err != nil {
		return nil, err
	}
	entryPoints := enabledAssignmentEntryPoints(assignment.EntryPoints)
	planCount, err := scanPlanCount(len(addresses), len(entryPoints))
	if err != nil {
		return nil, err
	}
	if partialReason == "" && len(results) != planCount {
		return nil, errors.New("complete scan result count does not match bounded plan")
	}
	if len(results) > planCount {
		return nil, errors.New("scan result count exceeds bounded plan")
	}
	seen := map[string]bool{}
	allowedAddresses := map[string]bool{}
	for _, address := range addresses {
		allowedAddresses[address] = true
	}
	converted := make([]discovery.ScanResult, 0, len(results))
	for _, result := range results {
		address, parseErr := netip.ParseAddr(strings.TrimSpace(result.Address))
		if parseErr != nil || !allowedAddresses[address.String()] {
			return nil, errors.New("scan result address is outside assignment")
		}
		entryPoint, found := assignmentEntryPointByPort(entryPoints, result.Port)
		if !found || result.Outcome == "" || !validAgentScanOutcome(result.Outcome) || result.ReasonCode != "" && !safeAgentScanReasonCodes[result.ReasonCode] || result.Latency < 0 || result.Latency > time.Duration(assignment.Limits.TimeoutMilliseconds)*time.Millisecond+5*time.Second {
			return nil, errors.New("scan result is invalid")
		}
		key := fmt.Sprintf("%s/%d", address.String(), result.Port)
		if seen[key] {
			return nil, errors.New("scan result is duplicated")
		}
		seen[key] = true
		latency := float64(result.Latency) / float64(time.Millisecond)
		converted = append(converted, discovery.ScanResult{Address: address.String(), EntryPointID: entryPoint.ID, Transport: entryPoint.Transport, Port: entryPoint.Port, Outcome: result.Outcome, ReasonCode: result.ReasonCode, LatencyMilliseconds: &latency, ObservedAt: observedAt})
	}
	pageSize := assignment.Limits.ResultPageSize
	if pageSize <= 0 || pageSize > maxScanPageResults {
		return nil, errors.New("scan page size is invalid")
	}
	pages := make([]discovery.ScanResultPage, 0, (len(converted)+pageSize-1)/pageSize)
	if len(converted) == 0 {
		pages = append(pages, agentScanPage(assignment, 0, nil, 0, 0, len(addresses), planCount, observedAt, true, partialReason))
	} else {
		completed := 0
		for ordinal, offset := 0, 0; offset < len(converted); ordinal, offset = ordinal+1, offset+pageSize {
			end := offset + pageSize
			if end > len(converted) {
				end = len(converted)
			}
			final := end == len(converted)
			pagePartial := ""
			if final {
				pagePartial = partialReason
			}
			page := agentScanPage(assignment, ordinal, converted[offset:end], completed, end-offset, len(addresses), planCount, observedAt, final, pagePartial)
			encoded, marshalErr := json.Marshal(page)
			if marshalErr != nil || len(encoded) > maxScanPageBytes {
				return nil, errors.New("scan result page exceeds size limit")
			}
			pages = append(pages, page)
			completed += end - offset
		}
	}
	return pages, nil
}

func enabledAssignmentEntryPoints(entries []store.ScanEntryPoint) []store.ScanEntryPoint {
	result := make([]store.ScanEntryPoint, 0, len(entries))
	for _, entry := range entries {
		if entry.Enabled {
			result = append(result, entry)
		}
	}
	return result
}

func scanPlanCount(targets, entryPoints int) (int, error) {
	if targets < 0 || entryPoints < 0 || entryPoints != 0 && targets > int(^uint(0)>>1)/entryPoints {
		return 0, errors.New("scan plan is invalid")
	}
	plan := targets * entryPoints
	if plan > maxScanAttempts {
		plan = maxScanAttempts
	}
	return plan, nil
}

func assignmentEntryPointByPort(entries []store.ScanEntryPoint, port int) (store.ScanEntryPoint, bool) {
	for _, entry := range entries {
		if entry.Port == port {
			return entry, true
		}
	}
	return store.ScanEntryPoint{}, false
}

func validAgentScanOutcome(outcome string) bool {
	switch outcome {
	case "open", "closed", "filtered", "unreachable", "skipped", "scanner_error":
		return true
	default:
		return false
	}
}

func agentScanPage(assignment ScanAssignment, ordinal int, results []discovery.ScanResult, completedBefore, resultCount, targetsPlanned, attemptsPlanned int, observedAt time.Time, final bool, partialReason string) discovery.ScanResultPage {
	page := discovery.ScanResultPage{ProtocolVersion: 1, RunID: assignment.RunID, LeaseEpoch: assignment.LeaseEpoch, ScopeRevision: assignment.ScopeRevision, PageOrdinal: ordinal, ObservedFrom: observedAt, ObservedTo: observedAt, Final: final, Results: results}
	if final {
		page.Summary = &discovery.ScanResultSummary{TargetsPlanned: targetsPlanned, AttemptsPlanned: attemptsPlanned, AttemptsCompleted: completedBefore + resultCount}
		if partialReason != "" {
			page.Summary.PartialReason = &partialReason
		}
	}
	return page
}

func (r *Runtime) startScan(ctx context.Context, assignment ScanAssignment) bool {
	if !r.scanMu.TryLock() {
		r.scanStateMu.Lock()
		if r.scanCancel != nil && (r.scanRunID != assignment.RunID || r.scanEpoch != assignment.LeaseEpoch || r.scanRevision != assignment.ScopeRevision) {
			r.scanCancel()
		}
		r.scanStateMu.Unlock()
		return false
	}
	scanContext, cancel := context.WithCancel(ctx)
	r.scanStateMu.Lock()
	r.scanCancel = cancel
	r.scanRunID = assignment.RunID
	r.scanEpoch = assignment.LeaseEpoch
	r.scanRevision = assignment.ScopeRevision
	r.scanStateMu.Unlock()
	go func() {
		defer cancel()
		defer func() {
			r.scanStateMu.Lock()
			if r.scanRunID == assignment.RunID && r.scanEpoch == assignment.LeaseEpoch && r.scanRevision == assignment.ScopeRevision {
				r.scanCancel = nil
				r.scanRunID = ""
				r.scanEpoch = 0
				r.scanRevision = 0
			}
			r.scanStateMu.Unlock()
		}()
		defer r.scanMu.Unlock()
		if r.scanExecutor != nil {
			_ = r.scanExecutor(scanContext, assignment)
			return
		}
		_ = r.executeScan(scanContext, assignment)
	}()
	return true
}

func (r *Runtime) cancelScan() {
	r.scanStateMu.Lock()
	defer r.scanStateMu.Unlock()
	if r.scanCancel != nil {
		r.scanCancel()
	}
}

func (r *Runtime) syncScan(ctx context.Context) error {
	if err := r.flushScanSpool(ctx); err != nil {
		return err
	}
	data, err := r.getResponse(ctx, "/api/v1/agent/v1/desired-state", r.identity.AgentToken)
	if err != nil {
		return err
	}
	var desired struct {
		ScanAssignment *ScanAssignment `json:"scanAssignment"`
	}
	if err := json.Unmarshal(data, &desired); err != nil {
		return err
	}
	if desired.ScanAssignment != nil {
		r.startScan(ctx, *desired.ScanAssignment)
	} else {
		r.cancelScan()
	}
	return nil
}

func (r *Runtime) executeScan(ctx context.Context, assignment ScanAssignment) error {
	now := time.Now().UTC()
	if err := ValidateScanAssignment(assignment, now); err != nil {
		return err
	}
	if err := r.flushScanSpool(ctx); err != nil {
		return err
	}
	addresses, err := discovery.ExpandTargets(assignment.Ranges, assignment.Exclusions, assignment.Limits.TargetBudget)
	if err != nil {
		return err
	}
	entryPoints := enabledAssignmentEntryPoints(assignment.EntryPoints)
	planCount, err := scanPlanCount(len(addresses), len(entryPoints))
	if err != nil {
		return err
	}
	attemptBudget := assignment.Limits.AttemptBudget
	if attemptBudget > planCount {
		attemptBudget = planCount
	}
	probePolicy := discovery.ProbePolicy{Ports: assignmentPorts(entryPoints), Concurrency: assignment.Limits.Concurrency, ProbesPerSecond: assignment.Limits.ProbesPerSecond, TargetBudget: assignment.Limits.TargetBudget, AttemptBudget: attemptBudget, Timeout: time.Duration(assignment.Limits.TimeoutMilliseconds) * time.Millisecond}
	deadline := assignment.AssignmentExpiresAt
	if runDeadline := now.Add(time.Duration(assignment.Limits.RunDeadlineSeconds) * time.Second); runDeadline.Before(deadline) {
		deadline = runDeadline
	}
	scanContext, cancel := context.WithDeadline(ctx, deadline)
	results, scanErr := discovery.TCPScanner{}.Scan(scanContext, addresses, probePolicy)
	contextErr := scanContext.Err()
	cancel()
	partialReason := agentScanPartialReason(scanErr, contextErr, ctx.Err())
	if partialReason == "" && len(results) != planCount {
		partialReason = "scanner_unavailable"
	}
	if partialReason == "" && hasAgentSkippedResults(results) {
		partialReason = "attempt_budget"
	}
	if partialReason == "" && planCount < len(addresses)*len(entryPoints) {
		partialReason = "result_limit"
	}
	pages, err := BuildScanResultPages(assignment, r.identity.AgentID, results, time.Now().UTC(), partialReason)
	if err != nil {
		return err
	}
	return r.postScanPages(ctx, pages)
}

func assignmentPorts(entries []store.ScanEntryPoint) []int {
	ports := make([]int, 0, len(entries))
	for _, entry := range entries {
		ports = append(ports, entry.Port)
	}
	return ports
}

func agentScanPartialReason(scanErr, contextErr, parentErr error) string {
	if errors.Is(parentErr, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(contextErr, context.DeadlineExceeded) || errors.Is(scanErr, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(contextErr, context.Canceled) || errors.Is(scanErr, context.Canceled) {
		return "cancelled"
	}
	if scanErr != nil {
		return "scanner_unavailable"
	}
	return ""
}

func hasAgentSkippedResults(results []discovery.ProbeResult) bool {
	for _, result := range results {
		if result.Outcome == "skipped" {
			return true
		}
	}
	return false
}

func (r *Runtime) postScanPages(ctx context.Context, pages []discovery.ScanResultPage) error {
	if r.scanSpool == nil {
		r.scanSpool = NewSpool(filepath.Join(r.Config.DataDir, "scan-spool"), 16<<20, time.Hour)
	}
	for index, page := range pages {
		data, err := json.Marshal(page)
		if err != nil {
			return err
		}
		if len(data) > maxScanPageBytes {
			return errors.New("scan result page exceeds size limit")
		}
		if _, err := r.postResponse(ctx, "/api/v1/agent/v1/scan-results", data, r.identity.AgentToken); err != nil {
			for _, pending := range pages[index:] {
				pendingData, marshalErr := json.Marshal(pending)
				if marshalErr != nil {
					return marshalErr
				}
				if spoolErr := r.scanSpool.Add(pendingData); spoolErr != nil {
					return errors.Join(err, spoolErr)
				}
			}
			return err
		}
	}
	return nil
}

func (r *Runtime) flushScanSpool(ctx context.Context) error {
	if r.scanSpool == nil {
		return nil
	}
	items, err := r.scanSpool.Items()
	if err != nil {
		return err
	}
	for _, item := range items {
		if _, err := r.postResponse(ctx, "/api/v1/agent/v1/scan-results", item.Data, r.identity.AgentToken); err != nil {
			return err
		}
		if err := r.scanSpool.Remove(item.Path); err != nil {
			return err
		}
	}
	return nil
}
