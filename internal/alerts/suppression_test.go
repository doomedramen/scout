package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/notifications/ntfy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

func TestRecurringSuppressionMatchesOvernightAndUnionTargets(t *testing.T) {
	window := store.SuppressionWindow{ID: "night", Enabled: true, Mode: store.SuppressionWindowRecurring, Timezone: "Europe/London", Weekdays: []int{1, 3}, StartLocal: "22:00", EndLocal: "06:00"}
	other := store.SuppressionWindow{ID: "device", Enabled: true, Mode: store.SuppressionWindowOneTime, StartsAt: timePointer(time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC)), EndsAt: timePointer(time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)), TargetKind: TargetDevice, TargetID: "device-1"}
	window.TargetKind = TargetFleet
	tests := []struct {
		name string
		at   time.Time
		want int
	}{
		{name: "monday start", at: time.Date(2026, 9, 14, 21, 0, 0, 0, time.UTC), want: 1},
		{name: "tuesday overnight", at: time.Date(2026, 9, 15, 4, 59, 0, 0, time.UTC), want: 1},
		{name: "tuesday end exclusive", at: time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC), want: 0},
		{name: "unselected sunday", at: time.Date(2026, 9, 13, 23, 0, 0, 0, time.UTC), want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := MatchSuppressionWindows([]store.SuppressionWindow{window}, SuppressionTarget{DeviceID: "device-1"}, test.at)
			if len(got) != test.want {
				t.Fatalf("matching windows = %d, want %d", len(got), test.want)
			}
		})
	}
	other.StartsAt = timePointer(time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC))
	other.EndsAt = timePointer(time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC))
	if got := MatchSuppressionWindows([]store.SuppressionWindow{window, other}, SuppressionTarget{DeviceID: "device-1"}, time.Date(2026, 9, 15, 4, 30, 0, 0, time.UTC)); len(got) != 2 {
		t.Fatalf("union matching windows = %d, want 2", len(got))
	}
	if got := MatchSuppressionWindows([]store.SuppressionWindow{window, other}, SuppressionTarget{DeviceID: "device-2"}, time.Date(2026, 9, 15, 4, 30, 0, 0, time.UTC)); len(got) != 1 {
		t.Fatalf("target matching windows = %d, want 1", len(got))
	}
}

func TestRecurringSuppressionUsesBothFallBackOccurrencesAndSkipsSpringForward(t *testing.T) {
	fallback := store.SuppressionWindow{ID: "fallback", Enabled: true, TargetKind: TargetFleet, Mode: store.SuppressionWindowRecurring, Timezone: "Europe/London", Weekdays: []int{7}, StartLocal: "01:00", EndLocal: "02:00"}
	first := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	second := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	if len(MatchSuppressionWindows([]store.SuppressionWindow{fallback}, SuppressionTarget{}, first)) != 1 || len(MatchSuppressionWindows([]store.SuppressionWindow{fallback}, SuppressionTarget{}, second)) != 1 {
		t.Fatal("fall-back repeated hour did not match both UTC instants")
	}
	if len(MatchSuppressionWindows([]store.SuppressionWindow{fallback}, SuppressionTarget{}, time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC))) != 0 {
		t.Fatal("fall-back end boundary matched")
	}

	spring := fallback
	spring.ID = "spring"
	spring.StartLocal = "01:30"
	spring.EndLocal = "02:30"
	if len(MatchSuppressionWindows([]store.SuppressionWindow{spring}, SuppressionTarget{}, time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC))) != 0 {
		t.Fatal("nonexistent spring-forward wall time matched")
	}
}

func TestIncidentSuppressionAddsHostOfflineOnlyToDependentStateIncidents(t *testing.T) {
	dependent := store.Incident{ID: "service", DeviceID: "device-1", RuleSnapshot: map[string]any{"kind": string(RuleKindState), "triggerState": "failed"}}
	host := dependent
	host.ID = "host"
	host.RuleSnapshot = map[string]any{"kind": string(RuleKindState), "triggerState": "offline"}
	if !hostOfflineSuppressionApplies(dependent) || hostOfflineSuppressionApplies(host) {
		t.Fatal("host-offline derivative suppression classification is incorrect")
	}
	decision := EvaluateSuppression(nil, SuppressionTarget{}, true, time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))
	if !decision.Suppressed || len(decision.Reasons) != 1 || decision.Reasons[0] != SuppressionReasonHostOffline {
		t.Fatalf("host-offline decision = %+v", decision)
	}
}

func TestSuppressedDeliveryIntentCreatesEpisodeAndReleaseSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)
	repository := store.NewMemory()
	clock := NewManualClock(now)
	repository.SetClock(func() time.Time { return clock.Now() })
	destination, err := repository.PutNotificationDestination(ctx, store.NotificationDestination{ID: "destination-suppression", Name: "Suppression", BaseURL: "https://ntfy.example.test", MaskedTopic: "sc********s", Enabled: true, SecretCiphertext: []byte("cipher"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	site, err := repository.CreateSite(ctx, store.Site{ID: "site-suppression", Name: "Suppression site"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{ID: "device-suppression", SiteID: site.ID, DisplayName: "Suppressed host", Availability: store.AvailabilityOnline})
	if err != nil {
		t.Fatal(err)
	}
	window, err := repository.PutSuppressionWindow(ctx, store.SuppressionWindow{ID: "window-suppression", Name: "Maintenance", TargetKind: TargetFleet, Enabled: true, Mode: store.SuppressionWindowOneTime, StartsAt: timePointer(now.Add(-time.Minute)), EndsAt: timePointer(now.Add(time.Hour))}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if window.Revision != 1 {
		t.Fatalf("window revision = %d", window.Revision)
	}
	incident := &store.Incident{ID: "incident-suppression", LineageID: "lineage-suppression", EntityID: "service-suppression", DeviceID: device.ID, RuleSnapshot: map[string]any{"kind": string(RuleKindState), "name": "web.service", "triggerState": "failed"}, Severity: SeverityCritical, Status: string(IncidentActive), EvidenceState: string(EvidenceFresh), OpenedAt: now, ObservedAt: now, EvaluatedAt: now, Revision: 1}
	intents, err := BuildNotificationDeliveryIntentsWithSuppression([]store.NotificationDestination{destination}, incident, []store.IncidentTransition{{ID: "transition-suppression", IncidentID: incident.ID, Kind: "triggered"}}, now, SuppressionDecision{Suppressed: true, Reasons: []string{"quiet:" + window.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || intents[0].Status != store.NotificationDeliverySuppressed {
		t.Fatalf("suppressed intents = %+v", intents)
	}
	if err := repository.ApplyAlertEvaluationWithDeliveries(ctx, store.AlertEvaluation{LineageID: incident.LineageID, EntityID: incident.EntityID, EvidenceState: string(EvidenceFresh), UpdatedAt: now}, incident, []store.IncidentTransition{{ID: "transition-suppression", IncidentID: incident.ID, Kind: "triggered", EvidenceState: string(EvidenceFresh), OccurredAt: now}}, intents); err != nil {
		t.Fatal(err)
	}
	episodes, err := repository.ListOpenSuppressionEpisodes(ctx)
	if err != nil || len(episodes) != 1 {
		t.Fatalf("open episodes = %+v err=%v", episodes, err)
	}
	clock.Set(now.Add(2 * time.Hour))
	summary, err := BuildSuppressionSummaryDelivery(destination, episodes[0], []store.Incident{*incident}, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CloseSuppressionEpisode(ctx, episodes[0], &summary, clock.Now()); err != nil {
		t.Fatal(err)
	}
	page, err := repository.ListNotificationDeliveries(ctx, store.NotificationDeliveryQuery{Status: store.NotificationDeliveryQueued, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("summary deliveries = %+v err=%v", page.Items, err)
	}
	if page.Items[0].SummaryKey == "" || page.Items[0].SafeError != "" {
		t.Fatalf("queued summary metadata = %+v", page.Items[0])
	}
	stored, err := repository.GetNotificationDelivery(ctx, page.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Payload != nil {
		t.Fatal("public delivery exposed summary payload")
	}
	var open []store.SuppressionEpisode
	open, err = repository.ListOpenSuppressionEpisodes(ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("episodes after close = %+v err=%v", open, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(summary.Payload, &payload); err != nil {
		t.Fatalf("summary payload JSON = %s", summary.Payload)
	}
	message, _ := payload["message"].(string)
	if !strings.Contains(message, "web.service") {
		t.Fatalf("summary payload = %s", summary.Payload)
	}
}

func TestPersistentEvaluatorSuppressesAndWorkerReleasesActiveSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)
	clock := NewManualClock(now)
	repository := store.NewMemory()
	repository.SetClock(func() time.Time { return clock.Now() })
	keyRing, err := secrets.NewKeyRing([]byte(strings.Repeat("p", 32)))
	if err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), "Suppression ended") || strings.Contains(string(body), "triggered") {
			t.Fatalf("released notification body = %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"summary-accepted"}`))
	}))
	defer receiver.Close()
	destination := encryptedDeliveryDestination(t, keyRing, "persistent-suppression-destination", receiver.URL, "scout_alerts", "summary-token")
	storedDestination, err := repository.PutNotificationDestination(ctx, destination, 0)
	if err != nil {
		t.Fatal(err)
	}
	site, err := repository.CreateSite(ctx, store.Site{ID: "persistent-suppression-site", Name: "Persistent suppression site"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{ID: "persistent-suppression-device", SiteID: site.ID, DisplayName: "Persistent suppression host", Availability: store.AvailabilityOnline})
	if err != nil {
		t.Fatal(err)
	}
	window, err := repository.PutSuppressionWindow(ctx, store.SuppressionWindow{ID: "persistent-suppression-window", Name: "Short maintenance", TargetKind: TargetDevice, TargetID: device.ID, Enabled: true, Mode: store.SuppressionWindowOneTime, StartsAt: timePointer(now.Add(-time.Minute)), EndsAt: timePointer(now.Add(time.Hour))}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if window.Revision != 1 {
		t.Fatalf("suppression window revision = %d", window.Revision)
	}
	evaluator := NewPersistentEvaluator(repository, clock, "persistent-suppression-evaluator")
	rule := Rule{ID: "persistent-suppression-rule", LineageID: "persistent-suppression-rule", Revision: 1, Name: "Service failure", Kind: RuleKindState, TriggerState: "failed", ClearState: "active", Severity: SeverityCritical, Enabled: true, MinimumConsecutiveSamples: 1}
	observation := Observation{EntityID: "persistent-suppression-service", State: "failed", Evidence: EvidenceFresh, ObservedAt: now, ReceivedAt: now, Source: "systemd"}
	if _, err := evaluator.EvaluateAndPersist(ctx, rule, observation, device.ID, nil); err != nil {
		t.Fatal(err)
	}
	suppressed, err := repository.ListNotificationDeliveries(ctx, store.NotificationDeliveryQuery{Status: store.NotificationDeliverySuppressed, Limit: 10})
	if err != nil || len(suppressed.Items) != 1 {
		t.Fatalf("suppressed deliveries = %+v err=%v", suppressed.Items, err)
	}
	open, err := repository.ListOpenSuppressionEpisodes(ctx)
	if err != nil || len(open) != 1 || open[0].EntityID != observation.EntityID {
		t.Fatalf("open suppression episodes = %+v err=%v", open, err)
	}
	clock.Set(now.Add(2 * time.Hour))
	worker := NewDeliveryWorker(repository, ntfy.NewClient(receiver.Client()), keyRing, clock, "persistent-suppression-worker")
	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 1 || result.Claimed != 1 || result.Failed != 0 {
		t.Fatalf("release worker result = %+v", result)
	}
	remaining, err := repository.ListOpenSuppressionEpisodes(ctx)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining episodes = %+v err=%v", remaining, err)
	}
	queued, err := repository.ListNotificationDeliveries(ctx, store.NotificationDeliveryQuery{Status: store.NotificationDeliveryAccepted, Limit: 10})
	if err != nil || len(queued.Items) != 1 || queued.Items[0].SummaryKey == "" {
		t.Fatalf("accepted summary = %+v err=%v", queued.Items, err)
	}
	_ = storedDestination
}
