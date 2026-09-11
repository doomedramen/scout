package alerts

import (
	"testing"
	"time"
)

func TestNumericStrictBoundariesAndHysteresis(t *testing.T) {
	start := time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	evaluator := NewEvaluator(clock)
	rule := Rule{LineageID: "cpu", Revision: 1, Kind: RuleKindNumeric, Metric: "cpu.utilization", Operator: OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, TriggerFor: 5 * time.Minute, ClearFor: 2 * time.Minute, MaxEvidenceAge: 3 * time.Minute, Severity: "warning", Enabled: true}

	if result := evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 90, start)); len(result.Transitions) != 0 || result.State.PendingSince != nil {
		t.Fatalf("equal trigger boundary advanced evaluation: %+v", result)
	}
	clock.Advance(time.Minute)
	if result := evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 90.1, clock.Now())); len(result.Transitions) != 0 || result.State.PendingSince == nil {
		t.Fatalf("trigger did not start above strict boundary: %+v", result)
	}
	for range 4 {
		clock.Advance(time.Minute)
		result := evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 90.1, clock.Now()))
		if len(result.Transitions) != 0 {
			t.Fatalf("incident opened before five continuous minutes: %+v", result.Transitions)
		}
	}
	clock.Advance(time.Minute)
	opened := evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 90.1, clock.Now()))
	if len(opened.Transitions) != 1 || opened.Transitions[0].Kind != TransitionTriggered || opened.State.ActiveIncident == nil {
		t.Fatalf("incident did not open after trigger duration: %+v", opened)
	}

	clock.Advance(time.Minute)
	if result := evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 85, clock.Now())); result.State.RecoverySince != nil {
		t.Fatalf("equal clear boundary started recovery: %+v", result)
	}
	clock.Advance(time.Minute)
	if result := evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 84.9, clock.Now())); result.State.RecoverySince == nil {
		t.Fatalf("value below clear boundary did not start recovery: %+v", result)
	}
	clock.Advance(time.Minute)
	between := evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 86, clock.Now()))
	if between.State.ActiveIncident == nil || between.State.RecoverySince != nil {
		t.Fatalf("hysteresis midpoint incorrectly recovered incident: %+v", between)
	}
	clock.Advance(time.Minute)
	evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 84.9, clock.Now()))
	clock.Advance(2 * time.Minute)
	closed := evaluator.Evaluate(rule, numericObservation("host", "cpu.utilization", 84.9, clock.Now()))
	if len(closed.Transitions) != 1 || closed.Transitions[0].Kind != TransitionRecovered || closed.State.ActiveIncident != nil || closed.State.LastIncident == nil || closed.State.LastIncident.Status != IncidentResolved {
		t.Fatalf("incident did not recover after clear duration: %+v", closed)
	}
}

func TestAcknowledgmentDoesNotRecover(t *testing.T) {
	start := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	evaluator := NewEvaluator(clock)
	rule := Rule{LineageID: "memory", Revision: 1, Kind: RuleKindNumeric, Metric: "memory.used_percent", Operator: OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, TriggerFor: 0, ClearFor: time.Minute, Enabled: true}

	opened := evaluator.Evaluate(rule, numericObservation("host", "memory.used_percent", 95, start))
	if opened.State.ActiveIncident == nil {
		t.Fatalf("incident did not open: %+v", opened)
	}
	acknowledged := evaluator.Acknowledge(rule, "host", "owner-1")
	if len(acknowledged.Transitions) != 1 || acknowledged.Transitions[0].Kind != TransitionAcknowledged || acknowledged.State.ActiveIncident == nil || acknowledged.State.ActiveIncident.AcknowledgedBy != "owner-1" {
		t.Fatalf("acknowledgment changed incident incorrectly: %+v", acknowledged)
	}
	repeated := evaluator.Acknowledge(rule, "host", "owner-1")
	if len(repeated.Transitions) != 0 || repeated.State.ActiveIncident == nil {
		t.Fatalf("repeated acknowledgment was not idempotent: %+v", repeated)
	}
	clock.Advance(time.Minute)
	recovery := evaluator.Evaluate(rule, numericObservation("host", "memory.used_percent", 84, clock.Now()))
	if recovery.State.ActiveIncident == nil || recovery.State.RecoverySince == nil {
		t.Fatalf("acknowledgment incorrectly established recovery: %+v", recovery)
	}
}

func TestStateRulesRequireConsecutiveFreshEvidence(t *testing.T) {
	start := time.Date(2026, 9, 11, 17, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	evaluator := NewEvaluator(clock)
	rule := Rule{LineageID: "service-failed", Revision: 1, Kind: RuleKindState, TriggerState: "failed", ClearState: "active", TriggerFor: 0, ClearFor: 0, MinimumConsecutiveSamples: 2, MaxEvidenceAge: time.Minute, Enabled: true}

	first := evaluator.Evaluate(rule, stateObservation("unit-x", "failed", start))
	if first.State.ActiveIncident != nil || first.State.TriggerConsecutive != 1 {
		t.Fatalf("one failed state sample triggered: %+v", first)
	}
	clock.Advance(10 * time.Second)
	second := evaluator.Evaluate(rule, stateObservation("unit-x", "failed", clock.Now()))
	if second.State.ActiveIncident == nil || len(second.Transitions) != 1 || second.Transitions[0].Kind != TransitionTriggered {
		t.Fatalf("two consecutive failed state samples did not trigger: %+v", second)
	}
	clock.Advance(10 * time.Second)
	unknown := stateObservation("unit-x", "failed", clock.Now())
	unknown.Evidence = EvidenceUnsupported
	unsupported := evaluator.Evaluate(rule, unknown)
	if unsupported.State.ActiveIncident == nil || unsupported.State.Evidence != EvidenceUnsupported || unsupported.State.RecoverySince != nil {
		t.Fatalf("unsupported state evidence advanced recovery: %+v", unsupported)
	}
	clock.Advance(10 * time.Second)
	cleared := evaluator.Evaluate(rule, stateObservation("unit-x", "active", clock.Now()))
	if cleared.State.ActiveIncident == nil || cleared.State.RecoverySince == nil {
		t.Fatalf("one clear state sample resolved immediately: %+v", cleared)
	}
	clock.Advance(10 * time.Second)
	resolved := evaluator.Evaluate(rule, stateObservation("unit-x", "active", clock.Now()))
	if resolved.State.ActiveIncident != nil || len(resolved.Transitions) != 1 || resolved.Transitions[0].Kind != TransitionRecovered {
		t.Fatalf("state rule did not recover after two fresh clear samples: %+v", resolved)
	}
}

func TestDuplicateOutOfOrderGapAndRevisionEvidence(t *testing.T) {
	start := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	evaluator := NewEvaluator(clock)
	rule := Rule{LineageID: "filesystem", Revision: 1, Kind: RuleKindNumeric, Metric: "filesystem.used_percent", Operator: OperatorGreaterThan, TriggerValue: 90, ClearValue: 85, TriggerFor: time.Minute, ClearFor: time.Minute, MaxEvidenceAge: 30 * time.Second, Enabled: true}

	first := evaluator.Evaluate(rule, numericObservation("root", rule.Metric, 95, start))
	clock.Advance(20 * time.Second)
	duplicate := evaluator.Evaluate(rule, numericObservation("root", rule.Metric, 95, start))
	if !duplicate.Ignored || duplicate.State.PendingSince == nil || first.State.PendingSince == nil || !duplicate.State.PendingSince.Equal(*first.State.PendingSince) {
		t.Fatalf("duplicate observation changed timing state: first=%+v duplicate=%+v", first, duplicate)
	}
	outOfOrder := evaluator.Evaluate(rule, numericObservation("root", rule.Metric, 95, start.Add(-time.Second)))
	if !outOfOrder.Ignored || outOfOrder.State.LastObservedAt != start {
		t.Fatalf("out-of-order observation changed last evidence: %+v", outOfOrder)
	}
	clock.Advance(20 * time.Second)
	gap := evaluator.Tick(rule, "root")
	if gap.State.Evidence != EvidenceUnknown || gap.State.PendingSince != nil || gap.State.ActiveIncident != nil {
		t.Fatalf("gap advanced or fabricated an incident: %+v", gap)
	}
	clock.Advance(time.Second)
	postGap := evaluator.Evaluate(rule, numericObservation("root", rule.Metric, 95, clock.Now()))
	if postGap.State.Evidence != EvidenceFresh || postGap.State.ActiveIncident != nil || postGap.State.PendingSince == nil {
		t.Fatalf("post-gap sample inherited stale trigger timing: %+v", postGap)
	}
	clock.Advance(20 * time.Second)
	evaluator.Evaluate(rule, numericObservation("root", rule.Metric, 95, clock.Now()))
	clock.Advance(20 * time.Second)
	evaluator.Evaluate(rule, numericObservation("root", rule.Metric, 95, clock.Now()))
	clock.Advance(20 * time.Second)
	opened := evaluator.Evaluate(rule, numericObservation("root", rule.Metric, 95, clock.Now()))
	if opened.State.ActiveIncident == nil {
		t.Fatalf("post-gap fresh evidence did not restart timing: %+v", opened)
	}

	revised := rule
	revised.Revision = 2
	revised.TriggerFor = 5 * time.Minute
	clock.Advance(time.Second)
	changed := evaluator.Evaluate(revised, numericObservation("root", rule.Metric, 95, clock.Now()))
	if len(changed.Transitions) != 1 || changed.Transitions[0].Kind != TransitionRuleChanged || changed.State.ActiveIncident != nil || changed.State.LastIncident == nil || changed.State.LastIncident.CloseReason != "rule_changed" {
		t.Fatalf("rule revision did not close prior episode and reset timing: %+v", changed)
	}
	if changed.State.PendingSince == nil {
		t.Fatalf("revised rule did not start a new pending interval: %+v", changed)
	}
}

func numericObservation(entityID, metric string, value float64, observedAt time.Time) Observation {
	return Observation{EntityID: entityID, Metric: metric, Value: &value, Evidence: EvidenceFresh, ObservedAt: observedAt, ReceivedAt: observedAt}
}

func stateObservation(entityID, state string, observedAt time.Time) Observation {
	return Observation{EntityID: entityID, State: state, Evidence: EvidenceFresh, ObservedAt: observedAt, ReceivedAt: observedAt}
}
