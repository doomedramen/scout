package alerts

import (
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestDefaultRuleTemplatesAreCompleteAndOwnerEditsArePreserved(t *testing.T) {
	now := time.Date(2026, 9, 11, 19, 0, 0, 0, time.UTC)
	templates := DefaultRuleTemplates(now)
	if len(templates) != 8 {
		t.Fatalf("default rule count = %d, want 8", len(templates))
	}
	byTemplate := make(map[string]store.AlertRule, len(templates))
	for _, rule := range templates {
		if err := ValidateRule(rule); err != nil {
			t.Fatalf("default %q is invalid: %v", rule.TemplateKey, err)
		}
		byTemplate[rule.TemplateKey] = rule
	}
	for _, key := range []string{"host_offline", "cpu_high", "memory_high", "filesystem_high", "systemd_failed", "service_required_inactive", "collector_degraded", "storage_fault"} {
		if _, ok := byTemplate[key]; !ok {
			t.Fatalf("missing default template %q", key)
		}
	}
	if got := byTemplate["cpu_high"]; *got.TriggerValue != 90 || *got.ClearValue != 85 || got.TriggerSeconds != 300 || got.ClearSeconds != 120 || got.Severity != SeverityWarning {
		t.Fatalf("unexpected CPU default: %+v", got)
	}
	if got := byTemplate["host_offline"]; got.TriggerSeconds != 90 || got.Severity != SeverityCritical {
		t.Fatalf("unexpected offline default: %+v", got)
	}
	if got := byTemplate["systemd_failed"]; got.CollectorID != SystemdCollectorID || got.MinimumConsecutiveSamples != 2 || got.ClearSeconds != 30 {
		t.Fatalf("unexpected systemd failure default: %+v", got)
	}
	if got := byTemplate["service_required_inactive"]; got.CollectorID != SystemdCollectorID || got.TriggerSeconds != 60 || got.ClearSeconds != 60 || got.Severity != SeverityWarning {
		t.Fatalf("unexpected must-run default: %+v", got)
	}

	edited := byTemplate["cpu_high"]
	edited.ID = "cpu-lineage"
	edited.Name = "Owner CPU policy"
	edited.Enabled = false
	edited.TriggerValue = floatPointer(75)
	edited.ClearValue = floatPointer(70)
	provisioned := ProvisionDefaultRules([]store.AlertRule{edited}, now.Add(time.Hour))
	if len(provisioned) != len(templates) {
		t.Fatalf("provisioned rule count = %d, want %d", len(provisioned), len(templates))
	}
	var preserved store.AlertRule
	for _, rule := range provisioned {
		if rule.TemplateKey == "cpu_high" {
			preserved = rule
		}
	}
	if preserved.ID != edited.ID || preserved.Name != edited.Name || preserved.Enabled || preserved.TriggerValue == nil || *preserved.TriggerValue != 75 || preserved.ClearValue == nil || *preserved.ClearValue != 70 {
		t.Fatalf("owner-edited default was overwritten: %+v", preserved)
	}
}

func TestResolveRulesUsesDeviceThenSiteThenFleetOverrides(t *testing.T) {
	base := store.AlertRule{ID: "cpu", Name: "CPU", Kind: "numeric", Metric: "cpu.utilization", Operator: "gt", TriggerValue: floatPointer(90), ClearValue: floatPointer(85), TriggerSeconds: 300, ClearSeconds: 120, MinimumConsecutiveSamples: 1, Severity: SeverityWarning, TargetKind: TargetFleet, Enabled: true, Revision: 2}
	siteOverride := store.AlertOverride{ID: "site-override", LineageID: base.ID, TargetKind: TargetSite, TargetID: "site-a", Kind: "numeric", Metric: base.Metric, Operator: "gt", TriggerValue: floatPointer(80), ClearValue: floatPointer(70), TriggerSeconds: 60, ClearSeconds: 30, MinimumConsecutiveSamples: 1, Severity: SeverityCritical, Enabled: true, Revision: 3}
	deviceOverride := siteOverride
	deviceOverride.ID = "device-override"
	deviceOverride.TargetKind = TargetDevice
	deviceOverride.TargetID = "device-a"
	deviceOverride.TriggerValue = floatPointer(70)
	deviceOverride.ClearValue = floatPointer(60)
	deviceOverride.Enabled = false
	deviceA := store.Device{ID: "device-a", SiteID: "site-a"}
	deviceB := store.Device{ID: "device-b", SiteID: "site-a"}
	deviceC := store.Device{ID: "device-c", SiteID: "site-b"}

	for _, test := range []struct {
		name       string
		device     store.Device
		source     string
		trigger    float64
		enabled    bool
		wantSecond bool
	}{
		{name: "device", device: deviceA, source: TargetDevice, trigger: 70, enabled: false},
		{name: "site", device: deviceB, source: TargetSite, trigger: 80, enabled: true},
		{name: "fleet", device: deviceC, source: TargetFleet, trigger: 90, enabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved := ResolveRules([]store.AlertRule{base}, []store.AlertOverride{siteOverride, deviceOverride}, test.device)
			if len(resolved) != 1 {
				t.Fatalf("resolved rule count = %d", len(resolved))
			}
			if resolved[0].Source != test.source || resolved[0].Rule.Enabled != test.enabled || resolved[0].Rule.TriggerValue == nil || *resolved[0].Rule.TriggerValue != test.trigger {
				t.Fatalf("unexpected effective rule: %+v", resolved[0])
			}
		})
	}
	if err := ValidateOverride(base, siteOverride); err != nil {
		t.Fatalf("valid site override rejected: %v", err)
	}
	broad := siteOverride
	broad.TargetKind = TargetFleet
	if err := ValidateOverride(base, broad); err == nil {
		t.Fatal("broadening override was accepted")
	}
	wrongLineage := siteOverride
	wrongLineage.LineageID = "other"
	if err := ValidateOverride(base, wrongLineage); err == nil {
		t.Fatal("cross-lineage override was accepted")
	}
}

func TestEffectiveRuleConvertsToEvaluatorRule(t *testing.T) {
	rule := store.AlertRule{ID: "memory", Name: "Memory", Kind: "numeric", Metric: "memory.used_percent", Operator: "gt", TriggerValue: floatPointer(90), ClearValue: floatPointer(85), TriggerSeconds: 300, ClearSeconds: 120, MinimumConsecutiveSamples: 1, Severity: SeverityWarning, TargetKind: TargetFleet, Enabled: true, Revision: 4}
	converted, err := (EffectiveRule{Rule: rule, Source: TargetFleet}).EvaluatorRule()
	if err != nil {
		t.Fatal(err)
	}
	if converted.LineageID != rule.ID || converted.TriggerFor != 5*time.Minute || converted.ClearFor != 2*time.Minute || converted.TriggerValue != 90 || converted.ClearValue != 85 {
		t.Fatalf("unexpected evaluator rule: %+v", converted)
	}
}
