package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrExpired      = errors.New("expired")
	ErrRevoked      = errors.New("revoked")
	ErrDuplicate    = errors.New("duplicate")
	ErrBackpressure = errors.New("backpressure")
	ErrInvalid      = errors.New("invalid")
)

// NewID returns a UUID-shaped random identifier without adding a second UUID
// dependency to the control plane. It is generated from the OS CSPRNG.
func NewID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("generate identifier: %v", err))
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(raw[0:4]), hex.EncodeToString(raw[4:6]), hex.EncodeToString(raw[6:8]), hex.EncodeToString(raw[8:10]), hex.EncodeToString(raw[10:16]))
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	state  State
	loaded bool
	now    func() time.Time
}

func NewMemory() *Store {
	return &Store{state: newState(), loaded: true, now: func() time.Time { return time.Now().UTC() }}
}

func NewSQL(db *sql.DB) *Store {
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
}

func newState() State {
	return State{
		Version: 1, Sessions: map[string]Session{}, Sites: map[string]Site{}, Scopes: map[string]Scope{},
		Devices: map[string]Device{}, Invitations: map[string]BootstrapInvitation{}, Agents: map[string]AgentIdentity{},
		Credentials: map[string]CredentialRef{}, Trust: map[string]TrustRecord{}, AccessRequests: map[string]AccessRequest{},
		Jobs: map[string]Job{}, BatchReceipts: map[string]string{}, Samples: []MetricSample{}, Observations: map[string]Observation{},
		Relationships: map[string]Relationship{}, Collectors: map[string]CollectorDescriptorState{}, Releases: map[string]Release{},
		Assignments: map[string]Assignment{}, AuditEvents: []AuditEvent{}, Workspace: WorkspaceState{SchemaVersion: 1},
	}
}

func (s *Store) ensureMaps() {
	if s.state.Version == 0 {
		s.state.Version = 1
	}
	if s.state.Sessions == nil {
		s.state.Sessions = map[string]Session{}
	}
	if s.state.Sites == nil {
		s.state.Sites = map[string]Site{}
	}
	if s.state.Scopes == nil {
		s.state.Scopes = map[string]Scope{}
	}
	if s.state.Devices == nil {
		s.state.Devices = map[string]Device{}
	}
	if s.state.Invitations == nil {
		s.state.Invitations = map[string]BootstrapInvitation{}
	}
	if s.state.Agents == nil {
		s.state.Agents = map[string]AgentIdentity{}
	}
	if s.state.Credentials == nil {
		s.state.Credentials = map[string]CredentialRef{}
	}
	if s.state.Trust == nil {
		s.state.Trust = map[string]TrustRecord{}
	}
	if s.state.AccessRequests == nil {
		s.state.AccessRequests = map[string]AccessRequest{}
	}
	if s.state.Jobs == nil {
		s.state.Jobs = map[string]Job{}
	}
	if s.state.BatchReceipts == nil {
		s.state.BatchReceipts = map[string]string{}
	}
	if s.state.Samples == nil {
		s.state.Samples = []MetricSample{}
	}
	if s.state.Observations == nil {
		s.state.Observations = map[string]Observation{}
	}
	if s.state.Relationships == nil {
		s.state.Relationships = map[string]Relationship{}
	}
	if s.state.Collectors == nil {
		s.state.Collectors = map[string]CollectorDescriptorState{}
	}
	if s.state.Releases == nil {
		s.state.Releases = map[string]Release{}
	}
	if s.state.Assignments == nil {
		s.state.Assignments = map[string]Assignment{}
	}
	if s.state.Workspace.SchemaVersion == 0 {
		s.state.Workspace.SchemaVersion = 1
	}
}

func (s *Store) loadLocked(ctx context.Context) error {
	if s.loaded {
		return nil
	}
	if s.db == nil {
		s.state = newState()
		s.loaded = true
		return nil
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM workspace_state WHERE singleton = true`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		s.state = newState()
		s.loaded = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("load workspace state: %w", err)
	}
	s.state = newState()
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &s.state); err != nil {
			return fmt.Errorf("decode workspace state: %w", err)
		}
	}
	s.ensureMaps()
	s.loaded = true
	return nil
}

func (s *Store) persistLocked(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	s.ensureMaps()
	raw, err := json.Marshal(s.state)
	if err != nil {
		return fmt.Errorf("encode workspace state: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO workspace_state(singleton, recovery_mode, enrollment_paused, updates_paused, schema_version, state_json, updated_at)
		VALUES (true, $1, $2, $3, $4, $5, now())
		ON CONFLICT (singleton) DO UPDATE SET recovery_mode = EXCLUDED.recovery_mode,
		 enrollment_paused = EXCLUDED.enrollment_paused, updates_paused = EXCLUDED.updates_paused,
		 schema_version = EXCLUDED.schema_version, state_json = EXCLUDED.state_json, updated_at = now()`,
		s.state.Workspace.RecoveryMode, s.state.Workspace.EnrollmentPaused, s.state.Workspace.UpdatesPaused, s.state.Workspace.SchemaVersion, raw)
	if err != nil {
		return fmt.Errorf("persist workspace state: %w", err)
	}
	return nil
}

func (s *Store) mutate(ctx context.Context, fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(ctx); err != nil {
		return err
	}
	if err := fn(&s.state); err != nil {
		return err
	}
	return s.persistLocked(ctx)
}

func (s *Store) read(ctx context.Context, fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(ctx); err != nil {
		return err
	}
	return fn(&s.state)
}

func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now != nil {
		s.now = now
	}
}

func (s *Store) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func (s *Store) Owner(ctx context.Context) (Owner, error) {
	var result Owner
	err := s.read(ctx, func(state *State) error {
		if state.Owner == nil {
			return ErrNotFound
		}
		result = *state.Owner
		result.TOTPSecretCiphertext = append([]byte(nil), state.Owner.TOTPSecretCiphertext...)
		result.TOTPSecretNonce = append([]byte(nil), state.Owner.TOTPSecretNonce...)
		result.TOTPWrappedDataKey = append([]byte(nil), state.Owner.TOTPWrappedDataKey...)
		result.RecoveryCodeHashes = append([]string(nil), state.Owner.RecoveryCodeHashes...)
		return nil
	})
	return result, err
}

func (s *Store) CreateOwner(ctx context.Context, owner Owner) error {
	return s.mutate(ctx, func(state *State) error {
		if state.Owner != nil {
			return ErrConflict
		}
		if owner.ID == "" {
			owner.ID = NewID()
		}
		if owner.CreatedAt.IsZero() {
			owner.CreatedAt = s.now().UTC()
		}
		state.Owner = &owner
		return nil
	})
}

func (s *Store) UpdateOwner(ctx context.Context, owner Owner) error {
	return s.mutate(ctx, func(state *State) error {
		if state.Owner == nil || state.Owner.ID != owner.ID {
			return ErrNotFound
		}
		state.Owner = &owner
		return nil
	})
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	return s.mutate(ctx, func(state *State) error {
		if state.Owner == nil || state.Owner.ID != session.OwnerID {
			return ErrUnauthorized
		}
		if session.TokenHash == "" || session.CSRFHash == "" {
			return ErrInvalid
		}
		if _, ok := state.Sessions[session.TokenHash]; ok {
			return ErrConflict
		}
		state.Sessions[session.TokenHash] = session
		return nil
	})
}

func (s *Store) Session(ctx context.Context, tokenHash string) (Session, error) {
	var result Session
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Sessions[tokenHash]
		if !ok {
			return ErrUnauthorized
		}
		now := s.now().UTC()
		if item.RevokedAt != nil || !now.Before(item.AbsoluteExpiry) || now.Sub(item.LastSeen) > 30*time.Minute {
			return ErrUnauthorized
		}
		result = item
		return nil
	})
	return result, err
}

func (s *Store) TouchSession(ctx context.Context, tokenHash string, at time.Time) error {
	return s.mutate(ctx, func(state *State) error {
		item, ok := state.Sessions[tokenHash]
		if !ok || item.RevokedAt != nil {
			return ErrUnauthorized
		}
		item.LastSeen = at.UTC()
		state.Sessions[tokenHash] = item
		return nil
	})
}

func (s *Store) SetSessionMFA(ctx context.Context, tokenHash string, at time.Time) error {
	return s.mutate(ctx, func(state *State) error {
		item, ok := state.Sessions[tokenHash]
		if !ok || item.RevokedAt != nil {
			return ErrUnauthorized
		}
		value := at.UTC()
		item.RecentMFAAt = &value
		state.Sessions[tokenHash] = item
		return nil
	})
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash string) error {
	return s.mutate(ctx, func(state *State) error {
		item, ok := state.Sessions[tokenHash]
		if !ok {
			return nil
		}
		now := s.now().UTC()
		item.RevokedAt = &now
		state.Sessions[tokenHash] = item
		return nil
	})
}

func (s *Store) RevokeAllSessions(ctx context.Context) error {
	return s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		for key, item := range state.Sessions {
			item.RevokedAt = &now
			state.Sessions[key] = item
		}
		return nil
	})
}

func (s *Store) HasRecentMFA(session Session) bool {
	return session.RecentMFAAt != nil && s.Now().Sub(*session.RecentMFAAt) <= 5*time.Minute
}

func (s *Store) ListAudit(ctx context.Context) ([]AuditEvent, error) {
	result := []AuditEvent{}
	err := s.read(ctx, func(state *State) error {
		result = append(result, state.AuditEvents...)
		return nil
	})
	return result, err
}

func (s *Store) AppendAudit(ctx context.Context, event AuditEvent) error {
	return s.mutate(ctx, func(state *State) error {
		if event.ID == "" {
			event.ID = NewID()
		}
		if event.EventTime.IsZero() {
			event.EventTime = s.now().UTC()
		}
		if event.RedactedOutcome == nil {
			event.RedactedOutcome = map[string]any{}
		}
		state.AuditEvents = append(state.AuditEvents, event)
		if len(state.AuditEvents) > 100000 {
			state.AuditEvents = state.AuditEvents[len(state.AuditEvents)-100000:]
		}
		return nil
	})
}

func cloneStrings(values []string) []string { return append([]string(nil), values...) }
func cloneInts(values []int) []int          { return append([]int(nil), values...) }
func cloneMap(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		result[key] = value
	}
	return result
}
func cloneFloatMap(values map[string]Freshness) map[string]Freshness {
	result := map[string]Freshness{}
	for key, value := range values {
		result[key] = value
	}
	return result
}

func containsFold(haystack, needle string) bool {
	return needle == "" || strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func sortDevices(items []Device) {
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].DisplayName) < strings.ToLower(items[j].DisplayName)
	})
}
