package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"scout.local/scout/internal/notifications/ntfy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

const (
	notificationDestinationSecretKind = "notification-destination"
	defaultDeliverySweepInterval      = 1 * time.Second
	defaultDeliveryLease              = 10 * time.Second
	defaultDeliveryWorkLimit          = 100
)

type notificationDeliveryPayload struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
	ClickURL string `json:"clickUrl,omitempty"`
}

// BuildNotificationDeliveryIntents projects durable incident transitions into
// safe, secret-free queue records. Only trigger and recovery transitions are
// externally visible notifications; acknowledgement and evidence changes are
// deliberately not sent as notifications in the initial delivery contract.
func BuildNotificationDeliveryIntents(destinations []store.NotificationDestination, incident *store.Incident, transitions []store.IncidentTransition, now time.Time) ([]store.NotificationDelivery, error) {
	return BuildNotificationDeliveryIntentsWithSuppression(destinations, incident, transitions, now, SuppressionDecision{})
}

func encodeNotificationDeliveryPayload(incident store.Incident, transition store.IncidentTransition) ([]byte, error) {
	severity := strings.ToLower(strings.TrimSpace(incident.Severity))
	if severity == "" {
		severity = "warning"
	}
	title := "Scout alert: " + severity
	message := "Incident " + boundedDeliveryText(incident.ID, 128)
	if transition.Kind == "recovered" {
		message += " recovered"
	} else {
		message += " triggered"
	}
	if incident.EntityID != "" {
		message += " for " + boundedDeliveryText(incident.EntityID, 128)
	}
	if incident.Source != "" {
		message += " (" + boundedDeliveryText(incident.Source, 96) + ")"
	}
	priority := 3
	if severity == "critical" {
		priority = 5
	}
	payload, err := json.Marshal(notificationDeliveryPayload{Title: title, Message: message, Priority: priority})
	if err != nil {
		return nil, fmt.Errorf("encode notification delivery payload: %w", err)
	}
	if len(payload) > 4096 {
		return nil, fmt.Errorf("%w: notification delivery payload", store.ErrBackpressure)
	}
	return payload, nil
}

func boundedDeliveryText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxBytes {
		return value
	}
	used := 0
	result := make([]rune, 0, len(value))
	for _, char := range value {
		size := len(string(char))
		if used+size > maxBytes {
			break
		}
		result = append(result, char)
		used += size
	}
	return string(result)
}

type DeliveryWorker struct {
	Store         *store.Store
	Client        *ntfy.Client
	Secrets       *secrets.KeyRing
	Clock         Clock
	WorkerID      string
	LeaseDuration time.Duration
	SweepInterval time.Duration
	WorkLimit     int
}

type DeliverySweepResult struct {
	Claimed   int
	Accepted  int
	Retried   int
	Failed    int
	Cancelled int
	Expired   int
}

func NewDeliveryWorker(repository *store.Store, client *ntfy.Client, keyRing *secrets.KeyRing, clock Clock, workerID string) *DeliveryWorker {
	if clock == nil {
		clock = wallClock{}
	}
	if strings.TrimSpace(workerID) == "" {
		workerID = "notifications-1"
	}
	if client == nil {
		client = ntfy.NewClient(nil)
	}
	return &DeliveryWorker{Store: repository, Client: client, Secrets: keyRing, Clock: clock, WorkerID: workerID, LeaseDuration: defaultDeliveryLease, SweepInterval: defaultDeliverySweepInterval, WorkLimit: defaultDeliveryWorkLimit}
}

// RunOnce claims and processes a bounded batch. Delivery records are leased
// before any network request and completed with a CAS on their lease epoch.
func (w *DeliveryWorker) RunOnce(ctx context.Context) (DeliverySweepResult, error) {
	if w == nil || w.Store == nil || w.Secrets == nil {
		return DeliverySweepResult{}, fmt.Errorf("%w: delivery worker dependencies", store.ErrInvalid)
	}
	if err := w.releaseSuppressionSummaries(ctx); err != nil {
		return DeliverySweepResult{}, err
	}
	claims, err := w.Store.ClaimNotificationDeliveries(ctx, w.WorkerID, w.WorkLimit, w.LeaseDuration)
	if err != nil {
		return DeliverySweepResult{}, err
	}
	result := DeliverySweepResult{Claimed: len(claims)}
	var firstErr error
	for _, claim := range claims {
		outcome := w.deliver(ctx, claim)
		if outcome.Suppressed && claim.IncidentID != "" {
			if incident, incidentErr := w.Store.GetIncident(ctx, claim.IncidentID); incidentErr == nil {
				if _, openErr := w.Store.OpenSuppressionEpisode(ctx, claim.DestinationID, incident.EntityID, incident.DeviceID, outcome.SuppressionReasons, w.Clock.Now().UTC()); openErr != nil && firstErr == nil {
					firstErr = openErr
				}
			}
		}
		completed, completeErr := w.Store.CompleteNotificationDelivery(ctx, claim, outcome)
		if completeErr != nil {
			if !errors.Is(completeErr, store.ErrConflict) {
				if firstErr == nil {
					firstErr = completeErr
				}
			}
			continue
		}
		switch completed.Status {
		case store.NotificationDeliveryAccepted:
			result.Accepted++
		case store.NotificationDeliveryRetry:
			result.Retried++
		case store.NotificationDeliveryCancelled:
			result.Cancelled++
		case store.NotificationDeliveryExpired:
			result.Expired++
		case store.NotificationDeliveryFailed:
			result.Failed++
		}
	}
	return result, firstErr
}

func (w *DeliveryWorker) deliver(ctx context.Context, claim store.NotificationDelivery) store.NotificationDeliveryOutcome {
	now := w.Clock.Now().UTC()
	outcome := store.NotificationDeliveryOutcome{Now: now}
	destination, err := w.Store.GetNotificationDestinationSecretAtRevision(ctx, claim.DestinationID, claim.DestinationRevision)
	if err != nil {
		outcome.Cancelled = true
		outcome.SafeError = "destination disabled or changed"
		return outcome
	}
	if claim.IncidentID != "" {
		incident, incidentErr := w.Store.GetIncident(ctx, claim.IncidentID)
		if incidentErr == nil {
			decision, suppressionErr := EvaluateIncidentSuppression(ctx, w.Store, incident, now)
			if suppressionErr != nil {
				outcome.Retryable = true
				outcome.SafeError = "notification suppression state unavailable"
				return outcome
			}
			if decision.Suppressed {
				outcome.Suppressed = true
				outcome.SuppressionReasons = append([]string(nil), decision.Reasons...)
				outcome.SafeError = "notification suppressed"
				return outcome
			}
		}
	}
	configured, err := w.decryptDestination(destination)
	if err != nil {
		outcome.SafeError = "notification destination secret unavailable"
		return outcome
	}
	message, err := decodeNotificationDeliveryPayload(claim.Payload)
	if err != nil {
		outcome.SafeError = "notification payload unavailable"
		return outcome
	}
	response, publishErr := w.Client.Publish(ctx, configured, message)
	if publishErr == nil && response.Accepted {
		outcome.Accepted = true
		outcome.RemoteID = response.RemoteID
		return outcome
	}
	outcome.RetryAfter = response.RetryAfter
	switch {
	case errors.Is(publishErr, ntfy.ErrRetryable), errors.Is(publishErr, ntfy.ErrTimeout), errors.Is(publishErr, ntfy.ErrResponseLost):
		outcome.Retryable = true
		outcome.SafeError = "notification endpoint temporarily unavailable"
	case errors.Is(publishErr, ntfy.ErrInvalidDestination), errors.Is(publishErr, ntfy.ErrAddressNotAllowed), errors.Is(publishErr, ntfy.ErrRedirect), errors.Is(publishErr, ntfy.ErrPermanent):
		outcome.SafeError = "notification endpoint rejected delivery"
	default:
		outcome.Retryable = true
		outcome.SafeError = "notification endpoint temporarily unavailable"
	}
	return outcome
}

func (w *DeliveryWorker) decryptDestination(destination store.NotificationDestination) (ntfy.Destination, error) {
	if w.Secrets == nil {
		return ntfy.Destination{}, secrets.ErrKeyUnavailable
	}
	plaintext, err := w.Secrets.DecryptSecret(destination.ID, notificationDestinationSecretKind, secrets.Envelope{Ciphertext: destination.SecretCiphertext, Nonce: destination.SecretNonce, WrappedDataKey: destination.SecretWrappedDataKey, KeyVersion: destination.SecretKeyVersion})
	if err != nil {
		return ntfy.Destination{}, err
	}
	var secret struct {
		Topic string `json:"topic"`
		Token string `json:"token,omitempty"`
	}
	if err := json.Unmarshal(plaintext, &secret); err != nil || strings.TrimSpace(secret.Topic) == "" {
		return ntfy.Destination{}, fmt.Errorf("notification destination secret is invalid")
	}
	return ntfy.Destination{BaseURL: destination.BaseURL, Topic: secret.Topic, Token: secret.Token, AllowPlainHTTP: destination.AllowPlainHTTP}, nil
}

func decodeNotificationDeliveryPayload(payload []byte) (ntfy.Message, error) {
	var value notificationDeliveryPayload
	if len(payload) == 0 || json.Unmarshal(payload, &value) != nil || strings.TrimSpace(value.Message) == "" || value.Priority < 1 || value.Priority > 5 || len(value.Title) > 256 || len(value.Message) > 4096 || len(value.ClickURL) > 2048 {
		return ntfy.Message{}, store.ErrInvalid
	}
	return ntfy.Message{Title: strings.TrimSpace(value.Title), Message: value.Message, Priority: value.Priority, ClickURL: strings.TrimSpace(value.ClickURL)}, nil
}

func (w *DeliveryWorker) Run(ctx context.Context) error {
	if _, err := w.RunOnce(ctx); err != nil {
		return err
	}
	interval := w.SweepInterval
	if interval <= 0 {
		interval = defaultDeliverySweepInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.RunOnce(ctx); err != nil {
				return err
			}
		}
	}
}
