package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestPersistentEvaluatorSweepsDirtyWorkAndPersistsOneIncident(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 11, 19, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	rule := Rule{ID: "cpu-high", LineageID: "cpu-high", Revision: 1, Kind: RuleKindNumeric, Metric: "cpu.utilization", Operator: OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, Severity: SeverityWarning, Enabled: true}
	if err := repository.MarkAlertWork(ctx, store.AlertWorkAllLineages, "host-1"); err != nil {
		t.Fatal(err)
	}

	evaluator := NewPersistentEvaluator(repository, clock, "alert-worker-1")
	result, err := evaluator.Sweep(ctx, func(context.Context, store.AlertWorkItem) ([]Rule, error) {
		return []Rule{rule}, nil
	}, func(context.Context, store.AlertWorkItem, Rule) (Observation, error) {
		return numericObservation("host-1", rule.Metric, 95, start), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 1 || result.Evaluated != 1 || result.Failed != 0 {
		t.Fatalf("sweep result = %+v", result)
	}

	persisted, err := repository.GetAlertEvaluation(ctx, rule.LineageID, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.IncidentID == "" || persisted.EvidenceState != string(EvidenceFresh) {
		t.Fatalf("evaluation = %+v", persisted)
	}
	incident, err := repository.GetIncident(ctx, persisted.IncidentID)
	if err != nil {
		t.Fatal(err)
	}
	if incident.Status != string(IncidentActive) || incident.RuleSnapshot["metric"] != rule.Metric {
		t.Fatalf("incident = %+v", incident)
	}
	transitions, err := repository.ListIncidentTransitions(ctx, incident.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 || transitions[0].Kind != string(TransitionTriggered) || transitions[0].Sequence != 1 {
		t.Fatalf("transitions = %+v", transitions)
	}
	if work, err := repository.ListAlertWork(ctx); err != nil {
		t.Fatal(err)
	} else if len(work) != 0 {
		t.Fatalf("completed work remains = %+v", work)
	}

	if err := repository.MarkAlertWork(ctx, store.AlertWorkAllLineages, "host-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.Sweep(ctx, func(context.Context, store.AlertWorkItem) ([]Rule, error) {
		return []Rule{rule}, nil
	}, func(context.Context, store.AlertWorkItem, Rule) (Observation, error) {
		return numericObservation("host-1", rule.Metric, 95, start), nil
	}); err != nil {
		t.Fatal(err)
	}
	transitions, err = repository.ListIncidentTransitions(ctx, incident.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 {
		t.Fatalf("duplicate sweep appended transitions = %+v", transitions)
	}
}

func TestPersistentEvaluatorRestartsThroughRecoveryAndUnknownEvidence(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	rule := Rule{ID: "memory-high", LineageID: "memory-high", Revision: 1, Kind: RuleKindNumeric, Metric: "memory.used_percent", Operator: OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, ClearFor: time.Minute, Severity: SeverityCritical, Enabled: true}
	evaluator := NewPersistentEvaluator(repository, clock, "alert-worker-1")
	if _, err := evaluator.EvaluateAndPersist(ctx, rule, numericObservation("host-1", rule.Metric, 95, start), "device-1", nil); err != nil {
		t.Fatal(err)
	}
	persisted, err := repository.GetAlertEvaluation(ctx, rule.LineageID, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	incidentID := persisted.IncidentID

	clock.Advance(time.Minute)
	restarted := NewPersistentEvaluator(repository, clock, "alert-worker-2")
	unknown := numericObservation("host-1", rule.Metric, 84, clock.Now())
	unknown.Evidence = EvidenceUnknown
	if _, err := restarted.EvaluateAndPersist(ctx, rule, unknown, "device-1", nil); err != nil {
		t.Fatal(err)
	}
	incident, err := repository.GetIncident(ctx, incidentID)
	if err != nil {
		t.Fatal(err)
	}
	if incident.Status != string(IncidentActive) || incident.EvidenceState != string(EvidenceUnknown) {
		t.Fatalf("unknown evidence changed incident = %+v", incident)
	}

	clock.Advance(time.Minute)
	if _, err := restarted.EvaluateAndPersist(ctx, rule, numericObservation("host-1", rule.Metric, 84, clock.Now()), "device-1", nil); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if _, err := restarted.EvaluateAndPersist(ctx, rule, numericObservation("host-1", rule.Metric, 84, clock.Now()), "device-1", nil); err != nil {
		t.Fatal(err)
	}
	incident, err = repository.GetIncident(ctx, incidentID)
	if err != nil {
		t.Fatal(err)
	}
	if incident.Status != string(IncidentResolved) {
		t.Fatalf("incident did not recover after fresh clear evidence = %+v", incident)
	}
	transitions, err := repository.ListIncidentTransitions(ctx, incidentID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 4 || transitions[1].Kind != string(TransitionEvidenceState) || transitions[2].Kind != string(TransitionEvidenceState) || transitions[3].Kind != string(TransitionRecovered) {
		t.Fatalf("restart transitions = %+v", transitions)
	}
}

func TestPersistentEvaluatorQueuesNotificationIntentWithIncident(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	repository.SetClock(func() time.Time { return clock.Now() })
	if _, err := repository.PutNotificationDestination(ctx, store.NotificationDestination{
		ID: "persistent-destination", Name: "Persistent destination", BaseURL: "https://ntfy.example.test", MaskedTopic: "sc********s", Enabled: true,
		SecretCiphertext: []byte("ciphertext"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}, 0); err != nil {
		t.Fatal(err)
	}
	rule := Rule{ID: "persistent-cpu", LineageID: "persistent-cpu", Revision: 1, Kind: RuleKindNumeric, Metric: "cpu.utilization", Operator: OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, Severity: SeverityCritical, Enabled: true}
	evaluator := NewPersistentEvaluator(repository, clock, "alert-worker")
	if _, err := evaluator.EvaluateAndPersist(ctx, rule, numericObservation("host-1", rule.Metric, 95, start), "device-1", nil); err != nil {
		t.Fatal(err)
	}
	persisted, err := repository.GetAlertEvaluation(ctx, rule.LineageID, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	page, err := repository.ListNotificationDeliveries(ctx, store.NotificationDeliveryQuery{IncidentID: persisted.IncidentID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Status != store.NotificationDeliveryQueued || page.Items[0].DestinationRevision != 1 {
		t.Fatalf("queued deliveries = %+v", page.Items)
	}
	if page.Items[0].Payload != nil || page.Items[0].LeaseOwner != "" {
		t.Fatalf("delivery history exposed private fields = %+v", page.Items[0])
	}

	if _, err := evaluator.EvaluateAndPersist(ctx, rule, numericObservation("host-1", rule.Metric, 95, start), "device-1", nil); err != nil {
		t.Fatal(err)
	}
	page, err = repository.ListNotificationDeliveries(ctx, store.NotificationDeliveryQuery{IncidentID: persisted.IncidentID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("duplicate delivery intent = %+v", page.Items)
	}
}

func TestAlertWorkKeepsNewerGenerationAfterLeaseCompletion(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	if err := repository.MarkAlertWork(ctx, "lineage", "entity"); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimAlertWork(ctx, "worker", 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed work = %+v", claimed)
	}
	if err := repository.MarkAlertWork(ctx, "lineage", "entity"); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteAlertWork(ctx, claimed[0], ""); err != nil {
		t.Fatal(err)
	}
	remaining, err := repository.ListAlertWork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].DirtyGeneration == claimed[0].DirtyGeneration || remaining[0].LeaseOwner != "" {
		t.Fatalf("newer generation was lost = %+v", remaining)
	}

	if _, err := repository.GetAlertEvaluation(ctx, "missing", "entity"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing evaluation error = %v", err)
	}
}

func TestPersistentEvaluatorUsesFifteenSecondDefaultSweep(t *testing.T) {
	evaluator := NewPersistentEvaluator(store.NewMemory(), nil, "worker")
	if evaluator.SweepInterval != DefaultSweepInterval {
		t.Fatalf("sweep interval = %s, want %s", evaluator.SweepInterval, DefaultSweepInterval)
	}
}
