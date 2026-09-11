package alerts

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

const (
	TargetFleet  = "fleet"
	TargetSite   = "site"
	TargetDevice = "device"

	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

var (
	ErrInvalidRule     = errors.New("invalid alert rule")
	ErrInvalidOverride = errors.New("invalid alert override")
)

// DefaultRuleTemplates returns the owner-visible defaults for every enrolled
// host. Template keys make provisioning idempotent without overwriting edits.
func DefaultRuleTemplates(now time.Time) []store.AlertRule {
	now = now.UTC()
	return []store.AlertRule{
		{
			TemplateKey:               "host_offline",
			Name:                      "Host offline",
			Kind:                      string(RuleKindState),
			TriggerState:              "offline",
			ClearState:                "online",
			TriggerSeconds:            90,
			ClearSeconds:              0,
			MinimumConsecutiveSamples: 1,
			Severity:                  SeverityCritical,
			TargetKind:                TargetFleet,
			Enabled:                   true,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		},
		{
			TemplateKey:               "cpu_high",
			Name:                      "CPU utilization high",
			Kind:                      string(RuleKindNumeric),
			Metric:                    "cpu.utilization",
			Operator:                  string(OperatorGreaterThan),
			TriggerValue:              floatPointer(90),
			ClearValue:                floatPointer(85),
			TriggerSeconds:            300,
			ClearSeconds:              120,
			MinimumConsecutiveSamples: 1,
			Severity:                  SeverityWarning,
			TargetKind:                TargetFleet,
			Enabled:                   true,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		},
		{
			TemplateKey:               "memory_high",
			Name:                      "Memory utilization high",
			Kind:                      string(RuleKindNumeric),
			Metric:                    "memory.used_percent",
			Operator:                  string(OperatorGreaterThan),
			TriggerValue:              floatPointer(90),
			ClearValue:                floatPointer(85),
			TriggerSeconds:            300,
			ClearSeconds:              120,
			MinimumConsecutiveSamples: 1,
			Severity:                  SeverityWarning,
			TargetKind:                TargetFleet,
			Enabled:                   true,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		},
		{
			TemplateKey:               "filesystem_high",
			Name:                      "Filesystem utilization high",
			Kind:                      string(RuleKindNumeric),
			Metric:                    "filesystem.used_percent",
			Operator:                  string(OperatorGreaterThan),
			TriggerValue:              floatPointer(90),
			ClearValue:                floatPointer(85),
			TriggerSeconds:            300,
			ClearSeconds:              120,
			MinimumConsecutiveSamples: 1,
			Severity:                  SeverityWarning,
			TargetKind:                TargetFleet,
			Enabled:                   true,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		},
		{
			TemplateKey:               "systemd_failed",
			Name:                      "Systemd unit failed",
			Kind:                      string(RuleKindState),
			TriggerState:              "failed",
			ClearState:                "active",
			MinimumConsecutiveSamples: 1,
			Severity:                  SeverityCritical,
			TargetKind:                TargetFleet,
			Enabled:                   true,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		},
		{
			TemplateKey:               "collector_degraded",
			Name:                      "Collector degraded",
			Kind:                      string(RuleKindState),
			TriggerState:              "degraded",
			ClearState:                "healthy",
			MinimumConsecutiveSamples: 2,
			Severity:                  SeverityWarning,
			TargetKind:                TargetFleet,
			Enabled:                   true,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		},
		{
			TemplateKey:               "storage_fault",
			Name:                      "Storage fault",
			Kind:                      string(RuleKindState),
			TriggerState:              "fault",
			ClearState:                "healthy",
			MinimumConsecutiveSamples: 1,
			Severity:                  SeverityCritical,
			TargetKind:                TargetFleet,
			Enabled:                   true,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		},
	}
}

// ProvisionDefaultRules adds only missing template keys. An existing rule is
// left byte-for-byte unchanged, including disabled and retired defaults.
func ProvisionDefaultRules(existing []store.AlertRule, now time.Time) []store.AlertRule {
	result := make([]store.AlertRule, 0, len(existing)+7)
	seen := make(map[string]struct{}, len(existing))
	for _, rule := range existing {
		result = append(result, cloneRule(rule))
		if rule.TemplateKey != "" {
			seen[rule.TemplateKey] = struct{}{}
		}
	}
	for _, rule := range DefaultRuleTemplates(now) {
		if _, exists := seen[rule.TemplateKey]; exists {
			continue
		}
		result = append(result, rule)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].TemplateKey == result[right].TemplateKey {
			return result[left].ID < result[right].ID
		}
		return result[left].TemplateKey < result[right].TemplateKey
	})
	return result
}

func ValidateRule(rule store.AlertRule) error {
	if strings.TrimSpace(rule.Name) == "" || len(rule.Name) > 120 {
		return fmt.Errorf("%w: name", ErrInvalidRule)
	}
	if rule.TemplateKey != "" && len(rule.TemplateKey) > 64 {
		return fmt.Errorf("%w: template key", ErrInvalidRule)
	}
	if rule.Kind != string(RuleKindNumeric) && rule.Kind != string(RuleKindState) {
		return fmt.Errorf("%w: kind", ErrInvalidRule)
	}
	if rule.TargetKind != TargetFleet && rule.TargetKind != TargetSite && rule.TargetKind != TargetDevice {
		return fmt.Errorf("%w: target kind", ErrInvalidRule)
	}
	if rule.TargetKind == TargetFleet && rule.TargetID != "" {
		return fmt.Errorf("%w: fleet target id", ErrInvalidRule)
	}
	if rule.TargetKind != TargetFleet && strings.TrimSpace(rule.TargetID) == "" {
		return fmt.Errorf("%w: scoped target id", ErrInvalidRule)
	}
	if rule.Severity != SeverityWarning && rule.Severity != SeverityCritical {
		return fmt.Errorf("%w: severity", ErrInvalidRule)
	}
	if rule.TriggerSeconds < 0 || rule.TriggerSeconds > 86400 || rule.ClearSeconds < 0 || rule.ClearSeconds > 86400 {
		return fmt.Errorf("%w: duration", ErrInvalidRule)
	}
	if rule.MinimumConsecutiveSamples < 1 || rule.MinimumConsecutiveSamples > 10 {
		return fmt.Errorf("%w: consecutive sample bound", ErrInvalidRule)
	}
	if rule.Kind == string(RuleKindNumeric) {
		if strings.TrimSpace(rule.Metric) == "" || (rule.Operator != string(OperatorGreaterThan) && rule.Operator != string(OperatorLessThan)) || rule.TriggerValue == nil || rule.ClearValue == nil || !finite(*rule.TriggerValue) || !finite(*rule.ClearValue) {
			return fmt.Errorf("%w: numeric condition", ErrInvalidRule)
		}
		if rule.Operator == string(OperatorGreaterThan) && *rule.ClearValue >= *rule.TriggerValue {
			return fmt.Errorf("%w: gt hysteresis", ErrInvalidRule)
		}
		if rule.Operator == string(OperatorLessThan) && *rule.ClearValue <= *rule.TriggerValue {
			return fmt.Errorf("%w: lt hysteresis", ErrInvalidRule)
		}
	} else if strings.TrimSpace(rule.TriggerState) == "" || strings.TrimSpace(rule.ClearState) == "" || rule.TriggerState == rule.ClearState {
		return fmt.Errorf("%w: state condition", ErrInvalidRule)
	}
	return nil
}

func ValidateOverride(base store.AlertRule, override store.AlertOverride) error {
	if override.LineageID == "" || base.ID == "" || override.LineageID != base.ID {
		return fmt.Errorf("%w: lineage", ErrInvalidOverride)
	}
	if override.TargetKind != TargetSite && override.TargetKind != TargetDevice || strings.TrimSpace(override.TargetID) == "" {
		return fmt.Errorf("%w: target", ErrInvalidOverride)
	}
	if targetRank(override.TargetKind) < targetRank(base.TargetKind) || (targetRank(override.TargetKind) == targetRank(base.TargetKind) && override.TargetID != base.TargetID) {
		return fmt.Errorf("%w: override broadens target", ErrInvalidOverride)
	}
	rule := store.AlertRule{
		ID: override.LineageID, Name: base.Name, Kind: override.Kind, Metric: override.Metric,
		EntityID: override.EntityID, Operator: override.Operator, TriggerValue: cloneFloatPointer(override.TriggerValue), ClearValue: cloneFloatPointer(override.ClearValue),
		TriggerState: override.TriggerState, ClearState: override.ClearState, ServicePattern: override.ServicePattern, CollectorID: override.CollectorID, TriggerSeconds: override.TriggerSeconds, ClearSeconds: override.ClearSeconds,
		MinimumConsecutiveSamples: override.MinimumConsecutiveSamples, Severity: override.Severity, TargetKind: base.TargetKind, TargetID: base.TargetID, Enabled: override.Enabled,
	}
	if err := ValidateRule(rule); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidOverride, err)
	}
	return nil
}

type EffectiveRule struct {
	Rule   store.AlertRule
	Source string
}

// ResolveRules selects one effective rule per lineage for a device. Device
// overrides win over site overrides, which win over the fleet definition.
func ResolveRules(rules []store.AlertRule, overrides []store.AlertOverride, device store.Device) []EffectiveRule {
	effective := make([]EffectiveRule, 0, len(rules))
	for _, base := range rules {
		if base.ID == "" || base.RetiredAt != nil || !targetMatches(base.TargetKind, base.TargetID, device) {
			continue
		}
		selected, source := selectOverride(base, overrides, device)
		if selected != nil {
			base = applyOverride(base, *selected)
		}
		effective = append(effective, EffectiveRule{Rule: base, Source: source})
	}
	sort.Slice(effective, func(left, right int) bool { return effective[left].Rule.ID < effective[right].Rule.ID })
	return effective
}

func (rule EffectiveRule) EvaluatorRule() (Rule, error) {
	if err := ValidateRule(rule.Rule); err != nil {
		return Rule{}, err
	}
	result := Rule{
		ID: rule.Rule.ID, LineageID: rule.Rule.ID, Revision: rule.Rule.Revision, Name: rule.Rule.Name, Kind: RuleKind(rule.Rule.Kind), Metric: rule.Rule.Metric, EntityID: rule.Rule.EntityID,
		Operator: Operator(rule.Rule.Operator), TriggerFor: time.Duration(rule.Rule.TriggerSeconds) * time.Second, ClearFor: time.Duration(rule.Rule.ClearSeconds) * time.Second,
		TriggerState: rule.Rule.TriggerState, ClearState: rule.Rule.ClearState, ServicePattern: rule.Rule.ServicePattern, CollectorID: rule.Rule.CollectorID, MinimumConsecutiveSamples: rule.Rule.MinimumConsecutiveSamples, Severity: rule.Rule.Severity, Enabled: rule.Rule.Enabled,
	}
	if rule.Rule.TriggerValue != nil {
		result.TriggerValue = *rule.Rule.TriggerValue
	}
	if rule.Rule.ClearValue != nil {
		result.ClearValue = *rule.Rule.ClearValue
	}
	return result, nil
}

func selectOverride(base store.AlertRule, overrides []store.AlertOverride, device store.Device) (*store.AlertOverride, string) {
	var site, selected *store.AlertOverride
	for index := range overrides {
		candidate := &overrides[index]
		if candidate.LineageID != base.ID {
			continue
		}
		if candidate.TargetKind == TargetDevice && candidate.TargetID == device.ID {
			selected = candidate
			break
		}
		if candidate.TargetKind == TargetSite && candidate.TargetID == device.SiteID {
			site = candidate
		}
	}
	if selected != nil {
		return selected, TargetDevice
	}
	if site != nil {
		return site, TargetSite
	}
	return nil, base.TargetKind
}

func applyOverride(base store.AlertRule, override store.AlertOverride) store.AlertRule {
	base.Kind = override.Kind
	base.Metric = override.Metric
	base.EntityID = override.EntityID
	base.Operator = override.Operator
	base.TriggerValue = cloneFloatPointer(override.TriggerValue)
	base.ClearValue = cloneFloatPointer(override.ClearValue)
	base.TriggerState = override.TriggerState
	base.ClearState = override.ClearState
	base.ServicePattern = override.ServicePattern
	base.CollectorID = override.CollectorID
	base.TriggerSeconds = override.TriggerSeconds
	base.ClearSeconds = override.ClearSeconds
	base.MinimumConsecutiveSamples = override.MinimumConsecutiveSamples
	base.Severity = override.Severity
	base.Enabled = override.Enabled
	base.Revision = effectiveRevision(base.Revision, override.Revision)
	return base
}

func targetMatches(kind, targetID string, device store.Device) bool {
	switch kind {
	case TargetFleet:
		return true
	case TargetSite:
		return targetID != "" && device.SiteID == targetID
	case TargetDevice:
		return targetID != "" && device.ID == targetID
	default:
		return false
	}
}

func targetRank(kind string) int {
	switch kind {
	case TargetFleet:
		return 0
	case TargetSite:
		return 1
	case TargetDevice:
		return 2
	default:
		return -1
	}
}

func effectiveRevision(base, override int64) int64 {
	if override <= 0 {
		return base
	}
	if base > (math.MaxInt64-override)/1000003 {
		return math.MaxInt64
	}
	return base*1000003 + override
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func floatPointer(value float64) *float64 { return &value }

func cloneFloatPointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneRule(rule store.AlertRule) store.AlertRule {
	rule.TriggerValue = cloneFloatPointer(rule.TriggerValue)
	rule.ClearValue = cloneFloatPointer(rule.ClearValue)
	rule.RetiredAt = cloneTime(rule.RetiredAt)
	return rule
}
