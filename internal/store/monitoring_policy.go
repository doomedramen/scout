package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	MonitoringDefaultsVersion      = "002"
	DefaultRawRetentionDays        = 30
	DefaultFiveMinuteRetentionDays = 90
	DefaultHourlyRetentionDays     = 365
	MonitoringPreviewTTL           = 5 * time.Minute
	MaxMonitoringQueue             = 100000
)

type RetentionSettings struct {
	RawDays        int `json:"rawDays"`
	FiveMinuteDays int `json:"fiveMinuteDays"`
	HourlyDays     int `json:"hourlyDays"`
}

type MonitoringSettings struct {
	Revision            int64             `json:"revision"`
	DefaultsVersion     string            `json:"defaultsVersion"`
	NotificationsPaused bool              `json:"notificationsPaused"`
	Retention           RetentionSettings `json:"retention"`
	DiskBudgetBytes     int64             `json:"diskBudgetBytes"`
	UpdatedAt           *time.Time        `json:"updatedAt"`
}

type MonitoringRetentionPatch struct {
	RawDays        *int `json:"rawDays"`
	FiveMinuteDays *int `json:"fiveMinuteDays"`
	HourlyDays     *int `json:"hourlyDays"`
}

type MonitoringSettingsPatch struct {
	ExpectedRevision   int64                     `json:"expectedRevision"`
	Retention          *MonitoringRetentionPatch `json:"retention,omitempty"`
	DiskBudgetBytes    *int64                    `json:"diskBudgetBytes,omitempty"`
	RetentionPreviewID string                    `json:"retentionPreviewId,omitempty"`
}

type RetentionPreviewInput struct {
	ExpectedRevision int64             `json:"expectedRevision"`
	Retention        RetentionSettings `json:"retention"`
	IdempotencyKey   string            `json:"-"`
}

type RetentionRange struct {
	Tier   string    `json:"tier"`
	Before time.Time `json:"before"`
	After  time.Time `json:"after"`
}

type RetentionPreview struct {
	PreviewID        string            `json:"previewId"`
	ExpectedRevision int64             `json:"expectedRevision"`
	Retention        RetentionSettings `json:"retention"`
	AffectedRanges   []RetentionRange  `json:"affectedRanges"`
	EstimatedRows    int64             `json:"estimatedRows"`
	Irreversible     bool              `json:"irreversible"`
	ExpiresAt        time.Time         `json:"expiresAt"`
	CreatedAt        time.Time         `json:"-"`
	IdempotencyKey   string            `json:"-"`
	ConsumedAt       *time.Time        `json:"-"`
}

type MonitoringQueueStatus struct {
	Evaluation    int `json:"evaluation"`
	Notifications int `json:"notifications"`
	Rollup        int `json:"rollup"`
}

type MonitoringCounters struct {
	Dropped                 int64 `json:"dropped"`
	Truncated               int64 `json:"truncated"`
	Backpressure            int64 `json:"backpressure"`
	DeliveryFailures        int64 `json:"deliveryFailures"`
	ActiveAdmissionFailures int64 `json:"activeAdmissionFailures"`
}

type StoragePressure struct {
	UsedBytes   int64   `json:"usedBytes"`
	BudgetBytes int64   `json:"budgetBytes"`
	Percent     float64 `json:"percent"`
	State       string  `json:"state"`
}

type MonitoringStatus struct {
	EvaluationLagSeconds float64               `json:"evaluationLagSeconds"`
	RollupLagSeconds     float64               `json:"rollupLagSeconds"`
	Queues               MonitoringQueueStatus `json:"queues"`
	Counters             MonitoringCounters    `json:"counters"`
	StoragePressure      StoragePressure       `json:"storagePressure"`
	LastSuccessfulJobs   map[string]*time.Time `json:"lastSuccessfulJobs"`
}

type RetentionResult struct {
	RawDeleted        int64 `json:"rawDeleted"`
	FiveMinuteDeleted int64 `json:"fiveMinuteDeleted"`
	HourlyDeleted     int64 `json:"hourlyDeleted"`
	RawDeferred       int64 `json:"rawDeferred"`
}

func defaultRetentionSettings() RetentionSettings {
	return RetentionSettings{RawDays: DefaultRawRetentionDays, FiveMinuteDays: DefaultFiveMinuteRetentionDays, HourlyDays: DefaultHourlyRetentionDays}
}

func validateRetentionSettings(retention RetentionSettings) error {
	if retention.RawDays < 1 || retention.RawDays > DefaultRawRetentionDays ||
		retention.FiveMinuteDays < 1 || retention.FiveMinuteDays > DefaultFiveMinuteRetentionDays ||
		retention.HourlyDays < 1 || retention.HourlyDays > DefaultHourlyRetentionDays {
		return ErrInvalid
	}
	return nil
}

func validateMonitoringBudget(budget int64) error {
	if budget < 1 || budget > 1<<40 {
		return ErrInvalid
	}
	return nil
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func retentionFromWorkspace(workspace WorkspaceState) RetentionSettings {
	retention := RetentionSettings{RawDays: workspace.RetentionRawDays, FiveMinuteDays: workspace.RetentionFiveMinuteDays, HourlyDays: workspace.RetentionHourlyDays}
	if retention.RawDays == 0 {
		retention.RawDays = clampInt(workspace.RetentionHours/24, 1, DefaultRawRetentionDays)
	}
	if retention.FiveMinuteDays == 0 {
		retention.FiveMinuteDays = DefaultFiveMinuteRetentionDays
	}
	if retention.HourlyDays == 0 {
		retention.HourlyDays = DefaultHourlyRetentionDays
	}
	return retention
}

func monitoringSettingsFromWorkspace(workspace WorkspaceState, now time.Time) MonitoringSettings {
	updated := now.UTC()
	retention := retentionFromWorkspace(workspace)
	revision := workspace.MonitoringRevision
	if revision < 1 {
		revision = workspace.PolicyRevision
	}
	if revision < 1 {
		revision = 1
	}
	defaultsVersion := workspace.MonitoringDefaultsVersion
	if defaultsVersion == "" {
		defaultsVersion = MonitoringDefaultsVersion
	}
	budget := workspace.TelemetryBudgetBytes
	if budget < 1 {
		budget = DefaultTelemetryBudgetBytes
	}
	return MonitoringSettings{Revision: revision, DefaultsVersion: defaultsVersion, NotificationsPaused: workspace.NotificationsPaused, Retention: retention, DiskBudgetBytes: budget, UpdatedAt: &updated}
}

func applyMonitoringSettingsToWorkspace(workspace *WorkspaceState, settings MonitoringSettings) {
	workspace.MonitoringRevision = settings.Revision
	workspace.MonitoringDefaultsVersion = settings.DefaultsVersion
	workspace.NotificationsPaused = settings.NotificationsPaused
	workspace.RetentionRawDays = settings.Retention.RawDays
	workspace.RetentionFiveMinuteDays = settings.Retention.FiveMinuteDays
	workspace.RetentionHourlyDays = settings.Retention.HourlyDays
	workspace.RetentionHours = settings.Retention.RawDays * 24
	workspace.TelemetryBudgetBytes = settings.DiskBudgetBytes
}

func applyRetentionToWorkspace(workspace *WorkspaceState, retention RetentionSettings, now time.Time) {
	workspace.RetentionRawDays = retention.RawDays
	workspace.RetentionFiveMinuteDays = retention.FiveMinuteDays
	workspace.RetentionHourlyDays = retention.HourlyDays
	workspace.RetentionHours = retention.RawDays * 24
	workspace.LastRetentionAt = timePointer(now.UTC())
}

func retentionReduced(current, proposed RetentionSettings) bool {
	return proposed.RawDays < current.RawDays || proposed.FiveMinuteDays < current.FiveMinuteDays || proposed.HourlyDays < current.HourlyDays
}

func retentionRanges(current, proposed RetentionSettings, now time.Time) []RetentionRange {
	now = now.UTC()
	values := []struct {
		tier     string
		current  int
		proposed int
	}{{"raw", current.RawDays, proposed.RawDays}, {"fiveMinute", current.FiveMinuteDays, proposed.FiveMinuteDays}, {"hourly", current.HourlyDays, proposed.HourlyDays}}
	ranges := make([]RetentionRange, 0, len(values))
	for _, item := range values {
		if item.proposed >= item.current {
			continue
		}
		ranges = append(ranges, RetentionRange{Tier: item.tier, Before: now.Add(-time.Duration(item.current) * 24 * time.Hour), After: now.Add(-time.Duration(item.proposed) * 24 * time.Hour)})
	}
	return ranges
}

func cloneRetentionPreview(input RetentionPreview) RetentionPreview {
	input.AffectedRanges = append([]RetentionRange(nil), input.AffectedRanges...)
	if input.ConsumedAt != nil {
		value := input.ConsumedAt.UTC()
		input.ConsumedAt = &value
	}
	return input
}

func (s *Store) GetMonitoringSettings(ctx context.Context) (MonitoringSettings, error) {
	if s == nil {
		return MonitoringSettings{}, ErrInvalid
	}
	if s.db != nil {
		return s.getMonitoringSettingsSQL(ctx)
	}
	var result MonitoringSettings
	err := s.read(ctx, func(state *State) error {
		result = monitoringSettingsFromWorkspace(state.Workspace, s.now())
		return nil
	})
	return result, err
}

func (s *Store) getMonitoringSettingsSQL(ctx context.Context) (MonitoringSettings, error) {
	var result MonitoringSettings
	var updated time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT revision, defaults_version, notifications_paused, raw_days,
		       five_minute_days, hourly_days, disk_budget_bytes, updated_at
		FROM monitoring_settings WHERE singleton = true`).Scan(
		&result.Revision, &result.DefaultsVersion, &result.NotificationsPaused,
		&result.Retention.RawDays, &result.Retention.FiveMinuteDays,
		&result.Retention.HourlyDays, &result.DiskBudgetBytes, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return MonitoringSettings{}, fmt.Errorf("monitoring settings are missing: %w", ErrNotFound)
	}
	if err != nil {
		return MonitoringSettings{}, fmt.Errorf("read monitoring settings: %w", err)
	}
	updated = updated.UTC()
	result.UpdatedAt = &updated
	return result, nil
}

func (s *Store) CreateRetentionPreview(ctx context.Context, input RetentionPreviewInput) (RetentionPreview, error) {
	if input.ExpectedRevision < 1 || strings.TrimSpace(input.IdempotencyKey) != input.IdempotencyKey || len(input.IdempotencyKey) > 128 {
		return RetentionPreview{}, ErrInvalid
	}
	if err := validateRetentionSettings(input.Retention); err != nil {
		return RetentionPreview{}, err
	}
	if s.db != nil {
		return s.createRetentionPreviewSQL(ctx, input)
	}
	var result RetentionPreview
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		settings := monitoringSettingsFromWorkspace(state.Workspace, now)
		if settings.Revision != input.ExpectedRevision {
			return ErrConflict
		}
		if err := validateRetentionAgainstCurrent(settings.Retention, input.Retention); err != nil {
			return err
		}
		for _, existing := range state.RetentionPreviews {
			if existing.IdempotencyKey == input.IdempotencyKey && input.IdempotencyKey != "" && existing.ConsumedAt == nil && existing.ExpiresAt.After(now) {
				if existing.ExpectedRevision != input.ExpectedRevision || existing.Retention != input.Retention {
					return ErrConflict
				}
				result = cloneRetentionPreview(existing)
				return nil
			}
		}
		result = RetentionPreview{PreviewID: NewID(), ExpectedRevision: input.ExpectedRevision, Retention: input.Retention, AffectedRanges: retentionRanges(settings.Retention, input.Retention, now), Irreversible: retentionReduced(settings.Retention, input.Retention), ExpiresAt: now.Add(MonitoringPreviewTTL), CreatedAt: now, IdempotencyKey: input.IdempotencyKey}
		result.EstimatedRows = estimateMemoryRetentionRows(state, result.AffectedRanges)
		state.RetentionPreviews[result.PreviewID] = cloneRetentionPreview(result)
		return nil
	})
	return result, err
}

func validateRetentionAgainstCurrent(current, proposed RetentionSettings) error {
	if err := validateRetentionSettings(proposed); err != nil {
		return err
	}
	if proposed.RawDays > current.RawDays || proposed.FiveMinuteDays > current.FiveMinuteDays || proposed.HourlyDays > current.HourlyDays {
		return ErrInvalid
	}
	return nil
}

func estimateMemoryRetentionRows(state *State, ranges []RetentionRange) int64 {
	var total int64
	for _, item := range state.Samples {
		for _, affected := range ranges {
			if affected.Tier == "raw" && !item.ObservedAt.Before(affected.Before) && item.ObservedAt.Before(affected.After) {
				total++
				break
			}
		}
	}
	return total
}

func (s *Store) createRetentionPreviewSQL(ctx context.Context, input RetentionPreviewInput) (RetentionPreview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetentionPreview{}, err
	}
	defer func() { _ = tx.Rollback() }()
	settings, err := scanMonitoringSettings(tx.QueryRowContext(ctx, `
		SELECT revision, defaults_version, notifications_paused, raw_days,
		       five_minute_days, hourly_days, disk_budget_bytes, updated_at
		FROM monitoring_settings WHERE singleton = true FOR UPDATE`))
	if err != nil {
		return RetentionPreview{}, err
	}
	if settings.Revision != input.ExpectedRevision {
		return RetentionPreview{}, ErrConflict
	}
	if err := validateRetentionAgainstCurrent(settings.Retention, input.Retention); err != nil {
		return RetentionPreview{}, err
	}
	now := s.Now()
	if input.IdempotencyKey != "" {
		var existing RetentionPreview
		var consumed sql.NullTime
		err := tx.QueryRowContext(ctx, `
			SELECT id, expected_revision, raw_days, five_minute_days, hourly_days,
			       estimated_rows, irreversible, expires_at, created_at, consumed_at
			FROM retention_previews
			WHERE idempotency_key=$1 AND consumed_at IS NULL AND expires_at > $2`, input.IdempotencyKey, now).Scan(
			&existing.PreviewID, &existing.ExpectedRevision, &existing.Retention.RawDays,
			&existing.Retention.FiveMinuteDays, &existing.Retention.HourlyDays,
			&existing.EstimatedRows, &existing.Irreversible, &existing.ExpiresAt,
			&existing.CreatedAt, &consumed)
		if err == nil {
			if existing.ExpectedRevision != input.ExpectedRevision || existing.Retention != input.Retention {
				return RetentionPreview{}, ErrConflict
			}
			existing.IdempotencyKey = input.IdempotencyKey
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return RetentionPreview{}, fmt.Errorf("read retention preview idempotency key: %w", err)
		}
	}
	preview := RetentionPreview{PreviewID: NewID(), ExpectedRevision: input.ExpectedRevision, Retention: input.Retention, AffectedRanges: retentionRanges(settings.Retention, input.Retention, now), Irreversible: retentionReduced(settings.Retention, input.Retention), ExpiresAt: now.Add(MonitoringPreviewTTL), CreatedAt: now, IdempotencyKey: input.IdempotencyKey}
	preview.EstimatedRows, err = estimateSQLRetentionRows(ctx, tx, preview.AffectedRanges)
	if err != nil {
		return RetentionPreview{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO retention_previews(id, idempotency_key, expected_revision, raw_days,
		       five_minute_days, hourly_days, estimated_rows, irreversible, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, preview.PreviewID, preview.IdempotencyKey, preview.ExpectedRevision, preview.Retention.RawDays, preview.Retention.FiveMinuteDays, preview.Retention.HourlyDays, preview.EstimatedRows, preview.Irreversible, preview.ExpiresAt, preview.CreatedAt); err != nil {
		return RetentionPreview{}, fmt.Errorf("store retention preview: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RetentionPreview{}, fmt.Errorf("commit retention preview: %w", err)
	}
	return preview, nil
}

func scanMonitoringSettings(scanner interface{ Scan(...any) error }) (MonitoringSettings, error) {
	var result MonitoringSettings
	var updated time.Time
	if err := scanner.Scan(&result.Revision, &result.DefaultsVersion, &result.NotificationsPaused, &result.Retention.RawDays, &result.Retention.FiveMinuteDays, &result.Retention.HourlyDays, &result.DiskBudgetBytes, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MonitoringSettings{}, fmt.Errorf("monitoring settings are missing: %w", ErrNotFound)
		}
		return MonitoringSettings{}, fmt.Errorf("scan monitoring settings: %w", err)
	}
	updated = updated.UTC()
	result.UpdatedAt = &updated
	return result, nil
}

func estimateSQLRetentionRows(ctx context.Context, tx *sql.Tx, ranges []RetentionRange) (int64, error) {
	var total int64
	for _, item := range ranges {
		var count int64
		var query string
		switch item.Tier {
		case "raw":
			query = `SELECT COUNT(*) FROM metric_samples WHERE observed_at >= $1 AND observed_at < $2`
		case "fiveMinute":
			query = `SELECT COUNT(*) FROM metric_aggregates WHERE resolution_seconds = 300 AND bucket_start >= $1 AND bucket_start < $2`
		case "hourly":
			query = `SELECT COUNT(*) FROM metric_aggregates WHERE resolution_seconds = 3600 AND bucket_start >= $1 AND bucket_start < $2`
		default:
			return 0, ErrInvalid
		}
		if err := tx.QueryRowContext(ctx, query, item.Before, item.After).Scan(&count); err != nil {
			return 0, fmt.Errorf("estimate %s retention rows: %w", item.Tier, err)
		}
		total += count
	}
	return total, nil
}

func (s *Store) UpdateMonitoringSettings(ctx context.Context, patch MonitoringSettingsPatch) (MonitoringSettings, error) {
	if patch.ExpectedRevision < 1 {
		return MonitoringSettings{}, ErrInvalid
	}
	if s.db != nil {
		return s.updateMonitoringSettingsSQL(ctx, patch)
	}
	var result MonitoringSettings
	err := s.mutate(ctx, func(state *State) error {
		now := s.now().UTC()
		current := monitoringSettingsFromWorkspace(state.Workspace, now)
		if current.Revision != patch.ExpectedRevision {
			return ErrConflict
		}
		proposed := current
		if err := applyMonitoringSettingsPatch(&proposed, patch); err != nil {
			return err
		}
		if err := validateRetentionAgainstCurrent(current.Retention, proposed.Retention); err != nil {
			return err
		}
		if err := validateMonitoringBudget(proposed.DiskBudgetBytes); err != nil {
			return err
		}
		if retentionReduced(current.Retention, proposed.Retention) {
			preview, ok := state.RetentionPreviews[patch.RetentionPreviewID]
			if !ok || preview.ConsumedAt != nil || !preview.ExpiresAt.After(now) || preview.ExpectedRevision != patch.ExpectedRevision || preview.Retention != proposed.Retention {
				return ErrConflict
			}
			consumed := now
			preview.ConsumedAt = &consumed
			state.RetentionPreviews[patch.RetentionPreviewID] = cloneRetentionPreview(preview)
		}
		proposed.Revision = current.Revision + 1
		proposed.UpdatedAt = &now
		applyMonitoringSettingsToWorkspace(&state.Workspace, proposed)
		result = proposed
		return nil
	})
	return result, err
}

func applyMonitoringSettingsPatch(settings *MonitoringSettings, patch MonitoringSettingsPatch) error {
	if patch.Retention != nil {
		if patch.Retention.RawDays != nil {
			settings.Retention.RawDays = *patch.Retention.RawDays
		}
		if patch.Retention.FiveMinuteDays != nil {
			settings.Retention.FiveMinuteDays = *patch.Retention.FiveMinuteDays
		}
		if patch.Retention.HourlyDays != nil {
			settings.Retention.HourlyDays = *patch.Retention.HourlyDays
		}
	}
	if patch.DiskBudgetBytes != nil {
		settings.DiskBudgetBytes = *patch.DiskBudgetBytes
	}
	return nil
}

func (s *Store) updateMonitoringSettingsSQL(ctx context.Context, patch MonitoringSettingsPatch) (MonitoringSettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MonitoringSettings{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanMonitoringSettings(tx.QueryRowContext(ctx, `
		SELECT revision, defaults_version, notifications_paused, raw_days,
		       five_minute_days, hourly_days, disk_budget_bytes, updated_at
		FROM monitoring_settings WHERE singleton = true FOR UPDATE`))
	if err != nil {
		return MonitoringSettings{}, err
	}
	if current.Revision != patch.ExpectedRevision {
		return MonitoringSettings{}, ErrConflict
	}
	proposed := current
	if err := applyMonitoringSettingsPatch(&proposed, patch); err != nil {
		return MonitoringSettings{}, err
	}
	if err := validateRetentionAgainstCurrent(current.Retention, proposed.Retention); err != nil {
		return MonitoringSettings{}, err
	}
	if err := validateMonitoringBudget(proposed.DiskBudgetBytes); err != nil {
		return MonitoringSettings{}, err
	}
	now := s.Now()
	if retentionReduced(current.Retention, proposed.Retention) {
		if strings.TrimSpace(patch.RetentionPreviewID) == "" {
			return MonitoringSettings{}, ErrConflict
		}
		var preview RetentionPreview
		var consumed sql.NullTime
		err := tx.QueryRowContext(ctx, `
			SELECT id, expected_revision, raw_days, five_minute_days, hourly_days,
			       estimated_rows, irreversible, expires_at, created_at, consumed_at
			FROM retention_previews
			WHERE id=$1 FOR UPDATE`, patch.RetentionPreviewID).Scan(
			&preview.PreviewID, &preview.ExpectedRevision, &preview.Retention.RawDays,
			&preview.Retention.FiveMinuteDays, &preview.Retention.HourlyDays,
			&preview.EstimatedRows, &preview.Irreversible, &preview.ExpiresAt,
			&preview.CreatedAt, &consumed)
		if errors.Is(err, sql.ErrNoRows) {
			return MonitoringSettings{}, ErrConflict
		}
		if err != nil {
			return MonitoringSettings{}, fmt.Errorf("read retention preview: %w", err)
		}
		if consumed.Valid || !preview.ExpiresAt.After(now) || preview.ExpectedRevision != patch.ExpectedRevision || preview.Retention != proposed.Retention {
			return MonitoringSettings{}, ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE retention_previews SET consumed_at=$1 WHERE id=$2`, now, patch.RetentionPreviewID); err != nil {
			return MonitoringSettings{}, fmt.Errorf("consume retention preview: %w", err)
		}
	}
	proposed.Revision = current.Revision + 1
	proposed.UpdatedAt = &now
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitoring_settings
		SET revision=$1, raw_days=$2, five_minute_days=$3, hourly_days=$4,
		    disk_budget_bytes=$5, updated_at=$6
		WHERE singleton=true AND revision=$7`, proposed.Revision, proposed.Retention.RawDays, proposed.Retention.FiveMinuteDays, proposed.Retention.HourlyDays, proposed.DiskBudgetBytes, now, patch.ExpectedRevision); err != nil {
		return MonitoringSettings{}, fmt.Errorf("update monitoring settings: %w", err)
	}
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return MonitoringSettings{}, err
	}
	applyMonitoringSettingsToWorkspace(&state.Workspace, proposed)
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return MonitoringSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return MonitoringSettings{}, fmt.Errorf("commit monitoring settings: %w", err)
	}
	s.invalidateLegacyCache()
	return proposed, nil
}

func syncMonitoringSettingsTx(ctx context.Context, tx *sql.Tx, workspace WorkspaceState, now time.Time) error {
	settings := monitoringSettingsFromWorkspace(workspace, now)
	if err := validateRetentionSettings(settings.Retention); err != nil {
		return err
	}
	if err := validateMonitoringBudget(settings.DiskBudgetBytes); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE monitoring_settings
		SET revision=GREATEST(revision,$1), defaults_version=$2,
		    notifications_paused=$3, raw_days=$4, five_minute_days=$5,
		    hourly_days=$6, disk_budget_bytes=$7, migration_generation=COALESCE((SELECT migration_generation FROM monitoring_storage_state WHERE singleton=true), migration_generation),
		    dropped_count=GREATEST(dropped_count,$8), truncated_count=GREATEST(truncated_count,$9),
		    backpressure_count=GREATEST(backpressure_count,$10), active_admission_failures=GREATEST(active_admission_failures,$11),
		    last_retention_at=COALESCE($12,last_retention_at), last_evaluation_at=COALESCE($13,last_evaluation_at), last_rollup_at=COALESCE($14,last_rollup_at), updated_at=$15
		WHERE singleton=true`, settings.Revision, settings.DefaultsVersion, settings.NotificationsPaused, settings.Retention.RawDays, settings.Retention.FiveMinuteDays, settings.Retention.HourlyDays, settings.DiskBudgetBytes, workspace.DroppedSamples, workspace.TelemetryTruncated, workspace.TelemetryBackpressureCount, workspace.ActiveAdmissionFailures, workspace.LastRetentionAt, workspace.LastEvaluationAt, workspace.LastRollupAt, now.UTC())
	if err != nil {
		return fmt.Errorf("sync monitoring settings: %w", err)
	}
	return nil
}

func (s *Store) RecordMonitoringJobSuccess(ctx context.Context, job string, at time.Time) error {
	job = strings.TrimSpace(job)
	if job != "evaluation" && job != "rollup" && job != "retention" {
		return ErrInvalid
	}
	at = at.UTC()
	if at.IsZero() {
		return ErrInvalid
	}
	if s.db != nil {
		return s.recordMonitoringJobSuccessSQL(ctx, job, at)
	}
	return s.mutate(ctx, func(state *State) error {
		switch job {
		case "evaluation":
			state.Workspace.LastEvaluationAt = timePointer(at)
		case "rollup":
			state.Workspace.LastRollupAt = timePointer(at)
		case "retention":
			state.Workspace.LastRetentionAt = timePointer(at)
		}
		return nil
	})
}

func (s *Store) recordMonitoringJobSuccessSQL(ctx context.Context, job string, at time.Time) error {
	column := map[string]string{"evaluation": "last_evaluation_at", "rollup": "last_rollup_at", "retention": "last_retention_at"}[job]
	if _, err := s.db.ExecContext(ctx, "UPDATE monitoring_settings SET "+column+"=$1, updated_at=$1 WHERE singleton=true", at); err != nil {
		return fmt.Errorf("record %s job success: %w", job, err)
	}
	return nil
}

func (s *Store) MonitoringStatus(ctx context.Context) (MonitoringStatus, error) {
	if s == nil {
		return MonitoringStatus{}, ErrInvalid
	}
	if s.db != nil {
		return s.monitoringStatusSQL(ctx)
	}
	var result MonitoringStatus
	err := s.read(ctx, func(state *State) error {
		settings := monitoringSettingsFromWorkspace(state.Workspace, s.now())
		result = monitoringStatusFromState(*state, settings, s.now())
		return nil
	})
	return result, err
}

func (s *Store) monitoringStatusSQL(ctx context.Context) (MonitoringStatus, error) {
	settings, err := s.GetMonitoringSettings(ctx)
	if err != nil {
		return MonitoringStatus{}, err
	}
	telemetry, err := s.TelemetryStatus(ctx)
	if err != nil {
		return MonitoringStatus{}, err
	}
	workspace, err := s.Workspace(ctx)
	if err != nil {
		return MonitoringStatus{}, err
	}
	var evaluationQueue, notificationQueue, rollupQueue int64
	var evaluationOldest, rollupOldest sql.NullTime
	now := s.Now()
	if err := s.db.QueryRowContext(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM alert_work),
		  (SELECT COUNT(*) FROM notification_deliveries WHERE status IN ('queued','sending','retry')),
		  (SELECT COUNT(*) FROM rollup_work),
		  (SELECT MIN(updated_at) FROM alert_work),
		  (SELECT MIN(bucket_start) FROM rollup_work)`).Scan(&evaluationQueue, &notificationQueue, &rollupQueue, &evaluationOldest, &rollupOldest); err != nil {
		return MonitoringStatus{}, fmt.Errorf("read monitoring queues: %w", err)
	}
	deliveryStats, err := s.NotificationDeliveryStats(ctx)
	if err != nil {
		return MonitoringStatus{}, err
	}
	var counters MonitoringCounters
	var lastEvaluation, lastRollup, lastRetention sql.NullTime
	if err := s.db.QueryRowContext(ctx, `
		SELECT dropped_count, truncated_count, backpressure_count,
		       active_admission_failures, last_evaluation_at, last_rollup_at,
		       last_retention_at
		FROM monitoring_settings WHERE singleton=true`).Scan(
		&counters.Dropped, &counters.Truncated, &counters.Backpressure,
		&counters.ActiveAdmissionFailures, &lastEvaluation, &lastRollup, &lastRetention); err != nil {
		return MonitoringStatus{}, fmt.Errorf("read monitoring counters: %w", err)
	}
	counters.Dropped = maxInt64(counters.Dropped, telemetry.DroppedSamples)
	counters.Dropped = maxInt64(counters.Dropped, workspace.DroppedSamples)
	counters.Truncated = maxInt64(counters.Truncated, workspace.TelemetryTruncated)
	counters.Backpressure = maxInt64(counters.Backpressure, workspace.TelemetryBackpressureCount)
	counters.ActiveAdmissionFailures = maxInt64(counters.ActiveAdmissionFailures, workspace.ActiveAdmissionFailures)
	counters.DeliveryFailures = deliveryStats.Failed + deliveryStats.Expired
	if telemetry.Backpressure && counters.Backpressure == 0 {
		counters.Backpressure = 1
	}
	result := MonitoringStatus{
		EvaluationLagSeconds: lagSeconds(now, evaluationOldest),
		RollupLagSeconds:     lagSeconds(now, rollupOldest),
		Queues:               MonitoringQueueStatus{Evaluation: boundedQueueCount(evaluationQueue, MaxMonitoringQueue), Notifications: boundedQueueCount(notificationQueue, MaxPendingNotificationDeliveries), Rollup: boundedQueueCount(rollupQueue, MaxMonitoringQueue)},
		Counters:             counters,
		StoragePressure:      storagePressure(telemetry.UsedBytes, settings.DiskBudgetBytes),
		LastSuccessfulJobs:   map[string]*time.Time{"evaluation": latestTime(nullableTimePointer(lastEvaluation), workspace.LastEvaluationAt), "rollup": latestTime(nullableTimePointer(lastRollup), workspace.LastRollupAt), "retention": latestTime(nullableTimePointer(lastRetention), workspace.LastRetentionAt)},
	}
	return result, nil
}

func monitoringStatusFromState(state State, settings MonitoringSettings, now time.Time) MonitoringStatus {
	var evaluationQueue, notificationQueue int
	var evaluationOldest time.Time
	for _, item := range state.AlertWork {
		evaluationQueue++
		if evaluationOldest.IsZero() || item.UpdatedAt.Before(evaluationOldest) {
			evaluationOldest = item.UpdatedAt
		}
	}
	for _, item := range state.NotificationDeliveries {
		if item.Status == NotificationDeliveryQueued || item.Status == NotificationDeliverySending || item.Status == NotificationDeliveryRetry {
			notificationQueue++
		}
	}
	backpressure := state.Workspace.TelemetryBackpressureCount
	if state.Workspace.TelemetryBackpressure && backpressure == 0 {
		backpressure = 1
	}
	return MonitoringStatus{
		EvaluationLagSeconds: lagSeconds(now, timePointerValue(evaluationOldest)),
		RollupLagSeconds:     0,
		Queues:               MonitoringQueueStatus{Evaluation: boundedQueueCount(int64(evaluationQueue), MaxMonitoringQueue), Notifications: boundedQueueCount(int64(notificationQueue), MaxPendingNotificationDeliveries)},
		Counters:             MonitoringCounters{Dropped: state.Workspace.DroppedSamples, Truncated: state.Workspace.TelemetryTruncated, Backpressure: backpressure, DeliveryFailures: countMemoryDeliveryFailures(state), ActiveAdmissionFailures: state.Workspace.ActiveAdmissionFailures},
		StoragePressure:      storagePressure(memoryTelemetryBytes(state), settings.DiskBudgetBytes),
		LastSuccessfulJobs:   map[string]*time.Time{"evaluation": cloneTime(state.Workspace.LastEvaluationAt), "rollup": cloneTime(state.Workspace.LastRollupAt), "retention": cloneTime(state.Workspace.LastRetentionAt)},
	}
}

func countMemoryDeliveryFailures(state State) int64 {
	var count int64
	for _, item := range state.NotificationDeliveries {
		if item.Status == NotificationDeliveryFailed || item.Status == NotificationDeliveryExpired {
			count++
		}
	}
	return count
}

func lagSeconds(now time.Time, oldest sql.NullTime) float64 {
	if !oldest.Valid {
		return 0
	}
	return lagSecondsValue(now, oldest.Time)
}

func lagSecondsValue(now, oldest time.Time) float64 {
	seconds := now.Sub(oldest.UTC()).Seconds()
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0
	}
	return seconds
}

func timePointerValue(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value, Valid: true}
}

func boundedQueueCount(value int64, maximum int) int {
	if value < 0 {
		return 0
	}
	if value > int64(maximum) {
		return maximum
	}
	return int(value)
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func latestTime(left, right *time.Time) *time.Time {
	if left == nil {
		return cloneTime(right)
	}
	if right == nil || !right.After(*left) {
		return cloneTime(left)
	}
	return cloneTime(right)
}

func storagePressure(usedBytes, budgetBytes int64) StoragePressure {
	if budgetBytes < 1 {
		budgetBytes = DefaultTelemetryBudgetBytes
	}
	percent := float64(usedBytes) / float64(budgetBytes) * 100
	if percent < 0 {
		percent = 0
	}
	state := "normal"
	if percent >= 90 {
		state = "critical"
	} else if percent >= 80 {
		state = "warning"
	}
	if percent > 100 {
		percent = 100
	}
	return StoragePressure{UsedBytes: maxInt64(usedBytes, 0), BudgetBytes: budgetBytes, Percent: percent, State: state}
}

func sortedRetentionRanges(ranges []RetentionRange) []RetentionRange {
	sort.Slice(ranges, func(left, right int) bool { return ranges[left].Tier < ranges[right].Tier })
	return ranges
}

func (s *Store) RetainTelemetry(ctx context.Context, retention RetentionSettings, now time.Time) (RetentionResult, error) {
	if err := validateRetentionSettings(retention); err != nil {
		return RetentionResult{}, err
	}
	if now.IsZero() {
		now = s.Now()
	}
	now = now.UTC()
	if s.db != nil {
		return s.retainTelemetrySQL(ctx, retention, now)
	}
	var result RetentionResult
	err := s.mutate(ctx, func(state *State) error {
		cutoff := now.Add(-time.Duration(retention.RawDays) * 24 * time.Hour)
		kept := state.Samples[:0]
		for _, sample := range state.Samples {
			if sample.ObservedAt.Before(cutoff) {
				result.RawDeleted++
				continue
			}
			kept = append(kept, sample)
		}
		state.Samples = kept
		applyRetentionToWorkspace(&state.Workspace, retention, now)
		return nil
	})
	return result, err
}

func (s *Store) retainTelemetrySQL(ctx context.Context, retention RetentionSettings, now time.Time) (RetentionResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetentionResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMonitoringMigration(ctx, tx); err != nil {
		return RetentionResult{}, err
	}
	storage, err := readMonitoringStorageRow(ctx, tx)
	if err != nil {
		return RetentionResult{}, err
	}
	if storage.Phase != MonitoringStorageAuthoritative {
		return RetentionResult{}, fmt.Errorf("SQL telemetry storage is not authoritative: %w", ErrConflict)
	}
	state, err := readWorkspaceStateTx(ctx, tx)
	if err != nil {
		return RetentionResult{}, err
	}
	rawCutoff := now.Add(-time.Duration(retention.RawDays) * 24 * time.Hour)
	fiveCutoff := now.Add(-time.Duration(retention.FiveMinuteDays) * 24 * time.Hour)
	hourlyCutoff := now.Add(-time.Duration(retention.HourlyDays) * 24 * time.Hour)
	rawPredicate := `
		metric_samples.observed_at < $1
		AND metric_samples.series_id IS NOT NULL
		AND metric_samples.series_id <> ''
		AND EXISTS (
			SELECT 1 FROM metric_aggregates AS five
			WHERE five.series_id = metric_samples.series_id
			  AND five.resolution_seconds = 300
			  AND five.bucket_start = date_bin('5 minutes', metric_samples.observed_at, TIMESTAMPTZ '1970-01-01 00:00:00+00')
			  AND five.partial = false
		)
		AND EXISTS (
			SELECT 1 FROM metric_aggregates AS hourly
			WHERE hourly.series_id = metric_samples.series_id
			  AND hourly.resolution_seconds = 3600
			  AND hourly.bucket_start = date_trunc('hour', metric_samples.observed_at)
			  AND hourly.partial = false
		)
		AND NOT EXISTS (
			SELECT 1 FROM rollup_work AS five_work
			WHERE five_work.series_id = metric_samples.series_id
			  AND five_work.resolution_seconds = 300
			  AND five_work.bucket_start = date_bin('5 minutes', metric_samples.observed_at, TIMESTAMPTZ '1970-01-01 00:00:00+00')
		)
		AND NOT EXISTS (
			SELECT 1 FROM rollup_work AS hourly_work
			WHERE hourly_work.series_id = metric_samples.series_id
			  AND hourly_work.resolution_seconds = 3600
			  AND hourly_work.bucket_start = date_trunc('hour', metric_samples.observed_at)
		)`
	var candidates int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE observed_at < $1 AND series_id IS NOT NULL AND series_id <> ''`, rawCutoff).Scan(&candidates); err != nil {
		return RetentionResult{}, fmt.Errorf("count retention candidates: %w", err)
	}
	seriesIDs, err := affectedSeriesSQL(ctx, tx, rawPredicate, rawCutoff)
	if err != nil {
		return RetentionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM current_series
		WHERE sample_id IN (SELECT id FROM metric_samples WHERE `+rawPredicate+`)`, rawCutoff); err != nil {
		return RetentionResult{}, fmt.Errorf("remove expired current series: %w", err)
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM metric_samples WHERE `+rawPredicate, rawCutoff)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("remove expired raw telemetry: %w", err)
	}
	rawDeleted, err := deleted.RowsAffected()
	if err != nil {
		return RetentionResult{}, fmt.Errorf("count expired raw telemetry: %w", err)
	}
	result := RetentionResult{RawDeleted: rawDeleted, RawDeferred: candidates - rawDeleted}
	if result.RawDeferred < 0 {
		result.RawDeferred = 0
	}
	for _, seriesID := range seriesIDs {
		if err := rebuildCurrentSeriesSQL(ctx, tx, seriesID, storage.StorageGeneration); err != nil {
			return RetentionResult{}, err
		}
	}
	childDeleted, err := tx.ExecContext(ctx, `
		DELETE FROM metric_aggregates AS five
		WHERE five.resolution_seconds = 300
		  AND five.bucket_start < $1
		  AND five.partial = false
		  AND NOT EXISTS (
			SELECT 1 FROM rollup_work AS work
			WHERE work.series_id = five.series_id
			  AND work.resolution_seconds = five.resolution_seconds
			  AND work.bucket_start = five.bucket_start
		  )
		  AND EXISTS (
			SELECT 1 FROM metric_aggregates AS hourly
			WHERE hourly.series_id = five.series_id
			  AND hourly.resolution_seconds = 3600
			  AND hourly.bucket_start = date_trunc('hour', five.bucket_start)
			  AND hourly.partial = false
		  )`, fiveCutoff)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("remove expired five-minute aggregates: %w", err)
	}
	result.FiveMinuteDeleted, err = childDeleted.RowsAffected()
	if err != nil {
		return RetentionResult{}, fmt.Errorf("count expired five-minute aggregates: %w", err)
	}
	hourlyDeleted, err := tx.ExecContext(ctx, `
		DELETE FROM metric_aggregates AS hourly
		WHERE hourly.resolution_seconds = 3600
		  AND hourly.bucket_start < $1
		  AND hourly.partial = false
		  AND NOT EXISTS (
			SELECT 1 FROM rollup_work AS work
			WHERE work.series_id = hourly.series_id
			  AND work.resolution_seconds = hourly.resolution_seconds
			  AND work.bucket_start = hourly.bucket_start
		  )`, hourlyCutoff)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("remove expired hourly aggregates: %w", err)
	}
	result.HourlyDeleted, err = hourlyDeleted.RowsAffected()
	if err != nil {
		return RetentionResult{}, fmt.Errorf("count expired hourly aggregates: %w", err)
	}
	applyRetentionToWorkspace(&state.Workspace, retention, now)
	stripMigratedTelemetry(&state)
	if err := writeWorkspaceStateTx(ctx, tx, state); err != nil {
		return RetentionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitoring_settings
		SET raw_days=$1, five_minute_days=$2, hourly_days=$3,
		    last_retention_at=$4, updated_at=$4
		WHERE singleton=true`, retention.RawDays, retention.FiveMinuteDays, retention.HourlyDays, now); err != nil {
		return RetentionResult{}, fmt.Errorf("persist retention job status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, fmt.Errorf("commit telemetry retention: %w", err)
	}
	s.invalidateLegacyCache()
	return result, nil
}
