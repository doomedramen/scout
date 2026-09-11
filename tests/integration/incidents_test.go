package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/alerts"
	"scout.local/scout/internal/store"
)

func TestSQLIncidentEvaluationIsRestartSafeAndTransactional(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	lineageID := "sql-incident-lineage-" + store.NewID()
	entityID := "sql-incident-entity-" + store.NewID()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_evaluations WHERE lineage_id = $1 AND entity_id = $2`, lineageID, entityID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_work WHERE lineage_id = $1 AND entity_id = $2`, store.AlertWorkAllLineages, entityID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM incidents WHERE lineage_id = $1 AND entity_id = $2`, lineageID, entityID)
	})

	start := time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC)
	clock := alerts.NewManualClock(start)
	rule := alerts.Rule{ID: lineageID, LineageID: lineageID, Revision: 1, Kind: alerts.RuleKindNumeric, Metric: "cpu.utilization", Operator: alerts.OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, Severity: alerts.SeverityCritical, Enabled: true}
	evaluator := alerts.NewPersistentEvaluator(repository, clock, "sql-alert-worker")
	if _, err := evaluator.EvaluateAndPersist(ctx, rule, numericIncidentObservation(entityID, rule.Metric, 95, start), "sql-device", nil); err != nil {
		t.Fatal(err)
	}
	persisted, err := repository.GetAlertEvaluation(ctx, lineageID, entityID)
	if err != nil {
		t.Fatal(err)
	}
	incident, err := repository.GetIncident(ctx, persisted.IncidentID)
	if err != nil || incident.Status != "active" {
		t.Fatalf("SQL incident = %+v err=%v", incident, err)
	}
	transitions, err := repository.ListIncidentTransitions(ctx, incident.ID, 100)
	if err != nil || len(transitions) != 1 || transitions[0].Sequence != 1 {
		t.Fatalf("SQL transitions = %+v err=%v", transitions, err)
	}

	restarted := alerts.NewPersistentEvaluator(repository, clock, "sql-alert-worker-restarted")
	restartedResult, err := restarted.EvaluateAndPersist(ctx, rule, numericIncidentObservation(entityID, rule.Metric, 95, start), "sql-device", nil)
	if err != nil || !restartedResult.Ignored {
		t.Fatalf("restarted evaluator replay = %+v err=%v", restartedResult, err)
	}
	restartedEvaluation, err := repository.GetAlertEvaluation(ctx, lineageID, entityID)
	if err != nil || restartedEvaluation.IncidentID != incident.ID {
		t.Fatalf("restarted evaluator changed checkpoint = %+v err=%v", restartedEvaluation, err)
	}
	restartedTransitions, err := repository.ListIncidentTransitions(ctx, incident.ID, 100)
	if err != nil || len(restartedTransitions) != 1 {
		t.Fatalf("restarted evaluator duplicated transitions = %+v err=%v", restartedTransitions, err)
	}

	if err := repository.MarkAlertWork(ctx, store.AlertWorkAllLineages, entityID); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimAlertWork(ctx, "sql-alert-worker", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("SQL alert work claim = %+v err=%v", claimed, err)
	}
	if err := repository.CompleteAlertWork(ctx, claimed[0], ""); err != nil {
		t.Fatal(err)
	}
	if work, err := repository.ListAlertWork(ctx); err != nil || len(work) != 0 {
		t.Fatalf("SQL alert work remains = %+v err=%v", work, err)
	}

	rollbackIncidentID := "sql-rollback-" + store.NewID()
	rollbackErr := repository.ApplyAlertEvaluation(ctx, store.AlertEvaluation{LineageID: lineageID, EntityID: entityID + "-rollback", EvidenceState: "fresh", UpdatedAt: start}, &store.Incident{ID: rollbackIncidentID, LineageID: lineageID, EntityID: entityID + "-rollback", Severity: "critical", Status: "active", EvidenceState: "fresh", OpenedAt: start, ObservedAt: start, EvaluatedAt: start}, []store.IncidentTransition{{IncidentID: "missing-incident", Kind: "triggered", EvidenceState: "fresh", OccurredAt: start}})
	if !errors.Is(rollbackErr, store.ErrInvalid) {
		t.Fatalf("invalid SQL transition error = %v", rollbackErr)
	}
	if _, err := repository.GetIncident(ctx, rollbackIncidentID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rolled-back incident lookup = %v", err)
	}
	if _, err := repository.GetAlertEvaluation(ctx, lineageID, entityID+"-rollback"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rolled-back evaluation lookup = %v", err)
	}
}

func TestSQLIncidentPolicyChangesCloseEpisodesAndEnforceAdmissionCap(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	start := time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)
	clock := alerts.NewManualClock(start)

	siteA, err := repository.CreateSite(ctx, store.Site{ID: "sql-incident-site-a-" + store.NewID(), Name: "Incident site A"})
	if err != nil {
		t.Fatal(err)
	}
	siteB, err := repository.CreateSite(ctx, store.Site{ID: "sql-incident-site-b-" + store.NewID(), Name: "Incident site B"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{ID: "sql-incident-device-" + store.NewID(), SiteID: siteA.ID, DisplayName: "Incident device"})
	if err != nil {
		t.Fatal(err)
	}

	cleanupIDs := []string{siteA.ID, siteB.ID, device.ID}
	ruleIDs := []string{}
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		for _, ruleID := range ruleIDs {
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_overrides WHERE lineage_id = $1`, ruleID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_evaluations WHERE lineage_id = $1`, ruleID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_work WHERE lineage_id = $1`, ruleID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM incidents WHERE lineage_id = $1`, ruleID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_rules WHERE id = $1`, ruleID)
		}
		for _, id := range cleanupIDs {
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM devices WHERE id = $1`, id)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM sites WHERE id = $1`, id)
		}
	}
	t.Cleanup(cleanup)

	t.Run("rule target change", func(t *testing.T) {
		ruleID := "sql-incident-target-rule-" + store.NewID()
		ruleIDs = append(ruleIDs, ruleID)
		created, err := repository.PutAlertRule(ctx, sqlIncidentRule(ruleID, "fleet", ""), 0)
		if err != nil {
			t.Fatal(err)
		}
		incident := persistSQLActiveIncident(t, ctx, repository, clock, ruleID, created.Revision, "target-rule-entity-"+store.NewID(), device.ID)

		changed := created
		changed.TargetKind = "site"
		changed.TargetID = siteA.ID
		if _, err := repository.PutAlertRule(ctx, changed, created.Revision); err != nil {
			t.Fatal(err)
		}
		assertSQLAdministrativeClosure(t, ctx, repository, incident, "rule_changed")
	})

	t.Run("rule disable", func(t *testing.T) {
		ruleID := "sql-incident-disabled-rule-" + store.NewID()
		ruleIDs = append(ruleIDs, ruleID)
		created, err := repository.PutAlertRule(ctx, sqlIncidentRule(ruleID, "fleet", ""), 0)
		if err != nil {
			t.Fatal(err)
		}
		incident := persistSQLActiveIncident(t, ctx, repository, clock, ruleID, created.Revision, "disabled-rule-entity-"+store.NewID(), device.ID)

		changed := created
		changed.Enabled = false
		if _, err := repository.PutAlertRule(ctx, changed, created.Revision); err != nil {
			t.Fatal(err)
		}
		assertSQLAdministrativeClosure(t, ctx, repository, incident, "disabled")
	})

	t.Run("rule retirement", func(t *testing.T) {
		ruleID := "sql-incident-retired-rule-" + store.NewID()
		ruleIDs = append(ruleIDs, ruleID)
		created, err := repository.PutAlertRule(ctx, sqlIncidentRule(ruleID, "fleet", ""), 0)
		if err != nil {
			t.Fatal(err)
		}
		incident := persistSQLActiveIncident(t, ctx, repository, clock, ruleID, created.Revision, "retired-rule-entity-"+store.NewID(), device.ID)

		if _, err := repository.RetireAlertRule(ctx, ruleID, created.Revision); err != nil {
			t.Fatal(err)
		}
		assertSQLAdministrativeClosure(t, ctx, repository, incident, "retired")
	})

	t.Run("target membership change", func(t *testing.T) {
		ruleID := "sql-incident-membership-rule-" + store.NewID()
		ruleIDs = append(ruleIDs, ruleID)
		targeted := sqlIncidentRule(ruleID, "site", siteA.ID)
		created, err := repository.PutAlertRule(ctx, targeted, 0)
		if err != nil {
			t.Fatal(err)
		}
		incident := persistSQLActiveIncident(t, ctx, repository, clock, ruleID, created.Revision, "membership-entity-"+store.NewID(), device.ID)

		if _, err := repository.UpdateDevice(ctx, device.ID, func(item *store.Device) error {
			item.SiteID = siteB.ID
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		assertSQLAdministrativeClosure(t, ctx, repository, incident, "administrative")
	})

	t.Run("device decommission", func(t *testing.T) {
		decommissioned, err := repository.CreateDevice(ctx, store.Device{ID: "sql-incident-decommissioned-" + store.NewID(), SiteID: siteA.ID, DisplayName: "Decommissioned incident device"})
		if err != nil {
			t.Fatal(err)
		}
		cleanupIDs = append(cleanupIDs, decommissioned.ID)
		ruleID := "sql-incident-decommission-rule-" + store.NewID()
		ruleIDs = append(ruleIDs, ruleID)
		created, err := repository.PutAlertRule(ctx, sqlIncidentRule(ruleID, "fleet", ""), 0)
		if err != nil {
			t.Fatal(err)
		}
		incident := persistSQLActiveIncident(t, ctx, repository, clock, ruleID, created.Revision, "decommission-entity-"+store.NewID(), decommissioned.ID)

		if _, err := repository.DecommissionDevice(ctx, decommissioned.ID, "integration test retirement"); err != nil {
			t.Fatal(err)
		}
		assertSQLAdministrativeClosure(t, ctx, repository, incident, "decommissioned")
	})

	t.Run("active incident admission cap", func(t *testing.T) {
		prefix := "sql-incident-cap-" + store.NewID() + "-"
		var existing int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status='active'`).Scan(&existing); err != nil {
			t.Fatal(err)
		}
		remaining := store.MaxActiveIncidents - existing
		if remaining <= 0 {
			t.Fatalf("fixture database already has %d active incidents", existing)
		}
		capTime := start.Add(time.Minute)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO incidents(id, lineage_id, entity_id, device_id, rule_revision, rule_snapshot, severity, status, evidence_state, value, unit, source, opened_at, observed_at, evaluated_at, acknowledged_at, acknowledged_by, closed_at, close_reason, revision)
			SELECT $1 || n::text, $1 || 'lineage-' || n::text, $1 || 'entity-' || n::text, '', 1, '{}'::jsonb, 'warning', 'active', 'fresh', NULL, '', '', $2, $2, $2, NULL, '', NULL, '', 1
			FROM generate_series(1, $3) AS series(n)`, prefix, capTime, remaining); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cleanupCancel()
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM incidents WHERE id LIKE $1 || '%'`, prefix)
		})

		lineageID := prefix + "rejected-lineage"
		entityID := prefix + "rejected-entity"
		rule := alerts.Rule{ID: lineageID, LineageID: lineageID, Revision: 1, Kind: alerts.RuleKindNumeric, Metric: "cpu.utilization", Operator: alerts.OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, Severity: alerts.SeverityWarning, Enabled: true}
		evaluator := alerts.NewPersistentEvaluator(repository, clock, "sql-cap-worker")
		_, err := evaluator.EvaluateAndPersist(ctx, rule, numericIncidentObservation(entityID, rule.Metric, 95, capTime), "", nil)
		if !errors.Is(err, store.ErrIncidentCap) {
			t.Fatalf("active incident cap error = %v", err)
		}
		if _, err := repository.GetAlertEvaluation(ctx, lineageID, entityID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("cap rejection left an evaluation checkpoint: %v", err)
		}
		var active int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status='active'`).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active != store.MaxActiveIncidents {
			t.Fatalf("active incidents after cap rejection = %d, want %d", active, store.MaxActiveIncidents)
		}
	})
}

func sqlIncidentRule(id, targetKind, targetID string) store.AlertRule {
	trigger, clear := 90.0, 85.0
	return store.AlertRule{
		ID: id, Name: "SQL incident fixture", Kind: "numeric", Metric: "cpu.utilization", Operator: "gt",
		TriggerValue: &trigger, ClearValue: &clear, MinimumConsecutiveSamples: 1, Severity: "warning",
		TargetKind: targetKind, TargetID: targetID, Enabled: true,
	}
}

func persistSQLActiveIncident(t *testing.T, ctx context.Context, repository *store.Store, clock *alerts.ManualClock, lineageID string, revision int64, entityID, deviceID string) store.Incident {
	t.Helper()
	rule := alerts.Rule{ID: lineageID, LineageID: lineageID, Revision: revision, Kind: alerts.RuleKindNumeric, Metric: "cpu.utilization", Operator: alerts.OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, Severity: alerts.SeverityWarning, Enabled: true}
	evaluator := alerts.NewPersistentEvaluator(repository, clock, "sql-policy-worker-"+store.NewID())
	if _, err := evaluator.EvaluateAndPersist(ctx, rule, numericIncidentObservation(entityID, rule.Metric, 95, clock.Now()), deviceID, nil); err != nil {
		t.Fatal(err)
	}
	evaluation, err := repository.GetAlertEvaluation(ctx, lineageID, entityID)
	if err != nil {
		t.Fatal(err)
	}
	incident, err := repository.GetIncident(ctx, evaluation.IncidentID)
	if err != nil {
		t.Fatal(err)
	}
	if incident.Status != "active" {
		t.Fatalf("fixture incident status = %s", incident.Status)
	}
	return incident
}

func assertSQLAdministrativeClosure(t *testing.T, ctx context.Context, repository *store.Store, incident store.Incident, reason string) {
	t.Helper()
	closed, err := repository.GetIncident(ctx, incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" || closed.CloseReason != reason || closed.ClosedAt == nil || closed.Revision <= incident.Revision {
		t.Fatalf("administrative closure = %+v, want closed/%s", closed, reason)
	}
	transitions, err := repository.ListIncidentTransitions(ctx, incident.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 2 {
		t.Fatalf("administrative closure transitions = %+v", transitions)
	}
	last := transitions[len(transitions)-1]
	if last.Kind != "administrative_close" || last.Reason != reason || last.Actor != "system" || last.Sequence != 2 {
		t.Fatalf("administrative closure transition = %+v", last)
	}
	evaluation, err := repository.GetAlertEvaluation(ctx, incident.LineageID, incident.EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.IncidentID != "" || evaluation.EvidenceState != "unknown" || evaluation.PendingSince != nil || evaluation.RecoverySince != nil || evaluation.TriggerConsecutive != 0 || evaluation.RecoveryConsecutive != 0 {
		t.Fatalf("administrative closure left evaluator state = %+v", evaluation)
	}
}

func numericIncidentObservation(entityID, metric string, value float64, observedAt time.Time) alerts.Observation {
	return alerts.Observation{EntityID: entityID, Metric: metric, Value: &value, Evidence: alerts.EvidenceFresh, ObservedAt: observedAt, ReceivedAt: observedAt}
}
