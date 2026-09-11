package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

const (
	SuppressionReasonHostOffline = "host_offline"
	maxSuppressionSummaryItems   = 20
)

type SuppressionTarget struct {
	DeviceID string
	SiteID   string
}

type SuppressionDecision struct {
	Suppressed bool
	Reasons    []string
	Windows    []store.SuppressionWindow
}

// MatchSuppressionWindows evaluates each UTC instant as local wall time. This
// makes both fall-back occurrences match and makes skipped spring-forward
// times match no instant.
func MatchSuppressionWindows(windows []store.SuppressionWindow, target SuppressionTarget, at time.Time) []store.SuppressionWindow {
	if at.IsZero() {
		return nil
	}
	result := make([]store.SuppressionWindow, 0, len(windows))
	for _, window := range windows {
		if !window.Enabled || window.RetiredAt != nil || !suppressionTargetMatches(window, target) {
			continue
		}
		if suppressionWindowMatches(window, at.UTC()) {
			result = append(result, window)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func EvaluateSuppression(windows []store.SuppressionWindow, target SuppressionTarget, hostOffline bool, at time.Time) SuppressionDecision {
	matching := MatchSuppressionWindows(windows, target, at)
	reasons := make([]string, 0, len(matching)+1)
	for _, window := range matching {
		reasons = append(reasons, "quiet:"+window.ID)
	}
	if hostOffline {
		reasons = append(reasons, SuppressionReasonHostOffline)
	}
	return SuppressionDecision{Suppressed: len(reasons) > 0, Reasons: reasons, Windows: matching}
}

func EvaluateIncidentSuppression(ctx context.Context, repository *store.Store, incident store.Incident, at time.Time) (SuppressionDecision, error) {
	if repository == nil {
		return SuppressionDecision{}, fmt.Errorf("%w: suppression store", store.ErrInvalid)
	}
	windows, err := listAllSuppressionWindows(ctx, repository)
	if err != nil {
		return SuppressionDecision{}, err
	}
	target := SuppressionTarget{DeviceID: incident.DeviceID}
	hostOffline := false
	if incident.DeviceID != "" {
		device, deviceErr := repository.GetDevice(ctx, incident.DeviceID)
		if deviceErr != nil && deviceErr != store.ErrNotFound {
			return SuppressionDecision{}, deviceErr
		}
		if deviceErr == nil {
			target.SiteID = device.SiteID
			hostOffline = device.Availability == store.AvailabilityOffline && hostOfflineSuppressionApplies(incident)
		}
	}
	return EvaluateSuppression(windows, target, hostOffline, at), nil
}

func listAllSuppressionWindows(ctx context.Context, repository *store.Store) ([]store.SuppressionWindow, error) {
	result := []store.SuppressionWindow{}
	cursor := ""
	for {
		page, err := repository.ListSuppressionWindows(ctx, store.SuppressionWindowQuery{Cursor: cursor, Limit: 500})
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

func suppressionTargetMatches(window store.SuppressionWindow, target SuppressionTarget) bool {
	switch window.TargetKind {
	case "fleet":
		return true
	case "site":
		return target.SiteID != "" && window.TargetID == target.SiteID
	case "device":
		return target.DeviceID != "" && window.TargetID == target.DeviceID
	default:
		return false
	}
}

func suppressionWindowMatches(window store.SuppressionWindow, at time.Time) bool {
	switch window.Mode {
	case store.SuppressionWindowOneTime:
		return window.StartsAt != nil && window.EndsAt != nil && !at.Before(*window.StartsAt) && at.Before(*window.EndsAt)
	case store.SuppressionWindowRecurring:
		return recurringSuppressionWindowMatches(window, at)
	default:
		return false
	}
}

func recurringSuppressionWindowMatches(window store.SuppressionWindow, at time.Time) bool {
	location, err := time.LoadLocation(window.Timezone)
	if err != nil {
		return false
	}
	local := at.UTC().In(location)
	start, startOK := localClockSeconds(window.StartLocal)
	end, endOK := localClockSeconds(window.EndLocal)
	if !startOK || !endOK || start == end {
		return false
	}
	current := localClockSecondsValue(local)
	currentDay := isoWeekday(local.Weekday())
	if start < end {
		return containsWeekday(window.Weekdays, currentDay) && current >= start && current < end
	}
	if current >= start && containsWeekday(window.Weekdays, currentDay) {
		return true
	}
	previousDay := isoWeekday(time.Date(local.Year(), local.Month(), local.Day()-1, 12, 0, 0, 0, location).Weekday())
	return current < end && containsWeekday(window.Weekdays, previousDay)
}

func localClockSeconds(value string) (int, bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}
	if value[0] < '0' || value[0] > '2' || value[1] < '0' || value[1] > '9' || value[3] < '0' || value[3] > '5' || value[4] < '0' || value[4] > '9' {
		return 0, false
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	if hour > 23 {
		return 0, false
	}
	minute := int(value[3]-'0')*10 + int(value[4]-'0')
	return hour*60*60 + minute*60, true
}

func localClockSecondsValue(value time.Time) int {
	return value.Hour()*60*60 + value.Minute()*60 + value.Second()
}

func isoWeekday(value time.Weekday) int {
	if value == time.Sunday {
		return 7
	}
	return int(value)
}

func containsWeekday(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func hostOfflineSuppressionApplies(incident store.Incident) bool {
	if incident.DeviceID == "" {
		return false
	}
	snapshot := incident.RuleSnapshot
	kind, _ := snapshot["kind"].(string)
	if kind != string(RuleKindState) {
		return false
	}
	state, _ := snapshot["triggerState"].(string)
	return state != "offline"
}

func BuildNotificationDeliveryIntentsWithSuppression(destinations []store.NotificationDestination, incident *store.Incident, transitions []store.IncidentTransition, now time.Time, decision SuppressionDecision) ([]store.NotificationDelivery, error) {
	if incident == nil || strings.TrimSpace(incident.ID) == "" {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	result := make([]store.NotificationDelivery, 0, len(destinations)*len(transitions))
	for _, transition := range transitions {
		if transition.IncidentID != incident.ID || transition.Kind != "triggered" && transition.Kind != "recovered" {
			continue
		}
		if strings.TrimSpace(transition.ID) == "" {
			return nil, fmt.Errorf("%w: notification transition id", store.ErrInvalid)
		}
		payload, err := encodeNotificationDeliveryPayload(*incident, transition)
		if err != nil {
			return nil, err
		}
		for _, destination := range destinations {
			if destination.RetiredAt != nil || !destination.Enabled || destination.Revision < 1 {
				continue
			}
			status := store.NotificationDeliveryQueued
			var nextAttemptAt *time.Time
			if decision.Suppressed {
				status = store.NotificationDeliverySuppressed
			} else {
				next := now
				nextAttemptAt = &next
			}
			result = append(result, store.NotificationDelivery{
				ID:                  store.NewID(),
				DestinationID:       destination.ID,
				DestinationRevision: destination.Revision,
				IncidentID:          incident.ID,
				TransitionID:        transition.ID,
				Status:              status,
				NextAttemptAt:       nextAttemptAt,
				ExpiresAt:           now.Add(store.NotificationDeliveryTTL),
				Payload:             payload,
				CreatedAt:           now,
				UpdatedAt:           now,
				SuppressionReasons:  append([]string(nil), decision.Reasons...),
			})
		}
	}
	return result, nil
}

func BuildSuppressionSummaryDelivery(destination store.NotificationDestination, episode store.SuppressionEpisode, incidents []store.Incident, now time.Time) (store.NotificationDelivery, error) {
	if strings.TrimSpace(destination.ID) == "" || strings.TrimSpace(episode.EntityID) == "" || episode.Epoch < 1 {
		return store.NotificationDelivery{}, fmt.Errorf("%w: suppression summary identity", store.ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	active := make([]store.Incident, 0, len(incidents))
	seen := map[string]struct{}{}
	for _, incident := range incidents {
		if incident.Status != string(IncidentActive) || incident.EntityID != episode.EntityID {
			continue
		}
		if _, exists := seen[incident.ID]; exists {
			continue
		}
		seen[incident.ID] = struct{}{}
		active = append(active, incident)
	}
	if len(active) == 0 {
		return store.NotificationDelivery{}, fmt.Errorf("%w: no active suppression incidents", store.ErrNotFound)
	}
	sort.Slice(active, func(left, right int) bool { return active[left].ID < active[right].ID })
	labels := make([]string, 0, minInt(maxSuppressionSummaryItems, len(active)))
	for _, incident := range active {
		if len(labels) >= maxSuppressionSummaryItems {
			break
		}
		label := incident.ID
		if name, ok := incident.RuleSnapshot["name"].(string); ok && strings.TrimSpace(name) != "" {
			label = name
		}
		labels = append(labels, boundedDeliveryText(label, 120))
	}
	message := "Suppression ended for " + boundedDeliveryText(episode.EntityID, 128) + ". Active incidents: " + strings.Join(labels, ", ")
	if remaining := len(active) - len(labels); remaining > 0 {
		message += fmt.Sprintf("; %d more", remaining)
	}
	payload, err := json.Marshal(notificationDeliveryPayload{Title: "Scout summary: suppression ended", Message: message, Priority: 3})
	if err != nil {
		return store.NotificationDelivery{}, fmt.Errorf("encode suppression summary: %w", err)
	}
	if len(payload) > 4096 {
		return store.NotificationDelivery{}, fmt.Errorf("%w: suppression summary payload", store.ErrBackpressure)
	}
	return store.NotificationDelivery{
		ID:                  store.NewID(),
		DestinationID:       destination.ID,
		DestinationRevision: destination.Revision,
		SummaryKey:          fmt.Sprintf("suppression:%s:%d", boundedDeliveryText(episode.EntityID, 180), episode.Epoch),
		Status:              store.NotificationDeliveryQueued,
		NextAttemptAt:       timePointer(now),
		ExpiresAt:           now.Add(store.NotificationDeliveryTTL),
		Payload:             payload,
		CreatedAt:           now,
		UpdatedAt:           now,
	}, nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (w *DeliveryWorker) releaseSuppressionSummaries(ctx context.Context) error {
	workspace, err := w.Store.Workspace(ctx)
	if err != nil {
		return err
	}
	if workspace.NotificationsPaused {
		return nil
	}
	episodes, err := w.Store.ListOpenSuppressionEpisodes(ctx)
	if err != nil {
		return err
	}
	now := w.Clock.Now().UTC()
	windows, err := listAllSuppressionWindows(ctx, w.Store)
	if err != nil {
		return err
	}
	var firstErr error
	for _, episode := range episodes {
		query := store.IncidentQuery{Status: string(IncidentActive), EntityID: episode.EntityID, DeviceID: episode.DeviceID, Limit: 500}
		active, activeErr := w.Store.ListIncidents(ctx, query)
		if activeErr != nil {
			if firstErr == nil {
				firstErr = activeErr
			}
			continue
		}
		if len(active) == 0 {
			if _, closeErr := w.Store.CloseSuppressionEpisode(ctx, episode, nil, now); closeErr != nil && !errors.Is(closeErr, store.ErrNotFound) && firstErr == nil {
				firstErr = closeErr
			}
			continue
		}
		target := SuppressionTarget{DeviceID: episode.DeviceID}
		deviceOffline := false
		if episode.DeviceID != "" {
			device, deviceErr := w.Store.GetDevice(ctx, episode.DeviceID)
			if deviceErr != nil && !errors.Is(deviceErr, store.ErrNotFound) {
				if firstErr == nil {
					firstErr = deviceErr
				}
				continue
			}
			if deviceErr == nil {
				target.SiteID = device.SiteID
				deviceOffline = device.Availability == store.AvailabilityOffline
			}
		}
		dependentOffline := false
		if deviceOffline {
			for _, incident := range active {
				if hostOfflineSuppressionApplies(incident) {
					dependentOffline = true
					break
				}
			}
		}
		decision := EvaluateSuppression(windows, target, dependentOffline, now)
		if decision.Suppressed {
			continue
		}
		destination, destinationErr := w.Store.GetNotificationDestination(ctx, episode.DestinationID)
		if destinationErr != nil && !errors.Is(destinationErr, store.ErrNotFound) {
			if firstErr == nil {
				firstErr = destinationErr
			}
			continue
		}
		var summary *store.NotificationDelivery
		if destinationErr == nil && destination.RetiredAt == nil && destination.Enabled {
			candidate, summaryErr := BuildSuppressionSummaryDelivery(destination, episode, active, now)
			if summaryErr != nil && !errors.Is(summaryErr, store.ErrNotFound) {
				if firstErr == nil {
					firstErr = summaryErr
				}
				continue
			}
			if summaryErr == nil {
				summary = &candidate
			}
		}
		if _, closeErr := w.Store.CloseSuppressionEpisode(ctx, episode, summary, now); closeErr != nil && !errors.Is(closeErr, store.ErrNotFound) && firstErr == nil {
			firstErr = closeErr
		}
	}
	return firstErr
}
