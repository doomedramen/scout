package telemetry

import (
	"context"

	"scout.local/scout/internal/store"
)

type RetentionPolicy struct {
	Hours          int
	MaxSamples     int
	RawDays        int
	FiveMinuteDays int
	HourlyDays     int
}

type RetentionReport struct {
	ExpiredSamples        int64                 `json:"expiredSamples"`
	ExpiredFiveMinuteRows int64                 `json:"expiredFiveMinuteRows"`
	ExpiredHourlyRows     int64                 `json:"expiredHourlyRows"`
	DeferredSamples       int64                 `json:"deferredSamples"`
	OrphanedItems         int                   `json:"orphanedItems"`
	LimitedSamples        int                   `json:"limitedSamples"`
	Status                store.TelemetryStatus `json:"status"`
}

func (s *Service) EnforceRetention(ctx context.Context, policy RetentionPolicy) (RetentionReport, error) {
	if s == nil || s.Store == nil {
		return RetentionReport{}, store.ErrInvalid
	}
	settings, err := s.Store.GetMonitoringSettings(ctx)
	if err != nil {
		return RetentionReport{}, err
	}
	if policy.RawDays == 0 {
		if policy.Hours > 0 {
			policy.RawDays = policy.Hours / 24
		} else {
			policy.RawDays = settings.Retention.RawDays
		}
	}
	if policy.FiveMinuteDays == 0 {
		policy.FiveMinuteDays = settings.Retention.FiveMinuteDays
	}
	if policy.HourlyDays == 0 {
		policy.HourlyDays = settings.Retention.HourlyDays
	}
	if policy.MaxSamples == 0 {
		workspace, workspaceErr := s.Store.Workspace(ctx)
		if workspaceErr != nil {
			return RetentionReport{}, workspaceErr
		}
		policy.MaxSamples = workspace.MaxSamples
	}
	now := s.clock()
	retained, err := s.Store.RetainTelemetry(ctx, store.RetentionSettings{RawDays: policy.RawDays, FiveMinuteDays: policy.FiveMinuteDays, HourlyDays: policy.HourlyDays}, now)
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
	if err := s.Store.RecordMonitoringJobSuccess(ctx, "retention", now); err != nil {
		return RetentionReport{}, err
	}
	status, err := s.Store.TelemetryStatus(ctx)
	if err != nil {
		return RetentionReport{}, err
	}
	return RetentionReport{ExpiredSamples: retained.RawDeleted, ExpiredFiveMinuteRows: retained.FiveMinuteDeleted, ExpiredHourlyRows: retained.HourlyDeleted, DeferredSamples: retained.RawDeferred, OrphanedItems: orphaned, LimitedSamples: limited, Status: status}, nil
}
