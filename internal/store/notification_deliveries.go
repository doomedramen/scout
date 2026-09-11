package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	MaxPendingNotificationDeliveries = 10000
	MaxNotificationDeliveryAttempts  = 6
	NotificationDeliveryTTL          = 24 * time.Hour
	maxNotificationDeliveryPayload   = 4096
	maxNotificationDeliveryError     = 512
)

var notificationDeliveryRetryBackoff = [...]time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
}

func (s *Store) EnqueueNotificationDeliveries(ctx context.Context, deliveries []NotificationDelivery) (NotificationDeliveryEnqueueResult, error) {
	if len(deliveries) == 0 {
		return NotificationDeliveryEnqueueResult{}, nil
	}
	if s.db != nil {
		return s.enqueueNotificationDeliveriesSQL(ctx, deliveries)
	}
	var result NotificationDeliveryEnqueueResult
	err := s.mutate(ctx, func(state *State) error {
		var err error
		result, err = enqueueNotificationDeliveriesState(state, deliveries, s.now().UTC())
		return err
	})
	return result, err
}

func enqueueNotificationDeliveriesState(state *State, deliveries []NotificationDelivery, now time.Time) (NotificationDeliveryEnqueueResult, error) {
	result := NotificationDeliveryEnqueueResult{}
	normalized := make([]NotificationDelivery, 0, len(deliveries))
	seenIDs := make(map[string]NotificationDelivery, len(deliveries))
	for _, candidate := range deliveries {
		destination, ok := state.NotificationDestinations[candidate.DestinationID]
		if !ok || destination.RetiredAt != nil || !destination.Enabled {
			continue
		}
		item, err := normalizeNotificationDelivery(candidate, now)
		if err != nil {
			return result, err
		}
		item.DestinationRevision = destination.Revision
		if existing, exists := state.NotificationDeliveries[item.ID]; exists {
			if sameNotificationDelivery(existing, item) {
				continue
			}
			return result, ErrConflict
		}
		if existing, exists := seenIDs[item.ID]; exists {
			if sameNotificationDelivery(existing, item) {
				continue
			}
			return result, ErrConflict
		}
		seenIDs[item.ID] = item
		normalized = append(normalized, item)
	}
	for _, item := range normalized {
		if notificationDeliveryExistsState(state.NotificationDeliveries, item) {
			continue
		}
		if countPendingNotificationDeliveriesState(state.NotificationDeliveries) >= MaxPendingNotificationDeliveries {
			state.Workspace.NotificationQueueOverflows++
			result.Dropped++
			continue
		}
		state.NotificationDeliveries[item.ID] = cloneNotificationDelivery(item)
		result.Enqueued++
	}
	return result, nil
}

func (s *Store) enqueueNotificationDeliveriesSQL(ctx context.Context, deliveries []NotificationDelivery) (NotificationDeliveryEnqueueResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NotificationDeliveryEnqueueResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return NotificationDeliveryEnqueueResult{}, err
	}
	result, err := enqueueNotificationDeliveriesSQLTx(ctx, tx, deliveries, s.now().UTC())
	if err != nil {
		return result, err
	}
	if result.Dropped > 0 {
		state.Workspace.NotificationQueueOverflows += int64(result.Dropped)
		if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return NotificationDeliveryEnqueueResult{}, fmt.Errorf("commit notification delivery enqueue: %w", err)
	}
	return result, nil
}

func enqueueNotificationDeliveriesSQLTx(ctx context.Context, tx *sql.Tx, deliveries []NotificationDelivery, now time.Time) (NotificationDeliveryEnqueueResult, error) {
	result := NotificationDeliveryEnqueueResult{}
	for _, candidate := range deliveries {
		item, err := normalizeNotificationDelivery(candidate, now)
		if err != nil {
			return result, err
		}
		var revision int64
		err = tx.QueryRowContext(ctx, `SELECT revision FROM notification_destinations WHERE id=$1 AND retired_at IS NULL AND enabled=true FOR SHARE`, item.DestinationID).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("read notification destination for delivery: %w", err)
		}
		item.DestinationRevision = revision
		var duplicate bool
		if item.TransitionID != "" {
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_deliveries WHERE destination_id=$1 AND transition_id=$2)`, item.DestinationID, item.TransitionID).Scan(&duplicate); err != nil {
				return result, fmt.Errorf("check notification transition duplicate: %w", err)
			}
		}
		if !duplicate && item.SummaryKey != "" {
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_deliveries WHERE destination_id=$1 AND summary_key=$2)`, item.DestinationID, item.SummaryKey).Scan(&duplicate); err != nil {
				return result, fmt.Errorf("check notification summary duplicate: %w", err)
			}
		}
		if duplicate {
			continue
		}
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries WHERE status IN ('queued','sending','retry')`).Scan(&pending); err != nil {
			return result, fmt.Errorf("count pending notification deliveries: %w", err)
		}
		if pending >= MaxPendingNotificationDeliveries {
			result.Dropped++
			continue
		}
		insertResult, err := tx.ExecContext(ctx, `
			INSERT INTO notification_deliveries (id, destination_id, destination_revision, incident_id, transition_id, summary_key, status, attempts, next_attempt_at, expires_at, lease_epoch, lease_owner, lease_until, accepted_at, remote_id, safe_error, payload, created_at, updated_at)
			VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
			ON CONFLICT DO NOTHING`, item.ID, item.DestinationID, item.DestinationRevision, item.IncidentID, item.TransitionID, item.SummaryKey, item.Status, item.Attempts, item.NextAttemptAt, item.ExpiresAt, item.LeaseEpoch, item.LeaseOwner, item.LeaseUntil, item.AcceptedAt, item.RemoteID, item.SafeError, item.Payload, item.CreatedAt, item.UpdatedAt)
		if err != nil {
			return result, mapNotificationDeliverySQLError(err)
		}
		if inserted, rowsErr := insertResult.RowsAffected(); rowsErr == nil && inserted == 1 {
			result.Enqueued++
		}
	}
	return result, nil
}

func (s *Store) GetNotificationDelivery(ctx context.Context, id string) (NotificationDelivery, error) {
	if strings.TrimSpace(id) == "" {
		return NotificationDelivery{}, ErrInvalid
	}
	if s.db != nil {
		item, err := s.getNotificationDeliverySQL(ctx, id)
		if err != nil {
			return NotificationDelivery{}, err
		}
		return publicNotificationDelivery(item), nil
	}
	var result NotificationDelivery
	err := s.read(ctx, func(state *State) error {
		item, ok := state.NotificationDeliveries[id]
		if !ok {
			return ErrNotFound
		}
		result = publicNotificationDelivery(item)
		return nil
	})
	return result, err
}

func (s *Store) ListNotificationDeliveries(ctx context.Context, query NotificationDeliveryQuery) (NotificationDeliveryPage, error) {
	query = normalizeNotificationDeliveryQuery(query)
	if query.Status != "" && !validNotificationDeliveryStatus(query.Status) || len(query.Cursor) > 512 {
		return NotificationDeliveryPage{}, ErrInvalid
	}
	if s.db != nil {
		return s.listNotificationDeliveriesSQL(ctx, query)
	}
	start, err := decodeNotificationDeliveryCursor(query.Cursor)
	if err != nil {
		return NotificationDeliveryPage{}, err
	}
	items := []NotificationDelivery{}
	err = s.read(ctx, func(state *State) error {
		for _, item := range state.NotificationDeliveries {
			if query.DestinationID != "" && item.DestinationID != query.DestinationID || query.IncidentID != "" && item.IncidentID != query.IncidentID || query.Status != "" && item.Status != query.Status {
				continue
			}
			if start != nil && !notificationDeliveryBeforeCursor(item, *start) {
				continue
			}
			items = append(items, publicNotificationDelivery(item))
		}
		sortNotificationDeliveries(items)
		return nil
	})
	if err != nil {
		return NotificationDeliveryPage{}, err
	}
	return notificationDeliveryPage(items, query.Limit), nil
}

func (s *Store) ClaimNotificationDeliveries(ctx context.Context, owner string, limit int, lease time.Duration) ([]NotificationDelivery, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, ErrInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if lease <= 0 {
		lease = 10 * time.Second
	}
	if s.db != nil {
		return s.claimNotificationDeliveriesSQL(ctx, owner, limit, lease)
	}
	result := []NotificationDelivery{}
	err := s.mutate(ctx, func(state *State) error {
		result = claimNotificationDeliveriesState(state, owner, limit, lease, s.now().UTC())
		return nil
	})
	return result, err
}

func claimNotificationDeliveriesState(state *State, owner string, limit int, lease time.Duration, now time.Time) []NotificationDelivery {
	for key, item := range state.NotificationDeliveries {
		if item.Status == NotificationDeliverySending && (item.LeaseUntil == nil || !item.LeaseUntil.After(now)) {
			item.LeaseOwner = ""
			item.LeaseUntil = nil
			if !now.Before(item.ExpiresAt) {
				item.Status = NotificationDeliveryExpired
				item.NextAttemptAt = nil
				item.SafeError = "delivery expired before completion"
			} else if item.Attempts >= MaxNotificationDeliveryAttempts {
				item.Status = NotificationDeliveryFailed
				item.NextAttemptAt = nil
				item.SafeError = "delivery retry limit reached"
			} else {
				item.Status = NotificationDeliveryRetry
				item.NextAttemptAt = timePointer(now)
				item.SafeError = "delivery lease expired; retrying"
			}
			item.UpdatedAt = now
			state.NotificationDeliveries[key] = item
		}
		if (item.Status == NotificationDeliveryQueued || item.Status == NotificationDeliveryRetry) && !now.Before(item.ExpiresAt) {
			item.Status = NotificationDeliveryExpired
			item.NextAttemptAt = nil
			item.LeaseOwner = ""
			item.LeaseUntil = nil
			item.SafeError = "delivery expired before attempt"
			item.UpdatedAt = now
			state.NotificationDeliveries[key] = item
			continue
		}
		if item.Status != NotificationDeliveryQueued && item.Status != NotificationDeliveryRetry {
			continue
		}
		destination, ok := state.NotificationDestinations[item.DestinationID]
		if !ok || destination.RetiredAt != nil || !destination.Enabled || destination.Revision != item.DestinationRevision {
			item.Status = NotificationDeliveryCancelled
			item.NextAttemptAt = nil
			item.LeaseOwner = ""
			item.LeaseUntil = nil
			item.SafeError = "destination disabled or changed"
			item.UpdatedAt = now
			state.NotificationDeliveries[key] = item
			continue
		}
		if item.Attempts >= MaxNotificationDeliveryAttempts {
			item.Status = NotificationDeliveryFailed
			item.NextAttemptAt = nil
			item.SafeError = "delivery retry limit reached"
			item.UpdatedAt = now
			state.NotificationDeliveries[key] = item
		}
	}

	inFlight := map[string]struct{}{}
	for _, item := range state.NotificationDeliveries {
		if item.Status == NotificationDeliverySending && item.LeaseUntil != nil && item.LeaseUntil.After(now) {
			inFlight[item.DestinationID] = struct{}{}
		}
	}
	candidates := make([]NotificationDelivery, 0, len(state.NotificationDeliveries))
	for _, item := range state.NotificationDeliveries {
		if item.Status != NotificationDeliveryQueued && item.Status != NotificationDeliveryRetry || item.NextAttemptAt != nil && item.NextAttemptAt.After(now) || !now.Before(item.ExpiresAt) {
			continue
		}
		candidates = append(candidates, item)
	}
	sortNotificationDeliveriesForClaim(candidates)
	until := now.Add(lease)
	result := make([]NotificationDelivery, 0, minInt(limit, len(candidates)))
	for _, item := range candidates {
		if len(result) >= limit {
			break
		}
		if _, exists := inFlight[item.DestinationID]; exists {
			continue
		}
		item.Status = NotificationDeliverySending
		item.Attempts++
		item.LeaseEpoch++
		item.LeaseOwner = owner
		item.LeaseUntil = &until
		item.NextAttemptAt = nil
		item.SafeError = ""
		item.UpdatedAt = now
		key := item.ID
		state.NotificationDeliveries[key] = cloneNotificationDelivery(item)
		result = append(result, cloneNotificationDelivery(item))
		inFlight[item.DestinationID] = struct{}{}
	}
	return result
}

func (s *Store) CompleteNotificationDelivery(ctx context.Context, claim NotificationDelivery, outcome NotificationDeliveryOutcome) (NotificationDelivery, error) {
	if strings.TrimSpace(claim.ID) == "" || strings.TrimSpace(claim.LeaseOwner) == "" || claim.LeaseEpoch < 1 || claim.Status != NotificationDeliverySending {
		return NotificationDelivery{}, ErrInvalid
	}
	if s.db != nil {
		return s.completeNotificationDeliverySQL(ctx, claim, outcome)
	}
	var result NotificationDelivery
	err := s.mutate(ctx, func(state *State) error {
		current, ok := state.NotificationDeliveries[claim.ID]
		if !ok {
			return ErrNotFound
		}
		if current.Status != NotificationDeliverySending || current.LeaseOwner != claim.LeaseOwner || current.LeaseEpoch != claim.LeaseEpoch {
			return ErrConflict
		}
		if err := applyNotificationDeliveryOutcome(&current, outcome, s.now().UTC()); err != nil {
			return err
		}
		state.NotificationDeliveries[claim.ID] = cloneNotificationDelivery(current)
		result = publicNotificationDelivery(current)
		return nil
	})
	return result, err
}

func applyNotificationDeliveryOutcome(item *NotificationDelivery, outcome NotificationDeliveryOutcome, now time.Time) error {
	if item == nil || item.Status != NotificationDeliverySending {
		return ErrInvalid
	}
	if !outcome.Now.IsZero() {
		now = outcome.Now.UTC()
	}
	item.LeaseOwner = ""
	item.LeaseUntil = nil
	item.NextAttemptAt = nil
	item.RemoteID = boundedIncidentText(outcome.RemoteID, 256)
	item.SafeError = boundedIncidentText(outcome.SafeError, maxNotificationDeliveryError)
	item.UpdatedAt = now
	if outcome.Cancelled {
		item.Status = NotificationDeliveryCancelled
		item.RemoteID = ""
		return nil
	}
	if outcome.Suppressed {
		item.Status = NotificationDeliverySuppressed
		item.RemoteID = ""
		if item.SafeError == "" {
			item.SafeError = "notification suppressed"
		}
		return nil
	}
	if outcome.Accepted {
		item.Status = NotificationDeliveryAccepted
		acceptedAt := now
		item.AcceptedAt = &acceptedAt
		item.SafeError = ""
		return nil
	}
	item.AcceptedAt = nil
	if outcome.Retryable && now.Before(item.ExpiresAt) && item.Attempts < MaxNotificationDeliveryAttempts {
		delay := outcome.RetryAfter
		if delay <= 0 {
			delay = NotificationRetryDelay(item.ID, item.Attempts)
		}
		next := now.Add(delay)
		if next.Before(item.ExpiresAt) {
			item.Status = NotificationDeliveryRetry
			item.NextAttemptAt = &next
			return nil
		}
		item.Status = NotificationDeliveryExpired
		item.SafeError = "delivery expired before next retry"
		return nil
	}
	if !now.Before(item.ExpiresAt) {
		item.Status = NotificationDeliveryExpired
		if item.SafeError == "" {
			item.SafeError = "delivery expired"
		}
		return nil
	}
	item.Status = NotificationDeliveryFailed
	return nil
}

func NotificationRetryDelay(deliveryID string, attempt int) time.Duration {
	if attempt < 1 || attempt > len(notificationDeliveryRetryBackoff) {
		return 0
	}
	sum := sha256.Sum256([]byte(deliveryID + "/" + fmt.Sprint(attempt)))
	factor := 0.8 + float64(sum[0])/255*0.4
	return time.Duration(float64(notificationDeliveryRetryBackoff[attempt-1]) * factor)
}

func (s *Store) GetNotificationDestinationSecretAtRevision(ctx context.Context, id string, revision int64) (NotificationDestination, error) {
	if strings.TrimSpace(id) == "" || revision < 1 {
		return NotificationDestination{}, ErrInvalid
	}
	if s.db != nil {
		item, err := scanNotificationDestination(s.db.QueryRowContext(ctx, `SELECT `+notificationDestinationSelectColumns+` FROM notification_destinations WHERE id=$1 AND revision=$2 AND enabled=true AND retired_at IS NULL`, id, revision))
		if errors.Is(err, sql.ErrNoRows) {
			return NotificationDestination{}, ErrConflict
		}
		if err != nil {
			return NotificationDestination{}, fmt.Errorf("get notification destination revision: %w", err)
		}
		return item, nil
	}
	var result NotificationDestination
	err := s.read(ctx, func(state *State) error {
		item, ok := state.NotificationDestinations[id]
		if !ok || item.RetiredAt != nil || !item.Enabled || item.Revision != revision {
			return ErrConflict
		}
		result = cloneNotificationDestination(item)
		return nil
	})
	return result, err
}

func (s *Store) NotificationDeliveryStats(ctx context.Context) (NotificationDeliveryStats, error) {
	if s.db != nil {
		return s.notificationDeliveryStatsSQL(ctx)
	}
	var result NotificationDeliveryStats
	err := s.read(ctx, func(state *State) error {
		result.QueueOverflow = state.Workspace.NotificationQueueOverflows
		for _, item := range state.NotificationDeliveries {
			incrementNotificationDeliveryStats(&result, item.Status)
		}
		return nil
	})
	return result, err
}

func (s *Store) notificationDeliveryStatsSQL(ctx context.Context) (NotificationDeliveryStats, error) {
	var result NotificationDeliveryStats
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM notification_deliveries GROUP BY status`)
	if err != nil {
		return result, fmt.Errorf("read notification delivery stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return result, fmt.Errorf("scan notification delivery stats: %w", err)
		}
		setNotificationDeliveryStat(&result, status, count)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate notification delivery stats: %w", err)
	}
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT state_json FROM workspace_state WHERE singleton=true`).Scan(&raw); err != nil {
		return result, fmt.Errorf("read notification queue overflow: %w", err)
	}
	state := newState()
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &state); err != nil {
			return result, fmt.Errorf("decode notification queue overflow: %w", err)
		}
	}
	result.QueueOverflow = state.Workspace.NotificationQueueOverflows
	return result, nil
}

const notificationDeliverySelectColumns = `id, destination_id, destination_revision, incident_id, transition_id, summary_key, status, attempts, next_attempt_at, expires_at, lease_epoch, lease_owner, lease_until, accepted_at, remote_id, safe_error, payload, created_at, updated_at`

type notificationDeliveryScanner interface {
	Scan(...any) error
}

func scanNotificationDelivery(scanner notificationDeliveryScanner) (NotificationDelivery, error) {
	var item NotificationDelivery
	var incidentID, transitionID, summaryKey sql.NullString
	var nextAttemptAt, leaseUntil, acceptedAt sql.NullTime
	if err := scanner.Scan(&item.ID, &item.DestinationID, &item.DestinationRevision, &incidentID, &transitionID, &summaryKey, &item.Status, &item.Attempts, &nextAttemptAt, &item.ExpiresAt, &item.LeaseEpoch, &item.LeaseOwner, &leaseUntil, &acceptedAt, &item.RemoteID, &item.SafeError, &item.Payload, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return NotificationDelivery{}, err
	}
	if incidentID.Valid {
		item.IncidentID = incidentID.String
	}
	if transitionID.Valid {
		item.TransitionID = transitionID.String
	}
	if summaryKey.Valid {
		item.SummaryKey = summaryKey.String
	}
	item.NextAttemptAt = nullableTimePointer(nextAttemptAt)
	item.LeaseUntil = nullableTimePointer(leaseUntil)
	item.AcceptedAt = nullableTimePointer(acceptedAt)
	return cloneNotificationDelivery(item), nil
}

func (s *Store) getNotificationDeliverySQL(ctx context.Context, id string) (NotificationDelivery, error) {
	item, err := scanNotificationDelivery(s.db.QueryRowContext(ctx, `SELECT `+notificationDeliverySelectColumns+` FROM notification_deliveries WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationDelivery{}, ErrNotFound
	}
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("get notification delivery: %w", err)
	}
	return item, nil
}

func (s *Store) listNotificationDeliveriesSQL(ctx context.Context, query NotificationDeliveryQuery) (NotificationDeliveryPage, error) {
	args := []any{}
	where := []string{}
	add := func(expression string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expression, len(args)))
	}
	if query.DestinationID != "" {
		add("destination_id=$%d", query.DestinationID)
	}
	if query.IncidentID != "" {
		add("incident_id=$%d", query.IncidentID)
	}
	if query.Status != "" {
		add("status=$%d", query.Status)
	}
	if query.Cursor != "" {
		cursor, err := decodeNotificationDeliveryCursor(query.Cursor)
		if err != nil {
			return NotificationDeliveryPage{}, err
		}
		args = append(args, cursor.UpdatedAt, cursor.ID)
		where = append(where, fmt.Sprintf("(updated_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	statement := `SELECT ` + notificationDeliverySelectColumns + ` FROM notification_deliveries`
	if len(where) > 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	statement += fmt.Sprintf(" ORDER BY updated_at DESC, id DESC LIMIT %d", query.Limit+1)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return NotificationDeliveryPage{}, fmt.Errorf("list notification deliveries: %w", err)
	}
	defer rows.Close()
	items := []NotificationDelivery{}
	for rows.Next() {
		item, scanErr := scanNotificationDelivery(rows)
		if scanErr != nil {
			return NotificationDeliveryPage{}, fmt.Errorf("scan notification delivery: %w", scanErr)
		}
		items = append(items, publicNotificationDelivery(item))
	}
	if err := rows.Err(); err != nil {
		return NotificationDeliveryPage{}, fmt.Errorf("iterate notification deliveries: %w", err)
	}
	return notificationDeliveryPage(items, query.Limit), nil
}

func (s *Store) claimNotificationDeliveriesSQL(ctx context.Context, owner string, limit int, lease time.Duration) ([]NotificationDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='expired', next_attempt_at=NULL, lease_owner='', lease_until=NULL, safe_error='delivery expired before attempt', updated_at=$1 WHERE status IN ('queued','retry') AND expires_at <= $1`, now); err != nil {
		return nil, fmt.Errorf("expire notification deliveries: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status=CASE WHEN expires_at <= $1 THEN 'expired' WHEN attempts >= $2 THEN 'failed' ELSE 'retry' END, next_attempt_at=CASE WHEN expires_at <= $1 OR attempts >= $2 THEN NULL ELSE $1 END, lease_owner='', lease_until=NULL, safe_error=CASE WHEN expires_at <= $1 THEN 'delivery expired before completion' WHEN attempts >= $2 THEN 'delivery retry limit reached' ELSE 'delivery lease expired; retrying' END, updated_at=$1 WHERE status='sending' AND (lease_until IS NULL OR lease_until <= $1)`, now, MaxNotificationDeliveryAttempts); err != nil {
		return nil, fmt.Errorf("recover notification delivery leases: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries AS delivery SET status='failed', next_attempt_at=NULL, safe_error='delivery retry limit reached', updated_at=$1 WHERE status IN ('queued','retry') AND attempts >= $2`, now, MaxNotificationDeliveryAttempts); err != nil {
		return nil, fmt.Errorf("fail exhausted notification deliveries: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries AS delivery SET status='cancelled', next_attempt_at=NULL, lease_owner='', lease_until=NULL, safe_error='destination disabled or changed', updated_at=$1 FROM notification_destinations AS destination WHERE delivery.destination_id=destination.id AND delivery.status IN ('queued','retry') AND (destination.retired_at IS NOT NULL OR destination.enabled=false OR destination.revision <> delivery.destination_revision)`, now); err != nil {
		return nil, fmt.Errorf("cancel stale notification deliveries: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+notificationDeliverySelectColumns+` FROM notification_deliveries AS delivery WHERE delivery.status IN ('queued','retry') AND (delivery.next_attempt_at IS NULL OR delivery.next_attempt_at <= $1) AND delivery.expires_at > $1 AND NOT EXISTS (SELECT 1 FROM notification_deliveries AS in_flight WHERE in_flight.destination_id=delivery.destination_id AND in_flight.status='sending' AND in_flight.lease_until > $1) ORDER BY COALESCE(delivery.next_attempt_at, delivery.created_at), delivery.created_at, delivery.id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim notification deliveries: %w", err)
	}
	candidates := []NotificationDelivery{}
	for rows.Next() {
		item, scanErr := scanNotificationDelivery(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan claimable notification delivery: %w", scanErr)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate claimable notification deliveries: %w", err)
	}
	_ = rows.Close()
	inFlight := map[string]struct{}{}
	result := []NotificationDelivery{}
	until := now.Add(lease)
	for _, item := range candidates {
		if len(result) >= limit {
			break
		}
		if _, exists := inFlight[item.DestinationID]; exists {
			continue
		}
		oldOwner := item.LeaseOwner
		oldEpoch := item.LeaseEpoch
		if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='sending', attempts=attempts+1, lease_epoch=lease_epoch+1, lease_owner=$1, lease_until=$2, next_attempt_at=NULL, safe_error='', updated_at=$3 WHERE id=$4 AND status IN ('queued','retry') AND lease_epoch=$5 AND lease_owner=$6`, owner, until, now, item.ID, oldEpoch, oldOwner); err != nil {
			return nil, fmt.Errorf("lease notification delivery: %w", err)
		}
		item.Status = NotificationDeliverySending
		item.Attempts++
		item.LeaseEpoch++
		item.LeaseOwner = owner
		item.LeaseUntil = &until
		item.NextAttemptAt = nil
		item.SafeError = ""
		item.UpdatedAt = now
		result = append(result, item)
		inFlight[item.DestinationID] = struct{}{}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit notification delivery leases: %w", err)
	}
	return result, nil
}

func (s *Store) completeNotificationDeliverySQL(ctx context.Context, claim NotificationDelivery, outcome NotificationDeliveryOutcome) (NotificationDelivery, error) {
	now := s.now().UTC()
	if !outcome.Now.IsZero() {
		now = outcome.Now.UTC()
	}
	updated := cloneNotificationDelivery(claim)
	if err := applyNotificationDeliveryOutcome(&updated, outcome, now); err != nil {
		return NotificationDelivery{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE notification_deliveries SET status=$1, next_attempt_at=$2, lease_owner='', lease_until=NULL, accepted_at=$3, remote_id=$4, safe_error=$5, updated_at=$6 WHERE id=$7 AND status='sending' AND lease_owner=$8 AND lease_epoch=$9`, updated.Status, updated.NextAttemptAt, updated.AcceptedAt, updated.RemoteID, updated.SafeError, updated.UpdatedAt, claim.ID, claim.LeaseOwner, claim.LeaseEpoch)
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("complete notification delivery: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if _, getErr := s.getNotificationDeliverySQL(ctx, claim.ID); errors.Is(getErr, ErrNotFound) {
			return NotificationDelivery{}, ErrNotFound
		}
		return NotificationDelivery{}, ErrConflict
	}
	final, err := s.getNotificationDeliverySQL(ctx, claim.ID)
	if err != nil {
		return NotificationDelivery{}, err
	}
	return publicNotificationDelivery(final), nil
}

type notificationDeliveryCursor struct {
	UpdatedAt time.Time
	ID        string
}

func normalizeNotificationDeliveryQuery(query NotificationDeliveryQuery) NotificationDeliveryQuery {
	if query.Limit <= 0 {
		query.Limit = 100
	}
	return query
}

func normalizeNotificationDelivery(item NotificationDelivery, now time.Time) (NotificationDelivery, error) {
	item.ID = strings.TrimSpace(item.ID)
	item.DestinationID = strings.TrimSpace(item.DestinationID)
	item.IncidentID = strings.TrimSpace(item.IncidentID)
	item.TransitionID = strings.TrimSpace(item.TransitionID)
	item.SummaryKey = strings.TrimSpace(item.SummaryKey)
	if item.ID == "" {
		item.ID = NewID()
	}
	if item.Status == "" {
		item.Status = NotificationDeliveryQueued
	}
	if item.Status != NotificationDeliveryQueued && item.Status != NotificationDeliverySuppressed || item.Attempts != 0 || item.LeaseEpoch != 0 || item.LeaseOwner != "" || item.LeaseUntil != nil || item.AcceptedAt != nil || item.RemoteID != "" {
		return NotificationDelivery{}, ErrInvalid
	}
	if item.Status == NotificationDeliverySuppressed {
		item.NextAttemptAt = nil
		item.SafeError = "notification suppressed"
	} else {
		if item.SafeError != "" {
			return NotificationDelivery{}, ErrInvalid
		}
		if item.NextAttemptAt == nil {
			item.NextAttemptAt = timePointer(now)
		} else {
			value := item.NextAttemptAt.UTC()
			item.NextAttemptAt = &value
		}
	}
	if item.ExpiresAt.IsZero() {
		item.ExpiresAt = now.Add(NotificationDeliveryTTL)
	} else {
		item.ExpiresAt = item.ExpiresAt.UTC()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	} else {
		item.CreatedAt = item.CreatedAt.UTC()
	}
	item.UpdatedAt = now
	if len(item.TransitionID) == 0 && len(item.SummaryKey) == 0 || len(item.TransitionID) > 256 || len(item.SummaryKey) > 256 {
		return NotificationDelivery{}, ErrInvalid
	}
	if len(item.IncidentID) > 256 || item.ExpiresAt.IsZero() || !item.ExpiresAt.After(now) || item.DestinationID == "" || item.DestinationRevision < 0 {
		return NotificationDelivery{}, ErrInvalid
	}
	if len(item.Payload) == 0 || len(item.Payload) > maxNotificationDeliveryPayload || !json.Valid(item.Payload) {
		return NotificationDelivery{}, ErrInvalid
	}
	var object map[string]any
	if err := json.Unmarshal(item.Payload, &object); err != nil || object == nil {
		return NotificationDelivery{}, ErrInvalid
	}
	return cloneNotificationDelivery(item), nil
}

func notificationDeliveryExistsState(items map[string]NotificationDelivery, candidate NotificationDelivery) bool {
	for _, item := range items {
		if sameNotificationDelivery(item, candidate) {
			return true
		}
	}
	return false
}

func sameNotificationDelivery(left, right NotificationDelivery) bool {
	if left.DestinationID != right.DestinationID {
		return false
	}
	if left.TransitionID != "" && right.TransitionID != "" {
		return left.TransitionID == right.TransitionID
	}
	if left.SummaryKey != "" && right.SummaryKey != "" {
		return left.SummaryKey == right.SummaryKey
	}
	return false
}

func countPendingNotificationDeliveriesState(items map[string]NotificationDelivery) int {
	count := 0
	for _, item := range items {
		if item.Status == NotificationDeliveryQueued || item.Status == NotificationDeliverySending || item.Status == NotificationDeliveryRetry {
			count++
		}
	}
	return count
}

func validNotificationDeliveryStatus(status string) bool {
	switch status {
	case NotificationDeliveryQueued, NotificationDeliverySending, NotificationDeliveryRetry, NotificationDeliveryAccepted, NotificationDeliveryFailed, NotificationDeliveryCancelled, NotificationDeliverySuppressed, NotificationDeliveryExpired:
		return true
	default:
		return false
	}
}

func incrementNotificationDeliveryStats(stats *NotificationDeliveryStats, status string) {
	setNotificationDeliveryStat(stats, status, 1)
}

func setNotificationDeliveryStat(stats *NotificationDeliveryStats, status string, count int64) {
	switch status {
	case NotificationDeliveryQueued:
		stats.Queued += count
	case NotificationDeliverySending:
		stats.Sending += count
	case NotificationDeliveryRetry:
		stats.Retry += count
	case NotificationDeliveryAccepted:
		stats.Accepted += count
	case NotificationDeliveryFailed:
		stats.Failed += count
	case NotificationDeliveryCancelled:
		stats.Cancelled += count
	case NotificationDeliverySuppressed:
		stats.Suppressed += count
	case NotificationDeliveryExpired:
		stats.Expired += count
	}
}

func sortNotificationDeliveries(items []NotificationDelivery) {
	sort.Slice(items, func(left, right int) bool {
		if items[left].UpdatedAt.Equal(items[right].UpdatedAt) {
			return items[left].ID > items[right].ID
		}
		return items[left].UpdatedAt.After(items[right].UpdatedAt)
	})
}

func sortNotificationDeliveriesForClaim(items []NotificationDelivery) {
	sort.Slice(items, func(left, right int) bool {
		leftAttempt := time.Time{}
		if items[left].NextAttemptAt != nil {
			leftAttempt = *items[left].NextAttemptAt
		}
		rightAttempt := time.Time{}
		if items[right].NextAttemptAt != nil {
			rightAttempt = *items[right].NextAttemptAt
		}
		if leftAttempt.Equal(rightAttempt) {
			if items[left].CreatedAt.Equal(items[right].CreatedAt) {
				return items[left].ID < items[right].ID
			}
			return items[left].CreatedAt.Before(items[right].CreatedAt)
		}
		return leftAttempt.Before(rightAttempt)
	})
}

func notificationDeliveryPage(items []NotificationDelivery, limit int) NotificationDeliveryPage {
	page := NotificationDeliveryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeNotificationDeliveryCursor(last)
	}
	return page
}

func notificationDeliveryBeforeCursor(item NotificationDelivery, cursor notificationDeliveryCursor) bool {
	return item.UpdatedAt.Before(cursor.UpdatedAt) || item.UpdatedAt.Equal(cursor.UpdatedAt) && item.ID < cursor.ID
}

func encodeNotificationDeliveryCursor(item NotificationDelivery) string {
	value := item.UpdatedAt.UTC().Format(time.RFC3339Nano) + "\x00" + item.ID
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeNotificationDeliveryCursor(value string) (*notificationDeliveryCursor, error) {
	if value == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrInvalid
	}
	parts := strings.SplitN(string(data), "\x00", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, ErrInvalid
	}
	when, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, ErrInvalid
	}
	return &notificationDeliveryCursor{UpdatedAt: when.UTC(), ID: parts[1]}, nil
}

func publicNotificationDelivery(item NotificationDelivery) NotificationDelivery {
	result := cloneNotificationDelivery(item)
	result.Payload = nil
	result.LeaseOwner = ""
	result.LeaseUntil = nil
	return result
}

func cloneNotificationDelivery(item NotificationDelivery) NotificationDelivery {
	item.NextAttemptAt = cloneTime(item.NextAttemptAt)
	item.LeaseUntil = cloneTime(item.LeaseUntil)
	item.AcceptedAt = cloneTime(item.AcceptedAt)
	item.Payload = append([]byte(nil), item.Payload...)
	item.SuppressionReasons = append([]string(nil), item.SuppressionReasons...)
	return item
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func mapNotificationDeliverySQLError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "notification_deliveries_transition_uq") || strings.Contains(message, "notification_deliveries_summary_uq") || strings.Contains(message, "notification_deliveries_pkey") {
		return ErrConflict
	}
	return fmt.Errorf("notification delivery storage: %w", err)
}
