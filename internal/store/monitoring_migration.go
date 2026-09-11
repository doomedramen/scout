package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultMonitoringMigrationBatchSize = 100
	maxMonitoringMigrationBatchSize     = 1000

	monitoringStreamDevices      = "devices"
	monitoringStreamAgents       = "agents"
	monitoringStreamReceipts     = "receipts"
	monitoringStreamSamples      = "samples"
	monitoringStreamObservations = "observations"
)

var monitoringMigrationStreams = []string{
	monitoringStreamDevices,
	monitoringStreamAgents,
	monitoringStreamReceipts,
	monitoringStreamSamples,
	monitoringStreamObservations,
}

type MonitoringStoragePhase string

const (
	MonitoringStorageLegacy        MonitoringStoragePhase = "legacy"
	MonitoringStorageImporting     MonitoringStoragePhase = "importing"
	MonitoringStorageAuthoritative MonitoringStoragePhase = "authoritative"
)

type MonitoringMigrationCheckpoint struct {
	Generation int64  `json:"generation"`
	Stream     string `json:"stream"`
	NextIndex  int64  `json:"nextIndex"`
	Completed  bool   `json:"completed"`
}

type MonitoringMigrationStatus struct {
	StorageGeneration          int64                           `json:"storageGeneration"`
	MigrationGeneration        int64                           `json:"migrationGeneration"`
	Phase                      MonitoringStoragePhase          `json:"phase"`
	LegacyStateHash            string                          `json:"legacyStateHash,omitempty"`
	LegacySampleCount          int64                           `json:"legacySampleCount"`
	NormalizedSampleCount      int64                           `json:"normalizedSampleCount"`
	LegacyReceiptCount         int64                           `json:"legacyReceiptCount"`
	NormalizedReceiptCount     int64                           `json:"normalizedReceiptCount"`
	LegacyObservationCount     int64                           `json:"legacyObservationCount"`
	NormalizedObservationCount int64                           `json:"normalizedObservationCount"`
	ParityCheckedAt            *time.Time                      `json:"parityCheckedAt,omitempty"`
	CutoverAt                  *time.Time                      `json:"cutoverAt,omitempty"`
	Checkpoints                []MonitoringMigrationCheckpoint `json:"checkpoints"`
}

type MonitoringMigrationParity struct {
	Generation             int64                  `json:"generation"`
	Phase                  MonitoringStoragePhase `json:"phase"`
	SourceHashMatches      bool                   `json:"sourceHashMatches"`
	CheckpointsComplete    bool                   `json:"checkpointsComplete"`
	LegacySamples          int64                  `json:"legacySamples"`
	NormalizedSamples      int64                  `json:"normalizedSamples"`
	LegacyReceipts         int64                  `json:"legacyReceipts"`
	NormalizedReceipts     int64                  `json:"normalizedReceipts"`
	LegacyObservations     int64                  `json:"legacyObservations"`
	NormalizedObservations int64                  `json:"normalizedObservations"`
	Complete               bool                   `json:"complete"`
}

type monitoringStorageRow struct {
	StorageGeneration          int64
	MigrationGeneration        int64
	Phase                      MonitoringStoragePhase
	LegacyStateHash            string
	LegacySampleCount          int64
	NormalizedSampleCount      int64
	LegacyReceiptCount         int64
	NormalizedReceiptCount     int64
	LegacyObservationCount     int64
	NormalizedObservationCount int64
	ParityCheckedAt            *time.Time
	CutoverAt                  *time.Time
}

type monitoringSQLReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type legacyTelemetrySnapshot struct {
	state      State
	hash       string
	snapshotAt time.Time
}

type legacyReceipt struct {
	agentID     string
	bootID      string
	batchID     string
	payloadHash string
}

type legacyTelemetryStreams struct {
	devices      []Device
	agents       []AgentIdentity
	receipts     []legacyReceipt
	samples      []MetricSample
	observations []Observation
}

// MonitoringStorage reports the durable telemetry generation and migration
// checkpoints. It is intentionally SQL-only: the memory store is a fixture for
// pre-002 behavior and cannot prove migration durability.
func (s *Store) MonitoringStorage(ctx context.Context) (MonitoringMigrationStatus, error) {
	if s.db == nil {
		return MonitoringMigrationStatus{}, ErrInvalid
	}
	return readMonitoringStorageStatus(ctx, s.db)
}

// BeginMonitoringMigration creates or resumes one checkpointed migration. It
// records a hash of the legacy workspace snapshot so a live writer cannot be
// silently cut over after changing the source underneath the import.
func (s *Store) BeginMonitoringMigration(ctx context.Context) (MonitoringMigrationStatus, error) {
	if s.db == nil {
		return MonitoringMigrationStatus{}, ErrInvalid
	}
	snapshot, err := s.legacyTelemetrySnapshot(ctx)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return MonitoringMigrationStatus{}, err
	}
	row, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	switch row.Phase {
	case MonitoringStorageAuthoritative:
		return MonitoringMigrationStatus{}, fmt.Errorf("monitoring storage is already authoritative: %w", ErrConflict)
	case MonitoringStorageImporting:
		if row.LegacyStateHash != snapshot.hash {
			return MonitoringMigrationStatus{}, fmt.Errorf("legacy workspace changed during migration: %w", ErrConflict)
		}
	case MonitoringStorageLegacy:
		generation := row.MigrationGeneration + 1
		if generation < 1 {
			generation = 1
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE monitoring_storage_state
			SET migration_generation = $1, phase = 'importing', legacy_state_hash = $2,
			    legacy_sample_count = 0, normalized_sample_count = 0,
			    legacy_receipt_count = 0, normalized_receipt_count = 0,
			    legacy_observation_count = 0, normalized_observation_count = 0,
			    parity_checked_at = NULL, cutover_at = NULL, updated_at = now()
			WHERE singleton = true`, generation, snapshot.hash); err != nil {
			return MonitoringMigrationStatus{}, fmt.Errorf("start monitoring migration: %w", err)
		}
		for _, stream := range monitoringMigrationStreams {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO monitoring_migration_checkpoints(migration_generation, stream)
				VALUES ($1, $2)`, generation, stream); err != nil {
				return MonitoringMigrationStatus{}, fmt.Errorf("create %s migration checkpoint: %w", stream, err)
			}
		}
	default:
		return MonitoringMigrationStatus{}, fmt.Errorf("unsupported monitoring migration phase %q", row.Phase)
	}
	if err := tx.Commit(); err != nil {
		return MonitoringMigrationStatus{}, fmt.Errorf("commit monitoring migration start: %w", err)
	}
	return s.MonitoringStorage(ctx)
}

// ImportLegacyTelemetry copies the telemetry-bearing portions of the legacy
// workspace snapshot in deterministic bounded batches. Each batch commits its
// rows and checkpoint together, so a crash either repeats no work or resumes at
// the last committed index.
func (s *Store) ImportLegacyTelemetry(ctx context.Context, batchSize int) (MonitoringMigrationParity, error) {
	if s.db == nil {
		return MonitoringMigrationParity{}, ErrInvalid
	}
	if batchSize <= 0 {
		batchSize = defaultMonitoringMigrationBatchSize
	}
	if batchSize > maxMonitoringMigrationBatchSize {
		batchSize = maxMonitoringMigrationBatchSize
	}
	status, err := s.BeginMonitoringMigration(ctx)
	if err != nil {
		return MonitoringMigrationParity{}, err
	}
	snapshot, err := s.legacyTelemetrySnapshot(ctx)
	if err != nil {
		return MonitoringMigrationParity{}, err
	}
	if snapshot.hash != status.LegacyStateHash {
		return MonitoringMigrationParity{}, fmt.Errorf("legacy workspace changed during migration: %w", ErrConflict)
	}
	migrationGeneration := status.MigrationGeneration
	streams := buildLegacyTelemetryStreams(snapshot.state)
	for _, stream := range monitoringMigrationStreams {
		total := legacyStreamLength(streams, stream)
		for {
			status, err = s.MonitoringStorage(ctx)
			if err != nil {
				return MonitoringMigrationParity{}, err
			}
			if status.MigrationGeneration != migrationGeneration {
				return MonitoringMigrationParity{}, fmt.Errorf("migration generation changed while importing: %w", ErrConflict)
			}
			checkpoint, ok := findMonitoringCheckpoint(status.Checkpoints, stream)
			if !ok {
				return MonitoringMigrationParity{}, fmt.Errorf("missing %s migration checkpoint: %w", stream, ErrConflict)
			}
			if checkpoint.Completed {
				break
			}
			start := checkpoint.NextIndex
			if start > int64(total) {
				return MonitoringMigrationParity{}, fmt.Errorf("%s migration checkpoint %d exceeds source length %d: %w", stream, start, total, ErrConflict)
			}
			end := start + int64(batchSize)
			if end > int64(total) {
				end = int64(total)
			}
			if err := s.importLegacyTelemetryBatch(ctx, migrationGeneration, status.LegacyStateHash, snapshot, streams, stream, start, end, int64(total)); err != nil {
				return MonitoringMigrationParity{}, err
			}
		}
	}
	return s.VerifyMonitoringParity(ctx)
}

// VerifyMonitoringParity compares the source snapshot with rows tagged by the
// active migration generation. It never marks the generation authoritative.
func (s *Store) VerifyMonitoringParity(ctx context.Context) (MonitoringMigrationParity, error) {
	if s.db == nil {
		return MonitoringMigrationParity{}, ErrInvalid
	}
	status, err := s.MonitoringStorage(ctx)
	if err != nil {
		return MonitoringMigrationParity{}, err
	}
	snapshot, err := s.legacyTelemetrySnapshot(ctx)
	if err != nil {
		return MonitoringMigrationParity{}, err
	}
	streams := buildLegacyTelemetryStreams(snapshot.state)
	parity := MonitoringMigrationParity{
		Generation:          status.MigrationGeneration,
		Phase:               status.Phase,
		SourceHashMatches:   status.LegacyStateHash != "" && status.LegacyStateHash == snapshot.hash,
		CheckpointsComplete: monitoringCheckpointsComplete(status.Checkpoints),
		LegacySamples:       int64(len(streams.samples)),
		LegacyReceipts:      int64(len(streams.receipts)),
		LegacyObservations:  int64(len(streams.observations)),
	}
	if status.Phase == MonitoringStorageLegacy {
		return parity, nil
	}
	parity.NormalizedSamples, err = countGeneration(ctx, s.db, "metric_samples", status.MigrationGeneration)
	if err != nil {
		return MonitoringMigrationParity{}, err
	}
	parity.NormalizedReceipts, err = countGeneration(ctx, s.db, "telemetry_receipts", status.MigrationGeneration)
	if err != nil {
		return MonitoringMigrationParity{}, err
	}
	parity.NormalizedObservations, err = countGeneration(ctx, s.db, "observations", status.MigrationGeneration)
	if err != nil {
		return MonitoringMigrationParity{}, err
	}
	parity.Complete = parity.SourceHashMatches && parity.CheckpointsComplete &&
		parity.LegacySamples == parity.NormalizedSamples &&
		parity.LegacyReceipts == parity.NormalizedReceipts &&
		parity.LegacyObservations == parity.NormalizedObservations
	_, err = s.db.ExecContext(ctx, `
		UPDATE monitoring_storage_state
		SET legacy_sample_count = $1, normalized_sample_count = $2,
		    legacy_receipt_count = $3, normalized_receipt_count = $4,
		    legacy_observation_count = $5, normalized_observation_count = $6,
		    parity_checked_at = now(), updated_at = now()
		WHERE singleton = true`, parity.LegacySamples, parity.NormalizedSamples, parity.LegacyReceipts, parity.NormalizedReceipts, parity.LegacyObservations, parity.NormalizedObservations)
	if err != nil {
		return MonitoringMigrationParity{}, fmt.Errorf("record monitoring parity: %w", err)
	}
	return parity, nil
}

// CutoverMonitoringStorage marks a verified generation authoritative. The
// source hash, checkpoint completion, and all three row counts are rechecked
// while the migration row is locked, preventing a stale verification from
// crossing the cutover boundary.
func (s *Store) CutoverMonitoringStorage(ctx context.Context, expectedGeneration int64) (MonitoringMigrationStatus, error) {
	if s.db == nil {
		return MonitoringMigrationStatus{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return MonitoringMigrationStatus{}, err
	}
	row, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	if row.Phase != MonitoringStorageImporting || row.MigrationGeneration != expectedGeneration {
		return MonitoringMigrationStatus{}, fmt.Errorf("migration generation %d is not ready for cutover: %w", expectedGeneration, ErrConflict)
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT state_json FROM workspace_state WHERE singleton = true`).Scan(&raw); err != nil {
		return MonitoringMigrationStatus{}, fmt.Errorf("read legacy state for cutover: %w", err)
	}
	snapshot, err := decodeLegacyTelemetrySnapshot(raw, time.Now().UTC())
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	if snapshot.hash != row.LegacyStateHash {
		return MonitoringMigrationStatus{}, fmt.Errorf("legacy workspace changed before cutover: %w", ErrConflict)
	}
	streams := buildLegacyTelemetryStreams(snapshot.state)
	complete, err := monitoringCheckpointsCompleteTx(ctx, tx, expectedGeneration)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	if !complete {
		return MonitoringMigrationStatus{}, fmt.Errorf("monitoring migration checkpoints are incomplete: %w", ErrConflict)
	}
	normalizedSamples, err := countGeneration(ctx, tx, "metric_samples", expectedGeneration)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	normalizedReceipts, err := countGeneration(ctx, tx, "telemetry_receipts", expectedGeneration)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	normalizedObservations, err := countGeneration(ctx, tx, "observations", expectedGeneration)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	if int64(len(streams.samples)) != normalizedSamples || int64(len(streams.receipts)) != normalizedReceipts || int64(len(streams.observations)) != normalizedObservations {
		return MonitoringMigrationStatus{}, fmt.Errorf("monitoring telemetry parity failed at cutover: %w", ErrConflict)
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitoring_storage_state
		SET storage_generation = migration_generation, phase = 'authoritative',
		    legacy_sample_count = $1, normalized_sample_count = $2,
		    legacy_receipt_count = $3, normalized_receipt_count = $4,
		    legacy_observation_count = $5, normalized_observation_count = $6,
		    parity_checked_at = $7, cutover_at = $7, updated_at = $7
		WHERE singleton = true`, len(streams.samples), normalizedSamples, len(streams.receipts), normalizedReceipts, len(streams.observations), normalizedObservations, now); err != nil {
		return MonitoringMigrationStatus{}, fmt.Errorf("mark monitoring storage authoritative: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MonitoringMigrationStatus{}, fmt.Errorf("commit monitoring storage cutover: %w", err)
	}
	return s.MonitoringStorage(ctx)
}

func (s *Store) legacyTelemetrySnapshot(ctx context.Context) (legacyTelemetrySnapshot, error) {
	if s.db == nil {
		return legacyTelemetrySnapshot{}, ErrInvalid
	}
	var raw []byte
	var updatedAt time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT state_json, updated_at FROM workspace_state WHERE singleton = true`).Scan(&raw, &updatedAt); err != nil {
		return legacyTelemetrySnapshot{}, fmt.Errorf("read legacy telemetry snapshot: %w", err)
	}
	return decodeLegacyTelemetrySnapshot(raw, updatedAt)
}

func decodeLegacyTelemetrySnapshot(raw []byte, snapshotAt time.Time) (legacyTelemetrySnapshot, error) {
	state := newState()
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &state); err != nil {
			return legacyTelemetrySnapshot{}, fmt.Errorf("decode legacy telemetry snapshot: %w", err)
		}
	}
	ensureStateMaps(&state)
	if snapshotAt.IsZero() {
		snapshotAt = time.Now().UTC()
	}
	digest := sha256.Sum256(raw)
	return legacyTelemetrySnapshot{state: state, hash: hex.EncodeToString(digest[:]), snapshotAt: snapshotAt.UTC()}, nil
}

func buildLegacyTelemetryStreams(state State) legacyTelemetryStreams {
	streams := legacyTelemetryStreams{
		devices:      make([]Device, 0, len(state.Devices)),
		agents:       make([]AgentIdentity, 0, len(state.Agents)),
		receipts:     make([]legacyReceipt, 0, len(state.BatchReceipts)),
		samples:      append([]MetricSample(nil), state.Samples...),
		observations: make([]Observation, 0, len(state.Observations)),
	}
	for _, item := range state.Devices {
		streams.devices = append(streams.devices, item)
	}
	for _, item := range state.Agents {
		streams.agents = append(streams.agents, item)
	}
	for key, payloadHash := range state.BatchReceipts {
		receipt, ok := parseBatchReceiptKey(key)
		if !ok {
			// Keep malformed legacy receipts in the source count so import fails
			// visibly instead of silently claiming parity.
			receipt.payloadHash = payloadHash
		}
		streams.receipts = append(streams.receipts, receipt)
	}
	for _, item := range state.Observations {
		streams.observations = append(streams.observations, item)
	}
	sort.Slice(streams.devices, func(i, j int) bool { return streams.devices[i].ID < streams.devices[j].ID })
	sort.Slice(streams.agents, func(i, j int) bool { return streams.agents[i].ID < streams.agents[j].ID })
	sort.Slice(streams.receipts, func(i, j int) bool {
		return strings.Join([]string{streams.receipts[i].agentID, streams.receipts[i].bootID, streams.receipts[i].batchID}, "\x00") < strings.Join([]string{streams.receipts[j].agentID, streams.receipts[j].bootID, streams.receipts[j].batchID}, "\x00")
	})
	sort.Slice(streams.samples, func(i, j int) bool {
		if streams.samples[i].ReceivedAt.Equal(streams.samples[j].ReceivedAt) {
			return streams.samples[i].ID < streams.samples[j].ID
		}
		return streams.samples[i].ReceivedAt.Before(streams.samples[j].ReceivedAt)
	})
	sort.Slice(streams.observations, func(i, j int) bool { return streams.observations[i].ID < streams.observations[j].ID })
	return streams
}

// batchReceiptKey is JSON-safe because workspace_state is stored as JSONB.
// parseBatchReceiptKey also understands the original NUL-delimited form so a
// memory backup created before this fix remains importable.
func batchReceiptKey(agentID, bootID, batchID string) string {
	encoded, _ := json.Marshal([]string{agentID, bootID, batchID})
	return string(encoded)
}

func parseBatchReceiptKey(key string) (legacyReceipt, bool) {
	parts := strings.Split(key, "\x00")
	if len(parts) == 3 {
		return legacyReceipt{agentID: parts[0], bootID: parts[1], batchID: parts[2]}, parts[0] != "" && parts[1] != "" && parts[2] != ""
	}
	var values []string
	if err := json.Unmarshal([]byte(key), &values); err != nil || len(values) != 3 {
		return legacyReceipt{}, false
	}
	return legacyReceipt{agentID: values[0], bootID: values[1], batchID: values[2]}, values[0] != "" && values[1] != "" && values[2] != ""
}

func legacyStreamLength(streams legacyTelemetryStreams, stream string) int {
	switch stream {
	case monitoringStreamDevices:
		return len(streams.devices)
	case monitoringStreamAgents:
		return len(streams.agents)
	case monitoringStreamReceipts:
		return len(streams.receipts)
	case monitoringStreamSamples:
		return len(streams.samples)
	case monitoringStreamObservations:
		return len(streams.observations)
	default:
		return 0
	}
}

func (s *Store) importLegacyTelemetryBatch(ctx context.Context, generation int64, sourceHash string, snapshot legacyTelemetrySnapshot, streams legacyTelemetryStreams, stream string, start, end, total int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return err
	}
	row, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return err
	}
	if row.Phase != MonitoringStorageImporting || row.MigrationGeneration != generation || row.LegacyStateHash != sourceHash {
		return fmt.Errorf("monitoring migration changed while importing %s: %w", stream, ErrConflict)
	}
	var nextIndex int64
	var completed bool
	if err := tx.QueryRowContext(ctx, `SELECT next_index, completed FROM monitoring_migration_checkpoints WHERE migration_generation = $1 AND stream = $2 FOR UPDATE`, generation, stream).Scan(&nextIndex, &completed); err != nil {
		return fmt.Errorf("read %s migration checkpoint: %w", stream, err)
	}
	if nextIndex != start || completed {
		return fmt.Errorf("stale %s migration checkpoint: got %d completed=%t want %d: %w", stream, nextIndex, completed, start, ErrConflict)
	}
	switch stream {
	case monitoringStreamDevices:
		for _, item := range streams.devices[start:end] {
			if err := importLegacyDevice(ctx, tx, item, snapshot.snapshotAt); err != nil {
				return err
			}
		}
	case monitoringStreamAgents:
		for _, item := range streams.agents[start:end] {
			if err := importLegacyAgent(ctx, tx, item, snapshot.snapshotAt); err != nil {
				return err
			}
		}
	case monitoringStreamReceipts:
		for _, item := range streams.receipts[start:end] {
			if err := importLegacyReceipt(ctx, tx, item, generation, snapshot.snapshotAt); err != nil {
				return err
			}
		}
	case monitoringStreamSamples:
		for index := start; index < end; index++ {
			if err := importLegacySample(ctx, tx, streams.samples[index], index, generation, snapshot.snapshotAt); err != nil {
				return err
			}
		}
	case monitoringStreamObservations:
		for _, item := range streams.observations[start:end] {
			if err := importLegacyObservation(ctx, tx, item, generation, snapshot.snapshotAt); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown monitoring migration stream %q", stream)
	}
	completed = end >= total
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitoring_migration_checkpoints
		SET next_index = $1, completed = $2, updated_at = now()
		WHERE migration_generation = $3 AND stream = $4`, end, completed, generation, stream); err != nil {
		return fmt.Errorf("advance %s migration checkpoint: %w", stream, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s migration batch: %w", stream, err)
	}
	return nil
}

func importLegacyDevice(ctx context.Context, tx *sql.Tx, item Device, snapshotAt time.Time) error {
	if item.ID == "" {
		return fmt.Errorf("legacy device has no id: %w", ErrInvalid)
	}
	if item.DisplayName == "" {
		item.DisplayName = item.ID
	}
	if item.Platform == "" {
		item.Platform = "unknown"
	}
	if item.Architecture == "" {
		item.Architecture = "unknown"
	}
	if item.Lifecycle == "" {
		item.Lifecycle = "candidate"
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = snapshotAt
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO devices(id, site_id, display_name, platform, architecture, lifecycle, created_at, decommissioned_at)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET site_id = EXCLUDED.site_id,
		  display_name = EXCLUDED.display_name, platform = EXCLUDED.platform,
		  architecture = EXCLUDED.architecture, lifecycle = EXCLUDED.lifecycle,
		  created_at = EXCLUDED.created_at, decommissioned_at = EXCLUDED.decommissioned_at`,
		item.ID, item.SiteID, item.DisplayName, item.Platform, item.Architecture, item.Lifecycle, item.CreatedAt.UTC(), item.DecommissionedAt)
	if err != nil {
		return fmt.Errorf("import legacy device %s: %w", item.ID, err)
	}
	return nil
}

func importLegacyAgent(ctx context.Context, tx *sql.Tx, item AgentIdentity, snapshotAt time.Time) error {
	if item.ID == "" || item.DeviceID == "" {
		return fmt.Errorf("legacy agent is missing identity or device reference: %w", ErrInvalid)
	}
	if item.CertSerial == "" {
		item.CertSerial = "legacy-" + item.ID
	}
	if item.ExpiresAt.IsZero() {
		item.ExpiresAt = snapshotAt
	}
	var authTokenHash any
	if item.AuthTokenHash != "" {
		authTokenHash = item.AuthTokenHash
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO agent_identities(id, device_id, public_key_hash, cert_serial, auth_token_hash, certificate_pem, expires_at, revoked_at, installed_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET device_id = EXCLUDED.device_id,
		  public_key_hash = EXCLUDED.public_key_hash, cert_serial = EXCLUDED.cert_serial,
		  auth_token_hash = EXCLUDED.auth_token_hash, certificate_pem = EXCLUDED.certificate_pem,
		  expires_at = EXCLUDED.expires_at, revoked_at = EXCLUDED.revoked_at,
		  installed_version = EXCLUDED.installed_version`,
		item.ID, item.DeviceID, item.PublicKeyHash, item.CertSerial, authTokenHash, item.CertificatePEM, item.ExpiresAt.UTC(), item.RevokedAt, item.InstalledVersion)
	if err != nil {
		return fmt.Errorf("import legacy agent %s: %w", item.ID, err)
	}
	return nil
}

func importLegacyReceipt(ctx context.Context, tx *sql.Tx, item legacyReceipt, generation int64, snapshotAt time.Time) error {
	if item.agentID == "" || item.bootID == "" || item.batchID == "" {
		return fmt.Errorf("legacy receipt has an incomplete identity: %w", ErrInvalid)
	}
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT payload_hash FROM telemetry_receipts WHERE agent_id = $1 AND boot_id = $2 AND batch_id = $3`, item.agentID, item.bootID, item.batchID).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `
			INSERT INTO telemetry_receipts(agent_id, boot_id, batch_id, payload_hash, accepted_at, storage_generation)
			VALUES ($1, $2, $3, $4, $5, $6)`, item.agentID, item.bootID, item.batchID, item.payloadHash, snapshotAt.UTC(), generation)
	case err != nil:
		return fmt.Errorf("read legacy receipt %s/%s: %w", item.bootID, item.batchID, err)
	case existing != item.payloadHash:
		return fmt.Errorf("legacy receipt hash conflict for %s/%s: %w", item.bootID, item.batchID, ErrConflict)
	default:
		_, err = tx.ExecContext(ctx, `
			UPDATE telemetry_receipts SET storage_generation = $1
			WHERE agent_id = $2 AND boot_id = $3 AND batch_id = $4`, generation, item.agentID, item.bootID, item.batchID)
	}
	if err != nil {
		return fmt.Errorf("import legacy receipt %s/%s: %w", item.bootID, item.batchID, err)
	}
	return nil
}

func importLegacySample(ctx context.Context, tx *sql.Tx, item MetricSample, index, generation int64, snapshotAt time.Time) error {
	if item.DeviceID == "" || item.AgentID == "" || item.Metric == "" {
		return fmt.Errorf("legacy sample %d has incomplete identity: %w", index, ErrInvalid)
	}
	collectorID := item.CollectorID
	if collectorID == "" {
		collectorID = "legacy"
	}
	entityID := item.EntityID
	if entityID == "" {
		entityID = "host"
	}
	unit := item.Unit
	if unit == "" {
		unit = "unknown"
	}
	availability := item.Availability
	if availability == "" {
		availability = FreshnessUnavailable
	}
	observedAt := item.ObservedAt
	if observedAt.IsZero() {
		observedAt = snapshotAt
	}
	receivedAt := item.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = snapshotAt
	}
	labels := item.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("encode legacy sample labels: %w", err)
	}
	seriesID := deterministicSeriesID(item.DeviceID, collectorID, entityID, item.Metric, unit, labelsJSON)
	if err := importLegacySeries(ctx, tx, seriesID, item.DeviceID, collectorID, entityID, item.Metric, unit, labelsJSON, observedAt, generation); err != nil {
		return err
	}
	sampleID := item.ID
	if sampleID == "" {
		sampleID = deterministicLegacySampleID(item, index)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO metric_samples(id, device_id, agent_id, collector_id, entity_id, metric, labels, value, availability, unit, observed_at, received_at, series_id, storage_generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id, received_at) DO NOTHING`,
		sampleID, item.DeviceID, item.AgentID, collectorID, entityID, item.Metric, labelsJSON, nullableMetricValue(item.Value), string(availability), unit, observedAt.UTC(), receivedAt.UTC(), seriesID, generation); err != nil {
		return fmt.Errorf("import legacy sample %s: %w", sampleID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO current_series(series_id, sample_id, observed_at, received_at, value, availability, storage_generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (series_id) DO UPDATE SET sample_id = EXCLUDED.sample_id,
		  observed_at = EXCLUDED.observed_at, received_at = EXCLUDED.received_at,
		  value = EXCLUDED.value, availability = EXCLUDED.availability,
		  storage_generation = EXCLUDED.storage_generation
		WHERE EXCLUDED.observed_at > current_series.observed_at
		   OR (EXCLUDED.observed_at = current_series.observed_at AND EXCLUDED.received_at > current_series.received_at)`,
		seriesID, sampleID, observedAt.UTC(), receivedAt.UTC(), nullableMetricValue(item.Value), string(availability), generation); err != nil {
		return fmt.Errorf("import current legacy sample %s: %w", sampleID, err)
	}
	return nil
}

func importLegacySeries(ctx context.Context, tx *sql.Tx, seriesID, deviceID, collectorID, entityID, metric, unit string, labelsJSON []byte, observedAt time.Time, generation int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO metric_series(id, device_id, collector_id, entity_id, metric, unit, labels, first_seen, last_seen, storage_generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9)
		ON CONFLICT (device_id, collector_id, entity_id, metric, unit, labels) DO UPDATE SET
		  first_seen = LEAST(metric_series.first_seen, EXCLUDED.first_seen),
		  last_seen = GREATEST(metric_series.last_seen, EXCLUDED.last_seen),
		  storage_generation = GREATEST(metric_series.storage_generation, EXCLUDED.storage_generation)`,
		seriesID, deviceID, collectorID, entityID, metric, unit, labelsJSON, observedAt.UTC(), generation)
	if err != nil {
		return fmt.Errorf("import metric series %s: %w", seriesID, err)
	}
	return nil
}

func importLegacyObservation(ctx context.Context, tx *sql.Tx, item Observation, generation int64, snapshotAt time.Time) error {
	id := item.ID
	if id == "" {
		id = deterministicLegacyObservationID(item)
	}
	reporterID := item.ReporterID
	if reporterID == "" {
		reporterID = "legacy"
	}
	collectorID := item.CollectorID
	if collectorID == "" {
		collectorID = "legacy"
	}
	subjectID := item.SubjectID
	if subjectID == "" {
		subjectID = "unknown"
	}
	kind := item.Kind
	if kind == "" {
		kind = "legacy"
	}
	payload := item.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode legacy observation %s: %w", id, err)
	}
	observedAt := item.ObservedAt
	if observedAt.IsZero() {
		observedAt = snapshotAt
	}
	receivedAt := item.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = snapshotAt
	}
	expiresAt := item.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = receivedAt.Add(24 * time.Hour)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO observations(id, reporter_id, collector_id, subject_id, kind, payload, observed_at, received_at, expires_at, confidence, storage_generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO NOTHING`, id, reporterID, collectorID, subjectID, kind, payloadJSON, observedAt.UTC(), receivedAt.UTC(), expiresAt.UTC(), item.Confidence, generation)
	if err != nil {
		return fmt.Errorf("import legacy observation %s: %w", id, err)
	}
	return nil
}

func nullableMetricValue(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func deterministicSeriesID(deviceID, collectorID, entityID, metric, unit string, labelsJSON []byte) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{deviceID, collectorID, entityID, metric, unit, string(labelsJSON)}, "\x00")))
	return uuidFromDigest(digest)
}

func deterministicLegacySampleID(item MetricSample, index int64) string {
	encoded, _ := json.Marshal(item)
	digest := sha256.Sum256([]byte(fmt.Sprintf("legacy-sample:%d:%s", index, encoded)))
	return uuidFromDigest(digest)
}

func deterministicLegacyObservationID(item Observation) string {
	encoded, _ := json.Marshal(item)
	digest := sha256.Sum256(append([]byte("legacy-observation:"), encoded...))
	return uuidFromDigest(digest)
}

func uuidFromDigest(digest [32]byte) string {
	raw := digest[:16]
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(raw[0:4]), hex.EncodeToString(raw[4:6]), hex.EncodeToString(raw[6:8]), hex.EncodeToString(raw[8:10]), hex.EncodeToString(raw[10:16]))
}

func findMonitoringCheckpoint(checkpoints []MonitoringMigrationCheckpoint, stream string) (MonitoringMigrationCheckpoint, bool) {
	for _, checkpoint := range checkpoints {
		if checkpoint.Stream == stream {
			return checkpoint, true
		}
	}
	return MonitoringMigrationCheckpoint{}, false
}

func monitoringCheckpointsComplete(checkpoints []MonitoringMigrationCheckpoint) bool {
	if len(checkpoints) != len(monitoringMigrationStreams) {
		return false
	}
	for _, stream := range monitoringMigrationStreams {
		checkpoint, ok := findMonitoringCheckpoint(checkpoints, stream)
		if !ok || !checkpoint.Completed {
			return false
		}
	}
	return true
}

func readMonitoringStorageStatus(ctx context.Context, reader monitoringSQLReader) (MonitoringMigrationStatus, error) {
	row, err := readMonitoringStorageRow(ctx, reader)
	if err != nil {
		return MonitoringMigrationStatus{}, err
	}
	status := MonitoringMigrationStatus{
		StorageGeneration:          row.StorageGeneration,
		MigrationGeneration:        row.MigrationGeneration,
		Phase:                      row.Phase,
		LegacyStateHash:            row.LegacyStateHash,
		LegacySampleCount:          row.LegacySampleCount,
		NormalizedSampleCount:      row.NormalizedSampleCount,
		LegacyReceiptCount:         row.LegacyReceiptCount,
		NormalizedReceiptCount:     row.NormalizedReceiptCount,
		LegacyObservationCount:     row.LegacyObservationCount,
		NormalizedObservationCount: row.NormalizedObservationCount,
		ParityCheckedAt:            row.ParityCheckedAt,
		CutoverAt:                  row.CutoverAt,
		Checkpoints:                []MonitoringMigrationCheckpoint{},
	}
	if row.MigrationGeneration == 0 {
		return status, nil
	}
	rows, err := reader.QueryContext(ctx, `
		SELECT migration_generation, stream, next_index, completed
		FROM monitoring_migration_checkpoints
		WHERE migration_generation = $1 ORDER BY stream`, row.MigrationGeneration)
	if err != nil {
		return MonitoringMigrationStatus{}, fmt.Errorf("read monitoring migration checkpoints: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var checkpoint MonitoringMigrationCheckpoint
		if err := rows.Scan(&checkpoint.Generation, &checkpoint.Stream, &checkpoint.NextIndex, &checkpoint.Completed); err != nil {
			return MonitoringMigrationStatus{}, fmt.Errorf("scan monitoring migration checkpoint: %w", err)
		}
		status.Checkpoints = append(status.Checkpoints, checkpoint)
	}
	if err := rows.Err(); err != nil {
		return MonitoringMigrationStatus{}, fmt.Errorf("iterate monitoring migration checkpoints: %w", err)
	}
	return status, nil
}

func readMonitoringStorageRow(ctx context.Context, reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (monitoringStorageRow, error) {
	var row monitoringStorageRow
	var parityCheckedAt, cutoverAt sql.NullTime
	err := reader.QueryRowContext(ctx, `
		SELECT storage_generation, migration_generation, phase, legacy_state_hash,
		       legacy_sample_count, normalized_sample_count, legacy_receipt_count,
		       normalized_receipt_count, legacy_observation_count,
		       normalized_observation_count, parity_checked_at, cutover_at
		FROM monitoring_storage_state WHERE singleton = true`).Scan(
		&row.StorageGeneration, &row.MigrationGeneration, &row.Phase, &row.LegacyStateHash,
		&row.LegacySampleCount, &row.NormalizedSampleCount, &row.LegacyReceiptCount,
		&row.NormalizedReceiptCount, &row.LegacyObservationCount,
		&row.NormalizedObservationCount, &parityCheckedAt, &cutoverAt)
	if errors.Is(err, sql.ErrNoRows) {
		return monitoringStorageRow{}, fmt.Errorf("monitoring storage state is missing: %w", ErrNotFound)
	}
	if err != nil {
		return monitoringStorageRow{}, fmt.Errorf("read monitoring storage state: %w", err)
	}
	if parityCheckedAt.Valid {
		value := parityCheckedAt.Time.UTC()
		row.ParityCheckedAt = &value
	}
	if cutoverAt.Valid {
		value := cutoverAt.Time.UTC()
		row.CutoverAt = &value
	}
	return row, nil
}

func lockMonitoringMigration(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('scout.monitoring.telemetry'))`); err != nil {
		return fmt.Errorf("lock monitoring migration: %w", err)
	}
	return nil
}

func countGeneration(ctx context.Context, reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, table string, generation int64) (int64, error) {
	if table != "metric_samples" && table != "telemetry_receipts" && table != "observations" {
		return 0, ErrInvalid
	}
	var count int64
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE storage_generation = $1", generation).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s generation %d: %w", table, generation, err)
	}
	return count, nil
}

func monitoringCheckpointsCompleteTx(ctx context.Context, tx *sql.Tx, generation int64) (bool, error) {
	var count, complete int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE completed)
		FROM monitoring_migration_checkpoints WHERE migration_generation = $1`, generation).Scan(&count, &complete); err != nil {
		return false, fmt.Errorf("check monitoring migration checkpoints: %w", err)
	}
	return count == int64(len(monitoringMigrationStreams)) && complete == count, nil
}
