package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const backupFormatVersion = 1

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
	return s.mutate(ctx, func(state *State) error {
		*state = cloneState(backup.State)
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
	result.ensureMaps()
	return result, nil
}

func (s *Store) StartRecovery(ctx context.Context) (WorkspaceState, error) {
	var result WorkspaceState
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		for id, session := range state.Sessions {
			session.RevokedAt = &now
			state.Sessions[id] = session
		}
		state.Workspace.RecoveryMode = true
		state.Workspace.EnrollmentPaused = true
		state.Workspace.UpdatesPaused = true
		result = state.Workspace
		return nil
	})
	return result, err
}

func (s *Store) ReconcileRecovery(ctx context.Context) (WorkspaceState, error) {
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
		result = state.Workspace
		return nil
	})
	return result, err
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
