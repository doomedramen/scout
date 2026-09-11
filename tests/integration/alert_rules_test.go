package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestSQLAlertRuleStorePersistsLineageOverridesAndRevisions(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	siteID := store.NewID()
	deviceID := store.NewID()
	ruleID := "sql-alert-rule-" + store.NewID()
	site, err := repository.CreateSite(ctx, store.Site{ID: siteID, Name: "SQL alert site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateDevice(ctx, store.Device{ID: deviceID, SiteID: site.ID, DisplayName: "SQL alert device"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_overrides WHERE lineage_id = $1`, ruleID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_rules WHERE id = $1`, ruleID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM devices WHERE id = $1`, deviceID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM sites WHERE id = $1`, siteID)
	})

	trigger, clear := 90.0, 85.0
	defaults := []store.AlertRule{{
		ID: ruleID, TemplateKey: "sql_alert_fixture", Name: "SQL CPU fixture", Kind: "numeric", Metric: "cpu.utilization", Operator: "gt",
		TriggerValue: &trigger, ClearValue: &clear, TriggerSeconds: 300, ClearSeconds: 120, MinimumConsecutiveSamples: 1,
		Severity: "warning", TargetKind: "fleet", Enabled: true,
	}}
	first, err := repository.EnsureDefaultAlertRules(ctx, defaults)
	if err != nil {
		t.Fatal(err)
	}
	var persisted store.AlertRule
	for _, rule := range first {
		if rule.TemplateKey == "sql_alert_fixture" {
			persisted = rule
		}
	}
	if persisted.ID != ruleID || persisted.Revision != 1 {
		t.Fatalf("SQL default was not persisted: %+v", persisted)
	}
	second, err := repository.EnsureDefaultAlertRules(ctx, defaults)
	if err != nil {
		t.Fatal(err)
	}
	var repeated store.AlertRule
	for _, rule := range second {
		if rule.TemplateKey == "sql_alert_fixture" {
			repeated = rule
		}
	}
	if repeated.ID != persisted.ID || repeated.UpdatedAt != persisted.UpdatedAt {
		t.Fatalf("SQL default provisioning was not idempotent: first=%+v second=%+v", persisted, repeated)
	}

	persisted.Name = "SQL CPU owner edit"
	persisted.Enabled = false
	persisted.TriggerValue = &trigger
	updated, err := repository.PutAlertRule(ctx, persisted, persisted.Revision)
	if err != nil || updated.Revision != 2 || updated.Name != persisted.Name || updated.Enabled {
		t.Fatalf("SQL rule revision update failed: %+v err=%v", updated, err)
	}
	if _, err := repository.PutAlertRule(ctx, updated, 1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("SQL stale rule revision accepted: %v", err)
	}

	deviceTrigger, deviceClear := 80.0, 70.0
	override, err := repository.PutAlertOverride(ctx, store.AlertOverride{
		LineageID: ruleID, TargetKind: "site", TargetID: siteID, Kind: "numeric", Metric: "cpu.utilization", Operator: "gt",
		TriggerValue: &deviceTrigger, ClearValue: &deviceClear, TriggerSeconds: 60, ClearSeconds: 30, MinimumConsecutiveSamples: 1,
		Severity: "critical", Enabled: true,
	}, 0)
	if err != nil || override.Revision != 1 || override.ID == "" {
		t.Fatalf("SQL override creation failed: %+v err=%v", override, err)
	}
	listed, err := repository.ListAlertOverrides(ctx, ruleID)
	if err != nil || len(listed) != 1 || listed[0].ID != override.ID {
		t.Fatalf("SQL override list failed: %+v err=%v", listed, err)
	}
	if _, err := repository.PutAlertOverride(ctx, override, 2); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("SQL stale override revision accepted: %v", err)
	}

	retired, err := repository.RetireAlertRule(ctx, ruleID, updated.Revision)
	if err != nil || retired.RetiredAt == nil || retired.Enabled || retired.Revision != 3 {
		t.Fatalf("SQL rule retirement failed: %+v err=%v", retired, err)
	}
	active, err := repository.ListAlertRules(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range active {
		if rule.ID == ruleID {
			t.Fatalf("retired SQL rule remained in active list: %+v", rule)
		}
	}
}
