package policy

import (
	"context"
	"errors"
	"testing"

	"scout.local/scout/internal/store"
)

func scanPolicyFixture(t *testing.T) (*store.Store, store.Scope, store.ScanPolicy) {
	t.Helper()
	ctx := context.Background()
	s := store.NewMemory()
	site, err := s.CreateSite(ctx, store.Site{Name: "scan-policy"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, Exclusions: []string{"192.0.2.9"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, scope, policy
}

func TestNormalizeScanPolicyDefaultsAndAttemptMultiplication(t *testing.T) {
	_, scope, policy := scanPolicyFixture(t)
	policy.Limits = store.ScanLimits{}
	policy.ScheduleSeconds = 0
	normalized, err := NormalizeScanPolicy(policy, scope)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.ScheduleSeconds != 300 || normalized.Limits.ProbesPerSecond != 10 || normalized.Limits.Concurrency != 16 || normalized.Limits.TargetBudget != 256 || normalized.Limits.AttemptBudget != 256 || normalized.Limits.ResultPageSize != 1000 {
		t.Fatalf("defaults not applied: %+v", normalized)
	}
	policy.EntryPoints = append(policy.EntryPoints, store.ScanEntryPoint{ID: "ssh-alt", Name: "SSH alt", Transport: store.ScanTransportTCP, Port: 2222, AccessMethod: store.ScanAccessSSH, Enabled: true})
	policy.Limits.AttemptBudget = 513
	if _, err := NormalizeScanPolicy(policy, scope); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("attempt multiplication overflow accepted: %v", err)
	}
}

func TestNormalizeScanPolicyRejectsUnboundedIPv6Prefix(t *testing.T) {
	_, scope, policy := scanPolicyFixture(t)
	scope.Ranges = []string{"2001:db8::/64"}
	if _, err := NormalizeScanPolicy(policy, scope); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unbounded IPv6 prefix accepted: %v", err)
	}
	scope.Ranges = []string{"2001:db8::/120"}
	if _, err := NormalizeScanPolicy(policy, scope); err != nil {
		t.Fatalf("finite IPv6 prefix rejected: %v", err)
	}
}

func TestScanAssignmentAndPreProbeRequireCurrentExplicitAuthority(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := scanPolicyFixture(t)
	engine := &Engine{Store: s}
	server, err := engine.ValidateScanVantage(ctx, scope.ID, ScanVantage{Kind: "server", ID: "control-server-1"})
	if err != nil || !server.Allowed {
		t.Fatalf("server assignment validation: %+v %v", server, err)
	}
	agent, err := engine.ValidateScanVantage(ctx, scope.ID, ScanVantage{Kind: "agent", ID: store.NewID(), DeviceID: store.NewID()})
	if err != nil || agent.Allowed || agent.Reason != "vantage_not_assigned" {
		t.Fatalf("unassigned agent was allowed: %+v %v", agent, err)
	}
	decision, err := engine.PreProbe(ctx, ScanPreProbeRequest{ScopeID: scope.ID, PolicyRevision: policy.Revision, Scanner: ScanVantage{Kind: "server", ID: "control-server-1"}, Address: "192.0.2.9", EntryPointID: policy.EntryPoints[0].ID})
	if err != nil || decision.Allowed || decision.Reason != "excluded" {
		t.Fatalf("excluded target was allowed: %+v %v", decision, err)
	}
	decision, err = engine.PreProbe(ctx, ScanPreProbeRequest{ScopeID: scope.ID, PolicyRevision: policy.Revision + 1, Scanner: ScanVantage{Kind: "server", ID: "control-server-1"}, Address: "192.0.2.10", EntryPointID: policy.EntryPoints[0].ID})
	if err != nil || decision.Allowed || decision.Reason != "policy_revision_changed" {
		t.Fatalf("stale policy was allowed: %+v %v", decision, err)
	}
}
