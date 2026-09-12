package policy

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"scout.local/scout/internal/store"
)

const (
	defaultScanScheduleSeconds = 300
	defaultScanProbeRate       = 10
	defaultScanConcurrency     = 16
	defaultScanTargetBudget    = 256
	defaultScanTimeoutMillis   = 2000
	defaultScanDeadlineSeconds = 600
	defaultScanResultPageSize  = 1000
	maxScanEntryPoints         = 64
	maxScanAgents              = 16
	maxScanScheduleSeconds     = 86400
	maxScanAttemptBudget       = 16384
)

var scanPolicyEntryPointIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

type ScanVantage struct {
	Kind     string
	ID       string
	DeviceID string
}

type ScanPreProbeRequest struct {
	ScopeID        string
	PolicyRevision int64
	Scanner        ScanVantage
	Address        string
	EntryPointID   string
}

type ScanPreProbeDecision struct {
	Allowed        bool
	Reason         string
	ScopeRevision  int64
	PolicyRevision int64
}

// NormalizeScanPolicy fills safe defaults and validates the complete bounded
// scan policy before a controller persists or snapshots it.
func NormalizeScanPolicy(policy store.ScanPolicy, scope store.Scope) (store.ScanPolicy, error) {
	if scope.ID == "" || policy.ScopeID != "" && policy.ScopeID != scope.ID {
		return store.ScanPolicy{}, fmt.Errorf("%w: scope mismatch", store.ErrInvalid)
	}
	if err := ValidateRanges(scope.Ranges); err != nil {
		return store.ScanPolicy{}, err
	}
	if err := ValidateExclusions(scope.Exclusions); err != nil {
		return store.ScanPolicy{}, err
	}

	policy.ScopeID = scope.ID
	if policy.Revision == 0 {
		policy.Revision = 1
	}
	if policy.ScheduleSeconds == 0 {
		policy.ScheduleSeconds = defaultScanScheduleSeconds
	}
	if len(policy.EntryPoints) == 0 {
		policy.EntryPoints = defaultScanEntryPoints()
	}
	if policy.Limits.ProbesPerSecond == 0 {
		policy.Limits.ProbesPerSecond = defaultScanProbeRate
	}
	if policy.Limits.Concurrency == 0 {
		policy.Limits.Concurrency = defaultScanConcurrency
	}
	if policy.Limits.TargetBudget == 0 {
		policy.Limits.TargetBudget = defaultScanTargetBudget
	}
	if policy.Limits.TimeoutMilliseconds == 0 {
		policy.Limits.TimeoutMilliseconds = defaultScanTimeoutMillis
	}
	if policy.Limits.RunDeadlineSeconds == 0 {
		policy.Limits.RunDeadlineSeconds = defaultScanDeadlineSeconds
	}
	if policy.Limits.ResultPageSize == 0 {
		policy.Limits.ResultPageSize = defaultScanResultPageSize
	}
	if err := validateScanEntryPoints(policy.EntryPoints); err != nil {
		return store.ScanPolicy{}, err
	}
	if err := validateScanAgents(policy.AgentIDs); err != nil {
		return store.ScanPolicy{}, err
	}
	if policy.ScheduleSeconds < 60 || policy.ScheduleSeconds > maxScanScheduleSeconds {
		return store.ScanPolicy{}, fmt.Errorf("%w: schedule", store.ErrInvalid)
	}
	limits := policy.Limits
	if limits.ProbesPerSecond < 1 || limits.ProbesPerSecond > 1000 || limits.Concurrency < 1 || limits.Concurrency > 16 || limits.TargetBudget < 1 || limits.TargetBudget > 4096 || limits.AttemptBudget < 0 || limits.AttemptBudget > maxScanAttemptBudget || limits.TimeoutMilliseconds < 100 || limits.TimeoutMilliseconds > 10000 || limits.RunDeadlineSeconds < 30 || limits.RunDeadlineSeconds > 900 || limits.ResultPageSize < 1 || limits.ResultPageSize > 1000 {
		return store.ScanPolicy{}, fmt.Errorf("%w: scan limits", store.ErrInvalid)
	}
	if err := validateFiniteIPv6Ranges(scope.Ranges, limits.TargetBudget); err != nil {
		return store.ScanPolicy{}, err
	}
	enabledEntryPoints := countEnabledEntryPoints(policy.EntryPoints)
	if enabledEntryPoints == 0 {
		return store.ScanPolicy{}, fmt.Errorf("%w: no enabled entry point", store.ErrInvalid)
	}
	maxAttempts := limits.TargetBudget * enabledEntryPoints
	if limits.AttemptBudget == 0 {
		limits.AttemptBudget = maxAttempts
		if limits.AttemptBudget > maxScanAttemptBudget {
			limits.AttemptBudget = maxScanAttemptBudget
		}
	}
	if limits.AttemptBudget > maxAttempts {
		return store.ScanPolicy{}, fmt.Errorf("%w: attempt budget exceeds target and entry points", store.ErrInvalid)
	}
	policy.Limits = limits
	policy.AgentIDs = append([]string(nil), policy.AgentIDs...)
	policy.EntryPoints = append([]store.ScanEntryPoint(nil), policy.EntryPoints...)
	return policy, nil
}

// ValidateScanVantage validates identity and explicit assignment. Server
// opt-in is checked by PreProbe because this method only validates identity.
func (e *Engine) ValidateScanVantage(ctx context.Context, scopeID string, vantage ScanVantage) (Decision, error) {
	if e == nil || e.Store == nil || scopeID == "" || strings.TrimSpace(vantage.ID) == "" {
		return Decision{}, store.ErrInvalid
	}
	scope, err := e.Store.GetScope(ctx, scopeID)
	if err != nil {
		return Decision{}, err
	}
	if !scope.Enabled {
		return Decision{Reason: "scope_disabled", ScopeRevision: scope.Revision}, nil
	}
	policy, err := e.Store.ScanPolicy(ctx, scopeID)
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{ScopeRevision: scope.Revision}
	now := e.Store.Now()
	switch vantage.Kind {
	case "server":
		decision.Allowed = true
		decision.Reason = "assigned"
		return decision, nil
	case "agent":
		if !containsString(policy.AgentIDs, vantage.ID) {
			decision.Reason = "vantage_not_assigned"
			return decision, nil
		}
		agent, agentErr := e.Store.Agent(ctx, vantage.ID)
		if agentErr != nil {
			if agentErr == store.ErrNotFound {
				decision.Reason = "vantage_unavailable"
				return decision, nil
			}
			return Decision{}, agentErr
		}
		if agent.RevokedAt != nil || !agent.ExpiresAt.IsZero() && !agent.ExpiresAt.After(now) {
			decision.Reason = "vantage_unavailable"
			return decision, nil
		}
		if vantage.DeviceID != "" && vantage.DeviceID != agent.DeviceID {
			decision.Reason = "vantage_unavailable"
			return decision, nil
		}
		device, deviceErr := e.Store.GetDevice(ctx, agent.DeviceID)
		if deviceErr != nil {
			if deviceErr == store.ErrNotFound {
				decision.Reason = "vantage_unavailable"
				return decision, nil
			}
			return Decision{}, deviceErr
		}
		if device.Excluded || device.DecommissionedAt != nil || device.Lifecycle == "decommissioned" || device.Availability == store.AvailabilityRevoked {
			decision.Reason = "vantage_unavailable"
			return decision, nil
		}
		decision.Allowed = true
		decision.Reason = "assigned"
		return decision, nil
	default:
		decision.Reason = "vantage_kind_not_supported"
		return decision, nil
	}
}

// PreProbe applies current scope, scan-policy, assignment, entry-point, and
// pause checks immediately before a network probe starts.
func (e *Engine) PreProbe(ctx context.Context, request ScanPreProbeRequest) (ScanPreProbeDecision, error) {
	if e == nil || e.Store == nil || request.ScopeID == "" {
		return ScanPreProbeDecision{}, store.ErrInvalid
	}
	scope, err := e.Store.GetScope(ctx, request.ScopeID)
	if err != nil {
		return ScanPreProbeDecision{}, err
	}
	policy, err := e.Store.ScanPolicy(ctx, request.ScopeID)
	if err != nil {
		return ScanPreProbeDecision{}, err
	}
	decision := ScanPreProbeDecision{ScopeRevision: scope.Revision, PolicyRevision: policy.Revision}
	if request.PolicyRevision != policy.Revision {
		decision.Reason = "policy_revision_changed"
		return decision, nil
	}
	if !scope.Enabled {
		decision.Reason = "scope_disabled"
		return decision, nil
	}
	if !policy.Enabled {
		decision.Reason = "scan_policy_disabled"
		return decision, nil
	}
	vantage, vantageErr := e.ValidateScanVantage(ctx, request.ScopeID, request.Scanner)
	if vantageErr != nil {
		return ScanPreProbeDecision{}, vantageErr
	}
	if !vantage.Allowed {
		decision.Reason = vantage.Reason
		return decision, nil
	}
	address, addressErr := netip.ParseAddr(strings.TrimSpace(request.Address))
	if addressErr != nil {
		decision.Reason = "destination_must_be_literal_address"
		return decision, nil
	}
	entryPoint, found := findScanEntryPoint(policy.EntryPoints, request.EntryPointID)
	if !found || !entryPoint.Enabled {
		decision.Reason = "entry_point_not_allowed"
		return decision, nil
	}
	if !methodAllowed(scope.AllowedMethods, entryPoint.Transport) || !portAllowed(scope.Ports, entryPoint.Port) {
		decision.Reason = "entry_point_not_allowed"
		return decision, nil
	}
	if excluded(scope.Exclusions, address.String(), address) {
		decision.Reason = "excluded"
		return decision, nil
	}
	if !addressInRanges(scope.Ranges, address) {
		decision.Reason = "outside_scope"
		return decision, nil
	}
	workspace, workspaceErr := e.Store.Workspace(ctx)
	if workspaceErr != nil {
		return ScanPreProbeDecision{}, workspaceErr
	}
	if workspace.RecoveryMode || workspace.DiscoveryPaused {
		decision.Reason = "discovery_paused"
		return decision, nil
	}
	if request.Scanner.Kind == "server" && !policy.ServerEnabled {
		decision.Reason = "server_vantage_disabled"
		return decision, nil
	}
	decision.Allowed = true
	decision.Reason = "authorized"
	return decision, nil
}

func defaultScanEntryPoints() []store.ScanEntryPoint {
	return []store.ScanEntryPoint{{
		ID:           "ssh-default",
		Name:         "SSH",
		Transport:    store.ScanTransportTCP,
		Port:         22,
		AccessMethod: store.ScanAccessSSH,
		Enabled:      true,
	}}
}

func validateScanEntryPoints(entries []store.ScanEntryPoint) error {
	if len(entries) == 0 || len(entries) > maxScanEntryPoints {
		return fmt.Errorf("%w: entry point count", store.ErrInvalid)
	}
	seenIDs := map[string]bool{}
	seenEndpoints := map[string]bool{}
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" || len(entry.ID) > 64 || !scanPolicyEntryPointIDPattern.MatchString(entry.ID) || seenIDs[entry.ID] || strings.TrimSpace(entry.Name) == "" || len(entry.Name) > 64 || entry.Transport != store.ScanTransportTCP || entry.Port < 1 || entry.Port > 65535 || entry.AccessMethod != "" && entry.AccessMethod != store.ScanAccessSSH {
			return fmt.Errorf("%w: entry point", store.ErrInvalid)
		}
		endpoint := fmt.Sprintf("%s/%d", entry.Transport, entry.Port)
		if seenEndpoints[endpoint] {
			return fmt.Errorf("%w: duplicate entry point endpoint", store.ErrInvalid)
		}
		seenIDs[entry.ID] = true
		seenEndpoints[endpoint] = true
	}
	return nil
}

func validateScanAgents(agentIDs []string) error {
	if len(agentIDs) > maxScanAgents {
		return fmt.Errorf("%w: agent count", store.ErrInvalid)
	}
	seen := map[string]bool{}
	for _, id := range agentIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			return fmt.Errorf("%w: agent id", store.ErrInvalid)
		}
		seen[id] = true
	}
	return nil
}

func validateFiniteIPv6Ranges(ranges []string, targetBudget int) error {
	for _, raw := range ranges {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		ones := prefix.Bits()
		bits := prefix.Addr().BitLen()
		if bits != 128 {
			continue
		}
		hostBits := bits - ones
		if hostBits > 12 || 1<<hostBits > targetBudget {
			return fmt.Errorf("%w: IPv6 range exceeds target budget", store.ErrInvalid)
		}
	}
	return nil
}

func countEnabledEntryPoints(entries []store.ScanEntryPoint) int {
	count := 0
	for _, entry := range entries {
		if entry.Enabled {
			count++
		}
	}
	return count
}

func findScanEntryPoint(entries []store.ScanEntryPoint, id string) (store.ScanEntryPoint, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return store.ScanEntryPoint{}, false
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func addressInRanges(ranges []string, address netip.Addr) bool {
	for _, raw := range ranges {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(raw)); err == nil && prefix.Contains(address) {
			return true
		}
		if literal, err := netip.ParseAddr(strings.TrimSpace(raw)); err == nil && literal == address {
			return true
		}
	}
	return false
}
