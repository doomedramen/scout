package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"scout.local/scout/internal/store"
)

const (
	defaultRollupOwner       = "scout-telemetry-rollup"
	defaultRollupLimit       = store.MaxRollupWorkItems
	defaultRollupLease       = 30 * time.Second
	defaultRollupMaxDuration = 5 * time.Second
)

// RollupPolicy bounds one worker cycle. A scheduler can call RunRollups once
// per minute; the limits keep a slow or unexpectedly large backlog from
// monopolizing the control process.
type RollupPolicy struct {
	Owner       string
	Limit       int
	Lease       time.Duration
	MaxDuration time.Duration
}

type RollupReport struct {
	Claimed     int        `json:"claimed"`
	Completed   int        `json:"completed"`
	Deferred    int        `json:"deferred"`
	Failed      int        `json:"failed"`
	Stale       int        `json:"stale"`
	LastSuccess *time.Time `json:"lastSuccess,omitempty"`
}

func normalizeRollupPolicy(policy RollupPolicy) RollupPolicy {
	if policy.Owner == "" {
		policy.Owner = defaultRollupOwner
	}
	if policy.Limit <= 0 {
		policy.Limit = defaultRollupLimit
	}
	if policy.Lease <= 0 {
		policy.Lease = defaultRollupLease
	}
	if policy.MaxDuration <= 0 {
		policy.MaxDuration = defaultRollupMaxDuration
	}
	return policy
}

// RunRollups processes one bounded leased batch. Computation failures are
// recorded on their work row and do not prevent independent series from
// making progress. A source-pending hourly row is released with a diagnostic
// and retried after its five-minute children complete.
func (s *Service) RunRollups(ctx context.Context, policy RollupPolicy) (RollupReport, error) {
	if s == nil || s.Store == nil {
		return RollupReport{}, store.ErrInvalid
	}
	policy = normalizeRollupPolicy(policy)
	started := time.Now()
	work, err := s.Store.ClaimRollupWork(ctx, policy.Owner, policy.Limit, policy.Lease)
	if err != nil {
		return RollupReport{}, err
	}
	report := RollupReport{Claimed: len(work)}
	for _, item := range work {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if time.Since(started) >= policy.MaxDuration {
			break
		}
		aggregate, computeErr := s.Store.ComputeRollupAggregate(ctx, item)
		if errors.Is(computeErr, store.ErrRollupSourcePending) {
			if failErr := s.Store.FailRollupWork(ctx, item, "source aggregate pending"); failErr != nil {
				return report, fmt.Errorf("release pending rollup %s: %w", item.SeriesID, failErr)
			}
			report.Deferred++
			continue
		}
		if computeErr != nil {
			if failErr := s.Store.FailRollupWork(ctx, item, safeRollupError(computeErr)); failErr != nil {
				return report, fmt.Errorf("record rollup failure for %s: %w", item.SeriesID, failErr)
			}
			report.Failed++
			continue
		}
		if completeErr := s.Store.CompleteRollupWork(ctx, item, aggregate); errors.Is(completeErr, store.ErrConflict) {
			report.Stale++
			continue
		} else if completeErr != nil {
			return report, fmt.Errorf("complete rollup for %s: %w", item.SeriesID, completeErr)
		}
		report.Completed++
		success := time.Now().UTC()
		report.LastSuccess = &success
	}
	return report, nil
}

func safeRollupError(err error) string {
	if err == nil {
		return "rollup computation failed"
	}
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

// Backfill enqueues a bounded range of retained raw telemetry for both
// resolutions. It does not manufacture work for dates with no retained
// samples and leaves retention policy enforcement to the caller.
func (s *Service) Backfill(ctx context.Context, from, to time.Time, limit int) (int, error) {
	if s == nil || s.Store == nil {
		return 0, store.ErrInvalid
	}
	return s.Store.BackfillRollupWork(ctx, from, to, limit)
}
