package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const MaxNotificationDestinations = 10

func (s *Store) ListNotificationDestinations(ctx context.Context) ([]NotificationDestination, error) {
	if s.db != nil {
		return s.listNotificationDestinationsSQL(ctx)
	}
	result := []NotificationDestination{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.NotificationDestinations {
			if item.RetiredAt != nil {
				continue
			}
			result = append(result, publicNotificationDestination(item))
		}
		sort.Slice(result, func(left, right int) bool {
			if result[left].Name == result[right].Name {
				return result[left].ID < result[right].ID
			}
			return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
		})
		return nil
	})
	return result, err
}

func (s *Store) GetNotificationDestination(ctx context.Context, id string) (NotificationDestination, error) {
	if strings.TrimSpace(id) == "" {
		return NotificationDestination{}, ErrInvalid
	}
	if s.db != nil {
		item, err := s.getNotificationDestinationSQL(ctx, id)
		if err != nil {
			return NotificationDestination{}, err
		}
		return publicNotificationDestination(item), nil
	}
	var result NotificationDestination
	err := s.read(ctx, func(state *State) error {
		item, ok := state.NotificationDestinations[id]
		if !ok {
			return ErrNotFound
		}
		result = publicNotificationDestination(item)
		return nil
	})
	return result, err
}

// GetNotificationDestinationSecret is deliberately explicit: only code that
// is about to publish may retrieve the encrypted envelope. It never decrypts
// or returns the topic/token itself.
func (s *Store) GetNotificationDestinationSecret(ctx context.Context, id string) (NotificationDestination, error) {
	if strings.TrimSpace(id) == "" {
		return NotificationDestination{}, ErrInvalid
	}
	if s.db != nil {
		return s.getNotificationDestinationSQL(ctx, id)
	}
	var result NotificationDestination
	err := s.read(ctx, func(state *State) error {
		item, ok := state.NotificationDestinations[id]
		if !ok {
			return ErrNotFound
		}
		result = cloneNotificationDestination(item)
		return nil
	})
	return result, err
}

func (s *Store) PutNotificationDestination(ctx context.Context, destination NotificationDestination, expectedRevision int64) (NotificationDestination, error) {
	if s.db != nil {
		return s.putNotificationDestinationSQL(ctx, destination, expectedRevision)
	}
	var result NotificationDestination
	err := s.mutate(ctx, func(state *State) error {
		if destination.ID == "" {
			destination.ID = NewID()
		}
		current, exists := state.NotificationDestinations[destination.ID]
		if exists {
			if expectedRevision > 0 && current.Revision != expectedRevision {
				return ErrConflict
			}
			if !notificationSecretEnvelopePresent(destination) {
				destination.SecretCiphertext = append([]byte(nil), current.SecretCiphertext...)
				destination.SecretNonce = append([]byte(nil), current.SecretNonce...)
				destination.SecretWrappedDataKey = append([]byte(nil), current.SecretWrappedDataKey...)
				destination.SecretKeyVersion = current.SecretKeyVersion
			} else if !notificationSecretEnvelopeComplete(destination) {
				return ErrInvalid
			}
			destination.CreatedAt = current.CreatedAt
			destination.Revision = current.Revision + 1
			destination.RetiredAt = cloneTime(current.RetiredAt)
		} else {
			if countActiveNotificationDestinations(state.NotificationDestinations) >= MaxNotificationDestinations {
				return ErrBackpressure
			}
			if !notificationSecretEnvelopeComplete(destination) {
				return ErrInvalid
			}
			destination.Revision = 1
			if destination.CreatedAt.IsZero() {
				destination.CreatedAt = s.now().UTC()
			}
		}
		if err := validateNotificationDestination(destination); err != nil {
			return err
		}
		destination.UpdatedAt = s.now().UTC()
		state.NotificationDestinations[destination.ID] = cloneNotificationDestination(destination)
		result = publicNotificationDestination(destination)
		return nil
	})
	return result, err
}

func (s *Store) RetireNotificationDestination(ctx context.Context, id string, expectedRevision int64) (NotificationDestination, error) {
	if strings.TrimSpace(id) == "" {
		return NotificationDestination{}, ErrInvalid
	}
	if s.db != nil {
		return s.retireNotificationDestinationSQL(ctx, id, expectedRevision)
	}
	var result NotificationDestination
	err := s.mutate(ctx, func(state *State) error {
		destination, ok := state.NotificationDestinations[id]
		if !ok {
			return ErrNotFound
		}
		if expectedRevision > 0 && destination.Revision != expectedRevision {
			return ErrConflict
		}
		if destination.RetiredAt != nil {
			result = publicNotificationDestination(destination)
			return nil
		}
		when := s.now().UTC()
		destination.Enabled = false
		destination.Revision++
		destination.RetiredAt = &when
		destination.UpdatedAt = when
		state.NotificationDestinations[id] = cloneNotificationDestination(destination)
		result = publicNotificationDestination(destination)
		return nil
	})
	return result, err
}

func (s *Store) RecordNotificationDestinationTest(ctx context.Context, id string, expectedRevision int64, when time.Time) (NotificationDestination, error) {
	if strings.TrimSpace(id) == "" || expectedRevision < 1 {
		return NotificationDestination{}, ErrInvalid
	}
	when = when.UTC()
	if when.IsZero() {
		when = s.now().UTC()
	}
	if s.db != nil {
		return s.recordNotificationDestinationTestSQL(ctx, id, expectedRevision, when)
	}
	var result NotificationDestination
	err := s.mutate(ctx, func(state *State) error {
		destination, ok := state.NotificationDestinations[id]
		if !ok {
			return ErrNotFound
		}
		if destination.Revision != expectedRevision || destination.RetiredAt != nil {
			return ErrConflict
		}
		destination.LastTestAt = &when
		destination.UpdatedAt = when
		state.NotificationDestinations[id] = cloneNotificationDestination(destination)
		result = publicNotificationDestination(destination)
		return nil
	})
	return result, err
}

func validateNotificationDestination(destination NotificationDestination) error {
	if strings.TrimSpace(destination.ID) == "" || strings.TrimSpace(destination.Name) == "" || len(destination.Name) > 120 || strings.TrimSpace(destination.BaseURL) == "" || len(destination.BaseURL) > 2048 || strings.TrimSpace(destination.MaskedTopic) == "" || len(destination.MaskedTopic) > 128 {
		return ErrInvalid
	}
	if destination.Revision < 1 || destination.SecretKeyVersion < 1 || !notificationSecretEnvelopeComplete(destination) {
		return ErrInvalid
	}
	return nil
}

func notificationSecretEnvelopePresent(destination NotificationDestination) bool {
	return len(destination.SecretCiphertext) > 0 || len(destination.SecretNonce) > 0 || len(destination.SecretWrappedDataKey) > 0 || destination.SecretKeyVersion > 0
}

func notificationSecretEnvelopeComplete(destination NotificationDestination) bool {
	return len(destination.SecretCiphertext) > 0 && len(destination.SecretNonce) > 0 && len(destination.SecretWrappedDataKey) > 0 && destination.SecretKeyVersion > 0
}

func countActiveNotificationDestinations(items map[string]NotificationDestination) int {
	count := 0
	for _, item := range items {
		if item.RetiredAt == nil {
			count++
		}
	}
	return count
}

func publicNotificationDestination(destination NotificationDestination) NotificationDestination {
	result := cloneNotificationDestination(destination)
	result.SecretCiphertext = nil
	result.SecretNonce = nil
	result.SecretWrappedDataKey = nil
	result.SecretKeyVersion = 0
	return result
}

func cloneNotificationDestination(destination NotificationDestination) NotificationDestination {
	destination.LastTestAt = cloneTime(destination.LastTestAt)
	destination.RetiredAt = cloneTime(destination.RetiredAt)
	destination.SecretCiphertext = append([]byte(nil), destination.SecretCiphertext...)
	destination.SecretNonce = append([]byte(nil), destination.SecretNonce...)
	destination.SecretWrappedDataKey = append([]byte(nil), destination.SecretWrappedDataKey...)
	return destination
}

const notificationDestinationSelectColumns = `id, name, base_url, masked_topic, has_token, allow_plain_http, enabled, revision, last_test_at, retired_at, secret_ciphertext, secret_nonce, secret_wrapped_data_key, secret_key_version, created_at, updated_at`

type notificationDestinationScanner interface {
	Scan(...any) error
}

func scanNotificationDestination(scanner notificationDestinationScanner) (NotificationDestination, error) {
	var destination NotificationDestination
	var lastTestAt, retiredAt sql.NullTime
	if err := scanner.Scan(&destination.ID, &destination.Name, &destination.BaseURL, &destination.MaskedTopic, &destination.HasToken, &destination.AllowPlainHTTP, &destination.Enabled, &destination.Revision, &lastTestAt, &retiredAt, &destination.SecretCiphertext, &destination.SecretNonce, &destination.SecretWrappedDataKey, &destination.SecretKeyVersion, &destination.CreatedAt, &destination.UpdatedAt); err != nil {
		return NotificationDestination{}, err
	}
	destination.LastTestAt = nullableTimePointer(lastTestAt)
	destination.RetiredAt = nullableTimePointer(retiredAt)
	destination = cloneNotificationDestination(destination)
	return destination, nil
}

func (s *Store) listNotificationDestinationsSQL(ctx context.Context) ([]NotificationDestination, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+notificationDestinationSelectColumns+` FROM notification_destinations WHERE retired_at IS NULL ORDER BY lower(name), id`)
	if err != nil {
		return nil, fmt.Errorf("list notification destinations: %w", err)
	}
	defer rows.Close()
	result := []NotificationDestination{}
	for rows.Next() {
		item, scanErr := scanNotificationDestination(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan notification destination: %w", scanErr)
		}
		result = append(result, publicNotificationDestination(item))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notification destination rows: %w", err)
	}
	return result, nil
}

func (s *Store) getNotificationDestinationSQL(ctx context.Context, id string) (NotificationDestination, error) {
	item, err := scanNotificationDestination(s.db.QueryRowContext(ctx, `SELECT `+notificationDestinationSelectColumns+` FROM notification_destinations WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationDestination{}, ErrNotFound
	}
	if err != nil {
		return NotificationDestination{}, fmt.Errorf("get notification destination: %w", err)
	}
	return item, nil
}

func (s *Store) putNotificationDestinationSQL(ctx context.Context, destination NotificationDestination, expectedRevision int64) (NotificationDestination, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NotificationDestination{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if destination.ID == "" {
		destination.ID = NewID()
	}
	current, err := scanNotificationDestination(tx.QueryRowContext(ctx, `SELECT `+notificationDestinationSelectColumns+` FROM notification_destinations WHERE id=$1 FOR UPDATE`, destination.ID))
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return NotificationDestination{}, fmt.Errorf("read notification destination: %w", err)
	}
	if exists {
		if expectedRevision > 0 && current.Revision != expectedRevision {
			return NotificationDestination{}, ErrConflict
		}
		if !notificationSecretEnvelopePresent(destination) {
			destination.SecretCiphertext = append([]byte(nil), current.SecretCiphertext...)
			destination.SecretNonce = append([]byte(nil), current.SecretNonce...)
			destination.SecretWrappedDataKey = append([]byte(nil), current.SecretWrappedDataKey...)
			destination.SecretKeyVersion = current.SecretKeyVersion
		} else if !notificationSecretEnvelopeComplete(destination) {
			return NotificationDestination{}, ErrInvalid
		}
		destination.CreatedAt = current.CreatedAt
		destination.Revision = current.Revision + 1
		destination.RetiredAt = cloneTime(current.RetiredAt)
	} else {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_destinations WHERE retired_at IS NULL`).Scan(&count); err != nil {
			return NotificationDestination{}, fmt.Errorf("count notification destinations: %w", err)
		}
		if count >= MaxNotificationDestinations {
			return NotificationDestination{}, ErrBackpressure
		}
		if !notificationSecretEnvelopeComplete(destination) {
			return NotificationDestination{}, ErrInvalid
		}
		destination.Revision = 1
		if destination.CreatedAt.IsZero() {
			destination.CreatedAt = s.now().UTC()
		}
	}
	if err := validateNotificationDestination(destination); err != nil {
		return NotificationDestination{}, err
	}
	destination.UpdatedAt = s.now().UTC()
	if exists {
		_, err = tx.ExecContext(ctx, `UPDATE notification_destinations SET name=$1, base_url=$2, masked_topic=$3, has_token=$4, allow_plain_http=$5, enabled=$6, revision=$7, retired_at=$8, secret_ciphertext=$9, secret_nonce=$10, secret_wrapped_data_key=$11, secret_key_version=$12, updated_at=$13 WHERE id=$14`, destination.Name, destination.BaseURL, destination.MaskedTopic, destination.HasToken, destination.AllowPlainHTTP, destination.Enabled, destination.Revision, destination.RetiredAt, destination.SecretCiphertext, destination.SecretNonce, destination.SecretWrappedDataKey, destination.SecretKeyVersion, destination.UpdatedAt, destination.ID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO notification_destinations (id, name, base_url, masked_topic, has_token, allow_plain_http, enabled, revision, last_test_at, retired_at, secret_ciphertext, secret_nonce, secret_wrapped_data_key, secret_key_version, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, destination.ID, destination.Name, destination.BaseURL, destination.MaskedTopic, destination.HasToken, destination.AllowPlainHTTP, destination.Enabled, destination.Revision, destination.LastTestAt, destination.RetiredAt, destination.SecretCiphertext, destination.SecretNonce, destination.SecretWrappedDataKey, destination.SecretKeyVersion, destination.CreatedAt, destination.UpdatedAt)
	}
	if err != nil {
		return NotificationDestination{}, mapNotificationDestinationSQLError(err)
	}
	if err := tx.Commit(); err != nil {
		return NotificationDestination{}, fmt.Errorf("commit notification destination: %w", err)
	}
	return publicNotificationDestination(destination), nil
}

func (s *Store) retireNotificationDestinationSQL(ctx context.Context, id string, expectedRevision int64) (NotificationDestination, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NotificationDestination{}, err
	}
	defer func() { _ = tx.Rollback() }()
	destination, err := scanNotificationDestination(tx.QueryRowContext(ctx, `SELECT `+notificationDestinationSelectColumns+` FROM notification_destinations WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationDestination{}, ErrNotFound
	}
	if err != nil {
		return NotificationDestination{}, fmt.Errorf("read notification destination for retirement: %w", err)
	}
	if expectedRevision > 0 && destination.Revision != expectedRevision {
		return NotificationDestination{}, ErrConflict
	}
	if destination.RetiredAt == nil {
		when := s.now().UTC()
		destination.Enabled = false
		destination.Revision++
		destination.RetiredAt = &when
		destination.UpdatedAt = when
		if _, err := tx.ExecContext(ctx, `UPDATE notification_destinations SET enabled=false, revision=$1, retired_at=$2, updated_at=$3 WHERE id=$4`, destination.Revision, destination.RetiredAt, destination.UpdatedAt, id); err != nil {
			return NotificationDestination{}, mapNotificationDestinationSQLError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return NotificationDestination{}, fmt.Errorf("commit notification destination retirement: %w", err)
	}
	return publicNotificationDestination(destination), nil
}

func (s *Store) recordNotificationDestinationTestSQL(ctx context.Context, id string, expectedRevision int64, when time.Time) (NotificationDestination, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE notification_destinations SET last_test_at=$1, updated_at=$1 WHERE id=$2 AND revision=$3 AND retired_at IS NULL`, when, id, expectedRevision)
	if err != nil {
		return NotificationDestination{}, fmt.Errorf("record notification destination test: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if _, getErr := s.getNotificationDestinationSQL(ctx, id); errors.Is(getErr, ErrNotFound) {
			return NotificationDestination{}, ErrNotFound
		}
		return NotificationDestination{}, ErrConflict
	}
	return s.GetNotificationDestination(ctx, id)
}

func mapNotificationDestinationSQLError(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "duplicate key") || strings.Contains(message, "unique constraint") {
		return ErrConflict
	}
	return fmt.Errorf("notification destination storage: %w", err)
}
