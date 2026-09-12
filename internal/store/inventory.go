package store

import (
	"context"
	"net/netip"
	"sort"
	"strings"
	"time"
)

func (s *Store) CreateSite(ctx context.Context, site Site) (Site, error) {
	if strings.TrimSpace(site.Name) == "" || len(site.Name) > 128 {
		return Site{}, ErrInvalid
	}
	var result Site
	err := s.mutate(ctx, func(state *State) error {
		if site.ID == "" {
			site.ID = NewID()
		}
		if site.CreatedAt.IsZero() {
			site.CreatedAt = s.now().UTC()
		}
		state.Sites[site.ID] = site
		result = site
		return nil
	})
	return result, err
}

func (s *Store) GetSite(ctx context.Context, id string) (Site, error) {
	var result Site
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Sites[id]
		if !ok {
			return ErrNotFound
		}
		result = item
		return nil
	})
	return result, err
}

func (s *Store) ListSites(ctx context.Context) ([]Site, error) {
	result := []Site{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Sites {
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
		return nil
	})
	return result, err
}

func ValidateScope(scope Scope) error {
	if scope.SiteID == "" || len(scope.Ranges) == 0 || len(scope.Ranges) > 128 || len(scope.Exclusions) > 128 || len(scope.Ports) > 64 {
		return ErrInvalid
	}
	if scope.Limits.ProbesPerSecond < 0 || scope.Limits.ProbesPerSecond > 1000 || scope.Limits.Concurrency < 0 || scope.Limits.Concurrency > 16 || scope.Limits.TargetBudget < 0 || scope.Limits.TargetBudget > 4096 {
		return ErrInvalid
	}
	if len(scope.AllowedMethods) == 0 {
		return ErrInvalid
	}
	for _, raw := range scope.Ranges {
		if _, err := netip.ParsePrefix(raw); err != nil {
			if _, err := netip.ParseAddr(raw); err != nil {
				return ErrInvalid
			}
		}
	}
	for _, raw := range scope.Exclusions {
		if _, err := netip.ParsePrefix(raw); err != nil {
			if _, err := netip.ParseAddr(raw); err != nil {
				return ErrInvalid
			}
		}
	}
	for _, port := range scope.Ports {
		if port < 1 || port > 65535 {
			return ErrInvalid
		}
	}
	for _, method := range scope.AllowedMethods {
		switch method {
		case "interface", "route", "neighbor", "tcp":
		default:
			return ErrInvalid
		}
	}
	return nil
}

func (s *Store) CreateScope(ctx context.Context, scope Scope) (Scope, error) {
	return s.createScope(ctx, scope, nil)
}

// CreateScopeWithScanPolicy persists a scope and its first scan policy in one
// transaction. The control API uses this to avoid exposing a partially
// configured scope when an owner creates a policy-enabled scope.
func (s *Store) CreateScopeWithScanPolicy(ctx context.Context, scope Scope, scanPolicy ScanPolicy) (Scope, error) {
	return s.createScope(ctx, scope, &scanPolicy)
}

func (s *Store) createScope(ctx context.Context, scope Scope, requestedPolicy *ScanPolicy) (Scope, error) {
	if err := ValidateScope(scope); err != nil {
		return Scope{}, err
	}
	var result Scope
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		if _, ok := state.Sites[scope.SiteID]; !ok {
			return ErrNotFound
		}
		if scope.ID == "" {
			scope.ID = NewID()
		}
		if scope.Revision == 0 {
			scope.Revision = 1
		}
		now := s.now().UTC()
		if scope.CreatedAt.IsZero() {
			scope.CreatedAt = now
		}
		if scope.UpdatedAt.IsZero() {
			scope.UpdatedAt = scope.CreatedAt
		}
		if scope.Limits.ProbesPerSecond == 0 {
			scope.Limits.ProbesPerSecond = 10
		}
		if scope.Limits.Concurrency == 0 {
			scope.Limits.Concurrency = 16
		}
		if scope.Limits.TargetBudget == 0 {
			scope.Limits.TargetBudget = 256
		}
		scope.Ranges = cloneStrings(scope.Ranges)
		scope.Exclusions = cloneStrings(scope.Exclusions)
		scope.Ports = cloneInts(scope.Ports)
		scope.AllowedMethods = cloneStrings(scope.AllowedMethods)
		state.Scopes[scope.ID] = scope
		initialPolicy := defaultScanPolicy(scope, now)
		if requestedPolicy != nil {
			initialPolicy = cloneScanPolicy(*requestedPolicy)
			initialPolicy.ScopeID = scope.ID
			initialPolicy.Revision = 1
			initialPolicy.Enabled = scope.Enabled
			initialPolicy.UpdatedAt = now
		}
		if err := validateScanPolicy(initialPolicy, scope); err != nil {
			delete(state.Scopes, scope.ID)
			return err
		}
		state.ScanPolicies[scope.ID] = initialPolicy
		result = scope
		return nil
	})
	return result, err
}

func (s *Store) GetScope(ctx context.Context, id string) (Scope, error) {
	var result Scope
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Scopes[id]
		if !ok {
			return ErrNotFound
		}
		item.Ranges = cloneStrings(item.Ranges)
		item.Exclusions = cloneStrings(item.Exclusions)
		item.Ports = cloneInts(item.Ports)
		item.AllowedMethods = cloneStrings(item.AllowedMethods)
		result = item
		return nil
	})
	return result, err
}

func (s *Store) ListScopes(ctx context.Context) ([]Scope, error) {
	result := []Scope{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Scopes {
			item.Ranges = cloneStrings(item.Ranges)
			item.Exclusions = cloneStrings(item.Exclusions)
			item.Ports = cloneInts(item.Ports)
			item.AllowedMethods = cloneStrings(item.AllowedMethods)
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
		return nil
	})
	return result, err
}

func (s *Store) UpdateScope(ctx context.Context, id string, expected int64, update Scope) (Scope, error) {
	if err := ValidateScope(update); err != nil {
		return Scope{}, err
	}
	var result Scope
	err := s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		current, ok := state.Scopes[id]
		if !ok {
			return ErrNotFound
		}
		if current.Revision != expected {
			return ErrConflict
		}
		update.ID = id
		update.Revision = current.Revision + 1
		update.CreatedAt = current.CreatedAt
		update.UpdatedAt = s.now().UTC()
		if update.Limits.ProbesPerSecond == 0 {
			update.Limits.ProbesPerSecond = 10
		}
		if update.Limits.Concurrency == 0 {
			update.Limits.Concurrency = 16
		}
		if update.Limits.TargetBudget == 0 {
			update.Limits.TargetBudget = 256
		}
		state.Scopes[id] = update
		if scanPolicy, exists := state.ScanPolicies[id]; exists {
			scanPolicy.Enabled = update.Enabled
			scanPolicy.UpdatedAt = s.now().UTC()
			state.ScanPolicies[id] = scanPolicy
			if !update.Enabled {
				for runID, run := range state.ScanRuns {
					if run.ScopeID == id && !scanRunTerminalStates[run.State] {
						state.ScanRuns[runID] = markScanRunCancellation(run, s.now().UTC())
						if scanRunTerminalStates[state.ScanRuns[runID].State] {
							delete(state.ScanRunLeases, runID)
						}
					}
				}
			}
		}
		result = update
		return nil
	})
	return result, err
}

func (s *Store) SetWorkspace(ctx context.Context, update func(*WorkspaceState) error) (WorkspaceState, error) {
	var result WorkspaceState
	err := s.mutate(ctx, func(state *State) error {
		if err := update(&state.Workspace); err != nil {
			return err
		}
		result = state.Workspace
		return nil
	})
	return result, err
}

func (s *Store) Workspace(ctx context.Context) (WorkspaceState, error) {
	var result WorkspaceState
	err := s.read(ctx, func(state *State) error { result = state.Workspace; return nil })
	return result, err
}

func (s *Store) CreateDevice(ctx context.Context, device Device) (Device, error) {
	if strings.TrimSpace(device.DisplayName) == "" || len(device.DisplayName) > 256 {
		return Device{}, ErrInvalid
	}
	var result Device
	err := s.mutate(ctx, func(state *State) error {
		if device.ID == "" {
			device.ID = NewID()
		}
		if device.CreatedAt.IsZero() {
			device.CreatedAt = s.now().UTC()
		}
		if device.Lifecycle == "" {
			device.Lifecycle = "candidate"
		}
		if device.Availability == "" {
			device.Availability = AvailabilityConnecting
		}
		if device.MetricFreshness == nil {
			device.MetricFreshness = map[string]Freshness{}
		}
		if device.CurrentMetrics == nil {
			device.CurrentMetrics = map[string]MetricSample{}
		}
		device.Addresses = cloneStrings(device.Addresses)
		state.Devices[device.ID] = device
		result = device
		return nil
	})
	return result, err
}

func (s *Store) GetDevice(ctx context.Context, id string) (Device, error) {
	var result Device
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Devices[id]
		if !ok {
			return ErrNotFound
		}
		result = copyDevice(item)
		return nil
	})
	if err == nil && s.db != nil {
		phase, phaseErr := s.monitoringPhase(ctx)
		if phaseErr != nil {
			return Device{}, phaseErr
		}
		if phase == MonitoringStorageAuthoritative {
			items := []Device{result}
			if phaseErr := s.populateCurrentMetricsSQL(ctx, items); phaseErr != nil {
				return Device{}, phaseErr
			}
			result = items[0]
		}
	}
	return result, err
}

type DeviceFilter struct {
	Query           string
	Health          string
	MonitoringState string
	SiteID          string
}

func (s *Store) ListDevices(ctx context.Context, filter DeviceFilter) ([]Device, error) {
	result := []Device{}
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		for key, item := range state.Devices {
			refreshDevice(&item, state, now)
			state.Devices[key] = item
			if filter.SiteID != "" && item.SiteID != filter.SiteID {
				continue
			}
			if filter.Query != "" && !containsFold(item.DisplayName+" "+strings.Join(item.Addresses, " ")+" "+item.Platform, filter.Query) {
				continue
			}
			if filter.MonitoringState != "" && monitoringState(item) != filter.MonitoringState {
				continue
			}
			if filter.Health != "" && healthState(item) != filter.Health {
				continue
			}
			result = append(result, copyDevice(item))
		}
		sortDevices(result)
		return nil
	})
	if err == nil && s.db != nil {
		phase, phaseErr := s.monitoringPhase(ctx)
		if phaseErr != nil {
			return nil, phaseErr
		}
		if phase == MonitoringStorageAuthoritative {
			if phaseErr := s.populateCurrentMetricsSQL(ctx, result); phaseErr != nil {
				return nil, phaseErr
			}
		}
	}
	return result, err
}

func (s *Store) UpdateDevice(ctx context.Context, id string, update func(*Device) error) (Device, error) {
	var result Device
	targetChanged := false
	err := s.mutate(ctx, func(state *State) error {
		item, ok := state.Devices[id]
		if !ok {
			return ErrNotFound
		}
		previousSiteID := item.SiteID
		if err := update(&item); err != nil {
			return err
		}
		targetChanged = previousSiteID != item.SiteID
		item.Revision++
		state.Devices[id] = item
		if targetChanged {
			for agentID, agent := range state.Agents {
				if agent.DeviceID == id {
					requestScanCancellationForAgent(state, agentID, s.now().UTC())
				}
			}
		}
		if targetChanged && s.db == nil {
			if _, err := closeIncidentsForState(state, func(incident Incident) bool { return incident.DeviceID == id }, "administrative", s.now().UTC()); err != nil {
				return err
			}
		}
		result = copyDevice(item)
		return nil
	})
	if err == nil && targetChanged && s.db != nil {
		if _, closeErr := s.closeIncidentsForDeviceSQL(ctx, id, "administrative"); closeErr != nil {
			return Device{}, closeErr
		}
	}
	return result, err
}

func copyDevice(item Device) Device {
	item.Addresses = cloneStrings(item.Addresses)
	item.Identifiers = append([]IdentifierEvidence(nil), item.Identifiers...)
	item.MetricFreshness = cloneFloatMap(item.MetricFreshness)
	metrics := map[string]MetricSample{}
	for key, sample := range item.CurrentMetrics {
		sample.Labels = cloneMap(sample.Labels)
		metrics[key] = sample
	}
	item.CurrentMetrics = metrics
	item.CollectorStates = append([]CollectorDescriptorState(nil), item.CollectorStates...)
	return item
}

func refreshDevice(item *Device, state *State, now time.Time) {
	if item.DecommissionedAt != nil || item.Lifecycle == "decommissioned" {
		item.Availability = AvailabilityRevoked
		return
	}
	if item.LastHeartbeat == nil {
		item.Availability = AvailabilityConnecting
	} else if now.Sub(*item.LastHeartbeat) <= 90*time.Second {
		item.Availability = AvailabilityOnline
	} else {
		item.Availability = AvailabilityOffline
	}
	if item.MetricFreshness == nil {
		item.MetricFreshness = map[string]Freshness{}
	}
	for metric, sample := range item.CurrentMetrics {
		age := now.Sub(sample.ObservedAt)
		if sample.Availability == FreshnessUnsupported || sample.Availability == FreshnessUnavailable {
			item.MetricFreshness[metric] = sample.Availability
			continue
		}
		if age <= 45*time.Second {
			item.MetricFreshness[metric] = FreshnessCurrent
		} else {
			item.MetricFreshness[metric] = FreshnessStale
		}
	}
	_ = state
}

func monitoringState(item Device) string {
	if item.Lifecycle == "decommissioned" || item.Availability == AvailabilityRevoked {
		return "revoked"
	}
	if item.AgentID == "" {
		return "unmonitored"
	}
	return "monitored"
}

func healthState(item Device) string {
	if item.Availability == AvailabilityRevoked {
		return "revoked"
	}
	if item.Availability == AvailabilityOffline {
		return "offline"
	}
	if item.AgentID == "" {
		return "needs_access"
	}
	for _, freshness := range item.MetricFreshness {
		if freshness != FreshnessCurrent {
			return "degraded"
		}
	}
	return "healthy"
}

func (s *Store) ReserveInvitation(ctx context.Context, invitation BootstrapInvitation) error {
	return s.mutate(ctx, func(state *State) error {
		if invitation.TokenHash == "" || invitation.DeviceID == "" {
			return ErrInvalid
		}
		if _, ok := state.Invitations[invitation.TokenHash]; ok {
			return ErrConflict
		}
		if _, ok := state.Devices[invitation.DeviceID]; !ok {
			return ErrNotFound
		}
		state.Invitations[invitation.TokenHash] = invitation
		return nil
	})
}

func (s *Store) Invitation(ctx context.Context, tokenHash string) (BootstrapInvitation, error) {
	var result BootstrapInvitation
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Invitations[tokenHash]
		if !ok {
			return ErrNotFound
		}
		result = item
		return nil
	})
	return result, err
}

func (s *Store) ConsumeInvitation(ctx context.Context, tokenHash string) (BootstrapInvitation, error) {
	var result BootstrapInvitation
	err := s.mutate(ctx, func(state *State) error {
		item, ok := state.Invitations[tokenHash]
		if !ok {
			return ErrUnauthorized
		}
		now := s.now().UTC()
		if item.RevokedAt != nil {
			return ErrRevoked
		}
		if item.ConsumedAt != nil {
			return ErrDuplicate
		}
		if !now.Before(item.ExpiresAt) {
			return ErrExpired
		}
		item.ConsumedAt = &now
		state.Invitations[tokenHash] = item
		result = item
		return nil
	})
	return result, err
}

func (s *Store) CreateAgentIdentity(ctx context.Context, agent AgentIdentity) error {
	agent.Capabilities = cloneScanCapabilities(agent.Capabilities)
	return s.mutate(ctx, func(state *State) error {
		if _, ok := state.Devices[agent.DeviceID]; !ok {
			return ErrNotFound
		}
		for _, existing := range state.Agents {
			if existing.DeviceID == agent.DeviceID && existing.RevokedAt == nil {
				return ErrConflict
			}
		}
		if agent.ID == "" {
			agent.ID = NewID()
		}
		state.Agents[agent.ID] = agent
		device := state.Devices[agent.DeviceID]
		device.AgentID = agent.ID
		device.AgentVersion = agent.InstalledVersion
		device.Lifecycle = "enrolled"
		device.Availability = AvailabilityConnecting
		state.Devices[agent.DeviceID] = device
		return nil
	})
}

func (s *Store) Agent(ctx context.Context, id string) (AgentIdentity, error) {
	var result AgentIdentity
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Agents[id]
		if !ok {
			return ErrNotFound
		}
		result = item
		result.Capabilities = cloneScanCapabilities(item.Capabilities)
		return nil
	})
	return result, err
}

func (s *Store) AgentByTokenHash(ctx context.Context, tokenHash string) (AgentIdentity, error) {
	var result AgentIdentity
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Agents {
			if item.AuthTokenHash == tokenHash {
				result = item
				result.Capabilities = cloneScanCapabilities(item.Capabilities)
				return nil
			}
		}
		return ErrUnauthorized
	})
	return result, err
}

func (s *Store) AgentBySerial(ctx context.Context, serial string) (AgentIdentity, error) {
	var result AgentIdentity
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Agents {
			if item.CertSerial == serial {
				result = item
				result.Capabilities = cloneScanCapabilities(item.Capabilities)
				return nil
			}
		}
		return ErrUnauthorized
	})
	return result, err
}

func (s *Store) RevokeAgent(ctx context.Context, id string) error {
	return s.mutateWithWorkspaceLock(ctx, func(state *State) error {
		item, ok := state.Agents[id]
		if !ok {
			return ErrNotFound
		}
		now := s.now().UTC()
		item.RevokedAt = &now
		state.Agents[id] = item
		requestScanCancellationForAgent(state, id, now)
		return nil
	})
}

func requestScanCancellationForAgent(state *State, agentID string, now time.Time) {
	for runID, run := range state.ScanRuns {
		if run.ScannerKind != "agent" || run.ScannerID != agentID || scanRunTerminalStates[run.State] {
			continue
		}
		run = markScanRunCancellation(run, now)
		if scanRunTerminalStates[run.State] {
			delete(state.ScanRunLeases, runID)
		}
		state.ScanRuns[runID] = run
	}
}

func (s *Store) UpdateAgent(ctx context.Context, id string, update func(*AgentIdentity) error) (AgentIdentity, error) {
	var result AgentIdentity
	err := s.mutate(ctx, func(state *State) error {
		item, ok := state.Agents[id]
		if !ok {
			return ErrNotFound
		}
		if item.RevokedAt != nil {
			return ErrRevoked
		}
		if err := update(&item); err != nil {
			return err
		}
		item.Capabilities = cloneScanCapabilities(item.Capabilities)
		state.Agents[id] = item
		result = item
		return nil
	})
	return result, err
}
