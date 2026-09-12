package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const backupFormatVersion = 1

const (
	notificationDeliveryRecoveryCancellation = "notification delivery cancelled during recovery"
	notificationDeliveryPaused               = "notification delivery paused"
)

type Backup struct {
	FormatVersion int       `json:"formatVersion"`
	SchemaVersion int       `json:"schemaVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	State         State     `json:"state"`
}

// Backup exports the complete logical workspace, including already encrypted
// secret envelopes. Plaintext secrets are never present in State.
func (s *Store) Backup(ctx context.Context) ([]byte, error) {
	var backup Backup
	err := s.read(ctx, func(state *State) error {
		backup = Backup{FormatVersion: backupFormatVersion, SchemaVersion: state.Workspace.SchemaVersion, CreatedAt: s.now().UTC(), State: cloneState(*state)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(backup)
}

func ParseBackup(data []byte) (Backup, error) {
	var backup Backup
	if err := json.Unmarshal(data, &backup); err != nil {
		return Backup{}, fmt.Errorf("decode backup: %w", err)
	}
	if backup.FormatVersion != backupFormatVersion || backup.SchemaVersion < 1 || backup.State.Version < 1 || backup.State.Workspace.SchemaVersion != backup.SchemaVersion {
		return Backup{}, ErrInvalid
	}
	if backup.CreatedAt.IsZero() {
		return Backup{}, ErrInvalid
	}
	ensureStateMaps(&backup.State)
	return backup, nil
}

// Restore replaces the logical workspace in one repository mutation. A
// caller should validate the backup key before invoking this method and use a
// separate destination database for operational restores.
func (s *Store) Restore(ctx context.Context, data []byte) error {
	backup, err := ParseBackup(data)
	if err != nil {
		return err
	}
	if s.db != nil {
		return s.restoreSQL(ctx, backup.State)
	}
	return s.mutate(ctx, func(state *State) error {
		restored := cloneState(backup.State)
		prepareRestoredState(&restored, s.now().UTC())
		*state = restored
		return nil
	})
}

func RestoreMemory(data []byte) (*Store, error) {
	backup, err := ParseBackup(data)
	if err != nil {
		return nil, err
	}
	result := NewMemory()
	result.state = cloneState(backup.State)
	prepareRestoredState(&result.state, result.now().UTC())
	result.ensureMaps()
	return result, nil
}

func (s *Store) StartRecovery(ctx context.Context) (WorkspaceState, error) {
	if s.db != nil {
		return s.startRecoverySQL(ctx)
	}
	var result WorkspaceState
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		for id, session := range state.Sessions {
			session.RevokedAt = &now
			state.Sessions[id] = session
		}
		pauseRecoveryState(state, now)
		result = state.Workspace
		return nil
	})
	return result, err
}

func (s *Store) ReconcileRecovery(ctx context.Context) (WorkspaceState, error) {
	if s.db != nil {
		return s.reconcileRecoverySQL(ctx)
	}
	var result WorkspaceState
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		for id, agent := range state.Agents {
			if !now.Before(agent.ExpiresAt) && agent.RevokedAt == nil {
				agent.RevokedAt = &now
				state.Agents[id] = agent
			}
		}
		for id, job := range state.Jobs {
			if isActiveJob(job.State) {
				job.State = "paused"
				job.LeaseOwner = ""
				job.LeaseExpiry = nil
				job.Result = map[string]string{"code": "recovery_reconciliation_required"}
				state.Jobs[id] = job
			}
		}
		state.Workspace.RecoveryMode = false
		// Enrollment and updates stay paused until the owner explicitly resumes
		// them after reviewing restored policy, keys, and revocations.
		state.Workspace.EnrollmentPaused = true
		state.Workspace.UpdatesPaused = true
		state.Workspace.NotificationsPaused = true
		advancePolicyRevision(&state.Workspace)
		resetAlertEvaluationsState(state, now)
		resetAlertWorkState(state, now)
		cancelPendingNotificationDeliveriesState(state, notificationDeliveryRecoveryCancellation, now)
		cancelExpiredSendingNotificationDeliveriesState(state, notificationDeliveryRecoveryCancellation, now)
		result = state.Workspace
		return nil
	})
	return result, err
}

// ResumeNotificationDeliveries is the only operation that clears the
// recovery notification fence. It cancels anything queued while the fence was
// active, then enqueues only the caller's fresh current-state summaries in the
// same transaction.
func (s *Store) ResumeNotificationDeliveries(ctx context.Context, expectedRevision int64, deliveries []NotificationDelivery) (WorkspaceState, NotificationDeliveryEnqueueResult, error) {
	if expectedRevision < 1 {
		return WorkspaceState{}, NotificationDeliveryEnqueueResult{}, ErrInvalid
	}
	if s.db != nil {
		return s.resumeNotificationDeliveriesSQL(ctx, expectedRevision, deliveries)
	}
	var workspace WorkspaceState
	var result NotificationDeliveryEnqueueResult
	err := s.mutate(ctx, func(state *State) error {
		working := cloneState(*state)
		if working.Workspace.PolicyRevision != expectedRevision || working.Workspace.RecoveryMode || !working.Workspace.NotificationsPaused {
			return ErrConflict
		}
		now := s.now().UTC()
		cancelPendingNotificationDeliveriesState(&working, notificationDeliveryRecoveryCancellation, now)
		cancelExpiredSendingNotificationDeliveriesState(&working, notificationDeliveryRecoveryCancellation, now)
		working.Workspace.NotificationsPaused = false
		advancePolicyRevision(&working.Workspace)
		var enqueueErr error
		result, enqueueErr = enqueueNotificationDeliveriesState(&working, deliveries, now)
		if enqueueErr != nil {
			return enqueueErr
		}
		workspace = working.Workspace
		*state = working
		return nil
	})
	return workspace, result, err
}

func (s *Store) restoreSQL(ctx context.Context, input State) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := readWorkspaceStateTx(ctx, tx); err != nil {
		return err
	}
	now := s.now().UTC()
	restored := cloneState(input)
	prepareRestoredState(&restored, now)
	if _, err := cancelPendingNotificationDeliveriesTx(ctx, tx, notificationDeliveryRecoveryCancellation, now); err != nil {
		return err
	}
	if _, err := cancelExpiredSendingNotificationDeliveriesTx(ctx, tx, notificationDeliveryRecoveryCancellation, now); err != nil {
		return err
	}
	if err := resetAlertEvaluationsSQLTx(ctx, tx, now); err != nil {
		return err
	}
	if err := resetAlertWorkSQLTx(ctx, tx, now); err != nil {
		return err
	}
	if _, err := closeOpenSuppressionEpisodesTx(ctx, tx, now); err != nil {
		return err
	}
	if err := writeWorkspaceStateTx(ctx, tx, restored); err != nil {
		return err
	}
	if err := syncMonitoringSettingsTx(ctx, tx, restored.Workspace, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workspace restore: %w", err)
	}
	s.invalidateLegacyCache()
	return nil
}

func (s *Store) startRecoverySQL(ctx context.Context) (WorkspaceState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkspaceState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return WorkspaceState{}, err
	}
	now := s.now().UTC()
	for id, session := range state.Sessions {
		session.RevokedAt = &now
		state.Sessions[id] = session
	}
	pauseRecoveryState(&state, now)
	if err := resetAlertEvaluationsSQLTx(ctx, tx, now); err != nil {
		return WorkspaceState{}, err
	}
	if err := resetAlertWorkSQLTx(ctx, tx, now); err != nil {
		return WorkspaceState{}, err
	}
	if _, err := cancelPendingNotificationDeliveriesTx(ctx, tx, notificationDeliveryRecoveryCancellation, now); err != nil {
		return WorkspaceState{}, err
	}
	if _, err := cancelExpiredSendingNotificationDeliveriesTx(ctx, tx, notificationDeliveryRecoveryCancellation, now); err != nil {
		return WorkspaceState{}, err
	}
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return WorkspaceState{}, err
	}
	if err := syncMonitoringSettingsTx(ctx, tx, state.Workspace, now); err != nil {
		return WorkspaceState{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceState{}, fmt.Errorf("commit recovery start: %w", err)
	}
	s.invalidateLegacyCache()
	return state.Workspace, nil
}

func (s *Store) reconcileRecoverySQL(ctx context.Context) (WorkspaceState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkspaceState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return WorkspaceState{}, err
	}
	now := s.now().UTC()
	for id, agent := range state.Agents {
		if !now.Before(agent.ExpiresAt) && agent.RevokedAt == nil {
			agent.RevokedAt = &now
			state.Agents[id] = agent
		}
	}
	for id, job := range state.Jobs {
		if isActiveJob(job.State) {
			job.State = "paused"
			job.LeaseOwner = ""
			job.LeaseExpiry = nil
			job.Result = map[string]string{"code": "recovery_reconciliation_required"}
			state.Jobs[id] = job
		}
	}
	state.Workspace.RecoveryMode = false
	state.Workspace.EnrollmentPaused = true
	state.Workspace.UpdatesPaused = true
	state.Workspace.NotificationsPaused = true
	advancePolicyRevision(&state.Workspace)
	resetAlertEvaluationsState(&state, now)
	resetAlertWorkState(&state, now)
	if err := resetAlertEvaluationsSQLTx(ctx, tx, now); err != nil {
		return WorkspaceState{}, err
	}
	if err := resetAlertWorkSQLTx(ctx, tx, now); err != nil {
		return WorkspaceState{}, err
	}
	if _, err := cancelPendingNotificationDeliveriesTx(ctx, tx, notificationDeliveryRecoveryCancellation, now); err != nil {
		return WorkspaceState{}, err
	}
	if _, err := cancelExpiredSendingNotificationDeliveriesTx(ctx, tx, notificationDeliveryRecoveryCancellation, now); err != nil {
		return WorkspaceState{}, err
	}
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return WorkspaceState{}, err
	}
	if err := syncMonitoringSettingsTx(ctx, tx, state.Workspace, now); err != nil {
		return WorkspaceState{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceState{}, fmt.Errorf("commit recovery reconciliation: %w", err)
	}
	s.invalidateLegacyCache()
	return state.Workspace, nil
}

func (s *Store) resumeNotificationDeliveriesSQL(ctx context.Context, expectedRevision int64, deliveries []NotificationDelivery) (WorkspaceState, NotificationDeliveryEnqueueResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkspaceState{}, NotificationDeliveryEnqueueResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return WorkspaceState{}, NotificationDeliveryEnqueueResult{}, err
	}
	if state.Workspace.PolicyRevision != expectedRevision || state.Workspace.RecoveryMode || !state.Workspace.NotificationsPaused {
		return WorkspaceState{}, NotificationDeliveryEnqueueResult{}, ErrConflict
	}
	now := s.now().UTC()
	if _, err := cancelPendingNotificationDeliveriesTx(ctx, tx, notificationDeliveryRecoveryCancellation, now); err != nil {
		return WorkspaceState{}, NotificationDeliveryEnqueueResult{}, err
	}
	if _, err := cancelExpiredSendingNotificationDeliveriesTx(ctx, tx, notificationDeliveryRecoveryCancellation, now); err != nil {
		return WorkspaceState{}, NotificationDeliveryEnqueueResult{}, err
	}
	state.Workspace.NotificationsPaused = false
	advancePolicyRevision(&state.Workspace)
	result := NotificationDeliveryEnqueueResult{}
	if len(deliveries) > 0 {
		result, err = enqueueNotificationDeliveriesSQLTx(ctx, tx, deliveries, now)
		if err != nil {
			return WorkspaceState{}, result, err
		}
		state.Workspace.NotificationQueueOverflows += int64(result.Dropped)
	}
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return WorkspaceState{}, result, err
	}
	if err := syncMonitoringSettingsTx(ctx, tx, state.Workspace, now); err != nil {
		return WorkspaceState{}, result, err
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceState{}, NotificationDeliveryEnqueueResult{}, fmt.Errorf("commit notification resume: %w", err)
	}
	s.invalidateLegacyCache()
	return state.Workspace, result, nil
}

func prepareRestoredState(state *State, now time.Time) {
	ensureStateMaps(state)
	state.Workspace.RecoveryMode = true
	state.Workspace.EnrollmentPaused = true
	state.Workspace.UpdatesPaused = true
	state.Workspace.NotificationsPaused = true
	advancePolicyRevision(&state.Workspace)
	resetAlertEvaluationsState(state, now)
	resetAlertWorkState(state, now)
	cancelPendingNotificationDeliveriesState(state, notificationDeliveryRecoveryCancellation, now)
	cancelExpiredSendingNotificationDeliveriesState(state, notificationDeliveryRecoveryCancellation, now)
	closeOpenSuppressionEpisodesState(state, now)
}

func pauseRecoveryState(state *State, now time.Time) {
	state.Workspace.RecoveryMode = true
	state.Workspace.EnrollmentPaused = true
	state.Workspace.UpdatesPaused = true
	state.Workspace.NotificationsPaused = true
	advancePolicyRevision(&state.Workspace)
	resetAlertEvaluationsState(state, now)
	resetAlertWorkState(state, now)
	cancelPendingNotificationDeliveriesState(state, notificationDeliveryRecoveryCancellation, now)
	cancelExpiredSendingNotificationDeliveriesState(state, notificationDeliveryRecoveryCancellation, now)
}

func advancePolicyRevision(workspace *WorkspaceState) {
	if workspace.PolicyRevision < 1 {
		workspace.PolicyRevision = 1
		workspace.MonitoringRevision = workspace.PolicyRevision
		return
	}
	workspace.PolicyRevision++
	workspace.MonitoringRevision = workspace.PolicyRevision
}

func resetAlertEvaluationsState(state *State, now time.Time) {
	for key, item := range state.AlertEvaluations {
		item.EvidenceState = "unknown"
		item.LastObservedAt = nil
		item.LastReceivedAt = nil
		item.PendingSince = nil
		item.RecoverySince = nil
		item.LastValidAt = nil
		item.TriggerConsecutive = 0
		item.RecoveryConsecutive = 0
		item.UpdatedAt = now
		state.AlertEvaluations[key] = cloneAlertEvaluation(item)
	}
}

func resetAlertWorkState(state *State, now time.Time) {
	for key, item := range state.AlertWork {
		if item.DirtyGeneration < 1 {
			item.DirtyGeneration = 1
		} else {
			item.DirtyGeneration++
		}
		item.LeaseEpoch++
		item.LeaseOwner = ""
		item.LeaseUntil = nil
		item.Attempts = 0
		item.LastError = ""
		item.UpdatedAt = now
		state.AlertWork[key] = cloneAlertWork(item)
	}
	for _, evaluation := range state.AlertEvaluations {
		if evaluation.LineageID == "" || evaluation.EntityID == "" {
			continue
		}
		key := alertWorkKey(evaluation.LineageID, evaluation.EntityID)
		if _, exists := state.AlertWork[key]; exists {
			continue
		}
		state.AlertWork[key] = AlertWorkItem{LineageID: evaluation.LineageID, EntityID: evaluation.EntityID, DirtyGeneration: 1, CreatedAt: now, UpdatedAt: now}
	}
}

func cancelPendingNotificationDeliveriesState(state *State, reason string, now time.Time) int {
	count := 0
	for key, item := range state.NotificationDeliveries {
		if item.Status != NotificationDeliveryQueued && item.Status != NotificationDeliveryRetry {
			continue
		}
		item.Status = NotificationDeliveryCancelled
		item.NextAttemptAt = nil
		item.LeaseOwner = ""
		item.LeaseUntil = nil
		item.SafeError = reason
		item.UpdatedAt = now
		state.NotificationDeliveries[key] = cloneNotificationDelivery(item)
		count++
	}
	return count
}

func cancelExpiredSendingNotificationDeliveriesState(state *State, reason string, now time.Time) int {
	count := 0
	for key, item := range state.NotificationDeliveries {
		if item.Status != NotificationDeliverySending || item.LeaseUntil != nil && item.LeaseUntil.After(now) {
			continue
		}
		item.Status = NotificationDeliveryCancelled
		item.NextAttemptAt = nil
		item.LeaseOwner = ""
		item.LeaseUntil = nil
		item.SafeError = reason
		item.UpdatedAt = now
		state.NotificationDeliveries[key] = cloneNotificationDelivery(item)
		count++
	}
	return count
}

func cancelPendingNotificationDeliveriesTx(ctx context.Context, tx *sql.Tx, reason string, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='cancelled', next_attempt_at=NULL, lease_owner='', lease_until=NULL, safe_error=$1, updated_at=$2 WHERE status IN ('queued','retry')`, reason, now)
	if err != nil {
		return 0, fmt.Errorf("cancel pending notification deliveries: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count cancelled notification deliveries: %w", err)
	}
	return count, nil
}

func cancelExpiredSendingNotificationDeliveriesTx(ctx context.Context, tx *sql.Tx, reason string, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='cancelled', next_attempt_at=NULL, lease_owner='', lease_until=NULL, safe_error=$1, updated_at=$2 WHERE status='sending' AND (lease_until IS NULL OR lease_until <= $2)`, reason, now)
	if err != nil {
		return 0, fmt.Errorf("cancel expired notification deliveries: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count expired notification deliveries: %w", err)
	}
	return count, nil
}

func resetAlertEvaluationsSQLTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE alert_evaluations SET evidence_state='unknown', last_observed_at=NULL, last_received_at=NULL, pending_since=NULL, recovery_since=NULL, last_valid_at=NULL, trigger_consecutive=0, recovery_consecutive=0, updated_at=$1`, now); err != nil {
		return fmt.Errorf("reset alert evaluations: %w", err)
	}
	return nil
}

func resetAlertWorkSQLTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE alert_work SET dirty_generation=GREATEST(dirty_generation + 1, 1), lease_epoch=lease_epoch + 1, lease_owner='', lease_until=NULL, attempts=0, last_error='', updated_at=$1`, now); err != nil {
		return fmt.Errorf("reset alert work leases: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO alert_work (lineage_id, entity_id, dirty_generation, lease_epoch, lease_owner, lease_until, attempts, last_error, created_at, updated_at) SELECT lineage_id, entity_id, 1, 0, '', NULL, 0, '', $1, $1 FROM alert_evaluations ON CONFLICT (lineage_id, entity_id) DO NOTHING`, now); err != nil {
		return fmt.Errorf("queue fresh alert evaluations: %w", err)
	}
	return nil
}

func closeOpenSuppressionEpisodesState(state *State, now time.Time) int {
	count := 0
	for key, episode := range state.SuppressionEpisodes {
		if episode.EndedAt != nil {
			continue
		}
		endedAt := now
		episode.EndedAt = &endedAt
		episode.SummaryBatchID = ""
		state.SuppressionEpisodes[key] = episode
		count++
	}
	return count
}

func closeOpenSuppressionEpisodesTx(ctx context.Context, tx *sql.Tx, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx, `UPDATE suppression_episodes SET ended_at=$1, summary_batch_id='' WHERE ended_at IS NULL`, now)
	if err != nil {
		return 0, fmt.Errorf("close open suppression episodes: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count closed suppression episodes: %w", err)
	}
	return count, nil
}

func cloneState(input State) State {
	data, err := json.Marshal(input)
	if err != nil {
		return newState()
	}
	var output State
	if err := json.Unmarshal(data, &output); err != nil {
		return newState()
	}
	ensureStateMaps(&output)
	return output
}
