package alerts

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type RuleKind string

const (
	RuleKindNumeric = RuleKind("numeric")
	RuleKindState   = RuleKind("state")
)

type Operator string

const (
	OperatorGreaterThan = Operator("gt")
	OperatorLessThan    = Operator("lt")
)

type EvidenceState string

const (
	EvidenceFresh       = EvidenceState("fresh")
	EvidenceUnknown     = EvidenceState("unknown")
	EvidenceUnsupported = EvidenceState("unsupported")
)

type IncidentStatus string

const (
	IncidentActive   = IncidentStatus("active")
	IncidentResolved = IncidentStatus("resolved")
	IncidentClosed   = IncidentStatus("closed")
)

type TransitionKind string

const (
	TransitionTriggered     = TransitionKind("triggered")
	TransitionAcknowledged  = TransitionKind("acknowledged")
	TransitionRecovered     = TransitionKind("recovered")
	TransitionRuleChanged   = TransitionKind("rule_changed")
	TransitionRuleDisabled  = TransitionKind("rule_disabled")
	TransitionEvidenceState = TransitionKind("evidence_state")
)

type Rule struct {
	ID                        string
	LineageID                 string
	Revision                  int64
	Kind                      RuleKind
	Metric                    string
	EntityID                  string
	Operator                  Operator
	TriggerValue              float64
	ClearValue                float64
	TriggerState              string
	ClearState                string
	TriggerFor                time.Duration
	ClearFor                  time.Duration
	MinimumConsecutiveSamples int
	MaxEvidenceAge            time.Duration
	Severity                  string
	Enabled                   bool
}

type Observation struct {
	EntityID   string
	Metric     string
	Value      *float64
	State      string
	Evidence   EvidenceState
	Unit       string
	Source     string
	ObservedAt time.Time
	ReceivedAt time.Time
}

type Incident struct {
	ID             string
	LineageID      string
	EntityID       string
	RuleRevision   int64
	RuleSnapshot   map[string]any
	Severity       string
	Status         IncidentStatus
	Evidence       EvidenceState
	Value          *float64
	Unit           string
	Source         string
	OpenedAt       time.Time
	ObservedAt     time.Time
	EvaluatedAt    time.Time
	AcknowledgedAt *time.Time
	AcknowledgedBy string
	ClosedAt       *time.Time
	CloseReason    string
}

type Transition struct {
	Kind         TransitionKind
	IncidentID   string
	LineageID    string
	EntityID     string
	RuleRevision int64
	Evidence     EvidenceState
	Reason       string
	Value        *float64
	ObservedAt   time.Time
	OccurredAt   time.Time
}

type EvaluationState struct {
	LineageID           string
	EntityID            string
	RuleRevision        int64
	Evidence            EvidenceState
	LastObservedAt      time.Time
	LastReceivedAt      time.Time
	LastValidAt         time.Time
	PendingSince        *time.Time
	RecoverySince       *time.Time
	TriggerConsecutive  int
	RecoveryConsecutive int
	ActiveIncident      *Incident
	LastIncident        *Incident
}

type EvaluationResult struct {
	Ignored     bool
	State       EvaluationState
	Transitions []Transition
}

type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

// ManualClock makes duration and gap behavior deterministic in unit tests.
type ManualClock struct {
	mu  sync.RWMutex
	now time.Time
}

func NewManualClock(now time.Time) *ManualClock {
	return &ManualClock{now: now.UTC()}
}

func (c *ManualClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *ManualClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now.UTC()
	c.mu.Unlock()
}

func (c *ManualClock) Advance(by time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(by)
	c.mu.Unlock()
}

type Evaluator struct {
	mu           sync.Mutex
	clock        Clock
	states       map[string]*evaluationState
	nextIncident uint64
	incidentID   func() string
}

type evaluationState struct {
	lineageID           string
	entityID            string
	ruleRevision        int64
	evidence            EvidenceState
	lastObservedAt      time.Time
	lastReceivedAt      time.Time
	lastValidAt         time.Time
	pendingSince        *time.Time
	recoverySince       *time.Time
	triggerConsecutive  int
	recoveryConsecutive int
	activeIncident      *Incident
	lastIncident        *Incident
}

func NewEvaluator(clock Clock) *Evaluator {
	if clock == nil {
		clock = wallClock{}
	}
	return &Evaluator{clock: clock, states: map[string]*evaluationState{}}
}

// Restore hydrates one evaluator key from durable state before evaluating a
// newly received observation. The in-memory evaluator remains the source of
// timing semantics; the store is the source of restart-safe state.
func (e *Evaluator) Restore(rule Rule, entityID string, state EvaluationState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if entityID == "" {
		entityID = state.EntityID
	}
	if entityID == "" {
		entityID = rule.EntityID
	}
	key := evaluationKey(rule, entityID)
	item := &evaluationState{
		lineageID:           ruleLineageID(rule),
		entityID:            entityID,
		ruleRevision:        state.RuleRevision,
		evidence:            state.Evidence,
		lastObservedAt:      state.LastObservedAt,
		lastReceivedAt:      state.LastReceivedAt,
		lastValidAt:         state.LastValidAt,
		pendingSince:        cloneTime(state.PendingSince),
		recoverySince:       cloneTime(state.RecoverySince),
		triggerConsecutive:  state.TriggerConsecutive,
		recoveryConsecutive: state.RecoveryConsecutive,
		activeIncident:      cloneIncident(state.ActiveIncident),
		lastIncident:        cloneIncident(state.LastIncident),
	}
	if item.evidence == "" {
		item.evidence = EvidenceUnknown
	}
	e.states[key] = item
}

// SetIncidentIDGenerator lets durable callers use globally unique IDs while
// keeping deterministic in-memory IDs useful in unit tests.
func (e *Evaluator) SetIncidentIDGenerator(generator func() string) {
	e.mu.Lock()
	e.incidentID = generator
	e.mu.Unlock()
}

func (e *Evaluator) Evaluate(rule Rule, observation Observation) EvaluationResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.clock.Now().UTC()
	entityID := observation.EntityID
	if entityID == "" {
		entityID = rule.EntityID
	}
	key := evaluationKey(rule, entityID)
	state := e.states[key]
	if state == nil {
		state = &evaluationState{lineageID: ruleLineageID(rule), entityID: entityID, evidence: EvidenceUnknown}
		e.states[key] = state
	}
	transitions := make([]Transition, 0, 2)

	if state.ruleRevision != 0 && rule.Revision != state.ruleRevision {
		e.closeForRuleChange(state, rule, now, &transitions)
		state.lastObservedAt = time.Time{}
		state.lastReceivedAt = time.Time{}
		state.lastValidAt = time.Time{}
	}
	state.ruleRevision = rule.Revision
	state.lineageID = ruleLineageID(rule)
	state.entityID = entityID

	if !rule.Enabled {
		if state.activeIncident != nil {
			e.closeIncident(state, rule, now, "rule_disabled", TransitionRuleDisabled, &transitions)
		}
		state.pendingSince = nil
		state.recoverySince = nil
		state.triggerConsecutive = 0
		state.recoveryConsecutive = 0
		state.evidence = EvidenceUnknown
		return e.result(state, false, transitions)
	}
	if observation.ObservedAt.IsZero() || !matchesRule(rule, observation) {
		return e.result(state, true, transitions)
	}
	observation.ObservedAt = observation.ObservedAt.UTC()
	observation.ReceivedAt = observation.ReceivedAt.UTC()
	if !state.lastObservedAt.IsZero() && !observation.ObservedAt.After(state.lastObservedAt) {
		return e.result(state, true, transitions)
	}
	if !state.lastObservedAt.IsZero() && rule.MaxEvidenceAge > 0 && observation.ObservedAt.Sub(state.lastObservedAt) > rule.MaxEvidenceAge {
		state.pendingSince = nil
		state.recoverySince = nil
		state.triggerConsecutive = 0
		state.recoveryConsecutive = 0
		state.evidence = EvidenceUnknown
	}
	state.lastObservedAt = observation.ObservedAt
	state.lastReceivedAt = observation.ReceivedAt

	evidence := observation.Evidence
	if evidence == "" {
		evidence = EvidenceFresh
	}
	if evidence != EvidenceFresh || (rule.Kind == RuleKindNumeric && observation.Value == nil) {
		if state.evidence != evidence && state.activeIncident != nil {
			transitions = append(transitions, Transition{Kind: TransitionEvidenceState, IncidentID: state.activeIncident.ID, LineageID: state.lineageID, EntityID: state.entityID, RuleRevision: rule.Revision, Evidence: evidence, OccurredAt: now})
		}
		if state.activeIncident != nil {
			state.activeIncident.Evidence = evidence
			state.activeIncident.EvaluatedAt = now
		}
		state.evidence = evidence
		state.pendingSince = nil
		state.recoverySince = nil
		state.triggerConsecutive = 0
		state.recoveryConsecutive = 0
		return e.result(state, false, transitions)
	}
	if state.evidence != EvidenceFresh && state.activeIncident != nil {
		transitions = append(transitions, Transition{Kind: TransitionEvidenceState, IncidentID: state.activeIncident.ID, LineageID: state.lineageID, EntityID: state.entityID, RuleRevision: rule.Revision, Evidence: EvidenceFresh, OccurredAt: now})
	}
	state.evidence = EvidenceFresh
	state.lastValidAt = observation.ObservedAt

	trigger, clear := conditions(rule, observation)
	minimum := rule.MinimumConsecutiveSamples
	if minimum < 1 {
		minimum = 1
	}
	if state.activeIncident != nil {
		state.activeIncident.Evidence = EvidenceFresh
		state.activeIncident.Value = cloneFloat(observation.Value)
		state.activeIncident.Unit = observation.Unit
		state.activeIncident.Source = observation.Source
		state.activeIncident.ObservedAt = observation.ObservedAt
		state.activeIncident.EvaluatedAt = now
		if clear {
			if state.recoverySince == nil {
				value := observation.ObservedAt
				state.recoverySince = &value
			}
			state.recoveryConsecutive++
			if durationReached(now, *state.recoverySince, rule.ClearFor) && state.recoveryConsecutive >= minimum {
				e.closeIncident(state, rule, now, "recovered", TransitionRecovered, &transitions)
			}
		} else {
			state.recoverySince = nil
			state.recoveryConsecutive = 0
		}
		return e.result(state, false, transitions)
	}
	if trigger {
		if state.pendingSince == nil {
			value := observation.ObservedAt
			state.pendingSince = &value
		}
		state.triggerConsecutive++
		if durationReached(now, *state.pendingSince, rule.TriggerFor) && state.triggerConsecutive >= minimum {
			e.openIncident(state, rule, observation, now, &transitions)
		}
	} else {
		state.pendingSince = nil
		state.triggerConsecutive = 0
	}
	state.recoverySince = nil
	state.recoveryConsecutive = 0
	return e.result(state, false, transitions)
}

// Tick marks pending timing evidence unknown once the configured freshness
// budget expires. It never advances a trigger or recovery without a fresh
// observation.
func (e *Evaluator) Tick(rule Rule, entityID string) EvaluationResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := evaluationKey(rule, entityID)
	state := e.states[key]
	if state == nil || state.lastObservedAt.IsZero() || rule.MaxEvidenceAge <= 0 {
		if state == nil {
			state = &evaluationState{lineageID: ruleLineageID(rule), entityID: entityID, ruleRevision: rule.Revision, evidence: EvidenceUnknown}
			e.states[key] = state
		}
		return e.result(state, false, nil)
	}
	now := e.clock.Now().UTC()
	if now.Sub(state.lastObservedAt) <= rule.MaxEvidenceAge {
		return e.result(state, false, nil)
	}
	transitions := make([]Transition, 0, 1)
	if state.evidence != EvidenceUnknown {
		if state.activeIncident != nil {
			transitions = append(transitions, Transition{Kind: TransitionEvidenceState, IncidentID: state.activeIncident.ID, LineageID: state.lineageID, EntityID: state.entityID, RuleRevision: rule.Revision, Evidence: EvidenceUnknown, Reason: "evidence_gap", OccurredAt: now})
		}
		state.evidence = EvidenceUnknown
	}
	state.pendingSince = nil
	state.recoverySince = nil
	state.triggerConsecutive = 0
	state.recoveryConsecutive = 0
	return e.result(state, false, transitions)
}

func (e *Evaluator) Acknowledge(rule Rule, entityID, actor string) EvaluationResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	state := e.states[evaluationKey(rule, entityID)]
	if state == nil || state.activeIncident == nil || state.activeIncident.AcknowledgedAt != nil {
		if state == nil {
			state = &evaluationState{lineageID: ruleLineageID(rule), entityID: entityID, ruleRevision: rule.Revision, evidence: EvidenceUnknown}
			e.states[evaluationKey(rule, entityID)] = state
		}
		return e.result(state, false, nil)
	}
	now := e.clock.Now().UTC()
	acknowledgedAt := now
	state.activeIncident.AcknowledgedAt = &acknowledgedAt
	state.activeIncident.AcknowledgedBy = actor
	transition := Transition{Kind: TransitionAcknowledged, IncidentID: state.activeIncident.ID, LineageID: state.lineageID, EntityID: state.entityID, RuleRevision: state.ruleRevision, Evidence: state.evidence, OccurredAt: now}
	return e.result(state, false, []Transition{transition})
}

func (e *Evaluator) State(rule Rule, entityID string) EvaluationState {
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.states[evaluationKey(rule, entityID)]
	if state == nil {
		return EvaluationState{LineageID: ruleLineageID(rule), EntityID: entityID, RuleRevision: rule.Revision, Evidence: EvidenceUnknown}
	}
	return e.snapshot(state)
}

func (e *Evaluator) openIncident(state *evaluationState, rule Rule, observation Observation, now time.Time, transitions *[]Transition) {
	incidentID := fmt.Sprintf("incident-%d", atomic.AddUint64(&e.nextIncident, 1))
	if e.incidentID != nil {
		incidentID = e.incidentID()
	}
	incident := &Incident{
		ID:           incidentID,
		LineageID:    state.lineageID,
		EntityID:     state.entityID,
		RuleRevision: rule.Revision,
		RuleSnapshot: snapshotRule(rule),
		Severity:     rule.Severity,
		Status:       IncidentActive,
		Evidence:     EvidenceFresh,
		Value:        cloneFloat(observation.Value),
		Unit:         observation.Unit,
		Source:       observation.Source,
		OpenedAt:     now,
		ObservedAt:   observation.ObservedAt,
		EvaluatedAt:  now,
	}
	state.activeIncident = incident
	state.lastIncident = incident
	state.pendingSince = nil
	state.triggerConsecutive = 0
	*transitions = append(*transitions, Transition{Kind: TransitionTriggered, IncidentID: incident.ID, LineageID: state.lineageID, EntityID: state.entityID, RuleRevision: rule.Revision, Evidence: EvidenceFresh, Value: cloneFloat(observation.Value), ObservedAt: observation.ObservedAt, OccurredAt: now})
}

func (e *Evaluator) closeIncident(state *evaluationState, rule Rule, now time.Time, reason string, kind TransitionKind, transitions *[]Transition) {
	if state.activeIncident == nil {
		return
	}
	incident := state.activeIncident
	incident.Status = IncidentResolved
	if kind == TransitionRuleChanged || kind == TransitionRuleDisabled {
		incident.Status = IncidentClosed
	}
	incident.ClosedAt = &now
	incident.CloseReason = reason
	incident.EvaluatedAt = now
	state.lastIncident = incident
	state.activeIncident = nil
	state.recoverySince = nil
	state.recoveryConsecutive = 0
	*transitions = append(*transitions, Transition{Kind: kind, IncidentID: incident.ID, LineageID: state.lineageID, EntityID: state.entityID, RuleRevision: rule.Revision, Evidence: state.evidence, Reason: reason, OccurredAt: now})
}

func (e *Evaluator) closeForRuleChange(state *evaluationState, rule Rule, now time.Time, transitions *[]Transition) {
	if state.activeIncident != nil {
		e.closeIncident(state, rule, now, "rule_changed", TransitionRuleChanged, transitions)
	}
	state.pendingSince = nil
	state.recoverySince = nil
	state.triggerConsecutive = 0
	state.recoveryConsecutive = 0
}

func (e *Evaluator) result(state *evaluationState, ignored bool, transitions []Transition) EvaluationResult {
	return EvaluationResult{Ignored: ignored, State: e.snapshot(state), Transitions: cloneTransitions(transitions)}
}

func (e *Evaluator) snapshot(state *evaluationState) EvaluationState {
	return EvaluationState{
		LineageID:           state.lineageID,
		EntityID:            state.entityID,
		RuleRevision:        state.ruleRevision,
		Evidence:            state.evidence,
		LastObservedAt:      state.lastObservedAt,
		LastReceivedAt:      state.lastReceivedAt,
		LastValidAt:         state.lastValidAt,
		PendingSince:        cloneTime(state.pendingSince),
		RecoverySince:       cloneTime(state.recoverySince),
		TriggerConsecutive:  state.triggerConsecutive,
		RecoveryConsecutive: state.recoveryConsecutive,
		ActiveIncident:      cloneIncident(state.activeIncident),
		LastIncident:        cloneIncident(state.lastIncident),
	}
}

func conditions(rule Rule, observation Observation) (bool, bool) {
	if rule.Kind == RuleKindState {
		return observation.State == rule.TriggerState, observation.State == rule.ClearState
	}
	if observation.Value == nil {
		return false, false
	}
	switch rule.Operator {
	case OperatorLessThan:
		return *observation.Value < rule.TriggerValue, *observation.Value > rule.ClearValue
	default:
		return *observation.Value > rule.TriggerValue, *observation.Value < rule.ClearValue
	}
}

func matchesRule(rule Rule, observation Observation) bool {
	if rule.EntityID != "" && observation.EntityID != "" && rule.EntityID != observation.EntityID {
		return false
	}
	if rule.Metric != "" && observation.Metric != rule.Metric {
		return false
	}
	return true
}

func durationReached(now, since time.Time, duration time.Duration) bool {
	return duration <= 0 || now.Sub(since) >= duration
}

func evaluationKey(rule Rule, entityID string) string {
	return ruleLineageID(rule) + "\x00" + entityID
}

func ruleLineageID(rule Rule) string {
	if rule.LineageID != "" {
		return rule.LineageID
	}
	return rule.ID
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func cloneIncident(value *Incident) *Incident {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Value = cloneFloat(value.Value)
	copy.RuleSnapshot = cloneRuleSnapshot(value.RuleSnapshot)
	copy.AcknowledgedAt = cloneTime(value.AcknowledgedAt)
	copy.ClosedAt = cloneTime(value.ClosedAt)
	return &copy
}

func cloneTransitions(values []Transition) []Transition {
	if len(values) == 0 {
		return nil
	}
	result := make([]Transition, len(values))
	copy(result, values)
	for index := range result {
		result[index].Value = cloneFloat(values[index].Value)
	}
	return result
}
