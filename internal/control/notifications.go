package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"scout.local/scout/internal/notifications/ntfy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

const notificationDestinationSecretKind = "notification-destination"

type notificationDestinationSecret struct {
	Topic string `json:"topic"`
	Token string `json:"token,omitempty"`
}

type notificationDestinationInput struct {
	Name           string          `json:"name"`
	BaseURL        string          `json:"baseUrl"`
	Topic          string          `json:"topic"`
	Token          json.RawMessage `json:"token"`
	Enabled        *bool           `json:"enabled"`
	AllowPlainHTTP bool            `json:"allowPlainHttp"`
}

type notificationDestinationPatch struct {
	ExpectedRevision int64           `json:"expectedRevision"`
	Name             json.RawMessage `json:"name"`
	BaseURL          json.RawMessage `json:"baseUrl"`
	Topic            json.RawMessage `json:"topic"`
	Token            json.RawMessage `json:"token"`
	Enabled          *bool           `json:"enabled"`
	AllowPlainHTTP   *bool           `json:"allowPlainHttp"`
}

func (a *App) registerNotificationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/notification-deliveries", a.listNotificationDeliveries)
	mux.HandleFunc("GET /api/v1/notification-destinations", a.listNotificationDestinations)
	mux.HandleFunc("POST /api/v1/notification-destinations", a.createNotificationDestination)
	mux.HandleFunc("GET /api/v1/notification-destinations/{destinationId}", a.getNotificationDestination)
	mux.HandleFunc("PATCH /api/v1/notification-destinations/{destinationId}", a.updateNotificationDestination)
	mux.HandleFunc("DELETE /api/v1/notification-destinations/{destinationId}", a.deleteNotificationDestination)
	mux.HandleFunc("POST /api/v1/notification-destinations/{destinationId}/test", a.testNotificationDestination)
}

func (a *App) listNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	limit, cursor, err := alertPageParams(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	page, err := a.Store.ListNotificationDeliveries(r.Context(), store.NotificationDeliveryQuery{
		DestinationID: strings.TrimSpace(r.URL.Query().Get("destinationId")),
		IncidentID:    strings.TrimSpace(r.URL.Query().Get("incidentId")),
		Status:        strings.TrimSpace(r.URL.Query().Get("status")),
		Cursor:        cursor,
		Limit:         limit,
	})
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, notificationDeliveryResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nullableCursor(page.NextCursor)})
}

func (a *App) listNotificationDestinations(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	limit, cursor, err := alertPageParams(r)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	items, err := a.Store.ListNotificationDestinations(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	page, err := paginateNotificationDestinations(items, cursor, limit)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	output := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		output = append(output, notificationDestinationResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": output, "nextCursor": nullableCursor(page.NextCursor)})
}

func (a *App) createNotificationDestination(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request notificationDestinationInput
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	token, err := parseNotificationToken(request.Token)
	if err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	validated, err := a.notificationClient().ValidateDestination(r.Context(), ntfy.Destination{
		BaseURL:        request.BaseURL,
		Topic:          request.Topic,
		Token:          token,
		AllowPlainHTTP: request.AllowPlainHTTP,
	})
	if err != nil {
		writeNotificationValidationError(w, r, err)
		return
	}
	id := store.NewID()
	envelope, err := a.encryptNotificationDestination(id, notificationDestinationSecret{Topic: validated.Topic, Token: validated.Token})
	if err != nil {
		writeNotificationSecretError(w, r, err)
		return
	}
	enabled := false
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	destination := store.NotificationDestination{
		ID:                   id,
		Name:                 strings.TrimSpace(request.Name),
		BaseURL:              validated.BaseURL,
		MaskedTopic:          validated.MaskedTopic(),
		HasToken:             validated.HasToken(),
		AllowPlainHTTP:       validated.AllowPlainHTTP,
		Enabled:              enabled,
		SecretCiphertext:     envelope.Ciphertext,
		SecretNonce:          envelope.Nonce,
		SecretWrappedDataKey: envelope.WrappedDataKey,
		SecretKeyVersion:     envelope.KeyVersion,
	}
	created, err := a.Store.PutNotificationDestination(r.Context(), destination, 0)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "notification_destination.create", created.ID, map[string]any{"revision": created.Revision, "enabled": created.Enabled, "allowPlainHttp": created.AllowPlainHTTP})
	writeJSON(w, http.StatusCreated, notificationDestinationResponse(created))
}

func (a *App) getNotificationDestination(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOwner(w, r, false); !ok {
		return
	}
	destination, err := a.Store.GetNotificationDestination(r.Context(), r.PathValue("destinationId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if destination.RetiredAt != nil {
		writeMappedError(w, r, store.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, notificationDestinationResponse(destination))
}

func (a *App) updateNotificationDestination(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request notificationDestinationPatch
	if err := decodeJSON(r, &request, 64<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if !notificationDestinationPatchHasChanges(request) {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	current, err := a.Store.GetNotificationDestination(r.Context(), r.PathValue("destinationId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if current.RetiredAt != nil {
		writeMappedError(w, r, store.ErrNotFound)
		return
	}
	if current.Revision != request.ExpectedRevision {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	stored, err := a.Store.GetNotificationDestinationSecret(r.Context(), current.ID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	secret, err := a.decryptNotificationDestination(stored)
	if err != nil {
		writeNotificationSecretError(w, r, err)
		return
	}

	updated := current
	if value, present, parseErr := parseNotificationStringPatch(request.Name); parseErr != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	} else if present {
		updated.Name = value
	}
	if value, present, parseErr := parseNotificationStringPatch(request.BaseURL); parseErr != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	} else if present {
		updated.BaseURL = value
	}
	if len(request.Topic) > 0 {
		value, parseErr := parseNotificationTopic(request.Topic)
		if parseErr != nil {
			writeMappedError(w, r, store.ErrInvalid)
			return
		}
		secret.Topic = value
	}
	if len(request.Token) > 0 {
		value, parseErr := parseNotificationToken(request.Token)
		if parseErr != nil {
			writeMappedError(w, r, store.ErrInvalid)
			return
		}
		secret.Token = value
	}
	if request.Enabled != nil {
		updated.Enabled = *request.Enabled
	}
	if request.AllowPlainHTTP != nil {
		updated.AllowPlainHTTP = *request.AllowPlainHTTP
	}
	validated, err := a.notificationClient().ValidateDestination(r.Context(), ntfy.Destination{
		BaseURL:        updated.BaseURL,
		Topic:          secret.Topic,
		Token:          secret.Token,
		AllowPlainHTTP: updated.AllowPlainHTTP,
	})
	if err != nil {
		writeNotificationValidationError(w, r, err)
		return
	}
	envelope, err := a.encryptNotificationDestination(current.ID, notificationDestinationSecret{Topic: validated.Topic, Token: validated.Token})
	if err != nil {
		writeNotificationSecretError(w, r, err)
		return
	}
	updated.BaseURL = validated.BaseURL
	updated.MaskedTopic = validated.MaskedTopic()
	updated.HasToken = validated.HasToken()
	updated.SecretCiphertext = envelope.Ciphertext
	updated.SecretNonce = envelope.Nonce
	updated.SecretWrappedDataKey = envelope.WrappedDataKey
	updated.SecretKeyVersion = envelope.KeyVersion
	updated, err = a.Store.PutNotificationDestination(r.Context(), updated, request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "notification_destination.update", updated.ID, map[string]any{"revision": updated.Revision, "enabled": updated.Enabled, "allowPlainHttp": updated.AllowPlainHTTP})
	writeJSON(w, http.StatusOK, notificationDestinationResponse(updated))
}

func (a *App) deleteNotificationDestination(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request expectedRevisionInput
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	destination, err := a.Store.RetireNotificationDestination(r.Context(), r.PathValue("destinationId"), request.ExpectedRevision)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "notification_destination.retire", destination.ID, map[string]any{"revision": destination.Revision})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) testNotificationDestination(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSensitive(w, r); !ok {
		return
	}
	var request expectedRevisionInput
	if err := decodeJSON(r, &request, 16<<10); err != nil || request.ExpectedRevision < 1 {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	destination, err := a.Store.GetNotificationDestination(r.Context(), r.PathValue("destinationId"))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	if destination.RetiredAt != nil {
		writeMappedError(w, r, store.ErrNotFound)
		return
	}
	if !destination.Enabled {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	if destination.Revision != request.ExpectedRevision {
		writeMappedError(w, r, store.ErrConflict)
		return
	}
	stored, err := a.Store.GetNotificationDestinationSecret(r.Context(), destination.ID)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	configured, err := a.decryptNotificationDestination(stored)
	if err != nil {
		writeNotificationSecretError(w, r, err)
		return
	}
	result, err := a.notificationClient().Publish(r.Context(), configured, ntfy.Message{
		Title:    "Scout test notification",
		Message:  "Scout notification delivery is configured.",
		Priority: 3,
	})
	if err != nil {
		writeNotificationPublishError(w, r, err)
		return
	}
	if !result.Accepted {
		writeNotificationPublishError(w, r, ntfy.ErrPermanent)
		return
	}
	if _, err := a.Store.RecordNotificationDestinationTest(r.Context(), destination.ID, request.ExpectedRevision, a.Store.Now()); err != nil {
		writeMappedError(w, r, err)
		return
	}
	a.recordOwnerAudit(r, "notification_destination.test", destination.ID, map[string]any{"revision": request.ExpectedRevision, "status": "accepted"})
	writeJSON(w, http.StatusAccepted, map[string]any{"deliveryId": store.NewID(), "status": "queued"})
}

func (a *App) notificationClient() *ntfy.Client {
	if a.Ntfy != nil {
		return a.Ntfy
	}
	return ntfy.NewClient(nil)
}

func (a *App) encryptNotificationDestination(id string, secret notificationDestinationSecret) (secrets.Envelope, error) {
	if a.Secrets == nil {
		return secrets.Envelope{}, secrets.ErrKeyUnavailable
	}
	plaintext, err := json.Marshal(secret)
	if err != nil {
		return secrets.Envelope{}, err
	}
	return a.Secrets.EncryptSecret(id, notificationDestinationSecretKind, plaintext)
}

func (a *App) decryptNotificationDestination(destination store.NotificationDestination) (ntfy.Destination, error) {
	if a.Secrets == nil {
		return ntfy.Destination{}, secrets.ErrKeyUnavailable
	}
	plaintext, err := a.Secrets.DecryptSecret(destination.ID, notificationDestinationSecretKind, secrets.Envelope{
		Ciphertext: destination.SecretCiphertext, Nonce: destination.SecretNonce, WrappedDataKey: destination.SecretWrappedDataKey, KeyVersion: destination.SecretKeyVersion,
	})
	if err != nil {
		return ntfy.Destination{}, err
	}
	var secret notificationDestinationSecret
	if err := json.Unmarshal(plaintext, &secret); err != nil || secret.Topic == "" {
		return ntfy.Destination{}, errors.New("notification destination secret is invalid")
	}
	return ntfy.Destination{BaseURL: destination.BaseURL, Topic: secret.Topic, Token: secret.Token, AllowPlainHTTP: destination.AllowPlainHTTP}, nil
}

func parseNotificationToken(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func parseNotificationTopic(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", store.ErrInvalid
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func parseNotificationStringPatch(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return "", true, store.ErrInvalid
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", true, err
	}
	return strings.TrimSpace(value), true, nil
}

func notificationDestinationPatchHasChanges(request notificationDestinationPatch) bool {
	return len(request.Name) > 0 || len(request.BaseURL) > 0 || len(request.Topic) > 0 || len(request.Token) > 0 || request.Enabled != nil || request.AllowPlainHTTP != nil
}

func notificationDestinationResponse(item store.NotificationDestination) map[string]any {
	var lastTestAt any
	if item.LastTestAt != nil {
		lastTestAt = item.LastTestAt.UTC()
	}
	return map[string]any{
		"id":             item.ID,
		"name":           item.Name,
		"baseUrl":        item.BaseURL,
		"maskedTopic":    item.MaskedTopic,
		"hasToken":       item.HasToken,
		"allowPlainHttp": item.AllowPlainHTTP,
		"enabled":        item.Enabled,
		"revision":       item.Revision,
		"lastTestAt":     lastTestAt,
	}
}

func notificationDeliveryResponse(item store.NotificationDelivery) map[string]any {
	var incidentID, transitionID, nextAttemptAt, acceptedAt, safeError any
	if item.IncidentID != "" {
		incidentID = item.IncidentID
	}
	if item.TransitionID != "" {
		transitionID = item.TransitionID
	}
	if item.NextAttemptAt != nil {
		nextAttemptAt = item.NextAttemptAt.UTC()
	}
	if item.AcceptedAt != nil {
		acceptedAt = item.AcceptedAt.UTC()
	}
	if item.SafeError != "" {
		safeError = item.SafeError
	}
	return map[string]any{
		"id":                  item.ID,
		"destinationId":       item.DestinationID,
		"incidentId":          incidentID,
		"transitionId":        transitionID,
		"status":              item.Status,
		"attempts":            item.Attempts,
		"nextAttemptAt":       nextAttemptAt,
		"acceptedAt":          acceptedAt,
		"expiresAt":           item.ExpiresAt.UTC(),
		"safeError":           safeError,
		"destinationRevision": item.DestinationRevision,
	}
}

func paginateNotificationDestinations(items []store.NotificationDestination, cursor string, limit int) (alertPage[store.NotificationDestination], error) {
	start, err := alertCursorStart(cursor, 2)
	if err != nil {
		return alertPage[store.NotificationDestination]{}, err
	}
	if start != nil {
		key := strings.ToLower(start[0]) + "\x00" + start[1]
		filtered := items[:0]
		for _, item := range items {
			if notificationDestinationKey(item) > key {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	page := alertPage[store.NotificationDestination]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeAlertCursor([]string{strings.ToLower(last.Name), last.ID})
	}
	return page, nil
}

func notificationDestinationKey(item store.NotificationDestination) string {
	return strings.ToLower(item.Name) + "\x00" + item.ID
}

func writeNotificationValidationError(w http.ResponseWriter, r *http.Request, _ error) {
	writeError(w, r, http.StatusBadRequest, "invalid_destination", "Notification destination failed validation", false)
}

func writeNotificationSecretError(w http.ResponseWriter, r *http.Request, _ error) {
	writeError(w, r, http.StatusInternalServerError, "secret_error", "Notification destination could not be secured", false)
}

func writeNotificationPublishError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ntfy.ErrInvalidDestination), errors.Is(err, ntfy.ErrAddressNotAllowed), errors.Is(err, ntfy.ErrRedirect):
		writeError(w, r, http.StatusBadGateway, "notification_rejected", "The notification endpoint rejected the test delivery", false)
	case errors.Is(err, ntfy.ErrRetryable), errors.Is(err, ntfy.ErrTimeout), errors.Is(err, ntfy.ErrResponseLost):
		writeError(w, r, http.StatusServiceUnavailable, "notification_unavailable", "The notification endpoint is temporarily unavailable", true)
	case errors.Is(err, ntfy.ErrPermanent):
		writeError(w, r, http.StatusBadGateway, "notification_rejected", "The notification endpoint rejected the test delivery", false)
	default:
		writeError(w, r, http.StatusInternalServerError, "notification_error", "The test delivery could not be completed", false)
	}
}
