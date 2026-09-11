package alerts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestRecoverySummaryIncludesOnlyCurrentActiveIncidentsAndIsBounded(t *testing.T) {
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	destinations := []store.NotificationDestination{
		{ID: "destination-enabled", Name: "Enabled", Enabled: true, Revision: 3},
		{ID: "destination-disabled", Name: "Disabled", Enabled: false, Revision: 2},
	}
	active := make([]store.Incident, 0, 22)
	for index := 0; index < 21; index++ {
		active = append(active, store.Incident{
			ID:           "incident-" + string(rune('a'+index)),
			EntityID:     "service/web",
			Severity:     SeverityCritical,
			Status:       string(IncidentActive),
			RuleSnapshot: map[string]any{"name": "web.service"},
		})
	}
	active = append(active, store.Incident{ID: "resolved", EntityID: "service/web", Severity: SeverityCritical, Status: string(IncidentResolved)})
	deliveries, err := BuildRecoverySummaryDeliveries(destinations, active, 8, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].DestinationID != "destination-enabled" {
		t.Fatalf("recovery summaries = %+v", deliveries)
	}
	delivery := deliveries[0]
	if delivery.IncidentID != "" || delivery.TransitionID != "" || delivery.SummaryKey != "recovery:8:service/web" {
		t.Fatalf("recovery summary identity = %+v", delivery)
	}
	var payload map[string]any
	if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	message, _ := payload["message"].(string)
	if !strings.Contains(message, "Notifications resumed") || !strings.Contains(message, "1 more") || strings.Contains(message, "resolved") {
		t.Fatalf("recovery summary message = %q", message)
	}
	if len(delivery.Payload) > 4096 || !delivery.ExpiresAt.Equal(now.Add(store.NotificationDeliveryTTL)) {
		t.Fatalf("recovery summary bounds = %+v", delivery)
	}
}
