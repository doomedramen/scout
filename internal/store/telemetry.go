package store

import (
	"context"
	"sort"
	"time"
)

type BatchResult struct {
	AcceptedAt time.Time `json:"acceptedAt"`
	Duplicate  bool      `json:"duplicate"`
}

type TelemetryStatus struct {
	Samples         int        `json:"samples"`
	DroppedSamples  int64      `json:"droppedSamples"`
	MaxSamples      int        `json:"maxSamples"`
	Backpressure    bool       `json:"backpressure"`
	RetentionHours  int        `json:"retentionHours"`
	LastRetentionAt *time.Time `json:"lastRetentionAt,omitempty"`
}

func (s *Store) IngestBatch(ctx context.Context, agentID, bootID, batchID, payloadHash string, samples []MetricSample, observations []Observation, dropped int) (BatchResult, error) {
	var result BatchResult
	err := s.mutate(ctx, func(state *State) error {
		if state.Workspace.TelemetryBackpressure {
			return ErrBackpressure
		}
		agent, ok := state.Agents[agentID]
		if !ok {
			return ErrUnauthorized
		}
		if agent.RevokedAt != nil {
			return ErrRevoked
		}
		key := batchReceiptKey(agentID, bootID, batchID)
		now := s.now().UTC()
		if old, exists := state.BatchReceipts[key]; exists {
			if old != payloadHash {
				return ErrConflict
			}
			result = BatchResult{AcceptedAt: now, Duplicate: true}
			return nil
		}
		device, ok := state.Devices[agent.DeviceID]
		if !ok {
			return ErrNotFound
		}
		if device.DecommissionedAt != nil || device.Lifecycle == "decommissioned" {
			return ErrRevoked
		}
		if state.Workspace.MaxSamples > 0 && len(state.Samples)+len(samples) > state.Workspace.MaxSamples {
			state.Workspace.TelemetryBackpressure = true
			return ErrBackpressure
		}
		state.BatchReceipts[key] = payloadHash
		for _, sample := range samples {
			sample.ID = NewID()
			sample.DeviceID = agent.DeviceID
			sample.AgentID = agentID
			if sample.ReceivedAt.IsZero() {
				sample.ReceivedAt = now
			}
			if sample.Labels == nil {
				sample.Labels = map[string]string{}
			}
			sample.Labels = cloneMap(sample.Labels)
			state.Samples = append(state.Samples, sample)
			if device.CurrentMetrics == nil {
				device.CurrentMetrics = map[string]MetricSample{}
			}
			previous, has := device.CurrentMetrics[sample.Metric]
			if !has || sample.ObservedAt.After(previous.ObservedAt) {
				device.CurrentMetrics[sample.Metric] = sample
			}
			if device.MetricFreshness == nil {
				device.MetricFreshness = map[string]Freshness{}
			}
			device.MetricFreshness[sample.Metric] = sample.Availability
		}
		for _, observation := range observations {
			observation.ID = NewID()
			observation.ReporterID = agentID
			if observation.ReceivedAt.IsZero() {
				observation.ReceivedAt = now
			}
			state.Observations[observation.ID] = observation
		}
		device.AgentVersion = agent.InstalledVersion
		state.Devices[agent.DeviceID] = device
		_ = dropped
		result = BatchResult{AcceptedAt: now}
		return nil
	})
	return result, err
}

type Heartbeat struct {
	BootID           string                     `json:"bootId"`
	InstalledVersion string                     `json:"installedVersion"`
	UptimeSeconds    int64                      `json:"uptimeSeconds"`
	CollectorStates  []CollectorDescriptorState `json:"collectorStates"`
	UpdateState      map[string]string          `json:"updateState"`
}

func (s *Store) RecordHeartbeat(ctx context.Context, agentID string, heartbeat Heartbeat) (time.Time, int64, error) {
	now := s.now().UTC()
	var revision int64
	err := s.mutate(ctx, func(state *State) error {
		agent, ok := state.Agents[agentID]
		if !ok {
			return ErrUnauthorized
		}
		if agent.RevokedAt != nil {
			return ErrRevoked
		}
		device, ok := state.Devices[agent.DeviceID]
		if !ok {
			return ErrNotFound
		}
		if device.DecommissionedAt != nil {
			return ErrRevoked
		}
		device.LastHeartbeat = &now
		device.Availability = AvailabilityOnline
		if heartbeat.InstalledVersion != "" {
			device.AgentVersion = heartbeat.InstalledVersion
			agent.InstalledVersion = heartbeat.InstalledVersion
		}
		device.CollectorStates = append([]CollectorDescriptorState(nil), heartbeat.CollectorStates...)
		state.Devices[agent.DeviceID] = device
		state.Agents[agentID] = agent
		revision = scopeRevision(state)
		return nil
	})
	return now, revision, err
}

func scopeRevision(state *State) int64 {
	var result int64
	for _, scope := range state.Scopes {
		if scope.Revision > result {
			result = scope.Revision
		}
	}
	return result
}

type MetricQuery struct {
	DeviceID  string
	From      time.Time
	To        time.Time
	Metric    string
	EntityID  string
	MaxPoints int
}

type MetricPoint struct {
	ObservedAt   time.Time `json:"observedAt"`
	Value        *float64  `json:"value"`
	Min          *float64  `json:"min,omitempty"`
	Max          *float64  `json:"max,omitempty"`
	Availability Freshness `json:"availability"`
}

type MetricSeries struct {
	Metric string        `json:"metric"`
	Unit   string        `json:"unit"`
	Points []MetricPoint `json:"points"`
}

func (s *Store) QueryMetrics(ctx context.Context, query MetricQuery) ([]MetricSeries, error) {
	if query.MaxPoints <= 0 {
		query.MaxPoints = 600
	}
	if query.MaxPoints > 600 {
		return nil, ErrInvalid
	}
	result := []MetricSeries{}
	err := s.read(ctx, func(state *State) error {
		if _, ok := state.Devices[query.DeviceID]; !ok {
			return ErrNotFound
		}
		buckets := map[string][]MetricSample{}
		for _, sample := range state.Samples {
			if sample.DeviceID != query.DeviceID {
				continue
			}
			if query.Metric != "" && sample.Metric != query.Metric {
				continue
			}
			if query.EntityID != "" && sample.EntityID != query.EntityID {
				continue
			}
			if !query.From.IsZero() && sample.ObservedAt.Before(query.From) {
				continue
			}
			if !query.To.IsZero() && sample.ObservedAt.After(query.To) {
				continue
			}
			buckets[sample.Metric] = append(buckets[sample.Metric], sample)
		}
		keys := make([]string, 0, len(buckets))
		for key := range buckets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			items := buckets[key]
			sort.Slice(items, func(i, j int) bool { return items[i].ObservedAt.Before(items[j].ObservedAt) })
			series := MetricSeries{Metric: key, Unit: items[0].Unit}
			if len(items) <= query.MaxPoints {
				for _, item := range items {
					series.Points = append(series.Points, MetricPoint{ObservedAt: item.ObservedAt, Value: item.Value, Availability: item.Availability})
				}
			} else {
				step := (len(items) + query.MaxPoints - 1) / query.MaxPoints
				for start := 0; start < len(items); start += step {
					end := start + step
					if end > len(items) {
						end = len(items)
					}
					var minValue, maxValue *float64
					var last *MetricSample
					for i := start; i < end; i++ {
						item := items[i]
						if item.Availability != FreshnessCurrent || item.Value == nil {
							continue
						}
						if minValue == nil || *item.Value < *minValue {
							v := *item.Value
							minValue = &v
						}
						if maxValue == nil || *item.Value > *maxValue {
							v := *item.Value
							maxValue = &v
						}
						last = &item
					}
					point := MetricPoint{ObservedAt: items[start].ObservedAt, Min: minValue, Max: maxValue, Availability: FreshnessCurrent}
					if last != nil {
						point.Value = last.Value
					}
					if minValue == nil {
						point.Availability = items[start].Availability
					}
					series.Points = append(series.Points, point)
				}
			}
			result = append(result, series)
		}
		return nil
	})
	return result, err
}

func (s *Store) PruneSamples(ctx context.Context, before time.Time) (int, error) {
	removed := 0
	err := s.mutate(ctx, func(state *State) error {
		kept := state.Samples[:0]
		for _, item := range state.Samples {
			if item.ReceivedAt.Before(before) {
				removed++
				continue
			}
			kept = append(kept, item)
		}
		state.Samples = kept
		return nil
	})
	return removed, err
}

func (s *Store) EnforceSampleLimit(ctx context.Context, maxSamples int) (int, error) {
	if maxSamples < 1 {
		return 0, ErrInvalid
	}
	removed := 0
	err := s.mutate(ctx, func(state *State) error {
		if len(state.Samples) <= maxSamples {
			return nil
		}
		sort.SliceStable(state.Samples, func(i, j int) bool { return state.Samples[i].ReceivedAt.Before(state.Samples[j].ReceivedAt) })
		removed = len(state.Samples) - maxSamples
		state.Samples = append([]MetricSample(nil), state.Samples[removed:]...)
		state.Workspace.DroppedSamples += int64(removed)
		return nil
	})
	return removed, err
}

func (s *Store) CleanupTelemetry(ctx context.Context, now time.Time) (int, error) {
	removed := 0
	err := s.mutate(ctx, func(state *State) error {
		keptSamples := state.Samples[:0]
		for _, sample := range state.Samples {
			if _, agentOK := state.Agents[sample.AgentID]; !agentOK {
				removed++
				continue
			}
			if _, deviceOK := state.Devices[sample.DeviceID]; !deviceOK {
				removed++
				continue
			}
			keptSamples = append(keptSamples, sample)
		}
		state.Samples = keptSamples
		for id, observation := range state.Observations {
			if !observation.ExpiresAt.IsZero() && observation.ExpiresAt.Before(now) {
				delete(state.Observations, id)
			}
		}
		if state.Workspace.TelemetryBackpressure && state.Workspace.MaxSamples > 0 && len(state.Samples) < state.Workspace.MaxSamples*9/10 {
			state.Workspace.TelemetryBackpressure = false
		}
		return nil
	})
	return removed, err
}

func (s *Store) SetTelemetryBackpressure(ctx context.Context, enabled bool) error {
	return s.mutate(ctx, func(state *State) error {
		state.Workspace.TelemetryBackpressure = enabled
		return nil
	})
}

func (s *Store) TelemetryStatus(ctx context.Context) (TelemetryStatus, error) {
	var result TelemetryStatus
	err := s.read(ctx, func(state *State) error {
		result = TelemetryStatus{Samples: len(state.Samples), DroppedSamples: state.Workspace.DroppedSamples, MaxSamples: state.Workspace.MaxSamples, Backpressure: state.Workspace.TelemetryBackpressure, RetentionHours: state.Workspace.RetentionHours, LastRetentionAt: cloneTime(state.Workspace.LastRetentionAt)}
		return nil
	})
	return result, err
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func (s *Store) StoreObservation(ctx context.Context, observation Observation) error {
	return s.mutate(ctx, func(state *State) error {
		if observation.ID == "" {
			observation.ID = NewID()
		}
		if observation.ReceivedAt.IsZero() {
			observation.ReceivedAt = s.now().UTC()
		}
		state.Observations[observation.ID] = observation
		return nil
	})
}
func (s *Store) ListObservations(ctx context.Context) ([]Observation, error) {
	result := []Observation{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Observations {
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].ObservedAt.After(result[j].ObservedAt) })
		return nil
	})
	return result, err
}
