package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

const maxRecoverySummaryIncidents = 20

// ResumeNotifications rebuilds a bounded summary from the current active
// incident state and atomically clears the store's notification pause fence.
// The expected revision prevents a stale owner tab from resuming a newer
// recovery session.
func ResumeNotifications(ctx context.Context, repository *store.Store, expectedRevision int64) (store.WorkspaceState, store.NotificationDeliveryEnqueueResult, error) {
	if repository == nil || expectedRevision < 1 {
		return store.WorkspaceState{}, store.NotificationDeliveryEnqueueResult{}, fmt.Errorf("%w: notification recovery dependencies", store.ErrInvalid)
	}
	destinations, err := repository.ListNotificationDestinations(ctx)
	if err != nil {
		return store.WorkspaceState{}, store.NotificationDeliveryEnqueueResult{}, err
	}
	active, err := listActiveIncidents(ctx, repository)
	if err != nil {
		return store.WorkspaceState{}, store.NotificationDeliveryEnqueueResult{}, err
	}
	now := repository.Now().UTC()
	deliveries, err := BuildRecoverySummaryDeliveries(destinations, active, expectedRevision+1, now)
	if err != nil {
		return store.WorkspaceState{}, store.NotificationDeliveryEnqueueResult{}, err
	}
	return repository.ResumeNotificationDeliveries(ctx, expectedRevision, deliveries)
}

// BuildRecoverySummaryDeliveries creates one summary per enabled destination
// and entity. It intentionally carries no incident or transition identity: a
// resume summary is a fresh snapshot, not a replay of the old outbox.
func BuildRecoverySummaryDeliveries(destinations []store.NotificationDestination, active []store.Incident, resumedRevision int64, now time.Time) ([]store.NotificationDelivery, error) {
	if resumedRevision < 1 {
		return nil, fmt.Errorf("%w: resumed notification revision", store.ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	byEntity := map[string][]store.Incident{}
	for _, incident := range active {
		if incident.Status != string(IncidentActive) || strings.TrimSpace(incident.EntityID) == "" {
			continue
		}
		byEntity[incident.EntityID] = append(byEntity[incident.EntityID], incident)
	}
	entities := make([]string, 0, len(byEntity))
	for entityID := range byEntity {
		entities = append(entities, entityID)
	}
	sort.Strings(entities)
	sortedDestinations := append([]store.NotificationDestination(nil), destinations...)
	sort.Slice(sortedDestinations, func(left, right int) bool {
		if sortedDestinations[left].ID == sortedDestinations[right].ID {
			return sortedDestinations[left].Name < sortedDestinations[right].Name
		}
		return sortedDestinations[left].ID < sortedDestinations[right].ID
	})

	result := make([]store.NotificationDelivery, 0, len(entities)*len(sortedDestinations))
	for _, entityID := range entities {
		incidents := append([]store.Incident(nil), byEntity[entityID]...)
		sort.Slice(incidents, func(left, right int) bool {
			if incidents[left].Severity == incidents[right].Severity {
				return incidents[left].ID < incidents[right].ID
			}
			return incidents[left].Severity > incidents[right].Severity
		})
		labels := make([]string, 0, minInt(maxRecoverySummaryIncidents, len(incidents)))
		for _, incident := range incidents[:minInt(maxRecoverySummaryIncidents, len(incidents))] {
			labels = append(labels, recoveryIncidentLabel(incident))
		}
		message := "Notifications resumed. Active incidents for " + boundedDeliveryText(entityID, 128) + ": " + strings.Join(labels, ", ")
		if remaining := len(incidents) - len(labels); remaining > 0 {
			message += fmt.Sprintf("; %d more", remaining)
		}
		payload, err := json.Marshal(notificationDeliveryPayload{Title: "Scout summary: notifications resumed", Message: message, Priority: 3})
		if err != nil {
			return nil, fmt.Errorf("encode recovery summary: %w", err)
		}
		if len(payload) > 4096 {
			return nil, fmt.Errorf("%w: recovery summary payload", store.ErrBackpressure)
		}
		for _, destination := range sortedDestinations {
			if destination.RetiredAt != nil || !destination.Enabled || destination.Revision < 1 {
				continue
			}
			result = append(result, store.NotificationDelivery{
				ID:                  store.NewID(),
				DestinationID:       destination.ID,
				DestinationRevision: destination.Revision,
				SummaryKey:          fmt.Sprintf("recovery:%d:%s", resumedRevision, boundedDeliveryText(entityID, 180)),
				Status:              store.NotificationDeliveryQueued,
				NextAttemptAt:       timePointer(now),
				ExpiresAt:           now.Add(store.NotificationDeliveryTTL),
				Payload:             append([]byte(nil), payload...),
				CreatedAt:           now,
				UpdatedAt:           now,
			})
		}
	}
	return result, nil
}

func listActiveIncidents(ctx context.Context, repository *store.Store) ([]store.Incident, error) {
	result := []store.Incident{}
	cursor := ""
	for {
		page, err := repository.ListIncidentPage(ctx, store.IncidentQuery{Status: string(IncidentActive), Cursor: cursor, Limit: 500})
		if err != nil {
			return nil, err
		}
		result = append(result, page.Items...)
		if page.NextCursor == "" {
			return result, nil
		}
		cursor = page.NextCursor
	}
}

func recoveryIncidentLabel(incident store.Incident) string {
	label := ""
	if incident.RuleSnapshot != nil {
		if value, ok := incident.RuleSnapshot["name"].(string); ok {
			label = strings.TrimSpace(value)
		}
	}
	if label == "" {
		label = incident.ID
	}
	if incident.Severity != "" {
		label = strings.ToLower(strings.TrimSpace(incident.Severity)) + " " + label
	}
	return boundedDeliveryText(label, 160)
}
