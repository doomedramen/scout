package telemetry

import (
	"context"
	"time"

	"scout.local/scout/internal/store"
)

type RetentionPolicy struct {
	Hours      int
	MaxSamples int
}

type RetentionReport struct {
	ExpiredSamples int                   `json:"expiredSamples"`
	OrphanedItems  int                   `json:"orphanedItems"`
	LimitedSamples int                   `json:"limitedSamples"`
	Status         store.TelemetryStatus `json:"status"`
}

func (s *Service) EnforceRetention(ctx context.Context, policy RetentionPolicy) (RetentionReport, error) {
	if s == nil || s.Store == nil {
		return RetentionReport{}, store.ErrInvalid
	}
	workspace, err := s.Store.Workspace(ctx)
	if err != nil {
		return RetentionReport{}, err
	}
	if policy.Hours == 0 {
		policy.Hours = workspace.RetentionHours
	}
	if policy.MaxSamples == 0 {
		policy.MaxSamples = workspace.MaxSamples
	}
	if policy.Hours < 1 || policy.MaxSamples < 0 {
		return RetentionReport{}, store.ErrInvalid
	}
	now := s.clock()
	expired, err := s.Store.PruneSamples(ctx, now.Add(-time.Duration(policy.Hours)*time.Hour))
	if err != nil {
		return RetentionReport{}, err
	}
	orphaned, err := s.Store.CleanupTelemetry(ctx, now)
	if err != nil {
		return RetentionReport{}, err
	}
	limited := 0
	if policy.MaxSamples > 0 {
		limited, err = s.Store.EnforceSampleLimit(ctx, policy.MaxSamples)
		if err != nil {
			return RetentionReport{}, err
		}
	}
	when := now
	if _, err := s.Store.SetWorkspace(ctx, func(state *store.WorkspaceState) error {
		state.RetentionHours = policy.Hours
		state.MaxSamples = policy.MaxSamples
		state.LastRetentionAt = &when
		return nil
	}); err != nil {
		return RetentionReport{}, err
	}
	status, err := s.Store.TelemetryStatus(ctx)
	if err != nil {
		return RetentionReport{}, err
	}
	return RetentionReport{ExpiredSamples: expired, OrphanedItems: orphaned, LimitedSamples: limited, Status: status}, nil
}
