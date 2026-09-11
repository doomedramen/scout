package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	AlertWorkAllLineages   = "*"
	MaxActiveIncidents     = 100000
	MaxIncidentTransitions = 1000
)

type IncidentQuery struct {
	Status       string
	Acknowledged *bool
	Severity     string
	DeviceID     string
	SiteID       string
	Cursor       string
	Limit        int
}

type IncidentPage struct {
	Items      []Incident
	NextCursor string
}

type IncidentTransitionPage struct {
	Items      []IncidentTransition
	NextCursor string
}

// MarkAlertWork records a coalesced evaluation request. A wildcard lineage is
// used by telemetry ingestion because the effective rule set can change after
// a sample arrives; the sweep expands it against the current rules.
func (s *Store) MarkAlertWork(ctx context.Context, lineageID, entityID string) error {
	lineageID = strings.TrimSpace(lineageID)
	entityID = strings.TrimSpace(entityID)
	if lineageID == "" {
		lineageID = AlertWorkAllLineages
	}
	if entityID == "" {
		return ErrInvalid
	}
	if s.db != nil {
		return s.markAlertWorkSQL(ctx, lineageID, entityID)
	}
	return s.mutate(ctx, func(state *State) error {
		return markAlertWorkState(state, lineageID, entityID, s.now().UTC())
	})
}

func (s *Store) MarkAlertWorkForEntity(ctx context.Context, entityID string) error {
	return s.MarkAlertWork(ctx, AlertWorkAllLineages, entityID)
}

func markAlertWorkState(state *State, lineageID, entityID string, now time.Time) error {
	key := alertWorkKey(lineageID, entityID)
	item, exists := state.AlertWork[key]
	if !exists {
		item = AlertWorkItem{LineageID: lineageID, EntityID: entityID, DirtyGeneration: 1, CreatedAt: now}
	}
	if item.DirtyGeneration < 1 {
		item.DirtyGeneration = 1
	}
	item.DirtyGeneration++
	if !exists {
		item.DirtyGeneration = 1
	}
	item.LastError = ""
	item.UpdatedAt = now
	state.AlertWork[key] = item
	return nil
}

func (s *Store) ListAlertWork(ctx context.Context) ([]AlertWorkItem, error) {
	if s.db != nil {
		return s.listAlertWorkSQL(ctx)
	}
	result := []AlertWorkItem{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.AlertWork {
			result = append(result, cloneAlertWork(item))
		}
		sortAlertWork(result)
		return nil
	})
	return result, err
}

func (s *Store) ClaimAlertWork(ctx context.Context, owner string, limit int, lease time.Duration) ([]AlertWorkItem, error) {
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
		lease = 30 * time.Second
	}
	if s.db != nil {
		return s.claimAlertWorkSQL(ctx, owner, limit, lease)
	}
	result := []AlertWorkItem{}
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		candidates := make([]AlertWorkItem, 0, len(state.AlertWork))
		for _, item := range state.AlertWork {
			if item.LeaseUntil != nil && item.LeaseUntil.After(now) {
				continue
			}
			candidates = append(candidates, item)
		}
		sortAlertWork(candidates)
		if len(candidates) > limit {
			candidates = candidates[:limit]
		}
		until := now.Add(lease)
		for _, item := range candidates {
			key := alertWorkKey(item.LineageID, item.EntityID)
			item.LeaseEpoch++
			item.LeaseOwner = owner
			item.LeaseUntil = &until
			item.Attempts++
			item.UpdatedAt = now
			state.AlertWork[key] = item
			result = append(result, cloneAlertWork(item))
		}
		return nil
	})
	return result, err
}

// CompleteAlertWork removes work only if no newer telemetry dirtied the row
// while it was leased. Newer work is released for the next sweep instead.
func (s *Store) CompleteAlertWork(ctx context.Context, work AlertWorkItem, lastError string) error {
	if work.LineageID == "" || work.EntityID == "" || work.LeaseOwner == "" || work.LeaseEpoch <= 0 || work.DirtyGeneration <= 0 {
		return ErrInvalid
	}
	lastError = boundedIncidentText(lastError, 512)
	if s.db != nil {
		return s.completeAlertWorkSQL(ctx, work, lastError)
	}
	return s.mutate(ctx, func(state *State) error {
		key := alertWorkKey(work.LineageID, work.EntityID)
		current, ok := state.AlertWork[key]
		if !ok || current.LeaseOwner != work.LeaseOwner || current.LeaseEpoch != work.LeaseEpoch {
			return ErrConflict
		}
		if lastError == "" && current.DirtyGeneration == work.DirtyGeneration {
			delete(state.AlertWork, key)
			return nil
		}
		current.LeaseOwner = ""
		current.LeaseUntil = nil
		current.LastError = lastError
		current.UpdatedAt = s.now().UTC()
		state.AlertWork[key] = current
		return nil
	})
}

func (s *Store) GetAlertEvaluation(ctx context.Context, lineageID, entityID string) (AlertEvaluation, error) {
	if strings.TrimSpace(lineageID) == "" || strings.TrimSpace(entityID) == "" {
		return AlertEvaluation{}, ErrInvalid
	}
	if s.db != nil {
		return s.getAlertEvaluationSQL(ctx, lineageID, entityID)
	}
	var result AlertEvaluation
	err := s.read(ctx, func(state *State) error {
		item, ok := state.AlertEvaluations[alertEvaluationKey(lineageID, entityID)]
		if !ok {
			return ErrNotFound
		}
		result = cloneAlertEvaluation(item)
		return nil
	})
	return result, err
}

// ApplyAlertEvaluation commits the evaluator checkpoint, incident snapshot,
// and append-only transitions as one store transaction.
func (s *Store) ApplyAlertEvaluation(ctx context.Context, evaluation AlertEvaluation, incident *Incident, transitions []IncidentTransition) error {
	return s.ApplyAlertEvaluationWithDeliveries(ctx, evaluation, incident, transitions, nil)
}

// ApplyAlertEvaluationWithDeliveries commits the evaluator checkpoint,
// incident snapshot, transitions, and notification delivery intents together.
// A queue overflow may drop a notification intent, but never rolls back the
// authoritative incident state.
func (s *Store) ApplyAlertEvaluationWithDeliveries(ctx context.Context, evaluation AlertEvaluation, incident *Incident, transitions []IncidentTransition, deliveries []NotificationDelivery) error {
	if strings.TrimSpace(evaluation.LineageID) == "" || strings.TrimSpace(evaluation.EntityID) == "" {
		return ErrInvalid
	}
	if evaluation.EvidenceState == "" {
		evaluation.EvidenceState = "unknown"
	}
	if evaluation.UpdatedAt.IsZero() {
		evaluation.UpdatedAt = s.now().UTC()
	}
	if s.db != nil {
		return s.applyAlertEvaluationSQL(ctx, evaluation, incident, transitions, deliveries)
	}
	return s.mutate(ctx, func(state *State) error {
		target := state
		working := cloneState(*state)
		state = &working
		if incident != nil {
			if err := validateIncident(*incident); err != nil {
				return err
			}
			if incident.Status == "active" {
				active := 0
				for id, existing := range state.Incidents {
					if id != incident.ID && existing.Status == "active" && existing.LineageID == incident.LineageID && existing.EntityID == incident.EntityID {
						return ErrConflict
					}
					if existing.Status == "active" {
						active++
					}
				}
				if _, exists := state.Incidents[incident.ID]; !exists && active >= MaxActiveIncidents {
					return ErrIncidentCap
				}
			}
			current, exists := state.Incidents[incident.ID]
			if exists && incident.Revision <= current.Revision {
				incident.Revision = current.Revision + 1
			}
			if incident.Revision == 0 {
				incident.Revision = 1
			}
			state.Incidents[incident.ID] = cloneIncident(*incident)
		}
		for index := range transitions {
			transition := transitions[index]
			if transition.IncidentID == "" {
				return ErrInvalid
			}
			if _, exists := state.Incidents[transition.IncidentID]; !exists {
				return ErrInvalid
			}
			if transition.ID == "" {
				transition.ID = NewID()
			}
			if transition.Sequence == 0 {
				transition.Sequence = nextTransitionSequence(state.IncidentTransitions, transition.IncidentID)
			}
			if transition.Sequence > MaxIncidentTransitions {
				return ErrBackpressure
			}
			state.IncidentTransitions[transition.ID] = cloneIncidentTransition(transition)
			transitions[index] = transition
		}
		if len(deliveries) > 0 {
			if _, err := enqueueNotificationDeliveriesState(state, deliveries, s.now().UTC()); err != nil {
				return err
			}
		}
		state.AlertEvaluations[alertEvaluationKey(evaluation.LineageID, evaluation.EntityID)] = cloneAlertEvaluation(evaluation)
		*target = working
		return nil
	})
}

func (s *Store) GetIncident(ctx context.Context, id string) (Incident, error) {
	if strings.TrimSpace(id) == "" {
		return Incident{}, ErrInvalid
	}
	if s.db != nil {
		return s.getIncidentSQL(ctx, id)
	}
	var result Incident
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Incidents[id]
		if !ok {
			return ErrNotFound
		}
		result = cloneIncident(item)
		return nil
	})
	return result, err
}

func (s *Store) ListIncidents(ctx context.Context, query IncidentQuery) ([]Incident, error) {
	page, err := s.ListIncidentPage(ctx, query)
	return page.Items, err
}

func (s *Store) ListIncidentPage(ctx context.Context, query IncidentQuery) (IncidentPage, error) {
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 100
	}
	if query.Cursor != "" {
		if _, _, err := decodeIncidentCursor(query.Cursor); err != nil {
			return IncidentPage{}, err
		}
	}
	if s.db != nil {
		return s.listIncidentsPageSQL(ctx, query)
	}
	result := []Incident{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Incidents {
			if !incidentMatches(item, query) {
				continue
			}
			if query.SiteID != "" {
				device, exists := state.Devices[item.DeviceID]
				if !exists || device.SiteID != query.SiteID {
					continue
				}
			}
			result = append(result, cloneIncident(item))
		}
		sort.Slice(result, func(left, right int) bool {
			if result[left].EvaluatedAt.Equal(result[right].EvaluatedAt) {
				return result[left].ID > result[right].ID
			}
			return result[left].EvaluatedAt.After(result[right].EvaluatedAt)
		})
		return nil
	})
	if err != nil {
		return IncidentPage{}, err
	}
	if query.Cursor != "" {
		cursorTime, cursorID, _ := decodeIncidentCursor(query.Cursor)
		filtered := result[:0]
		for _, item := range result {
			if item.EvaluatedAt.Before(cursorTime) || item.EvaluatedAt.Equal(cursorTime) && item.ID < cursorID {
				filtered = append(filtered, item)
			}
		}
		result = filtered
	}
	page := IncidentPage{Items: result}
	if len(result) > query.Limit {
		page.Items = result[:query.Limit]
		page.NextCursor = encodeIncidentCursor(page.Items[len(page.Items)-1])
	}
	return page, nil
}

func (s *Store) ListIncidentTransitions(ctx context.Context, incidentID string, limit int) ([]IncidentTransition, error) {
	page, err := s.ListIncidentTransitionPage(ctx, incidentID, "", limit)
	return page.Items, err
}

func (s *Store) ListIncidentTransitionPage(ctx context.Context, incidentID, cursor string, limit int) (IncidentTransitionPage, error) {
	if strings.TrimSpace(incidentID) == "" {
		return IncidentTransitionPage{}, ErrInvalid
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if cursor != "" {
		if _, _, err := decodeTransitionCursor(cursor); err != nil {
			return IncidentTransitionPage{}, err
		}
	}
	if s.db != nil {
		return s.listIncidentTransitionPageSQL(ctx, incidentID, cursor, limit)
	}
	result := []IncidentTransition{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.IncidentTransitions {
			if item.IncidentID == incidentID {
				result = append(result, cloneIncidentTransition(item))
			}
		}
		sort.Slice(result, func(left, right int) bool { return result[left].Sequence < result[right].Sequence })
		return nil
	})
	if err != nil {
		return IncidentTransitionPage{}, err
	}
	if cursor != "" {
		cursorSequence, cursorID, _ := decodeTransitionCursor(cursor)
		filtered := result[:0]
		for _, item := range result {
			if item.Sequence > cursorSequence || item.Sequence == cursorSequence && item.ID > cursorID {
				filtered = append(filtered, item)
			}
		}
		result = filtered
	}
	page := IncidentTransitionPage{Items: result}
	if len(result) > limit {
		page.Items = result[:limit]
		page.NextCursor = encodeTransitionCursor(page.Items[len(page.Items)-1])
	}
	return page, nil
}

// AcknowledgeIncident records acknowledgment as an append-only transition.
// Repeating an acknowledgment is deliberately a read of the existing state so
// clients can safely retry after a lost response.
func (s *Store) AcknowledgeIncident(ctx context.Context, id string, expectedRevision int64, actor string) (Incident, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(actor) == "" {
		return Incident{}, ErrInvalid
	}
	if s.db != nil {
		return s.acknowledgeIncidentSQL(ctx, id, expectedRevision, actor)
	}
	var result Incident
	err := s.mutate(ctx, func(state *State) error {
		incident, exists := state.Incidents[id]
		if !exists {
			return ErrNotFound
		}
		if incident.AcknowledgedAt != nil {
			result = cloneIncident(incident)
			return nil
		}
		if expectedRevision > 0 && incident.Revision != expectedRevision {
			return ErrConflict
		}
		when := s.now().UTC()
		incident.AcknowledgedAt = &when
		incident.AcknowledgedBy = boundedIncidentText(actor, 128)
		incident.Revision++
		transition := IncidentTransition{
			ID: NewID(), IncidentID: incident.ID, Sequence: nextTransitionSequence(state.IncidentTransitions, incident.ID),
			Kind: "acknowledged", Actor: incident.AcknowledgedBy, EvidenceState: incident.EvidenceState,
			Value: cloneAlertFloat(incident.Value), OccurredAt: when, RuleRevision: incident.RuleRevision,
		}
		if !incident.ObservedAt.IsZero() {
			observedAt := incident.ObservedAt
			transition.ObservedAt = &observedAt
		}
		if transition.Sequence > MaxIncidentTransitions {
			return ErrBackpressure
		}
		state.Incidents[id] = cloneIncident(incident)
		state.IncidentTransitions[transition.ID] = cloneIncidentTransition(transition)
		result = cloneIncident(incident)
		return nil
	})
	return result, err
}

func (s *Store) markAlertWorkSQL(ctx context.Context, lineageID, entityID string) error {
	now := s.now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alert_work(lineage_id, entity_id, dirty_generation, lease_epoch, lease_owner, lease_until, attempts, last_error, created_at, updated_at)
		VALUES ($1, $2, 1, 0, '', NULL, 0, '', $3, $3)
		ON CONFLICT (lineage_id, entity_id) DO UPDATE SET
		  dirty_generation = alert_work.dirty_generation + 1,
		  last_error = '', updated_at = EXCLUDED.updated_at`, lineageID, entityID, now)
	if err != nil {
		return fmt.Errorf("mark alert work: %w", err)
	}
	return nil
}

func (s *Store) listAlertWorkSQL(ctx context.Context) ([]AlertWorkItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lineage_id, entity_id, dirty_generation, lease_epoch, lease_owner, lease_until, attempts, last_error, created_at, updated_at FROM alert_work ORDER BY updated_at, lineage_id, entity_id`)
	if err != nil {
		return nil, fmt.Errorf("list alert work: %w", err)
	}
	defer rows.Close()
	result := []AlertWorkItem{}
	for rows.Next() {
		item, err := scanAlertWork(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alert work: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) claimAlertWorkSQL(ctx context.Context, owner string, limit int, lease time.Duration) ([]AlertWorkItem, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UTC()
	rows, err := tx.QueryContext(ctx, `
		SELECT lineage_id, entity_id, dirty_generation, lease_epoch, lease_owner, lease_until, attempts, last_error, created_at, updated_at
		FROM alert_work
		WHERE lease_until IS NULL OR lease_until <= $1
		ORDER BY updated_at, lineage_id, entity_id
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim alert work rows: %w", err)
	}
	items := []AlertWorkItem{}
	for rows.Next() {
		item, scanErr := scanAlertWork(rows)
		if scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scan claimable alert work: %w", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate claimable alert work: %w", err)
	}
	rows.Close()
	until := now.Add(lease)
	for index := range items {
		item := &items[index]
		item.LeaseEpoch++
		item.LeaseOwner = owner
		item.LeaseUntil = &until
		item.Attempts++
		item.UpdatedAt = now
		if _, err := tx.ExecContext(ctx, `UPDATE alert_work SET lease_epoch=$1, lease_owner=$2, lease_until=$3, attempts=$4, updated_at=$5 WHERE lineage_id=$6 AND entity_id=$7`, item.LeaseEpoch, owner, until, item.Attempts, now, item.LineageID, item.EntityID); err != nil {
			return nil, fmt.Errorf("lease alert work: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit alert work lease: %w", err)
	}
	return items, nil
}

func (s *Store) completeAlertWorkSQL(ctx context.Context, work AlertWorkItem, lastError string) error {
	if lastError == "" {
		result, err := s.db.ExecContext(ctx, `DELETE FROM alert_work WHERE lineage_id=$1 AND entity_id=$2 AND lease_owner=$3 AND lease_epoch=$4 AND dirty_generation=$5`, work.LineageID, work.EntityID, work.LeaseOwner, work.LeaseEpoch, work.DirtyGeneration)
		if err != nil {
			return fmt.Errorf("complete alert work: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			return nil
		}
	}
	result, err := s.db.ExecContext(ctx, `UPDATE alert_work SET lease_owner='', lease_until=NULL, last_error=$1, updated_at=$2 WHERE lineage_id=$3 AND entity_id=$4 AND lease_owner=$5 AND lease_epoch=$6`, lastError, s.now().UTC(), work.LineageID, work.EntityID, work.LeaseOwner, work.LeaseEpoch)
	if err != nil {
		return fmt.Errorf("release alert work: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) getAlertEvaluationSQL(ctx context.Context, lineageID, entityID string) (AlertEvaluation, error) {
	var item AlertEvaluation
	var lastObserved, lastReceived, pending, recovery, lastValid sql.NullTime
	var incidentID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT lineage_id, entity_id, effective_revision, evidence_state, last_observed_at, last_received_at,
		       pending_since, recovery_since, last_valid_at, trigger_consecutive, recovery_consecutive, incident_id, updated_at
		FROM alert_evaluations WHERE lineage_id=$1 AND entity_id=$2`, lineageID, entityID).Scan(
		&item.LineageID, &item.EntityID, &item.EffectiveRevision, &item.EvidenceState, &lastObserved, &lastReceived,
		&pending, &recovery, &lastValid, &item.TriggerConsecutive, &item.RecoveryConsecutive, &incidentID, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertEvaluation{}, ErrNotFound
	}
	if err != nil {
		return AlertEvaluation{}, fmt.Errorf("get alert evaluation: %w", err)
	}
	item.LastObservedAt = nullableTimePointer(lastObserved)
	item.LastReceivedAt = nullableTimePointer(lastReceived)
	item.PendingSince = nullableTimePointer(pending)
	item.RecoverySince = nullableTimePointer(recovery)
	item.LastValidAt = nullableTimePointer(lastValid)
	if incidentID.Valid {
		item.IncidentID = incidentID.String
	}
	return item, nil
}

func (s *Store) applyAlertEvaluationSQL(ctx context.Context, evaluation AlertEvaluation, incident *Incident, transitions []IncidentTransition, deliveries []NotificationDelivery) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if incident != nil {
		if err := validateIncident(*incident); err != nil {
			return err
		}
		if incident.Status == "active" {
			var active int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status='active' AND id <> $1`, incident.ID).Scan(&active); err != nil {
				return fmt.Errorf("count active incidents: %w", err)
			}
			if active >= MaxActiveIncidents {
				return ErrIncidentCap
			}
		}
		if incident.Revision <= 0 {
			incident.Revision = 1
		}
		snapshot, err := json.Marshal(incident.RuleSnapshot)
		if err != nil {
			return fmt.Errorf("encode incident rule snapshot: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO incidents(id, lineage_id, entity_id, device_id, rule_revision, rule_snapshot, severity, status, evidence_state, value, unit, source, opened_at, observed_at, evaluated_at, acknowledged_at, acknowledged_by, closed_at, close_reason, revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
			ON CONFLICT (id) DO UPDATE SET lineage_id=EXCLUDED.lineage_id, entity_id=EXCLUDED.entity_id, device_id=EXCLUDED.device_id,
			  rule_revision=EXCLUDED.rule_revision, rule_snapshot=EXCLUDED.rule_snapshot, severity=EXCLUDED.severity, status=EXCLUDED.status,
			  evidence_state=EXCLUDED.evidence_state, value=EXCLUDED.value, unit=EXCLUDED.unit, source=EXCLUDED.source,
			  opened_at=EXCLUDED.opened_at, observed_at=EXCLUDED.observed_at, evaluated_at=EXCLUDED.evaluated_at,
			  acknowledged_at=EXCLUDED.acknowledged_at, acknowledged_by=EXCLUDED.acknowledged_by, closed_at=EXCLUDED.closed_at,
			  close_reason=EXCLUDED.close_reason, revision=EXCLUDED.revision`, incident.ID, incident.LineageID, incident.EntityID, incident.DeviceID,
			incident.RuleRevision, snapshot, incident.Severity, incident.Status, incident.EvidenceState, incident.Value, incident.Unit, incident.Source,
			incident.OpenedAt.UTC(), incident.ObservedAt.UTC(), incident.EvaluatedAt.UTC(), incident.AcknowledgedAt, incident.AcknowledgedBy,
			incident.ClosedAt, incident.CloseReason, incident.Revision)
		if err != nil {
			return mapIncidentSQLError(err)
		}
	}
	sequence := int64(0)
	for index := range transitions {
		transition := &transitions[index]
		if transition.IncidentID == "" {
			return ErrInvalid
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM incidents WHERE id=$1)`, transition.IncidentID).Scan(&exists); err != nil {
			return fmt.Errorf("check incident transition parent: %w", err)
		}
		if !exists {
			return ErrInvalid
		}
		if transition.ID == "" {
			transition.ID = NewID()
		}
		if transition.Sequence == 0 {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM incident_transitions WHERE incident_id=$1`, transition.IncidentID).Scan(&sequence); err != nil {
				return fmt.Errorf("read incident transition sequence: %w", err)
			}
			sequence++
			transition.Sequence = sequence
		}
		if transition.Sequence > MaxIncidentTransitions {
			return ErrBackpressure
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO incident_transitions(id, incident_id, sequence, kind, actor, evidence_state, reason, value, observed_at, occurred_at, rule_revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, transition.ID, transition.IncidentID, transition.Sequence, transition.Kind, transition.Actor, transition.EvidenceState, transition.Reason, transition.Value, transition.ObservedAt, transition.OccurredAt.UTC(), transition.RuleRevision)
		if err != nil {
			return mapIncidentSQLError(err)
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO alert_evaluations(lineage_id, entity_id, effective_revision, evidence_state, last_observed_at, last_received_at, pending_since, recovery_since, last_valid_at, trigger_consecutive, recovery_consecutive, incident_id, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),$13)
		ON CONFLICT (lineage_id, entity_id) DO UPDATE SET effective_revision=EXCLUDED.effective_revision, evidence_state=EXCLUDED.evidence_state,
		  last_observed_at=EXCLUDED.last_observed_at, last_received_at=EXCLUDED.last_received_at, pending_since=EXCLUDED.pending_since,
		  recovery_since=EXCLUDED.recovery_since, last_valid_at=EXCLUDED.last_valid_at, trigger_consecutive=EXCLUDED.trigger_consecutive,
		  recovery_consecutive=EXCLUDED.recovery_consecutive, incident_id=EXCLUDED.incident_id, updated_at=EXCLUDED.updated_at`, evaluation.LineageID, evaluation.EntityID,
		evaluation.EffectiveRevision, evaluation.EvidenceState, evaluation.LastObservedAt, evaluation.LastReceivedAt, evaluation.PendingSince, evaluation.RecoverySince,
		evaluation.LastValidAt, evaluation.TriggerConsecutive, evaluation.RecoveryConsecutive, evaluation.IncidentID, evaluation.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("persist alert evaluation: %w", err)
	}
	if len(deliveries) > 0 {
		state, err := readWorkspaceStateTx(ctx, tx)
		if err != nil {
			return err
		}
		result, err := enqueueNotificationDeliveriesSQLTx(ctx, tx, deliveries, s.now().UTC())
		if err != nil {
			return err
		}
		if result.Dropped > 0 {
			state.Workspace.NotificationQueueOverflows += int64(result.Dropped)
			if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit alert evaluation: %w", err)
	}
	return nil
}

func (s *Store) getIncidentSQL(ctx context.Context, id string) (Incident, error) {
	row := s.db.QueryRowContext(ctx, incidentSelect+` WHERE id=$1`, id)
	item, err := scanIncident(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, fmt.Errorf("get incident: %w", err)
	}
	return item, nil
}

func (s *Store) listIncidentsSQL(ctx context.Context, query IncidentQuery) ([]Incident, error) {
	page, err := s.listIncidentsPageSQL(ctx, query)
	return page.Items, err
}

func (s *Store) listIncidentsPageSQL(ctx context.Context, query IncidentQuery) (IncidentPage, error) {
	args := []any{}
	where := []string{}
	add := func(expression string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expression, len(args)))
	}
	if query.Status != "" {
		add("incidents.status=$%d", query.Status)
	}
	if query.Severity != "" {
		add("incidents.severity=$%d", query.Severity)
	}
	if query.DeviceID != "" {
		add("incidents.device_id=$%d", query.DeviceID)
	}
	if query.SiteID != "" {
		add("EXISTS (SELECT 1 FROM devices AS incident_devices WHERE incident_devices.id = incidents.device_id AND incident_devices.site_id = $%d)", query.SiteID)
	}
	if query.Acknowledged != nil {
		if *query.Acknowledged {
			where = append(where, "incidents.acknowledged_at IS NOT NULL")
		} else {
			where = append(where, "incidents.acknowledged_at IS NULL")
		}
	}
	if query.Cursor != "" {
		cursorTime, cursorID, err := decodeIncidentCursor(query.Cursor)
		if err != nil {
			return IncidentPage{}, err
		}
		args = append(args, cursorTime, cursorID)
		where = append(where, fmt.Sprintf("(incidents.evaluated_at, incidents.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	sqlQuery := incidentSelect
	if len(where) > 0 {
		sqlQuery += " WHERE " + strings.Join(where, " AND ")
	}
	sqlQuery += fmt.Sprintf(" ORDER BY incidents.evaluated_at DESC, incidents.id DESC LIMIT %d", query.Limit+1)
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return IncidentPage{}, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()
	result := []Incident{}
	for rows.Next() {
		item, scanErr := scanIncident(rows)
		if scanErr != nil {
			return IncidentPage{}, fmt.Errorf("scan incident: %w", scanErr)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return IncidentPage{}, err
	}
	page := IncidentPage{Items: result}
	if len(result) > query.Limit {
		page.Items = result[:query.Limit]
		page.NextCursor = encodeIncidentCursor(page.Items[len(page.Items)-1])
	}
	return page, nil
}

func (s *Store) listIncidentTransitionsSQL(ctx context.Context, incidentID string, limit int) ([]IncidentTransition, error) {
	page, err := s.listIncidentTransitionPageSQL(ctx, incidentID, "", limit)
	return page.Items, err
}

func (s *Store) listIncidentTransitionPageSQL(ctx context.Context, incidentID, cursor string, limit int) (IncidentTransitionPage, error) {
	args := []any{incidentID}
	where := "incident_id=$1"
	if cursor != "" {
		sequence, id, err := decodeTransitionCursor(cursor)
		if err != nil {
			return IncidentTransitionPage{}, err
		}
		args = append(args, sequence, id)
		where += fmt.Sprintf(" AND (sequence, id) > ($%d, $%d)", len(args)-1, len(args))
	}
	query := fmt.Sprintf(`SELECT id, incident_id, sequence, kind, actor, evidence_state, reason, value, observed_at, occurred_at, rule_revision FROM incident_transitions WHERE %s ORDER BY sequence, id LIMIT %d`, where, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return IncidentTransitionPage{}, fmt.Errorf("list incident transitions: %w", err)
	}
	defer rows.Close()
	result := []IncidentTransition{}
	for rows.Next() {
		item, scanErr := scanIncidentTransition(rows)
		if scanErr != nil {
			return IncidentTransitionPage{}, fmt.Errorf("scan incident transition: %w", scanErr)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return IncidentTransitionPage{}, err
	}
	page := IncidentTransitionPage{Items: result}
	if len(result) > limit {
		page.Items = result[:limit]
		page.NextCursor = encodeTransitionCursor(page.Items[len(page.Items)-1])
	}
	return page, nil
}

func (s *Store) acknowledgeIncidentSQL(ctx context.Context, id string, expectedRevision int64, actor string) (Incident, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, err
	}
	defer func() { _ = tx.Rollback() }()
	incident, err := scanIncident(tx.QueryRowContext(ctx, incidentSelect+` WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, fmt.Errorf("read incident for acknowledgment: %w", err)
	}
	if incident.AcknowledgedAt != nil {
		return incident, nil
	}
	if expectedRevision > 0 && incident.Revision != expectedRevision {
		return Incident{}, ErrConflict
	}
	when := s.now().UTC()
	incident.AcknowledgedAt = &when
	incident.AcknowledgedBy = boundedIncidentText(actor, 128)
	incident.Revision++
	transitionID := NewID()
	var sequence int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM incident_transitions WHERE incident_id=$1`, id).Scan(&sequence); err != nil {
		return Incident{}, fmt.Errorf("read acknowledgment sequence: %w", err)
	}
	if sequence > MaxIncidentTransitions {
		return Incident{}, ErrBackpressure
	}
	var observedAt any
	if !incident.ObservedAt.IsZero() {
		observedAt = incident.ObservedAt.UTC()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE incidents SET acknowledged_at=$1, acknowledged_by=$2, revision=$3 WHERE id=$4`, incident.AcknowledgedAt, incident.AcknowledgedBy, incident.Revision, id); err != nil {
		return Incident{}, mapIncidentSQLError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO incident_transitions(id, incident_id, sequence, kind, actor, evidence_state, reason, value, observed_at, occurred_at, rule_revision) VALUES ($1,$2,$3,'acknowledged',$4,$5,'',$6,$7,$8,$9)`, transitionID, id, sequence, incident.AcknowledgedBy, incident.EvidenceState, incident.Value, observedAt, when, incident.RuleRevision); err != nil {
		return Incident{}, mapIncidentSQLError(err)
	}
	if err := tx.Commit(); err != nil {
		return Incident{}, fmt.Errorf("commit incident acknowledgment: %w", err)
	}
	return incident, nil
}

func closeIncidentsForState(state *State, matches func(Incident) bool, reason string, now time.Time) (int, error) {
	reason = boundedIncidentText(reason, 64)
	if !validIncidentCloseReason(reason) {
		return 0, ErrInvalid
	}

	ids := make([]string, 0)
	for id, incident := range state.Incidents {
		if incident.Status == "active" && matches(incident) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if nextTransitionSequence(state.IncidentTransitions, id) > MaxIncidentTransitions {
			return 0, ErrBackpressure
		}
	}

	now = now.UTC()
	for _, id := range ids {
		incident := state.Incidents[id]
		incident.Status = "closed"
		incident.EvidenceState = normalizedIncidentEvidence(incident.EvidenceState)
		incident.ClosedAt = &now
		incident.CloseReason = reason
		incident.EvaluatedAt = now
		incident.Revision++

		transition := administrativeIncidentTransition(incident, nextTransitionSequence(state.IncidentTransitions, id), now, reason)
		state.Incidents[id] = cloneIncident(incident)
		state.IncidentTransitions[transition.ID] = cloneIncidentTransition(transition)
		clearAlertEvaluationState(state, incident.LineageID, incident.EntityID, now)
	}
	return len(ids), nil
}

func (s *Store) closeIncidentsForLineageSQL(ctx context.Context, lineageID, reason string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	count, err := closeIncidentsTx(ctx, tx, "lineage_id=$1", lineageID, reason, s.now().UTC())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit lineage incident closure: %w", err)
	}
	return count, nil
}

func (s *Store) closeIncidentsForDeviceSQL(ctx context.Context, deviceID, reason string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	count, err := closeIncidentsTx(ctx, tx, "device_id=$1", deviceID, reason, s.now().UTC())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit device incident closure: %w", err)
	}
	return count, nil
}

func closeIncidentsTx(ctx context.Context, tx *sql.Tx, predicate string, value any, reason string, now time.Time) (int, error) {
	reason = boundedIncidentText(reason, 64)
	if !validIncidentCloseReason(reason) {
		return 0, ErrInvalid
	}

	rows, err := tx.QueryContext(ctx, incidentSelect+` WHERE status='active' AND `+predicate+` FOR UPDATE`, value)
	if err != nil {
		return 0, fmt.Errorf("select incidents for administrative closure: %w", err)
	}
	incidents := []Incident{}
	for rows.Next() {
		incident, scanErr := scanIncident(rows)
		if scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("scan incident for administrative closure: %w", scanErr)
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate incidents for administrative closure: %w", err)
	}
	rows.Close()
	sort.Slice(incidents, func(left, right int) bool { return incidents[left].ID < incidents[right].ID })

	now = now.UTC()
	for _, incident := range incidents {
		var nextSequence int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM incident_transitions WHERE incident_id=$1`, incident.ID).Scan(&nextSequence); err != nil {
			return 0, fmt.Errorf("read administrative closure sequence: %w", err)
		}
		if nextSequence > MaxIncidentTransitions {
			return 0, ErrBackpressure
		}
	}

	for _, incident := range incidents {
		incident.Status = "closed"
		incident.EvidenceState = normalizedIncidentEvidence(incident.EvidenceState)
		incident.ClosedAt = &now
		incident.CloseReason = reason
		incident.EvaluatedAt = now
		incident.Revision++
		transition := administrativeIncidentTransition(incident, 0, now, reason)
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM incident_transitions WHERE incident_id=$1`, incident.ID).Scan(&transition.Sequence); err != nil {
			return 0, fmt.Errorf("re-read administrative closure sequence: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `UPDATE incidents SET status='closed', evidence_state=$1, evaluated_at=$2, closed_at=$3, close_reason=$4, revision=$5 WHERE id=$6`, incident.EvidenceState, incident.EvaluatedAt, incident.ClosedAt, incident.CloseReason, incident.Revision, incident.ID); err != nil {
			return 0, mapIncidentSQLError(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO incident_transitions(id, incident_id, sequence, kind, actor, evidence_state, reason, value, observed_at, occurred_at, rule_revision) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, transition.ID, transition.IncidentID, transition.Sequence, transition.Kind, transition.Actor, transition.EvidenceState, transition.Reason, transition.Value, transition.ObservedAt, transition.OccurredAt, transition.RuleRevision); err != nil {
			return 0, mapIncidentSQLError(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE alert_evaluations SET evidence_state='unknown', last_observed_at=NULL, last_received_at=NULL, pending_since=NULL, recovery_since=NULL, last_valid_at=NULL, trigger_consecutive=0, recovery_consecutive=0, incident_id=NULL, updated_at=$1 WHERE incident_id=$2`, now, incident.ID); err != nil {
			return 0, fmt.Errorf("clear alert evaluation after administrative closure: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM alert_work WHERE entity_id=$1 AND (lineage_id=$2 OR lineage_id=$3)`, incident.EntityID, incident.LineageID, AlertWorkAllLineages); err != nil {
			return 0, fmt.Errorf("clear alert work after administrative closure: %w", err)
		}
	}
	return len(incidents), nil
}

func administrativeIncidentTransition(incident Incident, sequence int64, now time.Time, reason string) IncidentTransition {
	transition := IncidentTransition{
		ID:            NewID(),
		IncidentID:    incident.ID,
		Sequence:      sequence,
		Kind:          "administrative_close",
		Actor:         "system",
		EvidenceState: normalizedIncidentEvidence(incident.EvidenceState),
		Reason:        reason,
		Value:         cloneAlertFloat(incident.Value),
		OccurredAt:    now.UTC(),
		RuleRevision:  incident.RuleRevision,
	}
	if !incident.ObservedAt.IsZero() {
		observedAt := incident.ObservedAt.UTC()
		transition.ObservedAt = &observedAt
	}
	return transition
}

func clearAlertEvaluationState(state *State, lineageID, entityID string, now time.Time) {
	for key, evaluation := range state.AlertEvaluations {
		if evaluation.IncidentID == "" || evaluation.LineageID != lineageID || evaluation.EntityID != entityID {
			continue
		}
		evaluation.EvidenceState = "unknown"
		evaluation.LastObservedAt = nil
		evaluation.LastReceivedAt = nil
		evaluation.PendingSince = nil
		evaluation.RecoverySince = nil
		evaluation.LastValidAt = nil
		evaluation.TriggerConsecutive = 0
		evaluation.RecoveryConsecutive = 0
		evaluation.IncidentID = ""
		evaluation.UpdatedAt = now.UTC()
		state.AlertEvaluations[key] = cloneAlertEvaluation(evaluation)
	}
	delete(state.AlertWork, alertWorkKey(lineageID, entityID))
	delete(state.AlertWork, alertWorkKey(AlertWorkAllLineages, entityID))
}

func normalizedIncidentEvidence(value string) string {
	if value == "fresh" || value == "unsupported" {
		return value
	}
	return "unknown"
}

func validIncidentCloseReason(value string) bool {
	switch value {
	case "recovered", "rule_changed", "disabled", "retired", "decommissioned", "administrative":
		return true
	default:
		return false
	}
}

const incidentSelect = `SELECT id, lineage_id, entity_id, device_id, rule_revision, rule_snapshot, severity, status, evidence_state, value, unit, source, opened_at, observed_at, evaluated_at, acknowledged_at, acknowledged_by, closed_at, close_reason, revision FROM incidents`

type alertWorkScanner interface{ Scan(...any) error }

func scanAlertWork(scanner alertWorkScanner) (AlertWorkItem, error) {
	var item AlertWorkItem
	var leaseUntil sql.NullTime
	if err := scanner.Scan(&item.LineageID, &item.EntityID, &item.DirtyGeneration, &item.LeaseEpoch, &item.LeaseOwner, &leaseUntil, &item.Attempts, &item.LastError, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return AlertWorkItem{}, err
	}
	item.LeaseUntil = nullableTimePointer(leaseUntil)
	return item, nil
}

func scanIncident(scanner alertWorkScanner) (Incident, error) {
	var item Incident
	var snapshot []byte
	var value sql.NullFloat64
	var acknowledgedAt, closedAt sql.NullTime
	if err := scanner.Scan(&item.ID, &item.LineageID, &item.EntityID, &item.DeviceID, &item.RuleRevision, &snapshot, &item.Severity, &item.Status, &item.EvidenceState, &value, &item.Unit, &item.Source, &item.OpenedAt, &item.ObservedAt, &item.EvaluatedAt, &acknowledgedAt, &item.AcknowledgedBy, &closedAt, &item.CloseReason, &item.Revision); err != nil {
		return Incident{}, err
	}
	if len(snapshot) > 0 {
		if err := json.Unmarshal(snapshot, &item.RuleSnapshot); err != nil {
			return Incident{}, err
		}
	}
	if value.Valid {
		valueCopy := value.Float64
		item.Value = &valueCopy
	}
	item.AcknowledgedAt = nullableTimePointer(acknowledgedAt)
	item.ClosedAt = nullableTimePointer(closedAt)
	item.OpenedAt = item.OpenedAt.UTC()
	item.ObservedAt = item.ObservedAt.UTC()
	item.EvaluatedAt = item.EvaluatedAt.UTC()
	return item, nil
}

func scanIncidentTransition(scanner alertWorkScanner) (IncidentTransition, error) {
	var item IncidentTransition
	var value sql.NullFloat64
	var observedAt sql.NullTime
	if err := scanner.Scan(&item.ID, &item.IncidentID, &item.Sequence, &item.Kind, &item.Actor, &item.EvidenceState, &item.Reason, &value, &observedAt, &item.OccurredAt, &item.RuleRevision); err != nil {
		return IncidentTransition{}, err
	}
	if value.Valid {
		valueCopy := value.Float64
		item.Value = &valueCopy
	}
	item.ObservedAt = nullableTimePointer(observedAt)
	item.OccurredAt = item.OccurredAt.UTC()
	return item, nil
}

func validateIncident(incident Incident) error {
	if strings.TrimSpace(incident.ID) == "" || strings.TrimSpace(incident.LineageID) == "" || strings.TrimSpace(incident.EntityID) == "" {
		return ErrInvalid
	}
	if incident.Status != "active" && incident.Status != "resolved" && incident.Status != "closed" {
		return ErrInvalid
	}
	if incident.EvidenceState != "fresh" && incident.EvidenceState != "unknown" && incident.EvidenceState != "unsupported" {
		return ErrInvalid
	}
	if incident.Severity != "warning" && incident.Severity != "critical" {
		return ErrInvalid
	}
	return nil
}

func incidentMatches(incident Incident, query IncidentQuery) bool {
	if query.Status != "" && incident.Status != query.Status || query.Severity != "" && incident.Severity != query.Severity || query.DeviceID != "" && incident.DeviceID != query.DeviceID {
		return false
	}
	if query.Acknowledged != nil && (*query.Acknowledged != (incident.AcknowledgedAt != nil)) {
		return false
	}
	return true
}

func alertWorkKey(lineageID, entityID string) string { return lineageID + "\x00" + entityID }

func alertEvaluationKey(lineageID, entityID string) string { return lineageID + "\x00" + entityID }

func nextTransitionSequence(values map[string]IncidentTransition, incidentID string) int64 {
	var result int64
	for _, item := range values {
		if item.IncidentID == incidentID && item.Sequence >= result {
			result = item.Sequence + 1
		}
	}
	if result == 0 {
		return 1
	}
	return result
}

func sortAlertWork(items []AlertWorkItem) {
	sort.Slice(items, func(left, right int) bool {
		if items[left].UpdatedAt.Equal(items[right].UpdatedAt) {
			if items[left].LineageID == items[right].LineageID {
				return items[left].EntityID < items[right].EntityID
			}
			return items[left].LineageID < items[right].LineageID
		}
		return items[left].UpdatedAt.Before(items[right].UpdatedAt)
	})
}

func cloneAlertWork(item AlertWorkItem) AlertWorkItem {
	item.LeaseUntil = cloneTime(item.LeaseUntil)
	return item
}

func cloneAlertEvaluation(item AlertEvaluation) AlertEvaluation {
	item.LastObservedAt = cloneTime(item.LastObservedAt)
	item.LastReceivedAt = cloneTime(item.LastReceivedAt)
	item.PendingSince = cloneTime(item.PendingSince)
	item.RecoverySince = cloneTime(item.RecoverySince)
	item.LastValidAt = cloneTime(item.LastValidAt)
	return item
}

func cloneIncident(item Incident) Incident {
	item.RuleSnapshot = cloneAnyMap(item.RuleSnapshot)
	item.Value = cloneAlertFloat(item.Value)
	item.AcknowledgedAt = cloneTime(item.AcknowledgedAt)
	item.ClosedAt = cloneTime(item.ClosedAt)
	return item
}

func cloneIncidentTransition(item IncidentTransition) IncidentTransition {
	item.Value = cloneAlertFloat(item.Value)
	item.ObservedAt = cloneTime(item.ObservedAt)
	return item
}

func cloneAnyMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return map[string]any{}
	}
	return result
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func boundedIncidentText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxBytes {
		return value
	}
	return string([]rune(value)[:maxRunesForBytes(value, maxBytes)])
}

func maxRunesForBytes(value string, maxBytes int) int {
	used := 0
	count := 0
	for _, item := range value {
		size := len(string(item))
		if used+size > maxBytes {
			break
		}
		used += size
		count++
	}
	return count
}

func mapIncidentSQLError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "active_incident_lineage_entity") || strings.Contains(message, "incident_transitions_incident") {
		return ErrConflict
	}
	return err
}

func encodeIncidentCursor(item Incident) string {
	value := item.EvaluatedAt.UTC().Format(time.RFC3339Nano) + "\x00" + item.ID
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeIncidentCursor(value string) (time.Time, string, error) {
	if len(value) > 512 {
		return time.Time{}, "", ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", ErrInvalid
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", ErrInvalid
	}
	when, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", ErrInvalid
	}
	return when.UTC(), parts[1], nil
}

func encodeTransitionCursor(item IncidentTransition) string {
	value := fmt.Sprintf("%d\x00%s", item.Sequence, item.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeTransitionCursor(value string) (int64, string, error) {
	if len(value) > 512 {
		return 0, "", ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, "", ErrInvalid
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 || parts[1] == "" {
		return 0, "", ErrInvalid
	}
	sequence, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || sequence < 1 {
		return 0, "", ErrInvalid
	}
	return sequence, parts[1], nil
}
