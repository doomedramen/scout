package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxSuppressionReasons = 16

func (s *Store) OpenSuppressionEpisode(ctx context.Context, destinationID, entityID, deviceID string, reasons []string, startedAt time.Time) (SuppressionEpisode, error) {
	destinationID = strings.TrimSpace(destinationID)
	entityID = strings.TrimSpace(entityID)
	deviceID = strings.TrimSpace(deviceID)
	if destinationID == "" || entityID == "" {
		return SuppressionEpisode{}, ErrInvalid
	}
	reasons, err := normalizeSuppressionReasons(reasons)
	if err != nil {
		return SuppressionEpisode{}, err
	}
	if startedAt.IsZero() {
		startedAt = s.now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}
	if s.db != nil {
		return s.openSuppressionEpisodeSQL(ctx, destinationID, entityID, deviceID, reasons, startedAt)
	}
	var result SuppressionEpisode
	err = s.mutate(ctx, func(state *State) error {
		var err error
		result, err = openSuppressionEpisodeState(state, destinationID, entityID, deviceID, reasons, startedAt)
		return err
	})
	return result, err
}

func openSuppressionEpisodeState(state *State, destinationID, entityID, deviceID string, reasons []string, startedAt time.Time) (SuppressionEpisode, error) {
	latest, exists := latestSuppressionEpisodeState(state, destinationID, entityID)
	if exists && latest.EndedAt == nil {
		latest.ReasonBits = mergeSuppressionReasons(latest.ReasonBits, reasons)
		if latest.DeviceID == "" {
			latest.DeviceID = deviceID
		}
		state.SuppressionEpisodes[suppressionEpisodeKey(destinationID, entityID, latest.Epoch)] = cloneSuppressionEpisode(latest)
		return cloneSuppressionEpisode(latest), nil
	}
	epoch := int64(1)
	if exists && latest.Epoch >= epoch {
		epoch = latest.Epoch + 1
	}
	item := SuppressionEpisode{DestinationID: destinationID, EntityID: entityID, DeviceID: deviceID, Epoch: epoch, StartedAt: startedAt, ReasonBits: reasons}
	state.SuppressionEpisodes[suppressionEpisodeKey(destinationID, entityID, epoch)] = cloneSuppressionEpisode(item)
	return cloneSuppressionEpisode(item), nil
}

func (s *Store) ListOpenSuppressionEpisodes(ctx context.Context) ([]SuppressionEpisode, error) {
	if s.db != nil {
		return s.listOpenSuppressionEpisodesSQL(ctx)
	}
	result := []SuppressionEpisode{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.SuppressionEpisodes {
			if item.EndedAt == nil {
				result = append(result, cloneSuppressionEpisode(item))
			}
		}
		sort.Slice(result, func(left, right int) bool {
			if result[left].DestinationID == result[right].DestinationID && result[left].EntityID == result[right].EntityID {
				return result[left].Epoch < result[right].Epoch
			}
			if result[left].DestinationID == result[right].DestinationID {
				return result[left].EntityID < result[right].EntityID
			}
			return result[left].DestinationID < result[right].DestinationID
		})
		return nil
	})
	return result, err
}

func (s *Store) CloseSuppressionEpisode(ctx context.Context, episode SuppressionEpisode, summary *NotificationDelivery, endedAt time.Time) (NotificationDeliveryEnqueueResult, error) {
	if strings.TrimSpace(episode.DestinationID) == "" || strings.TrimSpace(episode.EntityID) == "" || episode.Epoch < 1 {
		return NotificationDeliveryEnqueueResult{}, ErrInvalid
	}
	if summary != nil && strings.TrimSpace(summary.SummaryKey) == "" {
		return NotificationDeliveryEnqueueResult{}, ErrInvalid
	}
	if endedAt.IsZero() {
		endedAt = s.now().UTC()
	} else {
		endedAt = endedAt.UTC()
	}
	if s.db != nil {
		return s.closeSuppressionEpisodeSQL(ctx, episode, summary, endedAt)
	}
	var result NotificationDeliveryEnqueueResult
	err := s.mutate(ctx, func(state *State) error {
		working := cloneState(*state)
		key := suppressionEpisodeKey(episode.DestinationID, episode.EntityID, episode.Epoch)
		current, ok := working.SuppressionEpisodes[key]
		if !ok {
			return ErrNotFound
		}
		if current.EndedAt != nil {
			return nil
		}
		if summary != nil {
			var enqueueErr error
			result, enqueueErr = enqueueNotificationDeliveriesState(&working, []NotificationDelivery{*summary}, endedAt)
			if enqueueErr != nil {
				return enqueueErr
			}
			current.SummaryBatchID = summary.SummaryKey
		}
		current.EndedAt = timePointer(endedAt)
		working.SuppressionEpisodes[key] = cloneSuppressionEpisode(current)
		*state = working
		return nil
	})
	return result, err
}

func normalizeSuppressionReasons(reasons []string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(reasons))
	for _, raw := range reasons {
		reason := strings.TrimSpace(raw)
		if reason == "" || len(reason) > 64 {
			return nil, ErrInvalid
		}
		if _, exists := seen[reason]; exists {
			continue
		}
		seen[reason] = struct{}{}
		result = append(result, reason)
	}
	if len(result) == 0 || len(result) > maxSuppressionReasons {
		return nil, ErrInvalid
	}
	sort.Strings(result)
	return result, nil
}

func mergeSuppressionReasons(left, right []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(left)+len(right))
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	if len(result) > maxSuppressionReasons {
		result = result[:maxSuppressionReasons]
	}
	return result
}

func suppressionEpisodeKey(destinationID, entityID string, epoch int64) string {
	return safeCompositeKey(destinationID, entityID, strconv.FormatInt(epoch, 10))
}

func latestSuppressionEpisodeState(state *State, destinationID, entityID string) (SuppressionEpisode, bool) {
	var result SuppressionEpisode
	exists := false
	for _, item := range state.SuppressionEpisodes {
		if item.DestinationID != destinationID || item.EntityID != entityID || !exists || item.Epoch > result.Epoch {
			if item.DestinationID == destinationID && item.EntityID == entityID {
				result = item
				exists = true
			}
		}
	}
	return result, exists
}

func cloneSuppressionEpisode(item SuppressionEpisode) SuppressionEpisode {
	item.EndedAt = cloneTime(item.EndedAt)
	item.ReasonBits = append([]string(nil), item.ReasonBits...)
	return item
}

const suppressionEpisodeSelectColumns = `destination_id, entity_id, device_id, epoch, started_at, ended_at, reason_bits, summary_batch_id`

type suppressionEpisodeScanner interface {
	Scan(...any) error
}

func scanSuppressionEpisode(scanner suppressionEpisodeScanner) (SuppressionEpisode, error) {
	var item SuppressionEpisode
	var endedAt sql.NullTime
	var reasonBits []byte
	if err := scanner.Scan(&item.DestinationID, &item.EntityID, &item.DeviceID, &item.Epoch, &item.StartedAt, &endedAt, &reasonBits, &item.SummaryBatchID); err != nil {
		return SuppressionEpisode{}, err
	}
	if endedAt.Valid {
		value := endedAt.Time.UTC()
		item.EndedAt = &value
	}
	if len(reasonBits) > 0 {
		if err := json.Unmarshal(reasonBits, &item.ReasonBits); err != nil {
			return SuppressionEpisode{}, fmt.Errorf("decode suppression episode reasons: %w", err)
		}
	}
	return cloneSuppressionEpisode(item), nil
}

func (s *Store) openSuppressionEpisodeSQL(ctx context.Context, destinationID, entityID, deviceID string, reasons []string, startedAt time.Time) (SuppressionEpisode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SuppressionEpisode{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := openSuppressionEpisodeTx(ctx, tx, destinationID, entityID, deviceID, reasons, startedAt)
	if err != nil {
		return SuppressionEpisode{}, err
	}
	if err := tx.Commit(); err != nil {
		return SuppressionEpisode{}, fmt.Errorf("commit suppression episode: %w", err)
	}
	return cloneSuppressionEpisode(result), nil
}

func openSuppressionEpisodeTx(ctx context.Context, tx *sql.Tx, destinationID, entityID, deviceID string, reasons []string, startedAt time.Time) (SuppressionEpisode, error) {
	latest, err := scanSuppressionEpisode(tx.QueryRowContext(ctx, `SELECT `+suppressionEpisodeSelectColumns+` FROM suppression_episodes WHERE destination_id=$1 AND entity_id=$2 ORDER BY epoch DESC LIMIT 1 FOR UPDATE`, destinationID, entityID))
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SuppressionEpisode{}, fmt.Errorf("read suppression episode: %w", err)
	}
	var result SuppressionEpisode
	if exists && latest.EndedAt == nil {
		latest.ReasonBits = mergeSuppressionReasons(latest.ReasonBits, reasons)
		if latest.DeviceID == "" {
			latest.DeviceID = deviceID
		}
		encoded, marshalErr := json.Marshal(latest.ReasonBits)
		if marshalErr != nil {
			return SuppressionEpisode{}, fmt.Errorf("encode suppression episode reasons: %w", marshalErr)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE suppression_episodes SET device_id=$1, reason_bits=$2 WHERE destination_id=$3 AND entity_id=$4 AND epoch=$5`, latest.DeviceID, encoded, latest.DestinationID, latest.EntityID, latest.Epoch); err != nil {
			return SuppressionEpisode{}, mapSuppressionSQLError(err)
		}
		result = latest
	} else {
		epoch := int64(1)
		if exists && latest.Epoch >= epoch {
			epoch = latest.Epoch + 1
		}
		encoded, marshalErr := json.Marshal(reasons)
		if marshalErr != nil {
			return SuppressionEpisode{}, fmt.Errorf("encode suppression episode reasons: %w", marshalErr)
		}
		result = SuppressionEpisode{DestinationID: destinationID, EntityID: entityID, DeviceID: deviceID, Epoch: epoch, StartedAt: startedAt, ReasonBits: reasons}
		if _, err := tx.ExecContext(ctx, `INSERT INTO suppression_episodes (destination_id, entity_id, device_id, epoch, started_at, reason_bits) VALUES ($1,$2,$3,$4,$5,$6)`, destinationID, entityID, deviceID, epoch, startedAt, encoded); err != nil {
			return SuppressionEpisode{}, mapSuppressionSQLError(err)
		}
	}
	return cloneSuppressionEpisode(result), nil
}

func (s *Store) listOpenSuppressionEpisodesSQL(ctx context.Context) ([]SuppressionEpisode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+suppressionEpisodeSelectColumns+` FROM suppression_episodes WHERE ended_at IS NULL ORDER BY destination_id, entity_id, epoch`)
	if err != nil {
		return nil, fmt.Errorf("list open suppression episodes: %w", err)
	}
	defer rows.Close()
	result := []SuppressionEpisode{}
	for rows.Next() {
		item, scanErr := scanSuppressionEpisode(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan open suppression episode: %w", scanErr)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open suppression episodes: %w", err)
	}
	return result, nil
}

func (s *Store) closeSuppressionEpisodeSQL(ctx context.Context, episode SuppressionEpisode, summary *NotificationDelivery, endedAt time.Time) (NotificationDeliveryEnqueueResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NotificationDeliveryEnqueueResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var state State
	if summary != nil {
		state, err = readWorkspaceStateTx(ctx, tx)
		if err != nil {
			return NotificationDeliveryEnqueueResult{}, err
		}
	}
	current, err := scanSuppressionEpisode(tx.QueryRowContext(ctx, `SELECT `+suppressionEpisodeSelectColumns+` FROM suppression_episodes WHERE destination_id=$1 AND entity_id=$2 AND epoch=$3 FOR UPDATE`, episode.DestinationID, episode.EntityID, episode.Epoch))
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationDeliveryEnqueueResult{}, ErrNotFound
	}
	if err != nil {
		return NotificationDeliveryEnqueueResult{}, fmt.Errorf("read suppression episode for close: %w", err)
	}
	if current.EndedAt != nil {
		if err := tx.Commit(); err != nil {
			return NotificationDeliveryEnqueueResult{}, fmt.Errorf("commit closed suppression episode: %w", err)
		}
		return NotificationDeliveryEnqueueResult{}, nil
	}
	result := NotificationDeliveryEnqueueResult{}
	if summary != nil {
		result, err = enqueueNotificationDeliveriesSQLTx(ctx, tx, []NotificationDelivery{*summary}, endedAt)
		if err != nil {
			return result, err
		}
		state.Workspace.NotificationQueueOverflows += int64(result.Dropped)
		current.SummaryBatchID = summary.SummaryKey
	}
	current.EndedAt = timePointer(endedAt)
	if _, err := tx.ExecContext(ctx, `UPDATE suppression_episodes SET ended_at=$1, summary_batch_id=$2 WHERE destination_id=$3 AND entity_id=$4 AND epoch=$5`, current.EndedAt, current.SummaryBatchID, current.DestinationID, current.EntityID, current.Epoch); err != nil {
		return result, mapSuppressionSQLError(err)
	}
	if summary != nil {
		if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit suppression episode close: %w", err)
	}
	return result, nil
}
