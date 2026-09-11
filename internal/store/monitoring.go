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

const telemetryStorageSizeSQL = `
SELECT COALESCE(pg_total_relation_size('public.metric_samples'::regclass), 0)
     + COALESCE((
           SELECT SUM(pg_total_relation_size(child.oid))
           FROM pg_inherits
           JOIN pg_class AS child ON child.oid = pg_inherits.inhrelid
           WHERE pg_inherits.inhparent = 'public.metric_samples'::regclass
         ), 0)
    + COALESCE(pg_total_relation_size('public.metric_series'::regclass), 0)
     + COALESCE(pg_total_relation_size('public.current_series'::regclass), 0)
     + COALESCE(pg_total_relation_size('public.telemetry_receipts'::regclass), 0)
     + COALESCE(pg_total_relation_size('public.telemetry_sample_ordinals'::regclass), 0)
     + COALESCE(pg_total_relation_size('public.observations'::regclass), 0)
     + COALESCE(pg_total_relation_size('public.rollup_work'::regclass), 0)
     + COALESCE(pg_total_relation_size('public.metric_aggregates'::regclass), 0)`

// monitoringPhase returns the SQL authority boundary. Before migration v2 is
// installed, SQL stores retain the 001 snapshot behavior so an upgrade can
// apply migrations before serving requests.
func (s *Store) monitoringPhase(ctx context.Context) (MonitoringStoragePhase, error) {
	if s.db == nil {
		return MonitoringStorageLegacy, nil
	}
	var table sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT to_regclass('public.monitoring_storage_state')`).Scan(&table); err != nil {
		return MonitoringStorageLegacy, fmt.Errorf("check monitoring storage schema: %w", err)
	}
	if !table.Valid || table.String == "" {
		return MonitoringStorageLegacy, nil
	}
	var phase string
	if err := s.db.QueryRowContext(ctx, `SELECT phase FROM monitoring_storage_state WHERE singleton = true`).Scan(&phase); errors.Is(err, sql.ErrNoRows) {
		return MonitoringStorageLegacy, nil
	} else if err != nil {
		return MonitoringStorageLegacy, fmt.Errorf("read monitoring storage phase: %w", err)
	}
	return MonitoringStoragePhase(phase), nil
}

func (s *Store) stripMigratedTelemetryOnLegacyWrite(ctx context.Context) (bool, error) {
	phase, err := s.monitoringPhase(ctx)
	if err != nil {
		return false, err
	}
	return phase == MonitoringStorageAuthoritative, nil
}

func stripMigratedTelemetry(state *State) {
	state.Samples = []MetricSample{}
	state.BatchReceipts = map[string]string{}
	state.Observations = map[string]Observation{}
}

func telemetryStorageBytes(ctx context.Context, reader monitoringSQLReader) (int64, error) {
	var bytes int64
	if err := reader.QueryRowContext(ctx, telemetryStorageSizeSQL).Scan(&bytes); err != nil {
		return 0, fmt.Errorf("measure SQL telemetry storage: %w", err)
	}
	return bytes, nil
}

func telemetryAdmissionEstimate(ctx context.Context, reader monitoringSQLReader, generation, usedBytes int64, sampleCount, observationCount int) (int64, error) {
	var existingSamples int64
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE storage_generation = $1`, generation).Scan(&existingSamples); err != nil {
		return 0, fmt.Errorf("count SQL telemetry samples for budget estimate: %w", err)
	}
	perSample := int64(1024)
	if existingSamples > 0 && usedBytes/existingSamples > perSample {
		perSample = usedBytes / existingSamples
	}
	perObservation := perSample * 2
	return 4096 + int64(sampleCount)*perSample + int64(observationCount)*perObservation, nil
}

func telemetryBudgetHasRoom(usedBytes, budgetBytes, estimate int64) bool {
	if budgetBytes <= 0 {
		return true
	}
	if usedBytes >= budgetBytes || estimate < 0 || usedBytes > budgetBytes-estimate {
		return false
	}
	return true
}

func telemetryBudgetBelowReleaseThreshold(usedBytes, budgetBytes int64) bool {
	if budgetBytes <= 0 {
		return true
	}
	return usedBytes < budgetBytes-(budgetBytes/10)
}

func readWorkspaceStateTx(ctx context.Context, tx *sql.Tx) (State, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT state_json FROM workspace_state WHERE singleton = true FOR UPDATE`).Scan(&raw); err != nil {
		return State{}, fmt.Errorf("read workspace state for telemetry transaction: %w", err)
	}
	state := newState()
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &state); err != nil {
			return State{}, fmt.Errorf("decode workspace state for telemetry transaction: %w", err)
		}
	}
	ensureStateMaps(&state)
	return state, nil
}

func writeWorkspaceStateTx(ctx context.Context, tx *sql.Tx, state State) error {
	ensureStateMaps(&state)
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode workspace state for telemetry transaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_state
		SET recovery_mode = $1, enrollment_paused = $2, updates_paused = $3,
		    schema_version = $4, state_json = $5, updated_at = now()
		WHERE singleton = true`, state.Workspace.RecoveryMode, state.Workspace.EnrollmentPaused, state.Workspace.UpdatesPaused, state.Workspace.SchemaVersion, raw); err != nil {
		return fmt.Errorf("persist workspace state for telemetry transaction: %w", err)
	}
	return nil
}

func (s *Store) invalidateLegacyCache() {
	s.mu.Lock()
	s.loaded = false
	s.mu.Unlock()
}

func (s *Store) ingestBatchSQL(ctx context.Context, agentID, bootID, batchID, payloadHash string, samples []MetricSample, observations []Observation, dropped int) (BatchResult, error) {
	if agentID == "" || bootID == "" || batchID == "" || payloadHash == "" || dropped < 0 {
		return BatchResult{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BatchResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return BatchResult{}, err
	}
	storage, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return BatchResult{}, err
	}
	if storage.Phase != MonitoringStorageAuthoritative {
		if storage.Phase == MonitoringStorageImporting {
			return BatchResult{}, fmt.Errorf("telemetry ingestion is paused during migration: %w", ErrConflict)
		}
		return BatchResult{}, fmt.Errorf("SQL telemetry storage is not authoritative: %w", ErrConflict)
	}
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return BatchResult{}, err
	}
	agent, device, err := ensureSQLTelemetryReferences(ctx, tx, state, agentID)
	if err != nil {
		return BatchResult{}, err
	}
	if agent.RevokedAt != nil {
		return BatchResult{}, ErrRevoked
	}
	if device.DecommissionedAt != nil || device.Lifecycle == "decommissioned" {
		return BatchResult{}, ErrRevoked
	}
	if state.Workspace.TelemetryBackpressure {
		return BatchResult{}, ErrBackpressure
	}

	var oldHash string
	var acceptedAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT payload_hash, accepted_at
		FROM telemetry_receipts
		WHERE agent_id = $1 AND boot_id = $2 AND batch_id = $3`, agentID, bootID, batchID).Scan(&oldHash, &acceptedAt)
	if err == nil {
		if oldHash != payloadHash {
			return BatchResult{}, ErrConflict
		}
		stripMigratedTelemetry(&state)
		if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
			return BatchResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return BatchResult{}, fmt.Errorf("commit duplicate telemetry receipt: %w", err)
		}
		s.invalidateLegacyCache()
		return BatchResult{AcceptedAt: acceptedAt.UTC(), Duplicate: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BatchResult{}, fmt.Errorf("read telemetry receipt: %w", err)
	}

	if state.Workspace.MaxSamples > 0 {
		var sampleCount int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE storage_generation = $1`, storage.StorageGeneration).Scan(&sampleCount); err != nil {
			return BatchResult{}, fmt.Errorf("count telemetry samples for backpressure: %w", err)
		}
		if sampleCount+int64(len(samples)) > int64(state.Workspace.MaxSamples) {
			state.Workspace.TelemetryBackpressure = true
			stripMigratedTelemetry(&state)
			if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
				return BatchResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return BatchResult{}, fmt.Errorf("commit telemetry backpressure: %w", err)
			}
			s.invalidateLegacyCache()
			return BatchResult{}, ErrBackpressure
		}
	}
	usedBytes, err := telemetryStorageBytes(ctx, tx)
	if err != nil {
		return BatchResult{}, err
	}
	estimate, err := telemetryAdmissionEstimate(ctx, tx, storage.StorageGeneration, usedBytes, len(samples), len(observations))
	if err != nil {
		return BatchResult{}, err
	}
	if !telemetryBudgetHasRoom(usedBytes, state.Workspace.TelemetryBudgetBytes, estimate) {
		state.Workspace.TelemetryBackpressure = true
		stripMigratedTelemetry(&state)
		if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
			return BatchResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return BatchResult{}, fmt.Errorf("commit telemetry disk backpressure: %w", err)
		}
		s.invalidateLegacyCache()
		return BatchResult{}, ErrBackpressure
	}

	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	for index, item := range samples {
		item.DeviceID = device.ID
		item.AgentID = agentID
		item.ID = NewID()
		if item.CollectorID == "" {
			item.CollectorID = "legacy"
		}
		if item.EntityID == "" {
			item.EntityID = "host"
		}
		if item.Metric == "" {
			return BatchResult{}, fmt.Errorf("telemetry sample %d has no metric: %w", index, ErrInvalid)
		}
		if item.Unit == "" {
			item.Unit = "unknown"
		}
		if item.Availability == "" {
			item.Availability = FreshnessUnavailable
		}
		if item.ObservedAt.IsZero() {
			item.ObservedAt = now
		}
		if item.ReceivedAt.IsZero() {
			item.ReceivedAt = now
		}
		labels := item.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		labelsJSON, err := json.Marshal(labels)
		if err != nil {
			return BatchResult{}, fmt.Errorf("encode telemetry sample labels: %w", err)
		}
		seriesID := deterministicSeriesID(item.DeviceID, item.CollectorID, item.EntityID, item.Metric, item.Unit, labelsJSON)
		if err := upsertMetricSeriesSQL(ctx, tx, seriesID, item, labelsJSON, storage.StorageGeneration); err != nil {
			return BatchResult{}, err
		}
		ordinalResult, err := tx.ExecContext(ctx, `
			INSERT INTO telemetry_sample_ordinals(agent_id, boot_id, batch_id, batch_ordinal, sample_id, received_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (agent_id, boot_id, batch_id, batch_ordinal) DO NOTHING`, agentID, bootID, batchID, index, item.ID, item.ReceivedAt.UTC())
		if err != nil {
			return BatchResult{}, fmt.Errorf("record telemetry sample ordinal %d: %w", index, err)
		}
		ordinalRows, err := ordinalResult.RowsAffected()
		if err != nil || ordinalRows != 1 {
			return BatchResult{}, fmt.Errorf("telemetry sample ordinal %d was already accepted: %w", index, ErrConflict)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO metric_samples(id, device_id, agent_id, collector_id, entity_id, metric, labels, value, availability, unit, observed_at, received_at, series_id, boot_id, batch_id, batch_ordinal, storage_generation)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
			ON CONFLICT (id, received_at) DO NOTHING`, item.ID, item.DeviceID, item.AgentID, item.CollectorID, item.EntityID, item.Metric, labelsJSON, nullableMetricValue(item.Value), string(item.Availability), item.Unit, item.ObservedAt.UTC(), item.ReceivedAt.UTC(), seriesID, bootID, batchID, index, storage.StorageGeneration)
		if err != nil {
			return BatchResult{}, fmt.Errorf("store telemetry sample %d: %w", index, err)
		}
		insertedRows, err := result.RowsAffected()
		if err != nil || insertedRows != 1 {
			return BatchResult{}, fmt.Errorf("telemetry sample %d already exists: %w", index, ErrConflict)
		}
		if err := upsertCurrentSeriesSQL(ctx, tx, item, seriesID, storage.StorageGeneration); err != nil {
			return BatchResult{}, err
		}
		if err := markRollupWorkSQL(ctx, tx, seriesID, item.ObservedAt, storage.StorageGeneration); err != nil {
			return BatchResult{}, err
		}
	}
	for index, item := range observations {
		item.ID = NewID()
		item.ReporterID = agentID
		if item.CollectorID == "" {
			item.CollectorID = "legacy"
		}
		if item.SubjectID == "" {
			item.SubjectID = device.ID
		}
		if item.Kind == "" {
			item.Kind = "telemetry"
		}
		if item.ObservedAt.IsZero() {
			item.ObservedAt = now
		}
		if item.ReceivedAt.IsZero() {
			item.ReceivedAt = now
		}
		if item.ExpiresAt.IsZero() {
			item.ExpiresAt = item.ReceivedAt.Add(24 * time.Hour)
		}
		payload := item.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return BatchResult{}, fmt.Errorf("encode telemetry observation %d: %w", index, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO observations(id, reporter_id, collector_id, subject_id, kind, payload, observed_at, received_at, expires_at, confidence, storage_generation)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO NOTHING`, item.ID, item.ReporterID, item.CollectorID, item.SubjectID, item.Kind, payloadJSON, item.ObservedAt.UTC(), item.ReceivedAt.UTC(), item.ExpiresAt.UTC(), item.Confidence, storage.StorageGeneration); err != nil {
			return BatchResult{}, fmt.Errorf("store telemetry observation %d: %w", index, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_receipts(agent_id, boot_id, batch_id, payload_hash, accepted_at, storage_generation)
		VALUES ($1, $2, $3, $4, $5, $6)`, agentID, bootID, batchID, payloadHash, now, storage.StorageGeneration); err != nil {
		return BatchResult{}, fmt.Errorf("store telemetry receipt: %w", err)
	}
	if dropped > 0 {
		state.Workspace.DroppedSamples += int64(dropped)
	}
	if current, ok := state.Devices[device.ID]; ok {
		current.AgentVersion = agent.InstalledVersion
		state.Devices[device.ID] = current
	}
	stripMigratedTelemetry(&state)
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return BatchResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return BatchResult{}, fmt.Errorf("commit telemetry batch: %w", err)
	}
	s.invalidateLegacyCache()
	return BatchResult{AcceptedAt: now}, nil
}

func ensureSQLTelemetryReferences(ctx context.Context, tx *sql.Tx, state State, agentID string) (AgentIdentity, Device, error) {
	agent, agentInState := state.Agents[agentID]
	if !agentInState {
		var err error
		agent, err = queryAgentSQL(ctx, tx, agentID)
		if errors.Is(err, sql.ErrNoRows) {
			return AgentIdentity{}, Device{}, ErrUnauthorized
		}
		if err != nil {
			return AgentIdentity{}, Device{}, fmt.Errorf("read SQL agent %s: %w", agentID, err)
		}
	}
	if agent.DeviceID == "" {
		return AgentIdentity{}, Device{}, ErrInvalid
	}
	device, deviceInState := state.Devices[agent.DeviceID]
	if !deviceInState {
		var err error
		device, err = queryDeviceSQL(ctx, tx, agent.DeviceID)
		if errors.Is(err, sql.ErrNoRows) {
			return AgentIdentity{}, Device{}, ErrNotFound
		}
		if err != nil {
			return AgentIdentity{}, Device{}, fmt.Errorf("read SQL device %s: %w", agent.DeviceID, err)
		}
	}
	if deviceInState {
		if err := upsertDeviceSQL(ctx, tx, device); err != nil {
			return AgentIdentity{}, Device{}, err
		}
	}
	if agentInState {
		if err := upsertAgentSQL(ctx, tx, agent); err != nil {
			return AgentIdentity{}, Device{}, err
		}
	}
	return agent, device, nil
}

func queryAgentSQL(ctx context.Context, reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (AgentIdentity, error) {
	var item AgentIdentity
	var token sql.NullString
	var revoked sql.NullTime
	err := reader.QueryRowContext(ctx, `
		SELECT id, device_id, public_key_hash, cert_serial, auth_token_hash,
		       certificate_pem, expires_at, revoked_at, installed_version
		FROM agent_identities WHERE id = $1`, id).Scan(&item.ID, &item.DeviceID, &item.PublicKeyHash, &item.CertSerial, &token, &item.CertificatePEM, &item.ExpiresAt, &revoked, &item.InstalledVersion)
	if err != nil {
		return AgentIdentity{}, err
	}
	if token.Valid {
		item.AuthTokenHash = token.String
	}
	if revoked.Valid {
		value := revoked.Time.UTC()
		item.RevokedAt = &value
	}
	return item, nil
}

func queryDeviceSQL(ctx context.Context, reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (Device, error) {
	var item Device
	var siteID sql.NullString
	var decommissioned sql.NullTime
	err := reader.QueryRowContext(ctx, `
		SELECT id, site_id, display_name, platform, architecture, lifecycle,
		       created_at, decommissioned_at
		FROM devices WHERE id = $1`, id).Scan(&item.ID, &siteID, &item.DisplayName, &item.Platform, &item.Architecture, &item.Lifecycle, &item.CreatedAt, &decommissioned)
	if err != nil {
		return Device{}, err
	}
	if siteID.Valid {
		item.SiteID = siteID.String
	}
	if decommissioned.Valid {
		value := decommissioned.Time.UTC()
		item.DecommissionedAt = &value
	}
	return item, nil
}

func upsertDeviceSQL(ctx context.Context, tx *sql.Tx, item Device) error {
	if item.ID == "" {
		return ErrInvalid
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
	_, err := tx.ExecContext(ctx, `
		INSERT INTO devices(id, site_id, display_name, platform, architecture, lifecycle, created_at, decommissioned_at)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET site_id = EXCLUDED.site_id,
		  display_name = EXCLUDED.display_name, platform = EXCLUDED.platform,
		  architecture = EXCLUDED.architecture, lifecycle = EXCLUDED.lifecycle,
		  created_at = EXCLUDED.created_at, decommissioned_at = EXCLUDED.decommissioned_at`, item.ID, item.SiteID, item.DisplayName, item.Platform, item.Architecture, item.Lifecycle, item.CreatedAt.UTC(), item.DecommissionedAt)
	if err != nil {
		return fmt.Errorf("ensure SQL device %s: %w", item.ID, err)
	}
	return nil
}

func upsertAgentSQL(ctx context.Context, tx *sql.Tx, item AgentIdentity) error {
	if item.ID == "" || item.DeviceID == "" {
		return ErrInvalid
	}
	if item.CertSerial == "" {
		item.CertSerial = "legacy-" + item.ID
	}
	var token any
	if item.AuthTokenHash != "" {
		token = item.AuthTokenHash
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO agent_identities(id, device_id, public_key_hash, cert_serial, auth_token_hash, certificate_pem, expires_at, revoked_at, installed_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET device_id = EXCLUDED.device_id,
		  public_key_hash = EXCLUDED.public_key_hash, cert_serial = EXCLUDED.cert_serial,
		  auth_token_hash = EXCLUDED.auth_token_hash, certificate_pem = EXCLUDED.certificate_pem,
		  expires_at = EXCLUDED.expires_at, revoked_at = EXCLUDED.revoked_at,
		  installed_version = EXCLUDED.installed_version`, item.ID, item.DeviceID, item.PublicKeyHash, item.CertSerial, token, item.CertificatePEM, item.ExpiresAt.UTC(), item.RevokedAt, item.InstalledVersion)
	if err != nil {
		return fmt.Errorf("ensure SQL agent %s: %w", item.ID, err)
	}
	return nil
}

func upsertMetricSeriesSQL(ctx context.Context, tx *sql.Tx, seriesID string, item MetricSample, labelsJSON []byte, generation int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO metric_series(id, device_id, collector_id, entity_id, metric, unit, labels, first_seen, last_seen, storage_generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9)
		ON CONFLICT (device_id, collector_id, entity_id, metric, unit, labels) DO UPDATE SET
		  first_seen = LEAST(metric_series.first_seen, EXCLUDED.first_seen),
		  last_seen = GREATEST(metric_series.last_seen, EXCLUDED.last_seen),
		  storage_generation = GREATEST(metric_series.storage_generation, EXCLUDED.storage_generation)`, seriesID, item.DeviceID, item.CollectorID, item.EntityID, item.Metric, item.Unit, labelsJSON, item.ObservedAt.UTC(), generation)
	if err != nil {
		return fmt.Errorf("upsert metric series %s: %w", seriesID, err)
	}
	return nil
}

func upsertCurrentSeriesSQL(ctx context.Context, tx *sql.Tx, item MetricSample, seriesID string, generation int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO current_series(series_id, sample_id, observed_at, received_at, value, availability, storage_generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (series_id) DO UPDATE SET sample_id = EXCLUDED.sample_id,
		  observed_at = EXCLUDED.observed_at, received_at = EXCLUDED.received_at,
		  value = EXCLUDED.value, availability = EXCLUDED.availability,
		  storage_generation = EXCLUDED.storage_generation
		WHERE EXCLUDED.observed_at > current_series.observed_at
		   OR (EXCLUDED.observed_at = current_series.observed_at AND EXCLUDED.received_at > current_series.received_at)`, seriesID, item.ID, item.ObservedAt.UTC(), item.ReceivedAt.UTC(), nullableMetricValue(item.Value), string(item.Availability), generation)
	if err != nil {
		return fmt.Errorf("upsert current series %s: %w", seriesID, err)
	}
	return nil
}

func markRollupWorkSQL(ctx context.Context, tx *sql.Tx, seriesID string, observedAt time.Time, generation int64) error {
	for _, resolution := range []int{300, 3600} {
		bucketStart := observedAt.UTC().Truncate(time.Duration(resolution) * time.Second)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO rollup_work(series_id, resolution_seconds, bucket_start, dirty_generation)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (series_id, resolution_seconds, bucket_start) DO UPDATE SET
			  dirty_generation = rollup_work.dirty_generation + 1,
			  lease_until = NULL, last_error = NULL`, seriesID, resolution, bucketStart, generation); err != nil {
			return fmt.Errorf("mark %d-second rollup work for %s: %w", resolution, seriesID, err)
		}
	}
	return nil
}

type sqlMetricRow struct {
	seriesID     string
	entityID     string
	metric       string
	unit         string
	value        *float64
	availability Freshness
	observedAt   time.Time
	receivedAt   time.Time
}

func (s *Store) queryMetricsSQL(ctx context.Context, query MetricQuery) ([]MetricSeries, error) {
	if query.MaxPoints <= 0 {
		query.MaxPoints = 600
	}
	if query.MaxPoints > 600 {
		return nil, ErrInvalid
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM devices WHERE id = $1)`, query.DeviceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check metric device: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	conditions := []string{
		"ms.device_id = $1",
		"ms.storage_generation = storage.storage_generation",
	}
	args := []any{query.DeviceID}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if !query.From.IsZero() {
		add("ms.observed_at >= $%d", query.From.UTC())
	}
	if !query.To.IsZero() {
		add("ms.observed_at <= $%d", query.To.UTC())
	}
	if query.Metric != "" {
		add("ms.metric = $%d", query.Metric)
	}
	if query.EntityID != "" {
		add("ms.entity_id = $%d", query.EntityID)
	}
	statement := `
		SELECT COALESCE(ms.series_id, ''), ms.entity_id, ms.metric, ms.unit,
		       ms.value, ms.availability, ms.observed_at, ms.received_at
		FROM metric_samples AS ms
		JOIN monitoring_storage_state AS storage ON storage.singleton = true
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY COALESCE(ms.series_id, ''), ms.observed_at, ms.received_at, ms.id`
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query SQL metrics: %w", err)
	}
	defer rows.Close()
	grouped := map[string][]sqlMetricRow{}
	metadata := map[string]MetricSeries{}
	for rows.Next() {
		var row sqlMetricRow
		var value sql.NullFloat64
		if err := rows.Scan(&row.seriesID, &row.entityID, &row.metric, &row.unit, &value, &row.availability, &row.observedAt, &row.receivedAt); err != nil {
			return nil, fmt.Errorf("scan SQL metric: %w", err)
		}
		if value.Valid {
			v := value.Float64
			row.value = &v
		}
		if row.seriesID == "" {
			row.seriesID = deterministicSeriesID(query.DeviceID, "legacy", row.entityID, row.metric, row.unit, []byte("{}"))
		}
		grouped[row.seriesID] = append(grouped[row.seriesID], row)
		metadata[row.seriesID] = MetricSeries{SeriesID: row.seriesID, EntityID: row.entityID, Metric: row.metric, Unit: row.unit}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQL metrics: %w", err)
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := metadata[keys[i]], metadata[keys[j]]
		if left.Metric != right.Metric {
			return left.Metric < right.Metric
		}
		if left.EntityID != right.EntityID {
			return left.EntityID < right.EntityID
		}
		return keys[i] < keys[j]
	})
	result := make([]MetricSeries, 0, len(keys))
	for _, key := range keys {
		series := metadata[key]
		series.Points = sqlMetricPoints(grouped[key], query.MaxPoints)
		result = append(result, series)
	}
	return result, nil
}

func sqlMetricPoints(items []sqlMetricRow, maxPoints int) []MetricPoint {
	points := make([]MetricPoint, 0, minInt(len(items), maxPoints))
	if len(items) <= maxPoints {
		for _, item := range items {
			points = append(points, MetricPoint{ObservedAt: item.observedAt, Value: item.value, Availability: item.availability})
		}
		return points
	}
	step := (len(items) + maxPoints - 1) / maxPoints
	for start := 0; start < len(items); start += step {
		end := start + step
		if end > len(items) {
			end = len(items)
		}
		var minValue, maxValue *float64
		var last *float64
		for index := start; index < end; index++ {
			item := items[index]
			if item.availability != FreshnessCurrent || item.value == nil {
				continue
			}
			if minValue == nil || *item.value < *minValue {
				value := *item.value
				minValue = &value
			}
			if maxValue == nil || *item.value > *maxValue {
				value := *item.value
				maxValue = &value
			}
			value := *item.value
			last = &value
		}
		point := MetricPoint{ObservedAt: items[start].observedAt, Min: minValue, Max: maxValue, Availability: FreshnessCurrent}
		if last != nil {
			point.Value = last
		}
		if minValue == nil {
			point.Availability = items[start].availability
		}
		points = append(points, point)
	}
	return points
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (s *Store) telemetryStatusSQL(ctx context.Context) (TelemetryStatus, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT state_json FROM workspace_state WHERE singleton = true`).Scan(&raw); err != nil {
		return TelemetryStatus{}, fmt.Errorf("read workspace telemetry status: %w", err)
	}
	state := newState()
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &state); err != nil {
			return TelemetryStatus{}, fmt.Errorf("decode workspace telemetry status: %w", err)
		}
	}
	ensureStateMaps(&state)
	var count int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM metric_samples AS samples
		JOIN monitoring_storage_state AS storage
		  ON storage.singleton = true AND samples.storage_generation = storage.storage_generation`).Scan(&count); err != nil {
		return TelemetryStatus{}, fmt.Errorf("count SQL telemetry samples: %w", err)
	}
	usedBytes, err := telemetryStorageBytes(ctx, s.db)
	if err != nil {
		return TelemetryStatus{}, err
	}
	return TelemetryStatus{Samples: int(count), DroppedSamples: state.Workspace.DroppedSamples, MaxSamples: state.Workspace.MaxSamples, UsedBytes: usedBytes, BudgetBytes: state.Workspace.TelemetryBudgetBytes, Backpressure: state.Workspace.TelemetryBackpressure, RetentionHours: state.Workspace.RetentionHours, LastRetentionAt: cloneTime(state.Workspace.LastRetentionAt)}, nil
}

func (s *Store) pruneSamplesSQL(ctx context.Context, before time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return 0, err
	}
	storage, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return 0, err
	}
	if storage.Phase != MonitoringStorageAuthoritative {
		return 0, fmt.Errorf("SQL telemetry storage is not authoritative: %w", ErrConflict)
	}
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	seriesIDs, err := affectedSeriesSQL(ctx, tx, `received_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM current_series WHERE sample_id IN (SELECT id FROM metric_samples WHERE received_at < $1)`, before.UTC()); err != nil {
		return 0, fmt.Errorf("remove pruned current series: %w", err)
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM metric_samples WHERE received_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("prune SQL samples: %w", err)
	}
	removed, err := deleted.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned SQL samples: %w", err)
	}
	for _, seriesID := range seriesIDs {
		if err := rebuildCurrentSeriesSQL(ctx, tx, seriesID, storage.StorageGeneration); err != nil {
			return 0, err
		}
	}
	stripMigratedTelemetry(&state)
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit SQL sample pruning: %w", err)
	}
	s.invalidateLegacyCache()
	return int(removed), nil
}

func (s *Store) enforceSampleLimitSQL(ctx context.Context, maxSamples int) (int, error) {
	if maxSamples < 1 {
		return 0, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return 0, err
	}
	storage, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return 0, err
	}
	if storage.Phase != MonitoringStorageAuthoritative {
		return 0, fmt.Errorf("SQL telemetry storage is not authoritative: %w", ErrConflict)
	}
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count SQL samples: %w", err)
	}
	if count <= int64(maxSamples) {
		return 0, nil
	}
	victims := count - int64(maxSamples)
	seriesIDs, err := affectedSeriesLimitedSQL(ctx, tx, victims)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM current_series
		WHERE sample_id IN (SELECT id FROM metric_samples ORDER BY received_at ASC, id ASC LIMIT $1)`, victims); err != nil {
		return 0, fmt.Errorf("remove limited current series: %w", err)
	}
	deleted, err := tx.ExecContext(ctx, `
		DELETE FROM metric_samples
		WHERE (id, received_at) IN (SELECT id, received_at FROM metric_samples ORDER BY received_at ASC, id ASC LIMIT $1)`, victims)
	if err != nil {
		return 0, fmt.Errorf("limit SQL samples: %w", err)
	}
	removed, err := deleted.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count limited SQL samples: %w", err)
	}
	for _, seriesID := range seriesIDs {
		if err := rebuildCurrentSeriesSQL(ctx, tx, seriesID, storage.StorageGeneration); err != nil {
			return 0, err
		}
	}
	state.Workspace.DroppedSamples += removed
	stripMigratedTelemetry(&state)
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit SQL sample limit: %w", err)
	}
	s.invalidateLegacyCache()
	return int(removed), nil
}

func affectedSeriesSQL(ctx context.Context, tx *sql.Tx, predicate string, arguments ...any) ([]string, error) {
	query := `SELECT DISTINCT series_id FROM metric_samples WHERE series_id IS NOT NULL AND ` + predicate
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("find affected telemetry series: %w", err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var seriesID string
		if err := rows.Scan(&seriesID); err != nil {
			return nil, fmt.Errorf("scan affected telemetry series: %w", err)
		}
		result = append(result, seriesID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate affected telemetry series: %w", err)
	}
	sort.Strings(result)
	return result, nil
}

func affectedSeriesLimitedSQL(ctx context.Context, tx *sql.Tx, limit int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT series_id
		FROM (
			SELECT series_id FROM metric_samples
			ORDER BY received_at ASC, id ASC LIMIT $1
		) AS victims
		WHERE series_id IS NOT NULL`, limit)
	if err != nil {
		return nil, fmt.Errorf("find limited telemetry series: %w", err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var seriesID string
		if err := rows.Scan(&seriesID); err != nil {
			return nil, fmt.Errorf("scan limited telemetry series: %w", err)
		}
		result = append(result, seriesID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate limited telemetry series: %w", err)
	}
	sort.Strings(result)
	return result, nil
}

func rebuildCurrentSeriesSQL(ctx context.Context, tx *sql.Tx, seriesID string, generation int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM current_series WHERE series_id = $1`, seriesID); err != nil {
		return fmt.Errorf("clear current series %s: %w", seriesID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO current_series(series_id, sample_id, observed_at, received_at, value, availability, storage_generation)
		SELECT series_id, id, observed_at, received_at, value, availability, storage_generation
		FROM metric_samples
		WHERE series_id = $1 AND storage_generation = $2
		ORDER BY observed_at DESC, received_at DESC, id DESC
		LIMIT 1`, seriesID, generation); err != nil {
		return fmt.Errorf("rebuild current series %s: %w", seriesID, err)
	}
	return nil
}

func (s *Store) cleanupTelemetrySQL(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return 0, err
	}
	storage, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return 0, err
	}
	if storage.Phase != MonitoringStorageAuthoritative {
		return 0, fmt.Errorf("SQL telemetry storage is not authoritative: %w", ErrConflict)
	}
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	seriesIDs, err := affectedSeriesSQL(ctx, tx, `NOT EXISTS (SELECT 1 FROM devices d WHERE d.id = metric_samples.device_id) OR NOT EXISTS (SELECT 1 FROM agent_identities a WHERE a.id = metric_samples.agent_id)`)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM current_series
		WHERE sample_id IN (
			SELECT id FROM metric_samples
			WHERE NOT EXISTS (SELECT 1 FROM devices d WHERE d.id = metric_samples.device_id)
			   OR NOT EXISTS (SELECT 1 FROM agent_identities a WHERE a.id = metric_samples.agent_id)
		)`); err != nil {
		return 0, fmt.Errorf("remove orphaned current series: %w", err)
	}
	deleted, err := tx.ExecContext(ctx, `
		DELETE FROM metric_samples
		WHERE NOT EXISTS (SELECT 1 FROM devices d WHERE d.id = metric_samples.device_id)
		   OR NOT EXISTS (SELECT 1 FROM agent_identities a WHERE a.id = metric_samples.agent_id)`)
	if err != nil {
		return 0, fmt.Errorf("remove orphaned SQL samples: %w", err)
	}
	removed, err := deleted.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count orphaned SQL samples: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM observations WHERE expires_at < $1`, now.UTC()); err != nil {
		return 0, fmt.Errorf("expire SQL observations: %w", err)
	}
	for _, seriesID := range seriesIDs {
		if err := rebuildCurrentSeriesSQL(ctx, tx, seriesID, storage.StorageGeneration); err != nil {
			return 0, err
		}
	}
	var sampleCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE storage_generation = $1`, storage.StorageGeneration).Scan(&sampleCount); err != nil {
		return 0, fmt.Errorf("count SQL samples after cleanup: %w", err)
	}
	usedBytes, err := telemetryStorageBytes(ctx, tx)
	if err != nil {
		return 0, err
	}
	underSampleThreshold := state.Workspace.MaxSamples <= 0 || sampleCount < int64(state.Workspace.MaxSamples*9/10)
	underByteThreshold := telemetryBudgetBelowReleaseThreshold(usedBytes, state.Workspace.TelemetryBudgetBytes)
	if state.Workspace.TelemetryBackpressure && underSampleThreshold && underByteThreshold {
		state.Workspace.TelemetryBackpressure = false
	}
	stripMigratedTelemetry(&state)
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit SQL telemetry cleanup: %w", err)
	}
	s.invalidateLegacyCache()
	return int(removed), nil
}

func (s *Store) setTelemetryBackpressureSQL(ctx context.Context, enabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return err
	}
	storage, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return err
	}
	if storage.Phase != MonitoringStorageAuthoritative {
		return fmt.Errorf("SQL telemetry storage is not authoritative: %w", ErrConflict)
	}
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return err
	}
	state.Workspace.TelemetryBackpressure = enabled
	stripMigratedTelemetry(&state)
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQL telemetry backpressure: %w", err)
	}
	s.invalidateLegacyCache()
	return nil
}

func (s *Store) storeObservationSQL(ctx context.Context, observation Observation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return err
	}
	storage, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return err
	}
	if storage.Phase != MonitoringStorageAuthoritative {
		return fmt.Errorf("SQL telemetry storage is not authoritative: %w", ErrConflict)
	}
	if observation.ID == "" {
		observation.ID = NewID()
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = now
	}
	if observation.ReceivedAt.IsZero() {
		observation.ReceivedAt = now
	}
	if observation.ExpiresAt.IsZero() {
		observation.ExpiresAt = observation.ReceivedAt.Add(24 * time.Hour)
	}
	if observation.Payload == nil {
		observation.Payload = map[string]any{}
	}
	payload, err := json.Marshal(observation.Payload)
	if err != nil {
		return fmt.Errorf("encode SQL observation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO observations(id, reporter_id, collector_id, subject_id, kind, payload, observed_at, received_at, expires_at, confidence, storage_generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET reporter_id = EXCLUDED.reporter_id,
		  collector_id = EXCLUDED.collector_id, subject_id = EXCLUDED.subject_id,
		  kind = EXCLUDED.kind, payload = EXCLUDED.payload,
		  observed_at = EXCLUDED.observed_at, received_at = EXCLUDED.received_at,
		  expires_at = EXCLUDED.expires_at, confidence = EXCLUDED.confidence,
		  storage_generation = EXCLUDED.storage_generation`, observation.ID, observation.ReporterID, observation.CollectorID, observation.SubjectID, observation.Kind, payload, observation.ObservedAt.UTC(), observation.ReceivedAt.UTC(), observation.ExpiresAt.UTC(), observation.Confidence, storage.StorageGeneration); err != nil {
		return fmt.Errorf("store SQL observation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQL observation: %w", err)
	}
	return nil
}

func (s *Store) listObservationsSQL(ctx context.Context) ([]Observation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT observations.id, observations.reporter_id, observations.collector_id,
		       observations.subject_id, observations.kind, observations.payload,
		       observations.observed_at, observations.received_at,
		       observations.expires_at, observations.confidence
		FROM observations
		JOIN monitoring_storage_state AS storage
		  ON storage.singleton = true AND observations.storage_generation = storage.storage_generation
		ORDER BY observations.observed_at DESC, observations.received_at DESC, observations.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query SQL observations: %w", err)
	}
	defer rows.Close()
	result := []Observation{}
	for rows.Next() {
		var item Observation
		var payload []byte
		if err := rows.Scan(&item.ID, &item.ReporterID, &item.CollectorID, &item.SubjectID, &item.Kind, &payload, &item.ObservedAt, &item.ReceivedAt, &item.ExpiresAt, &item.Confidence); err != nil {
			return nil, fmt.Errorf("scan SQL observation: %w", err)
		}
		item.Payload = map[string]any{}
		if len(payload) > 0 && string(payload) != "null" {
			if err := json.Unmarshal(payload, &item.Payload); err != nil {
				return nil, fmt.Errorf("decode SQL observation %s: %w", item.ID, err)
			}
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQL observations: %w", err)
	}
	return result, nil
}

func (s *Store) populateCurrentMetricsSQL(ctx context.Context, devices []Device) error {
	for index := range devices {
		devices[index].CurrentMetrics = map[string]MetricSample{}
		devices[index].MetricFreshness = map[string]Freshness{}
		rows, err := s.db.QueryContext(ctx, `
			SELECT series.metric, series.entity_id, series.unit, current.sample_id,
			       current.value, current.availability, current.observed_at,
			       current.received_at
			FROM current_series AS current
			JOIN metric_series AS series ON series.id = current.series_id
			JOIN monitoring_storage_state AS storage
			  ON storage.singleton = true AND current.storage_generation = storage.storage_generation
			WHERE series.device_id = $1
			ORDER BY series.metric, series.entity_id, series.id`, devices[index].ID)
		if err != nil {
			return fmt.Errorf("query current SQL metrics for %s: %w", devices[index].ID, err)
		}
		for rows.Next() {
			var metric, entityID, unit, sampleID, availability string
			var value sql.NullFloat64
			var observedAt, receivedAt time.Time
			if err := rows.Scan(&metric, &entityID, &unit, &sampleID, &value, &availability, &observedAt, &receivedAt); err != nil {
				rows.Close()
				return fmt.Errorf("scan current SQL metric for %s: %w", devices[index].ID, err)
			}
			var metricValue *float64
			if value.Valid {
				copy := value.Float64
				metricValue = &copy
			}
			key := metric
			if entityID != "" && entityID != "host" {
				key = entityID + ":" + metric
			}
			devices[index].CurrentMetrics[key] = MetricSample{ID: sampleID, DeviceID: devices[index].ID, EntityID: entityID, Metric: metric, Unit: unit, Value: metricValue, Availability: Freshness(availability), ObservedAt: observedAt, ReceivedAt: receivedAt}
			devices[index].MetricFreshness[key] = Freshness(availability)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate current SQL metrics for %s: %w", devices[index].ID, err)
		}
		rows.Close()
	}
	return nil
}
