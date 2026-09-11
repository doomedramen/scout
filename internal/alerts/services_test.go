package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestServiceObservationExpiresWithoutInventingFreshEvidence(t *testing.T) {
	start := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	entity := store.ServiceEntity{
		ID: "web.service", Provider: SystemdCollectorID, Kind: "service", Name: "web.service",
		Status: "active", Labels: map[string]string{"unit": "web.service", "mustRun": "true"},
		ObservedAt: start, ExpiresAt: start.Add(90 * time.Second),
	}
	observation := ServiceObservation(entity, start.Add(30*time.Second))
	if observation.Evidence != EvidenceFresh || observation.State != "active" || observation.EntityID != entity.ID || observation.Source != SystemdCollectorID {
		t.Fatalf("fresh service observation = %+v", observation)
	}
	observation.Labels["mutated"] = "caller"
	if _, exists := entity.Labels["mutated"]; exists {
		t.Fatal("service labels were not copied")
	}

	expired := ServiceObservation(entity, start.Add(90*time.Second))
	if !expired.ObservedAt.IsZero() || expired.Evidence != EvidenceUnknown {
		t.Fatalf("expired service observation = %+v", expired)
	}
	unsupported := entity
	unsupported.Status = "new-systemd-state"
	if observation := ServiceObservation(unsupported, start.Add(30*time.Second)); observation.Evidence != EvidenceUnsupported || observation.State != "unavailable" {
		t.Fatalf("unsupported service observation = %+v", observation)
	}
}

func TestServiceEntityWriteMarksCoalescedAlertWork(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	entity := store.ServiceEntity{ID: "queued.service", Provider: SystemdCollectorID, Kind: "service", Name: "queued.service", Status: "failed"}
	if err := repository.PutServiceEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if err := repository.PutServiceEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	work, err := repository.ListAlertWork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 1 || work[0].LineageID != store.AlertWorkAllLineages || work[0].EntityID != entity.ID {
		t.Fatalf("service alert work = %+v", work)
	}
	if work[0].DirtyGeneration != 2 {
		t.Fatalf("service writes were not coalesced = %+v", work[0])
	}
}

func TestServicePatternMatchesOnlyBoundedGlobSyntax(t *testing.T) {
	for _, test := range []struct {
		pattern string
		value   string
		want    bool
	}{
		{pattern: "scout-*.service", value: "scout-agent.service", want: true},
		{pattern: "backup.?.service", value: "backup.a.service", want: true},
		{pattern: "backup.?.service", value: "backup.ab.service", want: false},
		{pattern: "*.service", value: "socket.socket", want: false},
		{pattern: "(scout|backup).service", value: "scout.service", want: false},
	} {
		if got := ServicePatternMatches(test.pattern, test.value); got != test.want {
			t.Fatalf("pattern %q value %q = %v, want %v", test.pattern, test.value, got, test.want)
		}
	}
}

func TestSystemdFailedServiceRecoversAfterTwoHealthySamples(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	device := serviceTestDevice(t, ctx, repository, store.AvailabilityOnline)
	rule := Rule{
		ID: "systemd-failed", LineageID: "systemd-failed", Revision: 1, TemplateKey: SystemdFailedRuleTemplateKey,
		Name: "Systemd unit failed", Kind: RuleKindState, TriggerState: "failed", ClearState: "active",
		CollectorID: SystemdCollectorID, ClearFor: 30 * time.Second, MinimumConsecutiveSamples: 2,
		Severity: SeverityCritical, Enabled: true,
	}
	evaluator := NewPersistentEvaluator(repository, clock, "service-alert-worker")
	entity := serviceTestEntity(device.ID, "failed", false, start)
	first, err := evaluator.EvaluateServiceEntity(ctx, rule, entity)
	if err != nil {
		t.Fatal(err)
	}
	if first.State.ActiveIncident == nil || len(first.Transitions) != 1 || first.Transitions[0].Kind != TransitionTriggered {
		t.Fatalf("failed service result = %+v", first)
	}

	clock.Advance(30 * time.Second)
	entity.Status = "active"
	entity.ObservedAt = clock.Now()
	entity.ExpiresAt = clock.Now().Add(90 * time.Second)
	second, err := evaluator.EvaluateServiceEntity(ctx, rule, entity)
	if err != nil {
		t.Fatal(err)
	}
	if second.State.ActiveIncident == nil || len(second.Transitions) != 0 {
		t.Fatalf("first healthy service sample changed incident = %+v", second)
	}

	clock.Advance(30 * time.Second)
	entity.ObservedAt = clock.Now()
	entity.ExpiresAt = clock.Now().Add(90 * time.Second)
	third, err := evaluator.EvaluateServiceEntity(ctx, rule, entity)
	if err != nil {
		t.Fatal(err)
	}
	if third.State.ActiveIncident != nil || len(third.Transitions) != 1 || third.Transitions[0].Kind != TransitionRecovered {
		t.Fatalf("second healthy service sample did not recover = %+v", third)
	}

	incident, err := repository.GetIncident(ctx, first.Transitions[0].IncidentID)
	if err != nil {
		t.Fatal(err)
	}
	if incident.DeviceID != device.ID || incident.Status != string(IncidentResolved) || incident.RuleSnapshot["collectorId"] != SystemdCollectorID {
		t.Fatalf("persisted service incident = %+v", incident)
	}
}

func TestMustRunInactivityIsSelectedAndDoesNotDuplicateFailure(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	device := serviceTestDevice(t, ctx, repository, store.AvailabilityOnline)
	failedRule := Rule{
		ID: "systemd-failed", LineageID: "systemd-failed", Revision: 1, TemplateKey: SystemdFailedRuleTemplateKey,
		Name: "Systemd unit failed", Kind: RuleKindState, TriggerState: "failed", ClearState: "active",
		CollectorID: SystemdCollectorID, MinimumConsecutiveSamples: 2, Severity: SeverityCritical, Enabled: true,
	}
	mustRunRule := Rule{
		ID: "service-required-inactive", LineageID: "service-required-inactive", Revision: 1, TemplateKey: ServiceRequiredInactiveTemplateKey,
		Name: "Required systemd service inactive", Kind: RuleKindState, TriggerState: "inactive", ClearState: "active",
		CollectorID: SystemdCollectorID, TriggerFor: time.Minute, ClearFor: time.Minute,
		Severity: SeverityWarning, Enabled: true,
	}
	evaluator := NewPersistentEvaluator(repository, clock, "service-dedup-worker")
	failed := serviceTestEntity(device.ID, "failed", true, start)
	results, err := evaluator.EvaluateServiceRules(ctx, failed, []Rule{mustRunRule, failedRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State.ActiveIncident == nil || results[0].State.ActiveIncident.Severity != SeverityCritical {
		t.Fatalf("failed must-run results = %+v", results)
	}
	active, err := repository.ListIncidentPage(ctx, store.IncidentQuery{Status: string(IncidentActive), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Items) != 1 {
		t.Fatalf("failed unit opened duplicate incidents = %+v", active.Items)
	}

	clock.Advance(30 * time.Second)
	inactive := serviceTestEntity(device.ID, "inactive", true, clock.Now())
	results, err = evaluator.EvaluateServiceRules(ctx, inactive, []Rule{mustRunRule, failedRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State.ActiveIncident == nil || results[0].State.ActiveIncident.Severity != SeverityCritical {
		t.Fatalf("failed incident was not the only result after inactive transition = %+v", results)
	}
	active, err = repository.ListIncidentPage(ctx, store.IncidentQuery{Status: string(IncidentActive), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Items) != 1 || active.Items[0].Severity != SeverityCritical {
		t.Fatalf("inactive transition duplicated failure = %+v", active.Items)
	}

	intentional := serviceTestEntity(device.ID, "inactive", false, clock.Now().Add(time.Second))
	results, err = evaluator.EvaluateServiceRules(ctx, intentional, []Rule{mustRunRule, failedRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("unselected inactive service was evaluated as must-run = %+v", results)
	}
}

func TestMustRunInactiveOpensAfterBoundedDelay(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	device := serviceTestDevice(t, ctx, repository, store.AvailabilityOnline)
	rule := Rule{
		ID: "service-required-inactive", LineageID: "service-required-inactive", Revision: 1, TemplateKey: ServiceRequiredInactiveTemplateKey,
		Name: "Required systemd service inactive", Kind: RuleKindState, TriggerState: "inactive", ClearState: "active",
		CollectorID: SystemdCollectorID, TriggerFor: time.Minute, ClearFor: time.Minute, Severity: SeverityWarning, Enabled: true,
	}
	evaluator := NewPersistentEvaluator(repository, clock, "service-required-worker")
	entity := serviceTestEntity(device.ID, "inactive", true, start)
	results, err := evaluator.EvaluateServiceRules(ctx, entity, []Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State.ActiveIncident != nil {
		t.Fatalf("must-run opened before delay = %+v", results)
	}
	clock.Advance(time.Minute)
	entity.ObservedAt = clock.Now()
	entity.ExpiresAt = clock.Now().Add(90 * time.Second)
	results, err = evaluator.EvaluateServiceRules(ctx, entity, []Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State.ActiveIncident == nil || results[0].State.ActiveIncident.Severity != SeverityWarning {
		t.Fatalf("must-run did not open after delay = %+v", results)
	}
}

func TestServiceIncidentUsesHostOfflineNotificationSuppression(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 11, 23, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	repository := store.NewMemory()
	repository.SetClock(func() time.Time { return clock.Now() })
	device := serviceTestDevice(t, ctx, repository, store.AvailabilityOffline)
	if _, err := repository.PutNotificationDestination(ctx, store.NotificationDestination{
		ID: "service-suppression-destination", Name: "Fixture", BaseURL: "https://ntfy.example.test", MaskedTopic: "sc********s", Enabled: true,
		SecretCiphertext: []byte("cipher"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}, 0); err != nil {
		t.Fatal(err)
	}
	rule := Rule{
		ID: "systemd-failed", LineageID: "systemd-failed", Revision: 1, TemplateKey: SystemdFailedRuleTemplateKey,
		Name: "Systemd unit failed", Kind: RuleKindState, TriggerState: "failed", ClearState: "active",
		CollectorID: SystemdCollectorID, Severity: SeverityCritical, Enabled: true,
	}
	evaluator := NewPersistentEvaluator(repository, clock, "service-suppression-worker")
	result, err := evaluator.EvaluateServiceEntity(ctx, rule, serviceTestEntity(device.ID, "failed", false, start))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Transitions) != 1 {
		t.Fatalf("service failure transitions = %+v", result.Transitions)
	}
	persisted, err := repository.ListNotificationDeliveries(ctx, store.NotificationDeliveryQuery{IncidentID: result.Transitions[0].IncidentID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Items) != 1 || persisted.Items[0].Status != store.NotificationDeliverySuppressed || !strings.Contains(strings.Join(persisted.Items[0].SuppressionReasons, ","), SuppressionReasonHostOffline) {
		t.Fatalf("service notification was not host-offline suppressed = %+v", persisted.Items)
	}
}

func TestResolveServiceRulesCanonicalizesOwnerAliasesAndSelection(t *testing.T) {
	start := time.Date(2026, 9, 11, 23, 30, 0, 0, time.UTC)
	device := store.Device{ID: "service-rule-device", SiteID: "service-rule-site"}
	rules := []store.AlertRule{
		{ID: "failed", TemplateKey: SystemdFailedRuleTemplateKey, Name: "Failed", Kind: string(RuleKindState), TriggerState: "failed", ClearState: "active", CollectorID: SystemdCollectorID, MinimumConsecutiveSamples: 2, Severity: SeverityCritical, TargetKind: TargetFleet, Enabled: true, Revision: 1},
		{ID: "required", TemplateKey: ServiceRequiredInactiveTemplateKey, Name: "Required", Kind: string(RuleKindState), TriggerState: "inactive", ClearState: "active", CollectorID: SystemdCollectorID, TriggerSeconds: 60, ClearSeconds: 60, MinimumConsecutiveSamples: 1, Severity: SeverityWarning, TargetKind: TargetFleet, Enabled: true, Revision: 1},
		{ID: "owner-alias", Name: "Owner alias", Kind: string(RuleKindState), TriggerState: "service_failed", ClearState: "active", ServicePattern: "scout-*.service", MinimumConsecutiveSamples: 1, Severity: SeverityWarning, TargetKind: TargetFleet, Enabled: true, Revision: 1},
	}
	failed := store.ServiceEntity{ID: "scout-agent.service", Provider: SystemdCollectorID, Kind: "service", Name: "scout-agent.service", Status: "failed", Labels: map[string]string{"unit": "scout-agent.service", "mustRun": "true"}, ObservedAt: start}
	resolved, err := ResolveServiceRules(rules, nil, device, failed)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 2 {
		t.Fatalf("resolved failed service rules = %+v", resolved)
	}
	for _, rule := range resolved {
		if rule.TriggerState == "inactive" {
			t.Fatal("must-run rule duplicated failed unit")
		}
		if rule.TriggerState != "failed" {
			t.Fatalf("owner service alias was not canonicalized = %+v", rule)
		}
	}
	if resolved[0].TriggerState != "failed" || resolved[0].CollectorID != SystemdCollectorID || resolved[0].MaxEvidenceAge != serviceFreshnessBudget {
		t.Fatalf("systemd service rule was not normalized = %+v", resolved[0])
	}
}

func serviceTestDevice(t *testing.T, ctx context.Context, repository *store.Store, availability store.Availability) store.Device {
	t.Helper()
	site, err := repository.CreateSite(ctx, store.Site{ID: "site-" + store.NewID(), Name: "Service fixtures"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{ID: "device-" + store.NewID(), SiteID: site.ID, DisplayName: "Service fixture", Availability: availability})
	if err != nil {
		t.Fatal(err)
	}
	return device
}

func serviceTestEntity(deviceID, status string, mustRun bool, observedAt time.Time) store.ServiceEntity {
	labels := map[string]string{"unit": "scout-agent.service"}
	if mustRun {
		labels["mustRun"] = "true"
	}
	return store.ServiceEntity{ID: "scout-agent.service", Provider: SystemdCollectorID, DeviceID: deviceID, Kind: "service", Name: "scout-agent.service", Status: status, Labels: labels, ObservedAt: observedAt, ExpiresAt: observedAt.Add(90 * time.Second)}
}
