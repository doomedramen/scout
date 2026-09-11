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

func (s *Store) IngestBatch(ctx context.Context, agentID, bootID, batchID, payloadHash string, samples []MetricSample, observations []Observation, dropped int) (BatchResult, error) {
	var result BatchResult
	err := s.mutate(ctx, func(state *State) error {
		agent, ok := state.Agents[agentID]
		if !ok {
			return ErrUnauthorized
		}
		if agent.RevokedAt != nil {
			return ErrRevoked
		}
		key := agentID + "\x00" + bootID + "\x00" + batchID
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
