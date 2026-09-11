package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAlertRuleStoreProvisioningOverridesAndRevision(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	now := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	site, err := s.CreateSite(ctx, Site{ID: "site-a", Name: "Site A"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := s.CreateDevice(ctx, Device{ID: "device-a", SiteID: site.ID, DisplayName: "Device A"})
	if err != nil {
		t.Fatal(err)
	}
	defaults := []AlertRule{{TemplateKey: "cpu_high", Name: "CPU", Kind: "numeric", Metric: "cpu.utilization", Operator: "gt", TriggerValue: float64Pointer(90), ClearValue: float64Pointer(85), TriggerSeconds: 300, ClearSeconds: 120, MinimumConsecutiveSamples: 1, Severity: "warning", TargetKind: "fleet", Enabled: true, CreatedAt: now, UpdatedAt: now}}
	first, err := s.EnsureDefaultAlertRules(ctx, defaults)
	if err != nil || len(first) != 1 {
		t.Fatalf("provision defaults: rules=%+v err=%v", first, err)
	}
	if first[0].ID == "" || first[0].Revision != 1 {
		t.Fatalf("default identity/revision missing: %+v", first[0])
	}
	edited := first[0]
	edited.Name = "Owner CPU"
	edited.Enabled = false
	edited.TriggerValue = float64Pointer(75)
	edited.ClearValue = float64Pointer(70)
	updatedRule, err := s.PutAlertRule(ctx, edited, edited.Revision)
	if err != nil {
		t.Fatalf("put edited rule: %v (%+v)", err, edited)
	}
	edited = updatedRule
	second, err := s.EnsureDefaultAlertRules(ctx, defaults)
	if err != nil || len(second) != 1 || second[0].ID != edited.ID || second[0].Name != edited.Name || second[0].Enabled || *second[0].TriggerValue != 75 {
		t.Fatalf("default provisioning overwrote owner edit: rules=%+v err=%v", second, err)
	}
	if _, err := s.PutAlertRule(ctx, edited, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale rule revision accepted: %v", err)
	}

	override := AlertOverride{LineageID: edited.ID, TargetKind: "site", TargetID: site.ID, Kind: "numeric", Metric: edited.Metric, Operator: "gt", TriggerValue: float64Pointer(80), ClearValue: float64Pointer(70), TriggerSeconds: 60, ClearSeconds: 30, MinimumConsecutiveSamples: 1, Severity: "critical", Enabled: true}
	createdOverride, err := s.PutAlertOverride(ctx, override, 0)
	if err != nil {
		t.Fatal(err)
	}
	if createdOverride.ID == "" || createdOverride.Revision != 1 {
		t.Fatalf("override identity/revision missing: %+v", createdOverride)
	}
	updatedOverride := createdOverride
	updatedOverride.Enabled = false
	updatedOverride, err = s.PutAlertOverride(ctx, updatedOverride, createdOverride.Revision)
	if err != nil || updatedOverride.Revision != 2 || updatedOverride.Enabled {
		t.Fatalf("override revision update failed: %+v err=%v", updatedOverride, err)
	}
	if _, err := s.PutAlertOverride(ctx, updatedOverride, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale override revision accepted: %v", err)
	}
	overrides, err := s.ListAlertOverrides(ctx, edited.ID)
	if err != nil || len(overrides) != 1 || overrides[0].ID != updatedOverride.ID {
		t.Fatalf("override list = %+v err=%v", overrides, err)
	}
	otherSite, err := s.CreateSite(ctx, Site{ID: "site-b", Name: "Site B"})
	if err != nil {
		t.Fatal(err)
	}
	otherDevice, err := s.CreateDevice(ctx, Device{ID: "device-b", SiteID: otherSite.ID, DisplayName: "Device B"})
	if err != nil {
		t.Fatal(err)
	}
	siteRule := edited
	siteRule.ID = "site-scoped-rule"
	siteRule.TemplateKey = ""
	siteRule.TargetKind = "site"
	siteRule.TargetID = site.ID
	siteRule.Enabled = true
	siteRule.RetiredAt = nil
	siteRule, err = s.PutAlertRule(ctx, siteRule, 0)
	if err != nil {
		t.Fatal(err)
	}
	crossSite := updatedOverride
	crossSite.ID = ""
	crossSite.LineageID = siteRule.ID
	crossSite.TargetKind = "device"
	crossSite.TargetID = otherDevice.ID
	if _, err := s.PutAlertOverride(ctx, crossSite, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-site device override was accepted: %v", err)
	}
	if _, err := s.RetireAlertRule(ctx, edited.ID, edited.Revision); err != nil {
		t.Fatal(err)
	}
	active, err := s.ListAlertRules(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range active {
		if rule.ID == edited.ID {
			t.Fatalf("retired rule remained active: rules=%+v", active)
		}
	}
	all, err := s.ListAlertRules(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var retiredFound bool
	for _, rule := range all {
		if rule.ID == edited.ID {
			retiredFound = rule.RetiredAt != nil
		}
	}
	if !retiredFound {
		t.Fatalf("retired rule missing from full list: rules=%+v", all)
	}
	_ = device
}

func float64Pointer(value float64) *float64 { return &value }
