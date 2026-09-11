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

func numericIncidentObservation(entityID, metric string, value float64, observedAt time.Time) alerts.Observation {
	return alerts.Observation{EntityID: entityID, Metric: metric, Value: &value, Evidence: alerts.EvidenceFresh, ObservedAt: observedAt, ReceivedAt: observedAt}
}
