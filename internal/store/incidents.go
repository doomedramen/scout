package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	Limit        int
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
		return s.applyAlertEvaluationSQL(ctx, evaluation, incident, transitions)
	}
	return s.mutate(ctx, func(state *State) error {
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
		state.AlertEvaluations[alertEvaluationKey(evaluation.LineageID, evaluation.EntityID)] = cloneAlertEvaluation(evaluation)
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
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 100
	}
	if s.db != nil {
		return s.listIncidentsSQL(ctx, query)
	}
	result := []Incident{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Incidents {
			if !incidentMatches(item, query) {
				continue
			}
			result = append(result, cloneIncident(item))
		}
		sort.Slice(result, func(left, right int) bool {
			if result[left].EvaluatedAt.Equal(result[right].EvaluatedAt) {
				return result[left].ID > result[right].ID
			}
			return result[left].EvaluatedAt.After(result[right].EvaluatedAt)
		})
		if len(result) > query.Limit {
			result = result[:query.Limit]
		}
		return nil
	})
	return result, err
}

func (s *Store) ListIncidentTransitions(ctx context.Context, incidentID string, limit int) ([]IncidentTransition, error) {
	if strings.TrimSpace(incidentID) == "" {
		return nil, ErrInvalid
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if s.db != nil {
		return s.listIncidentTransitionsSQL(ctx, incidentID, limit)
	}
	result := []IncidentTransition{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.IncidentTransitions {
			if item.IncidentID == incidentID {
				result = append(result, cloneIncidentTransition(item))
			}
		}
		sort.Slice(result, func(left, right int) bool { return result[left].Sequence < result[right].Sequence })
		if len(result) > limit {
			result = result[:limit]
		}
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

func (s *Store) applyAlertEvaluationSQL(ctx context.Context, evaluation AlertEvaluation, incident *Incident, transitions []IncidentTransition) error {
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
	args := []any{}
	where := []string{}
	add := func(expression string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expression, len(args)))
	}
	if query.Status != "" {
		add("status=$%d", query.Status)
	}
	if query.Severity != "" {
		add("severity=$%d", query.Severity)
	}
	if query.DeviceID != "" {
		add("device_id=$%d", query.DeviceID)
	}
	if query.Acknowledged != nil {
		if *query.Acknowledged {
			where = append(where, "acknowledged_at IS NOT NULL")
		} else {
			where = append(where, "acknowledged_at IS NULL")
		}
	}
	sqlQuery := incidentSelect
	if len(where) > 0 {
		sqlQuery += " WHERE " + strings.Join(where, " AND ")
	}
	sqlQuery += fmt.Sprintf(" ORDER BY evaluated_at DESC, id DESC LIMIT %d", query.Limit)
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()
	result := []Incident{}
	for rows.Next() {
		item, scanErr := scanIncident(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan incident: %w", scanErr)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) listIncidentTransitionsSQL(ctx context.Context, incidentID string, limit int) ([]IncidentTransition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, incident_id, sequence, kind, actor, evidence_state, reason, value, observed_at, occurred_at, rule_revision FROM incident_transitions WHERE incident_id=$1 ORDER BY sequence LIMIT $2`, incidentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list incident transitions: %w", err)
	}
	defer rows.Close()
	result := []IncidentTransition{}
	for rows.Next() {
		item, scanErr := scanIncidentTransition(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan incident transition: %w", scanErr)
		}
		result = append(result, item)
	}
	return result, rows.Err()
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
