package discovery

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"scout.local/scout/internal/enrollment"
	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

type Sight struct {
	Address   string
	Hostname  string
	Source    string
	Port      int
	Reachable bool
}

type Service struct {
	Store      *store.Store
	Policy     *policy.Engine
	Enrollment *enrollment.Access
	Now        func() time.Time
}

func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// ReconcileSightings deduplicates observations, persists candidates, and
// creates at most one target-scoped enrollment job for each eligible address.
// No job is created for an excluded, unreachable, untrusted, or incomplete
// target.
func (s *Service) ReconcileSightings(ctx context.Context, scopeID string, sightings []Sight) ([]store.Candidate, error) {
	if s == nil || s.Store == nil || s.Policy == nil || scopeID == "" {
		return nil, store.ErrInvalid
	}
	scope, err := s.Store.GetScope(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	result := []store.Candidate{}
	seen := map[string]bool{}
	for _, sighting := range sightings {
		address, addressErr := netip.ParseAddr(strings.TrimSpace(sighting.Address))
		if addressErr != nil {
			continue
		}
		port := sighting.Port
		if port == 0 {
			port = 22
		}
		key := address.String() + ":" + strconvPort(port)
		if seen[key] {
			continue
		}
		seen[key] = true
		candidateState := "unreachable"
		if sighting.Reachable {
			candidateState = "needs_credentials"
		}
		candidate, candidateErr := s.Store.UpsertCandidate(ctx, store.Candidate{SiteID: scope.SiteID, ScopeID: scope.ID, Address: address.String(), Hostname: strings.TrimSpace(sighting.Hostname), Source: sighting.Source, State: candidateState, PreferredAccessMethod: store.ScanAccessSSH, EntryPointIDs: []string{"ssh-default"}, LastSeen: s.clock(), ExpiresAt: s.clock().Add(15 * time.Minute)})
		if candidateErr != nil {
			return nil, candidateErr
		}
		result = append(result, candidate)
		if candidate.Excluded {
			continue
		}
		endpoint := net.JoinHostPort(address.String(), strconvPort(port))
		if !sighting.Reachable {
			candidate.State = "unreachable"
			_, requestErr := s.Store.UpsertAccessRequest(ctx, store.AccessRequest{DeviceID: candidate.ID, CandidateID: candidate.ID, ScopeID: scope.ID, AccessMethod: store.ScanAccessSSH, Endpoint: endpoint, ReasonCode: string(enrollment.ReasonConnectivity), SafeDetails: map[string]string{"target": endpoint, "method": store.ScanAccessSSH}, State: "open", LastAttempt: s.clock()})
			if requestErr != nil {
				return nil, requestErr
			}
			result[len(result)-1] = candidate
			continue
		}
		candidate.State = "needs_credentials"
		candidate.PreferredAccessMethod = store.ScanAccessSSH
		if _, requestErr := s.Store.UpsertAccessRequest(ctx, store.AccessRequest{DeviceID: candidate.ID, CandidateID: candidate.ID, ScopeID: scope.ID, AccessMethod: store.ScanAccessSSH, Endpoint: endpoint, ReasonCode: string(enrollment.ReasonMissingCredentials), SafeDetails: map[string]string{"target": endpoint, "method": store.ScanAccessSSH}, State: "open", LastAttempt: s.clock()}); requestErr != nil {
			return nil, requestErr
		}
		if s.Enrollment != nil {
			evaluated, evaluateErr := s.Enrollment.ReevaluateCandidate(ctx, candidate.ID)
			if evaluateErr != nil {
				return nil, evaluateErr
			}
			candidate = evaluated.Candidate
		}
		result[len(result)-1] = candidate
	}
	return result, nil
}

func (s *Service) DiscoverScope(ctx context.Context, scopeID, source string, probePolicy ProbePolicy) ([]store.Candidate, error) {
	if s == nil || s.Store == nil {
		return nil, store.ErrInvalid
	}
	scope, err := s.Store.GetScope(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	addresses, err := ExpandTargets(scope.Ranges, scope.Exclusions, probePolicy.TargetBudget)
	if err != nil {
		return nil, err
	}
	probes, err := Probe(ctx, addresses, probePolicy)
	if err != nil {
		return nil, err
	}
	byAddress := map[string]bool{}
	for _, probe := range probes {
		if probe.Port != 22 {
			continue
		}
		byAddress[probe.Address] = byAddress[probe.Address] || probe.Reachable
	}
	sightings := make([]Sight, 0, len(addresses))
	for _, address := range addresses {
		sightings = append(sightings, Sight{Address: address, Source: source, Port: 22, Reachable: byAddress[address]})
	}
	return s.ReconcileSightings(ctx, scopeID, sightings)
}

func strconvPort(port int) string {
	return strconv.Itoa(port)
}
