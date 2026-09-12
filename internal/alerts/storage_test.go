package alerts

import (
	"context"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestStorageObservationUsesExplicitFaultPredicates(t *testing.T) {
	start := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		entity   store.ServiceEntity
		state    string
		evidence EvidenceState
	}{
		{
			name:   "smart errors and wear do not imply fault",
			entity: storageTestEntity(SMARTCollectorID, "storage", "online", map[string]string{"error_count": "9", "wear_percent": "99"}, start),
			state:  "healthy",
		},
		{
			name:   "smart failing health",
			entity: storageTestEntity(SMARTCollectorID, "storage", "online", map[string]string{"smartPassed": "false"}, start),
			state:  "fault",
		},
		{
			name:   "zfs scrub in progress is healthy",
			entity: storageTestEntity(ZFSCollectorID, "zfs_pool", "scrubbing", map[string]string{"healthState": "ONLINE", "scrubState": "SCANNING"}, start),
			state:  "healthy",
		},
		{
			name:   "zfs data errors are a fault",
			entity: storageTestEntity(ZFSCollectorID, "zfs_pool", "online", map[string]string{"healthState": "ONLINE", "dataErrors": "1"}, start),
			state:  "fault",
		},
		{
			name:   "zfs degraded pool",
			entity: storageTestEntity(ZFSCollectorID, "zfs_pool", "unavailable", map[string]string{"healthState": "DEGRADED"}, start),
			state:  "fault",
		},
		{
			name:     "permission unavailable",
			entity:   storageTestEntity(SMARTCollectorID, "storage", "unavailable", nil, start),
			state:    "unavailable",
			evidence: EvidenceUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := StorageObservation(test.entity, start.Add(10*time.Second))
			expectedEvidence := test.evidence
			if expectedEvidence == "" {
				expectedEvidence = EvidenceFresh
			}
			if observation.State != test.state || observation.Evidence != expectedEvidence {
				t.Fatalf("storage observation = %+v", observation)
			}
			if observation.Labels["rawState"] != test.entity.Status || observation.Labels["storageState"] != test.state {
				t.Fatalf("storage evidence labels = %+v", observation.Labels)
			}
		})
	}
}

func TestStorageObservationExpiresAsUnknown(t *testing.T) {
	start := time.Date(2026, 9, 12, 1, 30, 0, 0, time.UTC)
	entity := storageTestEntity(ZFSCollectorID, "zfs_pool", "fault", map[string]string{"hardwareFault": "true"}, start)
	entity.ExpiresAt = start.Add(time.Minute)
	observation := StorageObservation(entity, entity.ExpiresAt)
	if observation.State != "fault" || observation.Evidence != EvidenceUnknown || !observation.ObservedAt.IsZero() {
		t.Fatalf("expired storage observation = %+v", observation)
	}
}

func TestStorageRuleAppliesOnlyToFaultBearingEntities(t *testing.T) {
	rule := Rule{ID: "storage", LineageID: "storage", Kind: RuleKindState, TriggerState: "hardware_fault", ClearState: "healthy", Enabled: true}
	cases := []struct {
		name   string
		entity store.ServiceEntity
		want   bool
	}{
		{name: "smart disk", entity: storageTestEntity(SMARTCollectorID, "storage", "fault", nil, time.Now()), want: true},
		{name: "zfs pool", entity: storageTestEntity(ZFSCollectorID, "zfs_pool", "fault", nil, time.Now()), want: true},
		{name: "zfs dataset", entity: storageTestEntity(ZFSCollectorID, "zfs_dataset", "online", nil, time.Now()), want: false},
		{name: "systemd service", entity: storageTestEntity(SystemdCollectorID, "service", "failed", nil, time.Now()), want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := StorageRuleApplies(rule, test.entity); got != test.want {
				t.Fatalf("StorageRuleApplies = %v, want %v", got, test.want)
			}
		})
	}
	wrongCollector := rule
	wrongCollector.CollectorID = SystemdCollectorID
	if StorageRuleApplies(wrongCollector, cases[0].entity) {
		t.Fatal("storage rule with a non-storage collector was accepted")
	}
}

func TestStorageFaultRecoversAfterTwoIntervalSeparatedHealthySamples(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	device := serviceTestDevice(t, ctx, repository, store.AvailabilityOnline)
	rule := Rule{
		ID: "storage", LineageID: "storage", Revision: 1, TemplateKey: StorageFaultRuleTemplateKey,
		Name: "Storage fault", Kind: RuleKindState, TriggerState: "fault", ClearState: "healthy",
		MinimumConsecutiveSamples: 1, Severity: SeverityCritical, Enabled: true,
	}
	evaluator := NewPersistentEvaluator(repository, clock, "storage-alert-worker")
	entity := storageTestEntity(SMARTCollectorID, "storage", "fault", map[string]string{"hardwareFault": "true"}, start)
	entity.DeviceID = device.ID
	first, err := evaluator.EvaluateStorageEntity(ctx, rule, entity)
	if err != nil {
		t.Fatal(err)
	}
	if first.State.ActiveIncident == nil || len(first.Transitions) != 1 || first.Transitions[0].Kind != TransitionTriggered {
		t.Fatalf("storage fault result = %+v", first)
	}

	entity.Status = "online"
	entity.Labels["hardwareFault"] = "false"
	clock.Advance(storageSMARTInterval)
	entity.ObservedAt = clock.Now()
	entity.ExpiresAt = clock.Now().Add(storageDefaultFreshness)
	second, err := evaluator.EvaluateStorageEntity(ctx, rule, entity)
	if err != nil {
		t.Fatal(err)
	}
	if second.State.ActiveIncident == nil || len(second.Transitions) != 0 {
		t.Fatalf("first healthy storage sample changed incident = %+v", second)
	}

	clock.Advance(storageSMARTInterval)
	entity.ObservedAt = clock.Now()
	entity.ExpiresAt = clock.Now().Add(storageDefaultFreshness)
	third, err := evaluator.EvaluateStorageEntity(ctx, rule, entity)
	if err != nil {
		t.Fatal(err)
	}
	if third.State.ActiveIncident != nil || len(third.Transitions) != 1 || third.Transitions[0].Kind != TransitionRecovered {
		t.Fatalf("storage fault did not recover after interval = %+v", third)
	}
}

func TestStorageUnknownDoesNotRecoverFault(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	device := serviceTestDevice(t, ctx, repository, store.AvailabilityOnline)
	rule := Rule{ID: "zfs-storage", LineageID: "zfs-storage", Revision: 1, Kind: RuleKindState, TriggerState: "fault", ClearState: "healthy", Severity: SeverityCritical, Enabled: true}
	evaluator := NewPersistentEvaluator(repository, clock, "storage-unknown-worker")
	entity := storageTestEntity(ZFSCollectorID, "zfs_pool", "fault", map[string]string{"hardwareFault": "true"}, start)
	entity.DeviceID = device.ID
	result, err := evaluator.EvaluateStorageEntity(ctx, rule, entity)
	if err != nil || result.State.ActiveIncident == nil {
		t.Fatalf("initial ZFS fault = %+v, %v", result, err)
	}
	clock.Advance(storageZFSInterval)
	entity.Status = "unavailable"
	entity.Labels["hardwareFault"] = "false"
	entity.Labels["healthState"] = ""
	entity.ObservedAt = clock.Now()
	entity.ExpiresAt = clock.Now().Add(storageDefaultFreshness)
	result, err = evaluator.EvaluateStorageEntity(ctx, rule, entity)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.ActiveIncident == nil || result.State.Evidence != EvidenceUnsupported {
		t.Fatalf("unsupported ZFS state recovered fault = %+v", result)
	}
}

func storageTestEntity(provider, kind, status string, labels map[string]string, observedAt time.Time) store.ServiceEntity {
	copyLabels := map[string]string{}
	for key, value := range labels {
		copyLabels[key] = value
	}
	return store.ServiceEntity{
		ID: "storage-fixture-" + provider + "-" + kind, Provider: provider, DeviceID: "storage-device", Kind: kind,
		Name: "Storage fixture", Status: status, Labels: copyLabels, ObservedAt: observedAt, ExpiresAt: observedAt.Add(storageDefaultFreshness),
	}
}
