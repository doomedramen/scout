package telemetry

import (
	"context"
	"time"

	"scout.local/scout/internal/store"
)

type Service struct {
	Store *store.Store
	Now   func() time.Time
}

type Heartbeat struct {
	BootID           string
	InstalledVersion string
	UptimeSeconds    int64
	CollectorStates  []store.CollectorDescriptorState
	UpdateState      map[string]string
	Capabilities     store.ScanCapabilities
}

func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) Ingest(ctx context.Context, agentID string, batch Batch, encoded []byte) (store.BatchResult, error) {
	if s == nil || s.Store == nil {
		return store.BatchResult{}, store.ErrInvalid
	}
	now := s.clock()
	if err := ValidateBatch(batch, now); err != nil {
		return store.BatchResult{}, err
	}
	hash, _, err := CanonicalHash(batch)
	if err != nil {
		return store.BatchResult{}, err
	}
	samples, observations := ToStore(batch, agentID, now)
	for i := range samples {
		if samples[i].ObservedAt.After(now.Add(5 * time.Minute)) {
			samples[i].Availability = store.FreshnessUnavailable
			samples[i].Value = nil
		}
	}
	_ = encoded
	return s.Store.IngestBatch(ctx, agentID, batch.BootID, batch.BatchID, hash, samples, observations, batch.DroppedCount)
}

func (s *Service) Heartbeat(ctx context.Context, agentID string, heartbeat Heartbeat) (time.Time, int64, error) {
	if s == nil || s.Store == nil {
		return time.Time{}, 0, store.ErrInvalid
	}
	if heartbeat.BootID == "" || heartbeat.UptimeSeconds < 0 {
		return time.Time{}, 0, store.ErrInvalid
	}
	return s.Store.RecordHeartbeat(ctx, agentID, store.Heartbeat{BootID: heartbeat.BootID, InstalledVersion: heartbeat.InstalledVersion, UptimeSeconds: heartbeat.UptimeSeconds, CollectorStates: heartbeat.CollectorStates, UpdateState: heartbeat.UpdateState, Capabilities: heartbeat.Capabilities})
}
