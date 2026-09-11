package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	RollupResolutionFiveMinute   = 300
	RollupResolutionHourly       = 3600
	MaxRollupWorkItems           = 1000
	defaultRollupLease           = 30 * time.Second
	defaultMetricIntervalSeconds = 15
	maxMetricIntervalSeconds     = 24 * 60 * 60
)

var ErrRollupSourcePending = errors.New("rollup source pending")

// RollupWorkItem is a fenced unit of rollup work. LeaseEpoch is the fencing
// token; a worker must present the same owner, epoch, and dirty generation to
// complete or fail the work it claimed.
type RollupWorkItem struct {
	SeriesID          string     `json:"seriesId"`
	ResolutionSeconds int        `json:"resolutionSeconds"`
	BucketStart       time.Time  `json:"bucketStart"`
	DirtyGeneration   int64      `json:"dirtyGeneration"`
	LeaseEpoch        int64      `json:"leaseEpoch"`
	LeaseOwner        string     `json:"leaseOwner,omitempty"`
	LeaseUntil        *time.Time `json:"leaseUntil,omitempty"`
	Attempts          int        `json:"attempts"`
	LastError         string     `json:"lastError,omitempty"`
}

// MetricAggregate stores sufficient statistics for weighted, multi-tier
// history queries. A zero-count aggregate intentionally keeps Sum, Min, and
// Max nil instead of manufacturing a zero value for unavailable telemetry.
type MetricAggregate struct {
	SeriesID          string    `json:"seriesId"`
	ResolutionSeconds int       `json:"resolutionSeconds"`
	BucketStart       time.Time `json:"bucketStart"`
	Count             int64     `json:"count"`
	Sum               *float64  `json:"sum,omitempty"`
	Min               *float64  `json:"min,omitempty"`
	Max               *float64  `json:"max,omitempty"`
	ExpectedCount     int64     `json:"expectedCount"`
	CoveredSeconds    int       `json:"coveredSeconds"`
	BucketSeconds     int       `json:"bucketSeconds"`
	Partial           bool      `json:"partial"`
	Generation        int64     `json:"generation"`
}

func (s *Store) requireRollupAuthority(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrInvalid
	}
	phase, err := s.monitoringPhase(ctx)
	if err != nil {
		return err
	}
	if phase == MonitoringStorageImporting {
		return fmt.Errorf("rollup storage is paused during migration: %w", ErrConflict)
	}
	if phase != MonitoringStorageAuthoritative {
		return fmt.Errorf("rollup storage is not authoritative: %w", ErrConflict)
	}
	return nil
}

func normalizeRollupLimit(limit int) (int, error) {
	if limit <= 0 {
		return MaxRollupWorkItems, nil
	}
	if limit > MaxRollupWorkItems {
		return 0, ErrInvalid
	}
	return limit, nil
}

func normalizeRollupLease(lease time.Duration) (time.Duration, error) {
	if lease <= 0 {
		return defaultRollupLease, nil
	}
	if lease > 15*time.Minute {
		return 0, ErrInvalid
	}
	return lease, nil
}

func rollupBucketDuration(resolution int) (time.Duration, error) {
	switch resolution {
	case RollupResolutionFiveMinute:
		return 5 * time.Minute, nil
	case RollupResolutionHourly:
		return time.Hour, nil
	default:
		return 0, ErrInvalid
	}
}

func validateRollupWorkItem(item RollupWorkItem) error {
	duration, err := rollupBucketDuration(item.ResolutionSeconds)
	if err != nil || item.SeriesID == "" || item.DirtyGeneration < 1 || item.LeaseEpoch < 1 || item.LeaseOwner == "" || item.BucketStart.IsZero() {
		return ErrInvalid
	}
	if !item.BucketStart.UTC().Equal(item.BucketStart.UTC().Truncate(duration)) {
		return ErrInvalid
	}
	return nil
}

func validateMetricAggregate(aggregate MetricAggregate, work RollupWorkItem) error {
	bucketDuration, err := rollupBucketDuration(work.ResolutionSeconds)
	if err != nil || aggregate.SeriesID != work.SeriesID || aggregate.ResolutionSeconds != work.ResolutionSeconds || !aggregate.BucketStart.UTC().Equal(work.BucketStart.UTC()) || aggregate.Count < 0 || aggregate.ExpectedCount < 0 || aggregate.CoveredSeconds < 0 || aggregate.CoveredSeconds > int(bucketDuration/time.Second) || aggregate.BucketSeconds != int(bucketDuration/time.Second) || aggregate.Partial || aggregate.Generation != work.DirtyGeneration {
		return ErrInvalid
	}
	if aggregate.Count == 0 {
		if aggregate.Sum != nil || aggregate.Min != nil || aggregate.Max != nil {
			return ErrInvalid
		}
	} else if aggregate.Sum == nil || aggregate.Min == nil || aggregate.Max == nil {
		return ErrInvalid
	}
	return nil
}

// ClaimRollupWork leases ready work with PostgreSQL row locks. A new claim
// increments LeaseEpoch so a worker with an expired lease cannot commit stale
// output after another worker takes over.
func (s *Store) ClaimRollupWork(ctx context.Context, owner string, limit int, lease time.Duration) ([]RollupWorkItem, error) {
	if err := s.requireRollupAuthority(ctx); err != nil {
		return nil, err
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 128 {
		return nil, ErrInvalid
	}
	limit, err := normalizeRollupLimit(limit)
	if err != nil {
		return nil, err
	}
	lease, err = normalizeRollupLease(lease)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.Now()
	rows, err := tx.QueryContext(ctx, `
		SELECT series_id, resolution_seconds, bucket_start, dirty_generation,
		       lease_epoch, lease_owner, lease_until, attempts, last_error
		FROM rollup_work
		WHERE lease_until IS NULL OR lease_until <= $1
		ORDER BY resolution_seconds, bucket_start, series_id
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select rollup work: %w", err)
	}
	candidates := make([]RollupWorkItem, 0, limit)
	for rows.Next() {
		item, scanErr := scanRollupWork(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan rollup work: %w", scanErr)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate rollup work: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close rollup work rows: %w", err)
	}
	items := make([]RollupWorkItem, 0, len(candidates))
	for _, item := range candidates {
		until := now.Add(lease)
		newEpoch := item.LeaseEpoch + 1
		if _, err := tx.ExecContext(ctx, `
			UPDATE rollup_work
			SET lease_epoch = $1, lease_owner = $2, lease_until = $3,
			    attempts = attempts + 1, last_error = NULL
			WHERE series_id = $4 AND resolution_seconds = $5 AND bucket_start = $6
			  AND dirty_generation = $7 AND lease_epoch = $8`,
			newEpoch, owner, until, item.SeriesID, item.ResolutionSeconds, item.BucketStart, item.DirtyGeneration, item.LeaseEpoch); err != nil {
			return nil, fmt.Errorf("lease rollup work: %w", err)
		}
		item.LeaseEpoch = newEpoch
		item.LeaseOwner = owner
		item.LeaseUntil = rollupTimePointer(until)
		item.Attempts++
		item.LastError = ""
		items = append(items, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit rollup work leases: %w", err)
	}
	return items, nil
}

func scanRollupWork(scanner interface{ Scan(...any) error }) (RollupWorkItem, error) {
	var item RollupWorkItem
	var leaseUntil sql.NullTime
	var lastError sql.NullString
	if err := scanner.Scan(&item.SeriesID, &item.ResolutionSeconds, &item.BucketStart, &item.DirtyGeneration, &item.LeaseEpoch, &item.LeaseOwner, &leaseUntil, &item.Attempts, &lastError); err != nil {
		return RollupWorkItem{}, err
	}
	item.BucketStart = item.BucketStart.UTC()
	if leaseUntil.Valid {
		item.LeaseUntil = rollupTimePointer(leaseUntil.Time.UTC())
	}
	if lastError.Valid {
		item.LastError = lastError.String
	}
	return item, nil
}

// ComputeRollupAggregate recomputes one bounded bucket from authoritative
// sources. Five-minute rows read raw samples; hourly rows read only the twelve
// corresponding five-minute rows and defer while any child is still dirty.
func (s *Store) ComputeRollupAggregate(ctx context.Context, work RollupWorkItem) (MetricAggregate, error) {
	if err := s.requireRollupAuthority(ctx); err != nil {
		return MetricAggregate{}, err
	}
	if err := validateRollupWorkItem(work); err != nil {
		return MetricAggregate{}, err
	}
	var seriesInterval int
	if err := s.db.QueryRowContext(ctx, `SELECT interval_seconds FROM metric_series WHERE id = $1`, work.SeriesID).Scan(&seriesInterval); errors.Is(err, sql.ErrNoRows) {
		return MetricAggregate{}, ErrNotFound
	} else if err != nil {
		return MetricAggregate{}, fmt.Errorf("read metric series interval: %w", err)
	}
	if seriesInterval < 1 || seriesInterval > maxMetricIntervalSeconds {
		seriesInterval = defaultMetricIntervalSeconds
	}
	if work.ResolutionSeconds == RollupResolutionHourly {
		return s.computeHourlyAggregate(ctx, work, seriesInterval)
	}
	return s.computeFiveMinuteAggregate(ctx, work, seriesInterval)
}

type rawRollupPoint struct {
	ObservedAt      time.Time
	ReceivedAt      time.Time
	ID              string
	Value           sql.NullFloat64
	Availability    string
	IntervalSeconds int
}

type rollupInterval struct {
	Start time.Time
	End   time.Time
}

func (s *Store) computeFiveMinuteAggregate(ctx context.Context, work RollupWorkItem, seriesInterval int) (MetricAggregate, error) {
	bucketDuration, _ := rollupBucketDuration(work.ResolutionSeconds)
	start := work.BucketStart.UTC()
	end := start.Add(bucketDuration)
	rows, err := s.db.QueryContext(ctx, `
		WITH previous AS (
			SELECT observed_at, received_at, id, value, availability, interval_seconds
			FROM metric_samples
			WHERE series_id = $1 AND observed_at < $2
			ORDER BY observed_at DESC, received_at DESC, id DESC
			LIMIT 1
		), in_bucket AS (
			SELECT observed_at, received_at, id, value, availability, interval_seconds
			FROM metric_samples
			WHERE series_id = $1 AND observed_at >= $2 AND observed_at < $3
		)
		SELECT observed_at, received_at, id, value, availability, interval_seconds
		FROM (
			SELECT observed_at, received_at, id, value, availability, interval_seconds FROM previous
			UNION ALL
			SELECT observed_at, received_at, id, value, availability, interval_seconds FROM in_bucket
		) AS samples
		ORDER BY observed_at, received_at, id`, work.SeriesID, start, end)
	if err != nil {
		return MetricAggregate{}, fmt.Errorf("read raw samples for rollup: %w", err)
	}
	defer rows.Close()
	points := make([]rawRollupPoint, 0)
	for rows.Next() {
		var point rawRollupPoint
		if err := rows.Scan(&point.ObservedAt, &point.ReceivedAt, &point.ID, &point.Value, &point.Availability, &point.IntervalSeconds); err != nil {
			return MetricAggregate{}, fmt.Errorf("scan raw sample for rollup: %w", err)
		}
		point.ObservedAt = point.ObservedAt.UTC()
		point.ReceivedAt = point.ReceivedAt.UTC()
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return MetricAggregate{}, fmt.Errorf("iterate raw samples for rollup: %w", err)
	}

	var count int64
	var sum float64
	var minValue, maxValue *float64
	intervals := make([]rollupInterval, 0, len(points))
	for index, point := range points {
		if point.Availability != string(FreshnessCurrent) || !point.Value.Valid {
			continue
		}
		intervalSeconds := point.IntervalSeconds
		if intervalSeconds < 1 || intervalSeconds > maxMetricIntervalSeconds {
			intervalSeconds = seriesInterval
		}
		interval := time.Duration(intervalSeconds) * time.Second
		if !point.ObservedAt.Before(start) && point.ObservedAt.Before(end) {
			value := point.Value.Float64
			count++
			sum += value
			if minValue == nil || value < *minValue {
				copyValue := value
				minValue = &copyValue
			}
			if maxValue == nil || value > *maxValue {
				copyValue := value
				maxValue = &copyValue
			}
		}

		coverageStart := point.ObservedAt
		if coverageStart.Before(start) {
			coverageStart = start
		}
		coverageEnd := point.ObservedAt.Add(interval)
		if index+1 < len(points) {
			next := points[index+1].ObservedAt
			if next.After(point.ObservedAt) && next.Before(coverageEnd) {
				coverageEnd = next
			}
		}
		if coverageEnd.After(end) {
			coverageEnd = end
		}
		if coverageEnd.After(coverageStart) {
			intervals = append(intervals, rollupInterval{Start: coverageStart, End: coverageEnd})
		}
	}

	aggregate := MetricAggregate{SeriesID: work.SeriesID, ResolutionSeconds: work.ResolutionSeconds, BucketStart: start, Count: count, ExpectedCount: expectedRollupCount(work.ResolutionSeconds, seriesInterval), CoveredSeconds: boundedCoveredSeconds(intervalUnion(intervals), int(bucketDuration/time.Second)), BucketSeconds: int(bucketDuration / time.Second), Generation: work.DirtyGeneration}
	if count > 0 {
		aggregate.Sum = floatPointer(sum)
		aggregate.Min = minValue
		aggregate.Max = maxValue
	}
	return aggregate, nil
}

func (s *Store) computeHourlyAggregate(ctx context.Context, work RollupWorkItem, seriesInterval int) (MetricAggregate, error) {
	start := work.BucketStart.UTC()
	end := start.Add(time.Hour)
	rows, err := s.db.QueryContext(ctx, `
		WITH child_buckets AS (
			SELECT generate_series($2::timestamptz, $3::timestamptz - interval '5 minutes', interval '5 minutes') AS bucket_start
		)
		SELECT child.bucket_start, aggregate.count, aggregate.sum, aggregate.min,
		       aggregate.max, aggregate.expected_count, aggregate.covered_seconds,
		       aggregate.bucket_seconds, aggregate.partial,
		       (work.series_id IS NOT NULL) AS pending
		FROM child_buckets AS child
		LEFT JOIN metric_aggregates AS aggregate
		  ON aggregate.series_id = $1 AND aggregate.resolution_seconds = $4
		 AND aggregate.bucket_start = child.bucket_start
		LEFT JOIN rollup_work AS work
		  ON work.series_id = $1 AND work.resolution_seconds = $4
		 AND work.bucket_start = child.bucket_start
		ORDER BY child.bucket_start`, work.SeriesID, start, end, RollupResolutionFiveMinute)
	if err != nil {
		return MetricAggregate{}, fmt.Errorf("read five-minute rows for hourly rollup: %w", err)
	}
	defer rows.Close()
	var count, expectedCount int64
	var sum float64
	var minValue, maxValue *float64
	coveredSeconds := 0
	childCount := 0
	for rows.Next() {
		childCount++
		var bucketStart time.Time
		var childCountValue, childExpected sql.NullInt64
		var childSum, childMin, childMax sql.NullFloat64
		var childCovered, childBucket sql.NullInt64
		var childPartial, pending sql.NullBool
		if err := rows.Scan(&bucketStart, &childCountValue, &childSum, &childMin, &childMax, &childExpected, &childCovered, &childBucket, &childPartial, &pending); err != nil {
			return MetricAggregate{}, fmt.Errorf("scan five-minute row for hourly rollup: %w", err)
		}
		if pending.Valid && pending.Bool {
			return MetricAggregate{}, ErrRollupSourcePending
		}
		if !childCountValue.Valid {
			expectedCount += expectedRollupCount(RollupResolutionFiveMinute, seriesInterval)
			continue
		}
		if !childBucket.Valid || childBucket.Int64 != RollupResolutionFiveMinute || !childCovered.Valid || childCovered.Int64 < 0 || childCovered.Int64 > RollupResolutionFiveMinute || !childExpected.Valid || childExpected.Int64 < 0 || (childPartial.Valid && childPartial.Bool) {
			return MetricAggregate{}, ErrRollupSourcePending
		}
		count += childCountValue.Int64
		expectedCount += childExpected.Int64
		coveredSeconds += int(childCovered.Int64)
		if childSum.Valid {
			sum += childSum.Float64
		}
		if childMin.Valid && (minValue == nil || childMin.Float64 < *minValue) {
			value := childMin.Float64
			minValue = &value
		}
		if childMax.Valid && (maxValue == nil || childMax.Float64 > *maxValue) {
			value := childMax.Float64
			maxValue = &value
		}
	}
	if err := rows.Err(); err != nil {
		return MetricAggregate{}, fmt.Errorf("iterate five-minute rows for hourly rollup: %w", err)
	}
	if childCount != 12 {
		return MetricAggregate{}, ErrRollupSourcePending
	}
	aggregate := MetricAggregate{SeriesID: work.SeriesID, ResolutionSeconds: work.ResolutionSeconds, BucketStart: start, Count: count, ExpectedCount: expectedCount, CoveredSeconds: boundedCoveredSeconds(time.Duration(coveredSeconds)*time.Second, RollupResolutionHourly), BucketSeconds: RollupResolutionHourly, Generation: work.DirtyGeneration}
	if count > 0 {
		aggregate.Sum = floatPointer(sum)
		aggregate.Min = minValue
		aggregate.Max = maxValue
	}
	return aggregate, nil
}

func expectedRollupCount(resolutionSeconds, intervalSeconds int) int64 {
	if intervalSeconds < 1 {
		intervalSeconds = defaultMetricIntervalSeconds
	}
	return int64(math.Ceil(float64(resolutionSeconds) / float64(intervalSeconds)))
}

func intervalUnion(intervals []rollupInterval) time.Duration {
	if len(intervals) == 0 {
		return 0
	}
	for index := range intervals {
		for next := index + 1; next < len(intervals); next++ {
			if intervals[next].Start.Before(intervals[index].Start) {
				intervals[index], intervals[next] = intervals[next], intervals[index]
			}
		}
	}
	covered := time.Duration(0)
	start, end := intervals[0].Start, intervals[0].End
	for _, interval := range intervals[1:] {
		if !interval.Start.After(end) {
			if interval.End.After(end) {
				end = interval.End
			}
			continue
		}
		covered += end.Sub(start)
		start, end = interval.Start, interval.End
	}
	return covered + end.Sub(start)
}

func boundedCoveredSeconds(covered time.Duration, bucketSeconds int) int {
	seconds := int(covered / time.Second)
	if seconds < 0 {
		return 0
	}
	if seconds > bucketSeconds {
		return bucketSeconds
	}
	return seconds
}

func floatPointer(value float64) *float64 { return &value }

func rollupTimePointer(value time.Time) *time.Time { return &value }

// CompleteRollupWork writes one aggregate and removes only the exact fenced
// work row. A five-minute completion dirties its hourly parent in the same
// transaction, so a late child can never leave a stale hourly row looking
// complete.
func (s *Store) CompleteRollupWork(ctx context.Context, work RollupWorkItem, aggregate MetricAggregate) error {
	if err := s.requireRollupAuthority(ctx); err != nil {
		return err
	}
	if err := validateRollupWorkItem(work); err != nil {
		return err
	}
	if err := validateMetricAggregate(aggregate, work); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.Now()
	var dirtyGeneration, leaseEpoch int64
	var leaseOwner string
	var leaseUntil sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT dirty_generation, lease_epoch, lease_owner, lease_until
		FROM rollup_work
		WHERE series_id = $1 AND resolution_seconds = $2 AND bucket_start = $3
		FOR UPDATE`, work.SeriesID, work.ResolutionSeconds, work.BucketStart).Scan(&dirtyGeneration, &leaseEpoch, &leaseOwner, &leaseUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read rollup completion fence: %w", err)
	}
	if dirtyGeneration != work.DirtyGeneration || leaseEpoch != work.LeaseEpoch || leaseOwner != work.LeaseOwner || !leaseUntil.Valid || !leaseUntil.Time.After(now) {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO metric_aggregates(series_id, resolution_seconds, bucket_start, count, sum, min, max, expected_count, covered_seconds, bucket_seconds, partial, generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (series_id, resolution_seconds, bucket_start) DO UPDATE SET
		  count = EXCLUDED.count, sum = EXCLUDED.sum, min = EXCLUDED.min, max = EXCLUDED.max,
		  expected_count = EXCLUDED.expected_count, covered_seconds = EXCLUDED.covered_seconds,
		  bucket_seconds = EXCLUDED.bucket_seconds, partial = EXCLUDED.partial,
		  generation = EXCLUDED.generation`, work.SeriesID, work.ResolutionSeconds, work.BucketStart, aggregate.Count, nullableFloat(aggregate.Sum), nullableFloat(aggregate.Min), nullableFloat(aggregate.Max), aggregate.ExpectedCount, aggregate.CoveredSeconds, aggregate.BucketSeconds, aggregate.Partial, aggregate.Generation); err != nil {
		return fmt.Errorf("write metric aggregate: %w", err)
	}
	if work.ResolutionSeconds == RollupResolutionFiveMinute {
		parentStart := work.BucketStart.UTC().Truncate(time.Hour)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO rollup_work(series_id, resolution_seconds, bucket_start, dirty_generation, lease_owner, lease_until, last_error)
			VALUES ($1, $2, $3, 1, '', NULL, NULL)
			ON CONFLICT (series_id, resolution_seconds, bucket_start) DO UPDATE SET
			  dirty_generation = rollup_work.dirty_generation + 1,
			  lease_owner = '', lease_until = NULL, last_error = NULL`, work.SeriesID, RollupResolutionHourly, parentStart); err != nil {
			return fmt.Errorf("dirty hourly rollup parent: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM rollup_work
		WHERE series_id = $1 AND resolution_seconds = $2 AND bucket_start = $3
		  AND dirty_generation = $4 AND lease_epoch = $5 AND lease_owner = $6
		  AND lease_until > $7`, work.SeriesID, work.ResolutionSeconds, work.BucketStart, work.DirtyGeneration, work.LeaseEpoch, work.LeaseOwner, now)
	if err != nil {
		return fmt.Errorf("remove completed rollup work: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metric aggregate: %w", err)
	}
	return nil
}

// FailRollupWork releases a claim without touching aggregate history. The
// epoch and generation predicate prevents an old worker from overwriting a
// newer lease's diagnostic.
func (s *Store) FailRollupWork(ctx context.Context, work RollupWorkItem, reason string) error {
	if err := s.requireRollupAuthority(ctx); err != nil {
		return err
	}
	if err := validateRollupWorkItem(work); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "rollup computation failed"
	}
	if len(reason) > 512 {
		reason = reason[:512]
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE rollup_work
		SET lease_owner = '', lease_until = NULL, last_error = $1
		WHERE series_id = $2 AND resolution_seconds = $3 AND bucket_start = $4
		  AND dirty_generation = $5 AND lease_epoch = $6 AND lease_owner = $7`, reason, work.SeriesID, work.ResolutionSeconds, work.BucketStart, work.DirtyGeneration, work.LeaseEpoch, work.LeaseOwner)
	if err != nil {
		return fmt.Errorf("fail rollup work: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrConflict
	}
	return nil
}

// BackfillRollupWork discovers only retained raw samples in [from,to) and
// creates both source-tier and hourly-parent work. The limit bounds distinct
// source buckets discovered in this invocation; repeated calls are safe and
// increment the same generation instead of appending duplicate jobs.
func (s *Store) BackfillRollupWork(ctx context.Context, from, to time.Time, limit int) (int, error) {
	if err := s.requireRollupAuthority(ctx); err != nil {
		return 0, err
	}
	limit, err := normalizeRollupLimit(limit)
	if err != nil {
		return 0, err
	}
	from, to = from.UTC(), to.UTC()
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return 0, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
		WITH selected AS (
			SELECT DISTINCT series_id,
			       date_bin('5 minutes', observed_at, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket_start
			FROM metric_samples
			WHERE series_id IS NOT NULL AND series_id <> ''
			  AND observed_at >= $1 AND observed_at < $2
			ORDER BY series_id, bucket_start
			LIMIT $3
		), work_items AS (
			SELECT series_id, $4::integer AS resolution_seconds, bucket_start
			FROM selected
			UNION
			SELECT series_id, $5::integer AS resolution_seconds,
			       date_trunc('hour', bucket_start)
			FROM selected
		)
		INSERT INTO rollup_work(series_id, resolution_seconds, bucket_start, dirty_generation, lease_owner, lease_until, last_error)
		SELECT series_id, resolution_seconds, bucket_start, 1, '', NULL, NULL
		FROM work_items
		ON CONFLICT (series_id, resolution_seconds, bucket_start) DO UPDATE SET
		  dirty_generation = rollup_work.dirty_generation + 1,
		  lease_owner = '', lease_until = NULL, last_error = NULL`, from, to, limit, RollupResolutionFiveMinute, RollupResolutionHourly)
	if err != nil {
		return 0, fmt.Errorf("enqueue rollup backfill: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit rollup backfill: %w", err)
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *Store) GetMetricAggregate(ctx context.Context, seriesID string, resolution int, bucketStart time.Time) (MetricAggregate, error) {
	if err := s.requireRollupAuthority(ctx); err != nil {
		return MetricAggregate{}, err
	}
	if seriesID == "" || bucketStart.IsZero() {
		return MetricAggregate{}, ErrInvalid
	}
	if _, err := rollupBucketDuration(resolution); err != nil {
		return MetricAggregate{}, err
	}
	var aggregate MetricAggregate
	var sum, minValue, maxValue sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT series_id, resolution_seconds, bucket_start, count, sum, min, max,
		       expected_count, covered_seconds, bucket_seconds, partial, generation
		FROM metric_aggregates
		WHERE series_id = $1 AND resolution_seconds = $2 AND bucket_start = $3`, seriesID, resolution, bucketStart.UTC()).Scan(&aggregate.SeriesID, &aggregate.ResolutionSeconds, &aggregate.BucketStart, &aggregate.Count, &sum, &minValue, &maxValue, &aggregate.ExpectedCount, &aggregate.CoveredSeconds, &aggregate.BucketSeconds, &aggregate.Partial, &aggregate.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return MetricAggregate{}, ErrNotFound
	}
	if err != nil {
		return MetricAggregate{}, fmt.Errorf("read metric aggregate: %w", err)
	}
	if sum.Valid {
		aggregate.Sum = floatPointer(sum.Float64)
	}
	if minValue.Valid {
		aggregate.Min = floatPointer(minValue.Float64)
	}
	if maxValue.Valid {
		aggregate.Max = floatPointer(maxValue.Float64)
	}
	aggregate.BucketStart = aggregate.BucketStart.UTC()
	return aggregate, nil
}
