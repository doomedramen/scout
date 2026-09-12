package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMonitoringPolicyPreviewCASAndStatus(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	repository := NewMemory()
	repository.SetClock(func() time.Time { return now })

	settings, err := repository.GetMonitoringSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantRetention := RetentionSettings{RawDays: DefaultRawRetentionDays, FiveMinuteDays: DefaultFiveMinuteRetentionDays, HourlyDays: DefaultHourlyRetentionDays}
	if settings.Revision != 1 || settings.Retention != wantRetention || settings.DiskBudgetBytes != DefaultTelemetryBudgetBytes {
		t.Fatalf("unexpected default monitoring settings: %+v", settings)
	}

	_, agent := createTelemetryFixture(t, ctx, repository)
	oldValue, currentValue := 10.0, 20.0
	if _, err := repository.IngestBatch(ctx, agent.ID, "policy-boot", "policy-batch", "policy-hash", []MetricSample{
		{CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", Value: &oldValue, Availability: FreshnessCurrent, ObservedAt: now.Add(-2 * 24 * time.Hour), ReceivedAt: now.Add(-2 * 24 * time.Hour)},
		{CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", Value: &currentValue, Availability: FreshnessCurrent, ObservedAt: now.Add(-time.Hour), ReceivedAt: now.Add(-time.Hour)},
	}, nil, 3); err != nil {
		t.Fatal(err)
	}

	previewInput := RetentionPreviewInput{ExpectedRevision: settings.Revision, Retention: RetentionSettings{RawDays: 1, FiveMinuteDays: 90, HourlyDays: 365}, IdempotencyKey: "policy-preview"}
	preview, err := repository.CreateRetentionPreview(ctx, previewInput)
	if err != nil {
		t.Fatal(err)
	}
	if preview.PreviewID == "" || preview.EstimatedRows != 1 || !preview.Irreversible || len(preview.AffectedRanges) != 1 {
		t.Fatalf("unexpected retention preview: %+v", preview)
	}
	repeated, err := repository.CreateRetentionPreview(ctx, previewInput)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.PreviewID != preview.PreviewID {
		t.Fatalf("idempotency key created a second preview: first=%+v repeated=%+v", preview, repeated)
	}

	patch := MonitoringSettingsPatch{ExpectedRevision: settings.Revision, Retention: &MonitoringRetentionPatch{RawDays: intPointer(1)}}
	if _, err := repository.UpdateMonitoringSettings(ctx, patch); !errors.Is(err, ErrConflict) {
		t.Fatalf("retention reduction without preview returned %v, want conflict", err)
	}
	patch.RetentionPreviewID = preview.PreviewID
	updated, err := repository.UpdateMonitoringSettings(ctx, patch)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Retention.RawDays != 1 {
		t.Fatalf("retention policy was not revised: %+v", updated)
	}
	if _, err := repository.UpdateMonitoringSettings(ctx, patch); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale policy revision returned %v, want conflict", err)
	}

	retentionResult, err := repository.RetainTelemetry(ctx, updated.Retention, now)
	if err != nil {
		t.Fatal(err)
	}
	if retentionResult.RawDeleted != 1 {
		t.Fatalf("retention removed %d raw samples, want 1", retentionResult.RawDeleted)
	}
	status, err := repository.MonitoringStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Counters.Dropped != 3 || status.Counters.Backpressure != 0 || status.StoragePressure.State != "normal" {
		t.Fatalf("unexpected monitoring status counters/pressure: %+v", status)
	}
	if status.LastSuccessfulJobs["retention"] == nil {
		t.Fatalf("retention job success was not recorded: %+v", status.LastSuccessfulJobs)
	}
}

func intPointer(value int) *int {
	return &value
}
