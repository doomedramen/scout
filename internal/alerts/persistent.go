package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"scout.local/scout/internal/store"
)

const DefaultSweepInterval = 15 * time.Second

type RuleResolver func(context.Context, store.AlertWorkItem) ([]Rule, error)

type ObservationResolver func(context.Context, store.AlertWorkItem, Rule) (Observation, error)

type SweepResult struct {
	Claimed   int
	Evaluated int
	Failed    int
}

// PersistentEvaluator coordinates short-lived evaluator instances with the
// durable state machine. Rebuilding the pure evaluator for each operation
// makes a process restart equivalent to a normal worker retry.
type PersistentEvaluator struct {
	Store         *store.Store
	Clock         Clock
	WorkerID      string
	LeaseDuration time.Duration
	SweepInterval time.Duration
	WorkLimit     int
}

func NewPersistentEvaluator(repository *store.Store, clock Clock, workerID string) *PersistentEvaluator {
	if clock == nil {
		clock = wallClock{}
	}
	if workerID == "" {
		workerID = "alerts-1"
	}
	return &PersistentEvaluator{
		Store:         repository,
		Clock:         clock,
		WorkerID:      workerID,
		LeaseDuration: 30 * time.Second,
		SweepInterval: DefaultSweepInterval,
		WorkLimit:     100,
	}
}

// EvaluateAndPersist applies one observation or a freshness tick. The store
// commits the checkpoint, incident snapshot, and transition append together.
func (p *PersistentEvaluator) EvaluateAndPersist(ctx context.Context, rule Rule, observation Observation, deviceID string, ruleSnapshot map[string]any) (EvaluationResult, error) {
	if p == nil || p.Store == nil {
		return EvaluationResult{}, fmt.Errorf("persistent evaluator store is nil")
	}
	entityID := observation.EntityID
	if entityID == "" {
		entityID = rule.EntityID
	}
	if entityID == "" {
		return EvaluationResult{}, fmt.Errorf("%w: entity id", store.ErrInvalid)
	}
	if ruleSnapshot == nil {
		ruleSnapshot = snapshotRule(rule)
	}

	previous, err := p.Store.GetAlertEvaluation(ctx, ruleLineageID(rule), entityID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return EvaluationResult{}, err
	}
	state := evaluationStateFromStore(previous, entityID)
	if previous.IncidentID != "" {
		persistedIncident, incidentErr := p.Store.GetIncident(ctx, previous.IncidentID)
		if incidentErr != nil && !errors.Is(incidentErr, store.ErrNotFound) {
			return EvaluationResult{}, incidentErr
		}
		if incidentErr == nil {
			incident := incidentFromStore(persistedIncident)
			state.LastIncident = incident
			if persistedIncident.Status == string(IncidentActive) {
				state.ActiveIncident = incident
			}
		}
	}

	evaluator := NewEvaluator(p.Clock)
	evaluator.SetIncidentIDGenerator(store.NewID)
	evaluator.Restore(rule, entityID, state)
	var result EvaluationResult
	if observation.ObservedAt.IsZero() {
		result = evaluator.Tick(rule, entityID)
	} else {
		result = evaluator.Evaluate(rule, observation)
	}

	durableEvaluation := evaluationToStore(result.State, ruleLineageID(rule), entityID, p.Clock.Now().UTC())
	var durableIncident *store.Incident
	if result.State.ActiveIncident != nil {
		durableIncident = incidentToStore(result.State.ActiveIncident, deviceID, ruleSnapshot)
	} else if len(result.Transitions) > 0 && result.State.LastIncident != nil {
		durableIncident = incidentToStore(result.State.LastIncident, deviceID, ruleSnapshot)
	}
	durableTransitions := transitionsToStore(result.Transitions)
	if err := p.Store.ApplyAlertEvaluation(ctx, durableEvaluation, durableIncident, durableTransitions); err != nil {
		return EvaluationResult{}, err
	}
	return result, nil
}

// Sweep claims coalesced telemetry work, evaluates the current effective
// rules, and leaves newer work queued when telemetry arrives mid-evaluation.
func (p *PersistentEvaluator) Sweep(ctx context.Context, resolve RuleResolver, observe ObservationResolver) (SweepResult, error) {
	if p == nil || p.Store == nil || resolve == nil || observe == nil {
		return SweepResult{}, fmt.Errorf("%w: evaluator sweep dependencies", store.ErrInvalid)
	}
	claimed, err := p.Store.ClaimAlertWork(ctx, p.WorkerID, p.WorkLimit, p.LeaseDuration)
	if err != nil {
		return SweepResult{}, err
	}
	result := SweepResult{Claimed: len(claimed)}
	var firstErr error
	for _, work := range claimed {
		rules, resolveErr := resolve(ctx, work)
		if resolveErr == nil {
			for _, rule := range rules {
				if work.LineageID != store.AlertWorkAllLineages && ruleLineageID(rule) != work.LineageID {
					continue
				}
				observation, observeErr := observe(ctx, work, rule)
				if errors.Is(observeErr, store.ErrNotFound) {
					observation = Observation{EntityID: work.EntityID}
					observeErr = nil
				}
				if observeErr != nil {
					resolveErr = observeErr
					break
				}
				if _, evaluateErr := p.EvaluateAndPersist(ctx, rule, observation, "", nil); evaluateErr != nil {
					resolveErr = evaluateErr
					break
				}
				result.Evaluated++
			}
		}
		lastError := ""
		if resolveErr != nil {
			result.Failed++
			lastError = resolveErr.Error()
			if firstErr == nil {
				firstErr = resolveErr
			}
		}
		if completeErr := p.Store.CompleteAlertWork(ctx, work, lastError); completeErr != nil {
			result.Failed++
			if firstErr == nil {
				firstErr = completeErr
			}
		}
	}
	return result, firstErr
}

// Run performs an immediate sweep and then repeats on the specified 15-second
// cadence until the process is stopped.
func (p *PersistentEvaluator) Run(ctx context.Context, resolve RuleResolver, observe ObservationResolver) error {
	if _, err := p.Sweep(ctx, resolve, observe); err != nil {
		return err
	}
	interval := p.SweepInterval
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := p.Sweep(ctx, resolve, observe); err != nil {
				return err
			}
		}
	}
}

func evaluationStateFromStore(item store.AlertEvaluation, entityID string) EvaluationState {
	result := EvaluationState{LineageID: item.LineageID, EntityID: entityID, RuleRevision: item.EffectiveRevision, Evidence: EvidenceState(item.EvidenceState), TriggerConsecutive: item.TriggerConsecutive, RecoveryConsecutive: item.RecoveryConsecutive}
	if result.LineageID == "" {
		result.LineageID = item.LineageID
	}
	if result.Evidence == "" {
		result.Evidence = EvidenceUnknown
	}
	result.LastObservedAt = dereferenceTime(item.LastObservedAt)
	result.LastReceivedAt = dereferenceTime(item.LastReceivedAt)
	result.LastValidAt = dereferenceTime(item.LastValidAt)
	result.PendingSince = cloneTime(item.PendingSince)
	result.RecoverySince = cloneTime(item.RecoverySince)
	return result
}

func evaluationToStore(state EvaluationState, lineageID, entityID string, updatedAt time.Time) store.AlertEvaluation {
	result := store.AlertEvaluation{LineageID: lineageID, EntityID: entityID, EffectiveRevision: state.RuleRevision, EvidenceState: string(state.Evidence), TriggerConsecutive: state.TriggerConsecutive, RecoveryConsecutive: state.RecoveryConsecutive, UpdatedAt: updatedAt}
	result.LastObservedAt = timePointer(state.LastObservedAt)
	result.LastReceivedAt = timePointer(state.LastReceivedAt)
	result.LastValidAt = timePointer(state.LastValidAt)
	result.PendingSince = cloneTime(state.PendingSince)
	result.RecoverySince = cloneTime(state.RecoverySince)
	if state.ActiveIncident != nil {
		result.IncidentID = state.ActiveIncident.ID
	}
	return result
}

func incidentToStore(item *Incident, deviceID string, fallbackSnapshot map[string]any) *store.Incident {
	if item == nil {
		return nil
	}
	snapshot := cloneRuleSnapshot(item.RuleSnapshot)
	if snapshot == nil {
		snapshot = cloneRuleSnapshot(fallbackSnapshot)
	}
	return &store.Incident{ID: item.ID, LineageID: item.LineageID, EntityID: item.EntityID, DeviceID: deviceID, RuleRevision: item.RuleRevision, RuleSnapshot: snapshot, Severity: item.Severity, Status: string(item.Status), EvidenceState: string(item.Evidence), Value: cloneFloat(item.Value), Unit: item.Unit, Source: item.Source, OpenedAt: item.OpenedAt, ObservedAt: item.ObservedAt, EvaluatedAt: item.EvaluatedAt, AcknowledgedAt: cloneTime(item.AcknowledgedAt), AcknowledgedBy: item.AcknowledgedBy, ClosedAt: cloneTime(item.ClosedAt), CloseReason: item.CloseReason, Revision: 1}
}

func incidentFromStore(item store.Incident) *Incident {
	return &Incident{ID: item.ID, LineageID: item.LineageID, EntityID: item.EntityID, RuleRevision: item.RuleRevision, RuleSnapshot: cloneRuleSnapshot(item.RuleSnapshot), Severity: item.Severity, Status: IncidentStatus(item.Status), Evidence: EvidenceState(item.EvidenceState), Value: cloneFloat(item.Value), Unit: item.Unit, Source: item.Source, OpenedAt: item.OpenedAt, ObservedAt: item.ObservedAt, EvaluatedAt: item.EvaluatedAt, AcknowledgedAt: cloneTime(item.AcknowledgedAt), AcknowledgedBy: item.AcknowledgedBy, ClosedAt: cloneTime(item.ClosedAt), CloseReason: item.CloseReason}
}

func transitionsToStore(items []Transition) []store.IncidentTransition {
	result := make([]store.IncidentTransition, 0, len(items))
	for _, item := range items {
		var observedAt *time.Time
		if !item.ObservedAt.IsZero() {
			observedAt = timePointer(item.ObservedAt)
		}
		result = append(result, store.IncidentTransition{IncidentID: item.IncidentID, Kind: string(item.Kind), RuleRevision: item.RuleRevision, EvidenceState: string(item.Evidence), Reason: item.Reason, Value: cloneFloat(item.Value), ObservedAt: observedAt, OccurredAt: item.OccurredAt})
	}
	return result
}

func snapshotRule(rule Rule) map[string]any {
	return map[string]any{
		"id":                        rule.ID,
		"lineageId":                 ruleLineageID(rule),
		"revision":                  rule.Revision,
		"kind":                      string(rule.Kind),
		"metric":                    rule.Metric,
		"entityId":                  rule.EntityID,
		"operator":                  string(rule.Operator),
		"triggerValue":              rule.TriggerValue,
		"clearValue":                rule.ClearValue,
		"triggerState":              rule.TriggerState,
		"clearState":                rule.ClearState,
		"triggerSeconds":            int(rule.TriggerFor / time.Second),
		"clearSeconds":              int(rule.ClearFor / time.Second),
		"minimumConsecutiveSamples": rule.MinimumConsecutiveSamples,
		"severity":                  rule.Severity,
		"enabled":                   rule.Enabled,
	}
}

func cloneRuleSnapshot(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	result := value.UTC()
	return &result
}

func dereferenceTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}
