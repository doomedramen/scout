package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"strings"
	"time"

	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

const (
	scanProtocolVersion = 1
	scanClockSkew       = 5 * time.Second
	maxScanResultPages  = 16384
)

type ScanResultPage struct {
	ProtocolVersion int                `json:"protocolVersion"`
	RunID           string             `json:"runId"`
	LeaseEpoch      int64              `json:"leaseEpoch"`
	ScopeRevision   int64              `json:"scopeRevision"`
	PageOrdinal     int                `json:"pageOrdinal"`
	ObservedFrom    time.Time          `json:"observedFrom"`
	ObservedTo      time.Time          `json:"observedTo"`
	Final           bool               `json:"final"`
	Results         []ScanResult       `json:"results"`
	Summary         *ScanResultSummary `json:"summary"`
	ContentHash     string             `json:"-"`
}

type ScanResult struct {
	Address             string    `json:"address"`
	EntryPointID        string    `json:"entryPointId"`
	Transport           string    `json:"transport"`
	Port                int       `json:"port"`
	Outcome             string    `json:"outcome"`
	ReasonCode          string    `json:"reasonCode,omitempty"`
	LatencyMilliseconds *float64  `json:"latencyMilliseconds,omitempty"`
	ObservedAt          time.Time `json:"observedAt"`
}

type ScanResultSummary struct {
	TargetsPlanned    int     `json:"targetsPlanned"`
	AttemptsPlanned   int     `json:"attemptsPlanned"`
	AttemptsCompleted int     `json:"attemptsCompleted"`
	PartialReason     *string `json:"partialReason"`
}

var safeScanReasonCodes = map[string]bool{
	"connection_refused":    true,
	"timeout":               true,
	"network_unreachable":   true,
	"policy_excluded":       true,
	"outside_scope":         true,
	"bound_target_budget":   true,
	"bound_attempt_budget":  true,
	"bound_deadline":        true,
	"cancelled":             true,
	"scanner_revoked":       true,
	"scanner_error":         true,
	"unsupported_transport": true,
	"invalid_address":       true,
	"source_unavailable":    true,
}

var safeScanPartialReasons = map[string]bool{
	"target_budget":       true,
	"attempt_budget":      true,
	"deadline":            true,
	"cancelled":           true,
	"policy_changed":      true,
	"scanner_unavailable": true,
	"upload_backpressure": true,
	"result_limit":        true,
	"spool_overflow":      true,
}

// IngestScanResultPage validates authenticated scanner output against the
// immutable run snapshot, then atomically stores evidence. It never creates a
// candidate, access request, credential grant, or enrollment job.
func (s *Service) IngestScanResultPage(ctx context.Context, scanner policy.ScanVantage, page ScanResultPage) (store.ScanResultReceipt, bool, error) {
	if s == nil || s.Store == nil || s.Policy == nil {
		return store.ScanResultReceipt{}, false, store.ErrInvalid
	}
	if page.ProtocolVersion != scanProtocolVersion || page.RunID == "" || page.LeaseEpoch < 1 || page.ScopeRevision < 1 || page.PageOrdinal < 0 || page.PageOrdinal >= maxScanResultPages || page.ObservedFrom.IsZero() || page.ObservedTo.IsZero() || page.ObservedTo.Before(page.ObservedFrom) {
		return store.ScanResultReceipt{}, false, store.ErrInvalid
	}
	run, err := s.Store.GetScanRun(ctx, page.RunID)
	if err != nil {
		return store.ScanResultReceipt{}, false, err
	}
	if scanner.Kind != run.ScannerKind || scanner.ID != run.ScannerID {
		return store.ScanResultReceipt{}, false, store.ErrForbidden
	}
	vantageDecision, err := s.Policy.ValidateScanVantage(ctx, run.ScopeID, scanner)
	if err != nil {
		return store.ScanResultReceipt{}, false, err
	}
	if !vantageDecision.Allowed {
		return store.ScanResultReceipt{}, false, scanVantageError(vantageDecision.Reason)
	}
	currentPolicy, err := s.Store.ScanPolicy(ctx, run.ScopeID)
	if err != nil {
		return store.ScanResultReceipt{}, false, err
	}
	if page.ScopeRevision != run.ScopeRevision || currentPolicy.Revision != run.ScopeRevision {
		return store.ScanResultReceipt{}, false, store.ErrConflict
	}
	contentHash := page.ContentHash
	if contentHash == "" {
		contentHash = scanResultPageHash(page)
	}
	if existing, receiptErr := s.Store.ScanResultReceipt(ctx, page.RunID, page.PageOrdinal); receiptErr == nil {
		if existing.ContentHash == contentHash {
			return existing, true, nil
		}
		return store.ScanResultReceipt{}, false, store.ErrConflict
	} else if receiptErr != store.ErrNotFound {
		return store.ScanResultReceipt{}, false, receiptErr
	}
	now := s.clock()
	if run.State != store.ScanRunRunning && run.State != store.ScanRunUploading || run.LeaseOwner != scanner.ID || run.LeaseEpoch != page.LeaseEpoch || run.LeaseExpiresAt == nil || !now.Before(*run.LeaseExpiresAt) {
		return store.ScanResultReceipt{}, false, store.ErrConflict
	}
	if run.AssignmentExpiresAt.IsZero() || !now.Before(run.AssignmentExpiresAt) {
		return store.ScanResultReceipt{}, false, store.ErrConflict
	}
	if err := validateScanObservationWindow(run, page, now); err != nil {
		return store.ScanResultReceipt{}, false, err
	}
	if err := validateScanSummary(run, page); err != nil {
		return store.ScanResultReceipt{}, false, err
	}
	observations, err := scanObservations(run, page, now)
	if err != nil {
		return store.ScanResultReceipt{}, false, err
	}
	receipt := store.ScanResultReceipt{RunID: page.RunID, PageOrdinal: page.PageOrdinal, ContentHash: contentHash, AcceptedAt: now, ResultCount: len(observations), IsFinal: page.Final}
	accepted, duplicate, err := s.Store.AcceptScanResultPage(ctx, receipt, observations)
	if err != nil || duplicate || !page.Final {
		return accepted, duplicate, err
	}
	terminalState := store.ScanRunCompleted
	partialReason := ""
	if page.Summary.PartialReason != nil {
		terminalState = store.ScanRunPartial
		partialReason = *page.Summary.PartialReason
	}
	finalized, err := s.Store.FinalizeScanRun(ctx, page.RunID, scanner.ID, page.LeaseEpoch, terminalState, partialReason, "")
	if err != nil {
		return store.ScanResultReceipt{}, false, err
	}
	_ = finalized
	return accepted, false, nil
}

func scanVantageError(reason string) error {
	switch reason {
	case "vantage_not_assigned", "vantage_unavailable", "vantage_kind_not_supported":
		return store.ErrForbidden
	default:
		return store.ErrConflict
	}
}

func validateScanObservationWindow(run store.ScanRun, page ScanResultPage, now time.Time) error {
	start := run.ScheduledAt
	if run.StartedAt != nil {
		start = *run.StartedAt
	}
	if start.IsZero() || run.AssignmentExpiresAt.IsZero() || page.ObservedFrom.Before(start.Add(-scanClockSkew)) || page.ObservedTo.After(run.AssignmentExpiresAt.Add(scanClockSkew)) || page.ObservedTo.After(now.Add(scanClockSkew)) {
		return store.ErrConflict
	}
	return nil
}

func validateScanSummary(run store.ScanRun, page ScanResultPage) error {
	if !page.Final {
		if page.Summary != nil {
			return store.ErrInvalid
		}
		return nil
	}
	if page.Summary == nil || page.Summary.TargetsPlanned != run.TargetsPlanned || page.Summary.AttemptsPlanned != run.AttemptsPlanned || page.Summary.AttemptsCompleted != run.AttemptsCompleted+len(page.Results) || page.Summary.AttemptsCompleted > run.AttemptsPlanned {
		return store.ErrInvalid
	}
	if page.Summary.PartialReason != nil && !safeScanPartialReasons[*page.Summary.PartialReason] {
		return store.ErrInvalid
	}
	if page.Summary.PartialReason == nil && page.Summary.AttemptsCompleted != page.Summary.AttemptsPlanned {
		return store.ErrInvalid
	}
	return nil
}

func scanObservations(run store.ScanRun, page ScanResultPage, now time.Time) ([]store.EntryPointObservation, error) {
	if len(page.Results) > run.AttemptsPlanned-run.AttemptsCompleted {
		return nil, store.ErrBackpressure
	}
	observations := make([]store.EntryPointObservation, 0, len(page.Results))
	seen := map[string]bool{}
	for _, result := range page.Results {
		address, err := netip.ParseAddr(strings.TrimSpace(result.Address))
		if err != nil {
			return nil, store.ErrInvalid
		}
		entryPoint, found := scanSnapshotEntryPoint(run.PolicySnapshot.EntryPoints, result.EntryPointID)
		if !found || !entryPoint.Enabled || result.Transport != entryPoint.Transport || result.Port != entryPoint.Port {
			return nil, store.ErrForbidden
		}
		if !addressInScanRanges(run.PolicySnapshot.Ranges, address) || excludedScanAddress(run.PolicySnapshot.Exclusions, address) {
			return nil, store.ErrForbidden
		}
		if !validScanOutcomeValue(result.Outcome) || result.ReasonCode != "" && !safeScanReasonCodes[result.ReasonCode] {
			return nil, store.ErrInvalid
		}
		if result.ObservedAt.IsZero() || result.ObservedAt.Before(page.ObservedFrom.Add(-scanClockSkew)) || result.ObservedAt.After(page.ObservedTo.Add(scanClockSkew)) {
			return nil, store.ErrConflict
		}
		if result.LatencyMilliseconds != nil && (math.IsNaN(*result.LatencyMilliseconds) || math.IsInf(*result.LatencyMilliseconds, 0) || *result.LatencyMilliseconds < 0 || *result.LatencyMilliseconds > float64(run.PolicySnapshot.Limits.TimeoutMilliseconds)+float64(scanClockSkew/time.Millisecond)) {
			return nil, store.ErrInvalid
		}
		key := fmt.Sprintf("%s/%s/%d/%s", address.String(), result.Transport, result.Port, result.EntryPointID)
		if seen[key] {
			return nil, store.ErrDuplicate
		}
		seen[key] = true
		observations = append(observations, store.EntryPointObservation{
			RunID:               run.ID,
			PageOrdinal:         page.PageOrdinal,
			ScopeID:             run.ScopeID,
			ScopeRevision:       run.ScopeRevision,
			ScannerKind:         run.ScannerKind,
			ScannerID:           run.ScannerID,
			Address:             address.String(),
			Transport:           result.Transport,
			Port:                result.Port,
			EntryPointID:        result.EntryPointID,
			Outcome:             result.Outcome,
			ReasonCode:          result.ReasonCode,
			LatencyMilliseconds: result.LatencyMilliseconds,
			ObservedAt:          result.ObservedAt,
			ReceivedAt:          now,
			ExpiresAt:           now.Add(30 * 24 * time.Hour),
			Actionable:          false,
		})
	}
	return observations, nil
}

func scanSnapshotEntryPoint(entries []store.ScanEntryPoint, id string) (store.ScanEntryPoint, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return store.ScanEntryPoint{}, false
}

func validScanOutcomeValue(value string) bool {
	switch value {
	case "open", "closed", "filtered", "unreachable", "skipped", "scanner_error":
		return true
	default:
		return false
	}
}

func addressInScanRanges(ranges []string, address netip.Addr) bool {
	for _, raw := range ranges {
		raw = strings.TrimSpace(raw)
		if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Contains(address) {
			return true
		}
		if literal, err := netip.ParseAddr(raw); err == nil && literal == address {
			return true
		}
	}
	return false
}

func excludedScanAddress(exclusions []string, address netip.Addr) bool {
	for _, raw := range exclusions {
		raw = strings.TrimSpace(raw)
		if literal, err := netip.ParseAddr(raw); err == nil && literal == address {
			return true
		}
		if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Contains(address) {
			return true
		}
	}
	return false
}

func scanResultPageHash(page ScanResultPage) string {
	page.ContentHash = ""
	data, err := json.Marshal(page)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
