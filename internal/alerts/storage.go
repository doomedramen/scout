package alerts

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

const (
	SMARTCollectorID            = "smart"
	ZFSCollectorID              = "zfs"
	StorageFaultRuleTemplateKey = "storage_fault"
	storageStateMetric          = "storage.state"
	storageStateUnit            = "state"
	storageDefaultFreshness     = 15 * time.Minute
	storageSMARTInterval        = 5 * time.Minute
	storageZFSInterval          = time.Minute
)

// StorageObservation translates an explicit SMART or ZFS health predicate into
// the state vocabulary used by the durable evaluator. Capacity and wear
// values are deliberately not part of this predicate: a numeric owner rule
// is required when those values should alert.
func StorageObservation(entity store.ServiceEntity, receivedAt time.Time) Observation {
	entityID := storageEntityID(entity)
	observedAt := entity.ObservedAt.UTC()
	receivedAt = receivedAt.UTC()
	if receivedAt.IsZero() {
		receivedAt = observedAt
	}
	state, supported := storageState(entity)
	evidence := EvidenceFresh
	if !supported {
		evidence = EvidenceUnsupported
	}
	if observedAt.IsZero() {
		evidence = EvidenceUnknown
	}
	if !entity.ExpiresAt.IsZero() && (receivedAt.IsZero() || !receivedAt.Before(entity.ExpiresAt)) {
		observedAt = time.Time{}
		evidence = EvidenceUnknown
	}
	labels := cloneStorageLabels(entity.Labels)
	labels["rawState"] = boundedStorageLabel(entity.Status)
	labels["storageState"] = state
	return Observation{
		EntityID:   entityID,
		Metric:     storageStateMetric,
		State:      state,
		Evidence:   evidence,
		Unit:       storageStateUnit,
		Source:     strings.ToLower(strings.TrimSpace(entity.Provider)),
		Labels:     labels,
		ObservedAt: observedAt,
		ReceivedAt: receivedAt,
	}
}

// StorageRuleApplies limits the default fault lineage to entities with an
// explicit, supported storage-health predicate. ZFS datasets remain visible
// inventory, but are not health-bearing pool entities and can never open a
// second storage-fault incident.
func StorageRuleApplies(rule Rule, entity store.ServiceEntity) bool {
	if rule.Kind != RuleKindState || !isStorageFaultEntity(entity) {
		return false
	}
	if canonicalStorageState(rule.TriggerState) != "fault" {
		return false
	}
	if canonicalStorageState(rule.ClearState) != "healthy" {
		return false
	}
	if rule.CollectorID != "" && !strings.EqualFold(strings.TrimSpace(rule.CollectorID), strings.TrimSpace(entity.Provider)) {
		return false
	}
	if rule.EntityID != "" && rule.EntityID != storageEntityID(entity) {
		return false
	}
	return strings.TrimSpace(rule.ServicePattern) == ""
}

// ResolveStorageRules applies the normal fleet/site/device precedence before
// retaining rules that can evaluate the supplied storage entity.
func ResolveStorageRules(rules []store.AlertRule, overrides []store.AlertOverride, device store.Device, entity store.ServiceEntity) ([]Rule, error) {
	if storageEntityID(entity) == "" || strings.TrimSpace(entity.Provider) == "" {
		return nil, fmt.Errorf("%w: storage identity", store.ErrInvalid)
	}
	effective := ResolveRules(rules, overrides, device)
	result := make([]Rule, 0, len(effective))
	for _, item := range effective {
		rule, err := item.EvaluatorRule()
		if err != nil {
			return nil, err
		}
		rule = normalizeStorageRule(rule, entity)
		if StorageRuleApplies(rule, entity) {
			result = append(result, rule)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return ruleLineageID(result[left]) < ruleLineageID(result[right])
	})
	return result, nil
}

// EvaluateStorageEntity sends one typed storage observation through the same
// durable evaluator used by host and service alerts.
func (p *PersistentEvaluator) EvaluateStorageEntity(ctx context.Context, rule Rule, entity store.ServiceEntity) (EvaluationResult, error) {
	rule = normalizeStorageRule(rule, entity)
	if !StorageRuleApplies(rule, entity) {
		return EvaluationResult{Ignored: true, State: EvaluationState{LineageID: ruleLineageID(rule), EntityID: storageEntityID(entity), RuleRevision: rule.Revision, Evidence: EvidenceUnknown}}, nil
	}
	now := time.Now().UTC()
	if p != nil && p.Clock != nil {
		now = p.Clock.Now().UTC()
	}
	return p.EvaluateAndPersist(ctx, rule, StorageObservation(entity, now), entity.DeviceID, nil)
}

// EvaluateStorageRules evaluates storage rules in stable lineage order. A
// single rule lineage/entity key still provides the incident uniqueness
// boundary if SMART and ZFS report the same fault on separate entities.
func (p *PersistentEvaluator) EvaluateStorageRules(ctx context.Context, entity store.ServiceEntity, rules []Rule) ([]EvaluationResult, error) {
	ordered := append([]Rule(nil), rules...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ruleLineageID(ordered[left]) < ruleLineageID(ordered[right])
	})
	results := make([]EvaluationResult, 0, len(ordered))
	for _, candidate := range ordered {
		result, err := p.EvaluateStorageEntity(ctx, candidate, entity)
		if err != nil {
			return nil, err
		}
		if !result.Ignored {
			results = append(results, result)
		}
	}
	return results, nil
}

func normalizeStorageRule(rule Rule, entity store.ServiceEntity) Rule {
	rule.TriggerState = canonicalStorageState(rule.TriggerState)
	rule.ClearState = canonicalStorageState(rule.ClearState)
	if rule.MaxEvidenceAge <= 0 {
		rule.MaxEvidenceAge = storageFreshnessBudget(entity)
	}
	if StorageRuleApplies(rule, entity) {
		if rule.MinimumConsecutiveClearSamples < 2 {
			rule.MinimumConsecutiveClearSamples = 2
		}
		if rule.ClearFor < storageCollectionInterval(entity) {
			rule.ClearFor = storageCollectionInterval(entity)
		}
	}
	return rule
}

func storageState(entity store.ServiceEntity) (string, bool) {
	if !isStorageFaultEntity(entity) {
		return "unavailable", false
	}
	labels := entity.Labels
	if strings.EqualFold(strings.TrimSpace(labels["hardwareFault"]), "true") || strings.EqualFold(strings.TrimSpace(entity.Status), "fault") {
		return "fault", true
	}
	provider := strings.ToLower(strings.TrimSpace(entity.Provider))
	switch provider {
	case SMARTCollectorID:
		if strings.EqualFold(strings.TrimSpace(labels["smartPassed"]), "false") {
			return "fault", true
		}
		if strings.EqualFold(strings.TrimSpace(entity.Status), "online") || strings.EqualFold(strings.TrimSpace(entity.Status), "healthy") || strings.EqualFold(strings.TrimSpace(labels["smartPassed"]), "true") {
			return "healthy", true
		}
		return "unavailable", false
	case ZFSCollectorID:
		health := strings.ToUpper(strings.TrimSpace(labels["healthState"]))
		switch health {
		case "DEGRADED", "FAULTED", "UNAVAIL", "SUSPENDED":
			return "fault", true
		case "ONLINE":
			if zfsErrorLabel(labels, "scrubErrors") || zfsErrorLabel(labels, "dataErrors") {
				return "fault", true
			}
			return "healthy", true
		}
		if strings.EqualFold(strings.TrimSpace(entity.Status), "online") || strings.EqualFold(strings.TrimSpace(entity.Status), "scrubbing") {
			if zfsErrorLabel(labels, "scrubErrors") || zfsErrorLabel(labels, "dataErrors") {
				return "fault", true
			}
			return "healthy", true
		}
		return "unavailable", false
	default:
		return "unavailable", false
	}
}

func isStorageFaultEntity(entity store.ServiceEntity) bool {
	if !isStorageInventoryEntity(entity) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(entity.Provider), SMARTCollectorID) || strings.EqualFold(strings.TrimSpace(entity.Kind), "zfs_pool")
}

func isStorageInventoryEntity(entity store.ServiceEntity) bool {
	provider := strings.ToLower(strings.TrimSpace(entity.Provider))
	kind := strings.ToLower(strings.TrimSpace(entity.Kind))
	switch provider {
	case SMARTCollectorID:
		return kind == "storage"
	case ZFSCollectorID:
		return kind == "zfs_pool" || kind == "zfs_dataset"
	default:
		return false
	}
}

func canonicalStorageState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hardware_fault", "storage_fault":
		return "fault"
	case "online", "healthy":
		return "healthy"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func storageEntityID(entity store.ServiceEntity) string {
	if value := strings.TrimSpace(entity.ID); value != "" {
		return value
	}
	return strings.TrimSpace(entity.Name)
}

func storageFreshnessBudget(entity store.ServiceEntity) time.Duration {
	if strings.EqualFold(strings.TrimSpace(entity.Provider), ZFSCollectorID) {
		return 3 * storageZFSInterval
	}
	return storageDefaultFreshness
}

func storageCollectionInterval(entity store.ServiceEntity) time.Duration {
	if strings.EqualFold(strings.TrimSpace(entity.Provider), ZFSCollectorID) {
		return storageZFSInterval
	}
	return storageSMARTInterval
}

func zfsErrorLabel(labels map[string]string, key string) bool {
	value, err := strconv.ParseUint(strings.TrimSpace(labels[key]), 10, 64)
	return err == nil && value > 0
}

func cloneStorageLabels(value map[string]string) map[string]string {
	result := make(map[string]string, len(value)+2)
	for key, item := range value {
		result[key] = item
	}
	return result
}

func boundedStorageLabel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
