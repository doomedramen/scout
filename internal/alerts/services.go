package alerts

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

const (
	SystemdCollectorID                 = "systemd"
	SystemdFailedRuleTemplateKey       = "systemd_failed"
	ServiceRequiredInactiveTemplateKey = "service_required_inactive"
	serviceStateMetric                 = "service.state"
	serviceStateUnit                   = "state"
	serviceFreshnessBudget             = 90 * time.Second
)

// ServiceObservation translates a stored service entity into the state
// observation consumed by the existing evaluator. Expired entities become an
// evidence tick rather than a new observation, so stale inventory cannot
// advance a trigger or falsely recover an incident.
func ServiceObservation(entity store.ServiceEntity, receivedAt time.Time) Observation {
	entityID := serviceEntityID(entity)
	observedAt := entity.ObservedAt.UTC()
	receivedAt = receivedAt.UTC()
	if receivedAt.IsZero() {
		receivedAt = observedAt
	}
	state := NormalizeServiceState(entity.Status)
	evidence := EvidenceFresh
	if state == "unavailable" {
		evidence = EvidenceUnsupported
	}
	if observedAt.IsZero() {
		evidence = EvidenceUnknown
	}
	if !entity.ExpiresAt.IsZero() && (receivedAt.IsZero() || !receivedAt.Before(entity.ExpiresAt)) {
		observedAt = time.Time{}
		evidence = EvidenceUnknown
	}
	return Observation{
		EntityID:   entityID,
		Metric:     serviceStateMetric,
		State:      state,
		Evidence:   evidence,
		Unit:       serviceStateUnit,
		Source:     strings.TrimSpace(entity.Provider),
		Labels:     cloneServiceLabels(entity.Labels),
		ObservedAt: observedAt,
		ReceivedAt: receivedAt,
	}
}

// ResolveServiceRules resolves owner rules for one device and retains only
// state rules that can act on the supplied service entity. Must-run rules are
// selected by the collector's bounded labels, not by a second service policy.
func ResolveServiceRules(rules []store.AlertRule, overrides []store.AlertOverride, device store.Device, entity store.ServiceEntity) ([]Rule, error) {
	if serviceEntityID(entity) == "" || strings.TrimSpace(entity.Provider) == "" {
		return nil, fmt.Errorf("%w: service identity", store.ErrInvalid)
	}
	effective := ResolveRules(rules, overrides, device)
	result := make([]Rule, 0, len(effective))
	for _, item := range effective {
		rule, err := item.EvaluatorRule()
		if err != nil {
			return nil, err
		}
		rule = normalizeServiceRule(rule)
		if !ServiceRuleApplies(rule, entity) {
			continue
		}
		result = append(result, rule)
	}
	sort.Slice(result, func(left, right int) bool {
		leftPriority := serviceRulePriority(result[left])
		rightPriority := serviceRulePriority(result[right])
		if leftPriority == rightPriority {
			return ruleLineageID(result[left]) < ruleLineageID(result[right])
		}
		return leftPriority < rightPriority
	})
	return result, nil
}

// ServiceRuleApplies reports whether a resolved state rule is valid for the
// entity's provider and current selection. A failed unit is deliberately not
// also an inactive must-run unit; this is the service-incident deduplication
// boundary.
func ServiceRuleApplies(rule Rule, entity store.ServiceEntity) bool {
	if rule.Kind != RuleKindState || strings.TrimSpace(entity.Provider) == "" {
		return false
	}
	provider := strings.TrimSpace(entity.Provider)
	if rule.CollectorID != "" && !strings.EqualFold(strings.TrimSpace(rule.CollectorID), provider) {
		return false
	}
	serviceName := serviceEntityName(entity)
	if rule.ServicePattern != "" && !ServicePatternMatches(rule.ServicePattern, serviceName) {
		return false
	}

	switch canonicalServiceRuleState(rule.TriggerState) {
	case "failed":
		if rule.TemplateKey == SystemdFailedRuleTemplateKey || rule.CollectorID == "" {
			return strings.EqualFold(provider, SystemdCollectorID)
		}
		return true
	case "inactive":
		state := NormalizeServiceState(entity.Status)
		if state == "failed" || state != "active" && !serviceMustRun(entity) {
			return false
		}
		return strings.EqualFold(provider, SystemdCollectorID) || rule.CollectorID != ""
	default:
		return rule.CollectorID != "" || rule.ServicePattern != ""
	}
}

// EvaluateServiceEntity sends a single service observation through the
// durable evaluator. Passing the entity's device ID is what activates the
// existing host-offline notification suppression decision.
func (p *PersistentEvaluator) EvaluateServiceEntity(ctx context.Context, rule Rule, entity store.ServiceEntity) (EvaluationResult, error) {
	rule = normalizeServiceRule(rule)
	if !ServiceRuleApplies(rule, entity) {
		return EvaluationResult{Ignored: true, State: EvaluationState{LineageID: ruleLineageID(rule), EntityID: serviceEntityID(entity), RuleRevision: rule.Revision, Evidence: EvidenceUnknown}}, nil
	}
	now := time.Now().UTC()
	if p != nil && p.Clock != nil {
		now = p.Clock.Now().UTC()
	}
	return p.EvaluateAndPersist(ctx, rule, ServiceObservation(entity, now), entity.DeviceID, nil)
}

// EvaluateServiceRules evaluates one inventory snapshot in deterministic
// priority order. If a failed-unit rule remains active, the selected
// must-run rule is skipped so a state change cannot create two incidents for
// the same service fault.
func (p *PersistentEvaluator) EvaluateServiceRules(ctx context.Context, entity store.ServiceEntity, rules []Rule) ([]EvaluationResult, error) {
	ordered := append([]Rule(nil), rules...)
	sort.SliceStable(ordered, func(left, right int) bool {
		leftPriority := serviceRulePriority(ordered[left])
		rightPriority := serviceRulePriority(ordered[right])
		if leftPriority == rightPriority {
			return ruleLineageID(ordered[left]) < ruleLineageID(ordered[right])
		}
		return leftPriority < rightPriority
	})
	results := make([]EvaluationResult, 0, len(ordered))
	failedIncidentActive := false
	for _, candidate := range ordered {
		rule := normalizeServiceRule(candidate)
		if !ServiceRuleApplies(rule, entity) {
			continue
		}
		if serviceRulePriority(rule) > 0 && failedIncidentActive {
			continue
		}
		result, err := p.EvaluateServiceEntity(ctx, rule, entity)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
		if serviceRulePriority(rule) == 0 && result.State.ActiveIncident != nil {
			failedIncidentActive = true
		}
	}
	return results, nil
}

// NormalizeServiceState keeps collector state names bounded and stable for
// rule evaluation. API aliases are accepted here so stored owner rules and
// collector-native states share one evaluator representation.
func NormalizeServiceState(value string) string {
	switch canonicalServiceRuleState(value) {
	case "active", "inactive", "failed", "transitional", "unavailable":
		return canonicalServiceRuleState(value)
	default:
		return "unavailable"
	}
}

func normalizeServiceRule(rule Rule) Rule {
	rule.TriggerState = canonicalServiceRuleState(rule.TriggerState)
	rule.ClearState = canonicalServiceRuleState(rule.ClearState)
	if rule.CollectorID != "" || rule.TemplateKey == SystemdFailedRuleTemplateKey || rule.TemplateKey == ServiceRequiredInactiveTemplateKey || rule.TriggerState == "failed" || rule.TriggerState == "inactive" {
		if rule.MaxEvidenceAge <= 0 {
			rule.MaxEvidenceAge = serviceFreshnessBudget
		}
	}
	if rule.TemplateKey == SystemdFailedRuleTemplateKey && rule.MinimumConsecutiveSamples == 2 {
		rule.MinimumConsecutiveTriggerSamples = 1
		rule.MinimumConsecutiveClearSamples = 2
	}
	return rule
}

func canonicalServiceRuleState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "service_failed":
		return "failed"
	case "service_required_inactive":
		return "inactive"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func serviceRuleMatchesObservation(rule Rule, observation Observation) bool {
	if rule.CollectorID != "" && !strings.EqualFold(strings.TrimSpace(rule.CollectorID), strings.TrimSpace(observation.Source)) {
		return false
	}
	if rule.ServicePattern == "" {
		return true
	}
	serviceName := observation.EntityID
	if observation.Labels != nil && strings.TrimSpace(observation.Labels["unit"]) != "" {
		serviceName = observation.Labels["unit"]
	}
	return ServicePatternMatches(rule.ServicePattern, serviceName)
}

// ServicePatternMatches implements only the contract's '*' and '?' glob
// operators. It deliberately has no path expansion or regular-expression
// behavior.
func ServicePatternMatches(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	previous := make([]bool, len(value)+1)
	previous[0] = true
	for _, token := range pattern {
		current := make([]bool, len(value)+1)
		switch token {
		case '*':
			current[0] = previous[0]
			for index := 1; index <= len(value); index++ {
				current[index] = current[index-1] || previous[index]
			}
		case '?':
			for index := 1; index <= len(value); index++ {
				current[index] = previous[index-1]
			}
		default:
			for index := 1; index <= len(value); index++ {
				current[index] = previous[index-1] && value[index-1] == byte(token)
			}
		}
		previous = current
	}
	return previous[len(value)]
}

func serviceRulePriority(rule Rule) int {
	if canonicalServiceRuleState(rule.TriggerState) == "inactive" {
		return 1
	}
	return 0
}

func serviceEntityID(entity store.ServiceEntity) string {
	if value := strings.TrimSpace(entity.ID); value != "" {
		return value
	}
	return strings.TrimSpace(entity.Name)
}

func serviceEntityName(entity store.ServiceEntity) string {
	if value := strings.TrimSpace(entity.Labels["unit"]); value != "" {
		return value
	}
	if value := strings.TrimSpace(entity.Name); value != "" {
		return value
	}
	return serviceEntityID(entity)
}

func serviceMustRun(entity store.ServiceEntity) bool {
	return strings.EqualFold(strings.TrimSpace(entity.Labels["mustRun"]), "true")
}

func cloneServiceLabels(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
