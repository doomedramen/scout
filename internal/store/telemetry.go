package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	UsedBytes       int64      `json:"usedBytes"`
	BudgetBytes     int64      `json:"budgetBytes"`
	Backpressure    bool       `json:"backpressure"`
	RetentionHours  int        `json:"retentionHours"`
	LastRetentionAt *time.Time `json:"lastRetentionAt,omitempty"`
}

func (s *Store) IngestBatch(ctx context.Context, agentID, bootID, batchID, payloadHash string, samples []MetricSample, observations []Observation, dropped int) (BatchResult, error) {
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return BatchResult{}, err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.ingestBatchSQL(ctx, agentID, bootID, batchID, payloadHash, samples, observations, dropped)
		}
		if phase == MonitoringStorageImporting {
			return BatchResult{}, fmt.Errorf("telemetry ingestion is paused during migration: %w", ErrConflict)
		}
	}
	return s.ingestBatchMemory(ctx, agentID, bootID, batchID, payloadHash, samples, observations, dropped)
}

func (s *Store) ingestBatchMemory(ctx context.Context, agentID, bootID, batchID, payloadHash string, samples []MetricSample, observations []Observation, dropped int) (BatchResult, error) {
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
			state.Workspace.TelemetryBackpressureCount++
			return ErrBackpressure
		}
		if state.Workspace.TelemetryBudgetBytes > 0 {
			used := memoryTelemetryBytes(*state)
			if !telemetryBudgetHasRoom(used, state.Workspace.TelemetryBudgetBytes, memoryTelemetryEstimate(len(samples), len(observations))) {
				state.Workspace.TelemetryBackpressure = true
				state.Workspace.TelemetryBackpressureCount++
				return ErrBackpressure
			}
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
			currentKey := sample.Metric
			if sample.EntityID != "" && sample.EntityID != "host" {
				currentKey = sample.EntityID + ":" + sample.Metric
			}
			previous, has := device.CurrentMetrics[currentKey]
			if !has || sample.ObservedAt.After(previous.ObservedAt) {
				device.CurrentMetrics[currentKey] = sample
			}
			if device.MetricFreshness == nil {
				device.MetricFreshness = map[string]Freshness{}
			}
			device.MetricFreshness[currentKey] = sample.Availability
			if sample.EntityID != "" {
				if err := markAlertWorkState(state, AlertWorkAllLineages, sample.EntityID, now); err != nil {
					return err
				}
			}
		}
		for _, observation := range observations {
			observation.ID = NewID()
			observation.ReporterID = agentID
			if observation.ReceivedAt.IsZero() {
				observation.ReceivedAt = now
			}
			state.Observations[observation.ID] = observation
			if observation.SubjectID != "" {
				if err := markAlertWorkState(state, AlertWorkAllLineages, observation.SubjectID, now); err != nil {
					return err
				}
			}
		}
		device.AgentVersion = agent.InstalledVersion
		state.Devices[agent.DeviceID] = device
		if dropped > 0 {
			state.Workspace.DroppedSamples += int64(dropped)
		}
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
	Capabilities     ScanCapabilities           `json:"capabilities,omitempty"`
}

func ValidateScanCapabilities(capabilities ScanCapabilities) error {
	if len(capabilities.ScanProtocolVersions) > 8 || len(capabilities.ScanTransports) > 8 {
		return ErrInvalid
	}
	seenVersions := map[int]bool{}
	for _, version := range capabilities.ScanProtocolVersions {
		if version < 1 || version > 16 || seenVersions[version] {
			return ErrInvalid
		}
		seenVersions[version] = true
	}
	seenTransports := map[string]bool{}
	for _, transport := range capabilities.ScanTransports {
		transport = strings.TrimSpace(transport)
		if transport == "" || len(transport) > 16 || seenTransports[transport] {
			return ErrInvalid
		}
		seenTransports[transport] = true
	}
	return nil
}

func (s *Store) RecordHeartbeat(ctx context.Context, agentID string, heartbeat Heartbeat) (time.Time, int64, error) {
	if err := ValidateScanCapabilities(heartbeat.Capabilities); err != nil {
		return time.Time{}, 0, err
	}
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
		agent.Capabilities = cloneScanCapabilities(heartbeat.Capabilities)
		device.CollectorStates = append([]CollectorDescriptorState(nil), heartbeat.CollectorStates...)
		for _, collector := range heartbeat.CollectorStates {
			if strings.Contains(strings.ToLower(collector.Diagnostic), "truncated") {
				state.Workspace.TelemetryTruncated++
			}
		}
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
	SeriesIDs []string
	MaxPoints int
}

type MetricPoint struct {
	ObservedAt   time.Time `json:"observedAt"`
	Value        *float64  `json:"value"`
	Min          *float64  `json:"min"`
	Max          *float64  `json:"max"`
	Availability Freshness `json:"availability"`
	Count        int64     `json:"count"`
	Coverage     float64   `json:"coverage"`
	Partial      bool      `json:"partial"`
}

type MetricSeries struct {
	SeriesID          string        `json:"seriesId"`
	EntityID          string        `json:"entityId"`
	Metric            string        `json:"metric"`
	Unit              string        `json:"unit"`
	ResolutionSeconds int           `json:"resolutionSeconds"`
	Points            []MetricPoint `json:"points"`
}

const (
	MaxMetricHistoryPoints  = 600
	MaxMetricHistorySeries  = 16
	metricRawRetention      = 30 * 24 * time.Hour
	metricFiveMinuteAge     = 90 * 24 * time.Hour
	metricHourlyAge         = 365 * 24 * time.Hour
	metricHistoryMaxRange   = 365 * 24 * time.Hour
	metricHistoryDefaultAge = 30 * 24 * time.Hour
)

type metricHistoryTier string

const (
	metricHistoryTierRaw        metricHistoryTier = "raw"
	metricHistoryTierFiveMinute metricHistoryTier = "five-minute"
	metricHistoryTierHourly     metricHistoryTier = "hourly"
)

type metricHistoryDescriptor struct {
	series         MetricSeries
	sourceSeconds  int
	filterMismatch bool
}

type metricHistoryRaw struct {
	seriesID        string
	entityID        string
	metric          string
	unit            string
	value           *float64
	availability    Freshness
	observedAt      time.Time
	receivedAt      time.Time
	intervalSeconds int
}

type metricHistoryBucket struct {
	start        time.Time
	end          time.Time
	count        int64
	sum          float64
	min          *float64
	max          *float64
	covered      time.Duration
	intervals    []rollupInterval
	availability Freshness
	partial      bool
}

type metricHistoryPlan struct {
	start time.Time
	end   time.Time
	width time.Duration
	count int
	from  time.Time
	to    time.Time
}

// NormalizeMetricQuery applies the history contract's bounded defaults. It is
// exported so the HTTP layer can return the exact effective request bounds in
// its response metadata rather than silently hiding defaulted limits.
func NormalizeMetricQuery(query MetricQuery, now time.Time) (MetricQuery, error) {
	query.DeviceID = strings.TrimSpace(query.DeviceID)
	query.Metric = strings.TrimSpace(query.Metric)
	query.EntityID = strings.TrimSpace(query.EntityID)
	if query.DeviceID == "" {
		return MetricQuery{}, ErrInvalid
	}
	if query.MaxPoints == 0 {
		query.MaxPoints = MaxMetricHistoryPoints
	}
	if query.MaxPoints < 2 || query.MaxPoints > MaxMetricHistoryPoints {
		return MetricQuery{}, ErrInvalid
	}
	if len(query.SeriesIDs) > MaxMetricHistorySeries {
		return MetricQuery{}, ErrInvalid
	}
	for index, seriesID := range query.SeriesIDs {
		seriesID = strings.TrimSpace(seriesID)
		if seriesID == "" || len(seriesID) > 128 {
			return MetricQuery{}, ErrInvalid
		}
		query.SeriesIDs[index] = seriesID
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if !query.To.IsZero() {
		query.To = query.To.UTC()
	} else {
		query.To = now
	}
	if !query.From.IsZero() {
		query.From = query.From.UTC()
	} else {
		query.From = query.To.Add(-metricHistoryDefaultAge)
	}
	if !query.From.Before(query.To) || query.To.Sub(query.From) > metricHistoryMaxRange {
		return MetricQuery{}, ErrInvalid
	}
	return query, nil
}

func chooseMetricHistoryTier(from, now time.Time) metricHistoryTier {
	from, now = from.UTC(), now.UTC()
	age := now.Sub(from)
	if age <= metricRawRetention {
		return metricHistoryTierRaw
	}
	if age <= metricFiveMinuteAge {
		return metricHistoryTierFiveMinute
	}
	return metricHistoryTierHourly
}

func metricHistoryTierSeconds(tier metricHistoryTier) int {
	switch tier {
	case metricHistoryTierFiveMinute:
		return RollupResolutionFiveMinute
	case metricHistoryTierHourly:
		return RollupResolutionHourly
	default:
		return defaultMetricIntervalSeconds
	}
}

func makeMetricHistoryPlan(from, to time.Time, sourceSeconds, maxPoints int) metricHistoryPlan {
	if sourceSeconds < 1 {
		sourceSeconds = defaultMetricIntervalSeconds
	}
	base := time.Duration(sourceSeconds) * time.Second
	baseBuckets := metricHistoryAlignedBucketCount(from, to, base)
	multiplier := (baseBuckets + maxPoints - 1) / maxPoints
	if multiplier < 1 {
		multiplier = 1
	}
	for {
		width := base * time.Duration(multiplier)
		start := from.Truncate(width)
		end := metricHistoryCeil(to, width)
		count := int(end.Sub(start) / width)
		if count <= maxPoints {
			return metricHistoryPlan{start: start.UTC(), end: end.UTC(), width: width, count: count, from: from.UTC(), to: to.UTC()}
		}
		multiplier++
	}
}

func metricHistoryAlignedBucketCount(from, to time.Time, width time.Duration) int {
	if width <= 0 {
		return 1
	}
	start := from.Truncate(width)
	end := metricHistoryCeil(to, width)
	return maxInt(1, int(end.Sub(start)/width))
}

func metricHistoryCeil(value time.Time, width time.Duration) time.Time {
	truncated := value.Truncate(width)
	if truncated.Equal(value) {
		return truncated
	}
	return truncated.Add(width)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func metricHistoryOverlap(start, end, from, to time.Time) (time.Time, time.Time, bool) {
	if start.Before(from) {
		start = from
	}
	if end.After(to) {
		end = to
	}
	return start, end, end.After(start)
}

func metricHistoryBucketIndex(at time.Time, plan metricHistoryPlan) int {
	start := at.UTC().Truncate(plan.width)
	if start.Before(plan.start) || !start.Before(plan.end) {
		return -1
	}
	return int(start.Sub(plan.start) / plan.width)
}

func newMetricHistoryBuckets(plan metricHistoryPlan) []metricHistoryBucket {
	buckets := make([]metricHistoryBucket, plan.count)
	for index := range buckets {
		start := plan.start.Add(time.Duration(index) * plan.width)
		end := start.Add(plan.width)
		buckets[index] = metricHistoryBucket{start: start, end: end, partial: start.Before(plan.from) || end.After(plan.to)}
	}
	return buckets
}

func addMetricHistoryInterval(buckets []metricHistoryBucket, start, end time.Time, plan metricHistoryPlan) {
	start, end, ok := metricHistoryOverlap(start, end, plan.start, plan.end)
	if !ok {
		return
	}
	first := metricHistoryBucketIndex(start, plan)
	if first < 0 {
		first = 0
	}
	last := metricHistoryBucketIndex(end.Add(-time.Nanosecond), plan)
	if last < 0 {
		last = len(buckets) - 1
	}
	for index := first; index <= last && index < len(buckets); index++ {
		bucketStart, bucketEnd, overlaps := metricHistoryOverlap(buckets[index].start, buckets[index].end, start, end)
		if overlaps {
			buckets[index].intervals = append(buckets[index].intervals, rollupInterval{Start: bucketStart, End: bucketEnd})
		}
	}
}

func addMetricHistoryValue(bucket *metricHistoryBucket, value float64) {
	bucket.count++
	bucket.sum += value
	if bucket.min == nil || value < *bucket.min {
		copyValue := value
		bucket.min = &copyValue
	}
	if bucket.max == nil || value > *bucket.max {
		copyValue := value
		bucket.max = &copyValue
	}
}

func metricHistoryIntervalSeconds(value, fallback int) int {
	if value < 1 || value > maxMetricIntervalSeconds {
		value = fallback
	}
	if value < 1 || value > maxMetricIntervalSeconds {
		value = defaultMetricIntervalSeconds
	}
	return value
}

func metricHistoryPoint(bucket metricHistoryBucket, query MetricQuery) MetricPoint {
	covered := bucket.covered
	if len(bucket.intervals) > 0 {
		covered = intervalUnion(bucket.intervals)
	}
	overlapStart, overlapEnd, overlaps := metricHistoryOverlap(bucket.start, bucket.end, query.From, query.To)
	coverage := 0.0
	if overlaps {
		denominator := overlapEnd.Sub(overlapStart)
		if covered > denominator {
			covered = denominator
		}
		if covered > 0 {
			coverage = float64(covered) / float64(denominator)
		}
	}
	if coverage < 0 {
		coverage = 0
	}
	if coverage > 1 {
		coverage = 1
	}
	point := MetricPoint{ObservedAt: bucket.start, Min: bucket.min, Max: bucket.max, Availability: FreshnessUnavailable, Count: bucket.count, Coverage: coverage, Partial: bucket.partial}
	if bucket.count > 0 {
		mean := bucket.sum / float64(bucket.count)
		point.Value = &mean
		point.Availability = FreshnessCurrent
	} else if bucket.availability != "" {
		point.Availability = bucket.availability
	}
	if bucket.partial {
		point.Availability = FreshnessUnavailable
	}
	return point
}

func metricHistoryFromRaw(descriptor metricHistoryDescriptor, samples []metricHistoryRaw, query MetricQuery, tier metricHistoryTier) MetricSeries {
	baseSeconds := descriptor.sourceSeconds
	if tier != metricHistoryTierRaw && baseSeconds < metricHistoryTierSeconds(tier) {
		baseSeconds = metricHistoryTierSeconds(tier)
	}
	plan := makeMetricHistoryPlan(query.From, query.To, baseSeconds, query.MaxPoints)
	buckets := newMetricHistoryBuckets(plan)
	sort.SliceStable(samples, func(left, right int) bool {
		if samples[left].observedAt.Equal(samples[right].observedAt) {
			if samples[left].receivedAt.Equal(samples[right].receivedAt) {
				return samples[left].seriesID < samples[right].seriesID
			}
			return samples[left].receivedAt.Before(samples[right].receivedAt)
		}
		return samples[left].observedAt.Before(samples[right].observedAt)
	})
	for index, sample := range samples {
		observedAt := sample.observedAt.UTC()
		intervalSeconds := metricHistoryIntervalSeconds(sample.intervalSeconds, descriptor.sourceSeconds)
		coverageEnd := observedAt.Add(time.Duration(intervalSeconds) * time.Second)
		if index+1 < len(samples) && samples[index+1].observedAt.After(observedAt) && samples[index+1].observedAt.Before(coverageEnd) {
			coverageEnd = samples[index+1].observedAt
		}
		if sample.availability == FreshnessCurrent && sample.value != nil {
			addMetricHistoryInterval(buckets, observedAt, coverageEnd, plan)
			if !observedAt.Before(query.From) && observedAt.Before(query.To) {
				if bucketIndex := metricHistoryBucketIndex(observedAt, plan); bucketIndex >= 0 && bucketIndex < len(buckets) {
					addMetricHistoryValue(&buckets[bucketIndex], *sample.value)
				}
			}
			continue
		}
		if observedAt.Before(query.From) || !observedAt.Before(query.To) {
			continue
		}
		if bucketIndex := metricHistoryBucketIndex(observedAt, plan); bucketIndex >= 0 && bucketIndex < len(buckets) && buckets[bucketIndex].availability == "" {
			buckets[bucketIndex].availability = sample.availability
		}
	}
	result := descriptor.series
	result.ResolutionSeconds = int(plan.width / time.Second)
	result.Points = make([]MetricPoint, 0, len(buckets))
	for _, bucket := range buckets {
		result.Points = append(result.Points, metricHistoryPoint(bucket, query))
	}
	return result
}

func metricHistoryFromAggregates(descriptor metricHistoryDescriptor, aggregates []MetricAggregate, query MetricQuery, tier metricHistoryTier) MetricSeries {
	baseSeconds := metricHistoryTierSeconds(tier)
	plan := makeMetricHistoryPlan(query.From, query.To, baseSeconds, query.MaxPoints)
	buckets := newMetricHistoryBuckets(plan)
	for _, aggregate := range aggregates {
		start := aggregate.BucketStart.UTC()
		if !start.Before(query.To) || !start.Add(time.Duration(baseSeconds)*time.Second).After(query.From) {
			continue
		}
		bucketIndex := metricHistoryBucketIndex(start, plan)
		if bucketIndex < 0 || bucketIndex >= len(buckets) {
			continue
		}
		bucket := &buckets[bucketIndex]
		if aggregate.Count > 0 && aggregate.Sum != nil {
			bucket.count += aggregate.Count
			bucket.sum += *aggregate.Sum
			if aggregate.Min != nil && (bucket.min == nil || *aggregate.Min < *bucket.min) {
				copyValue := *aggregate.Min
				bucket.min = &copyValue
			}
			if aggregate.Max != nil && (bucket.max == nil || *aggregate.Max > *bucket.max) {
				copyValue := *aggregate.Max
				bucket.max = &copyValue
			}
		} else if bucket.availability == "" {
			bucket.availability = FreshnessUnavailable
		}
		coveredSeconds := aggregate.CoveredSeconds
		if coveredSeconds < 0 {
			coveredSeconds = 0
		}
		bucketSeconds := aggregate.BucketSeconds
		if bucketSeconds < 1 {
			bucketSeconds = baseSeconds
		}
		if coveredSeconds > bucketSeconds {
			coveredSeconds = bucketSeconds
		}
		bucket.covered += time.Duration(coveredSeconds) * time.Second
		bucket.partial = bucket.partial || aggregate.Partial
	}
	result := descriptor.series
	result.ResolutionSeconds = int(plan.width / time.Second)
	result.Points = make([]MetricPoint, 0, len(buckets))
	for _, bucket := range buckets {
		result.Points = append(result.Points, metricHistoryPoint(bucket, query))
	}
	return result
}

func metricHistoryEmptySeries(descriptor metricHistoryDescriptor, tier metricHistoryTier) MetricSeries {
	result := descriptor.series
	if result.ResolutionSeconds == 0 {
		result.ResolutionSeconds = metricHistoryTierSeconds(tier)
	}
	result.Points = []MetricPoint{}
	return result
}

func (s *Store) QueryMetrics(ctx context.Context, query MetricQuery) ([]MetricSeries, error) {
	normalized, err := NormalizeMetricQuery(query, s.Now())
	if err != nil {
		return nil, err
	}
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return nil, err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.queryMetricsSQL(ctx, normalized)
		}
		if phase == MonitoringStorageImporting {
			return nil, fmt.Errorf("metric history is paused during migration: %w", ErrConflict)
		}
	}
	return s.queryMetricsMemory(ctx, normalized)
}

func (s *Store) queryMetricsMemory(ctx context.Context, query MetricQuery) ([]MetricSeries, error) {
	tier := chooseMetricHistoryTier(query.From, s.Now())
	var samples []MetricSample
	err := s.read(ctx, func(state *State) error {
		if _, ok := state.Devices[query.DeviceID]; !ok {
			return ErrNotFound
		}
		samples = append(samples, state.Samples...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	explicit := make(map[string]bool, len(query.SeriesIDs))
	for _, seriesID := range query.SeriesIDs {
		explicit[seriesID] = true
	}
	descriptors := map[string]metricHistoryDescriptor{}
	rawBySeries := map[string][]metricHistoryRaw{}
	for _, sample := range samples {
		if sample.DeviceID != query.DeviceID {
			continue
		}
		entityID := sample.EntityID
		if entityID == "" {
			entityID = "host"
		}
		unit := sample.Unit
		if unit == "" {
			unit = "unknown"
		}
		collectorID := sample.CollectorID
		if collectorID == "" {
			collectorID = "legacy"
		}
		labels := sample.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		labelsJSON, _ := json.Marshal(labels)
		seriesID := deterministicSeriesID(query.DeviceID, collectorID, entityID, sample.Metric, unit, labelsJSON)
		if sample.Metric == "" {
			continue
		}
		if len(query.SeriesIDs) == 0 && (query.Metric != "" && query.Metric != sample.Metric || query.EntityID != "" && query.EntityID != entityID) {
			continue
		}
		if len(query.SeriesIDs) > 0 && !explicit[seriesID] {
			continue
		}
		descriptor, exists := descriptors[seriesID]
		if !exists {
			descriptor = metricHistoryDescriptor{series: MetricSeries{SeriesID: seriesID, EntityID: entityID, Metric: sample.Metric, Unit: unit}, sourceSeconds: metricHistoryIntervalSeconds(sample.IntervalSeconds, defaultMetricIntervalSeconds)}
		} else if sample.IntervalSeconds > 0 && sample.IntervalSeconds < descriptor.sourceSeconds {
			descriptor.sourceSeconds = sample.IntervalSeconds
		}
		if len(query.SeriesIDs) > 0 && (query.Metric != "" && query.Metric != sample.Metric || query.EntityID != "" && query.EntityID != entityID) {
			descriptor.filterMismatch = true
			descriptors[seriesID] = descriptor
			continue
		}
		descriptors[seriesID] = descriptor
		value := cloneMetricValue(sample.Value)
		rawBySeries[seriesID] = append(rawBySeries[seriesID], metricHistoryRaw{seriesID: seriesID, entityID: entityID, metric: sample.Metric, unit: unit, value: value, availability: sample.Availability, observedAt: sample.ObservedAt.UTC(), receivedAt: sample.ReceivedAt.UTC(), intervalSeconds: sample.IntervalSeconds})
	}
	ordered := orderMetricHistoryDescriptors(descriptors, query.SeriesIDs)
	for _, seriesID := range query.SeriesIDs {
		if _, exists := descriptors[seriesID]; !exists {
			ordered = append(ordered, metricHistoryDescriptor{series: MetricSeries{SeriesID: seriesID}, sourceSeconds: metricHistoryTierSeconds(tier)})
		}
	}
	result := make([]MetricSeries, 0, len(ordered))
	for _, descriptor := range ordered {
		if descriptor.filterMismatch || descriptor.series.Metric == "" {
			result = append(result, metricHistoryEmptySeries(descriptor, tier))
			continue
		}
		result = append(result, metricHistoryFromRaw(descriptor, rawBySeries[descriptor.series.SeriesID], query, tier))
	}
	return result, nil
}

func orderMetricHistoryDescriptors(descriptors map[string]metricHistoryDescriptor, seriesIDs []string) []metricHistoryDescriptor {
	ordered := make([]metricHistoryDescriptor, 0, len(descriptors))
	if len(seriesIDs) > 0 {
		for _, seriesID := range seriesIDs {
			if descriptor, exists := descriptors[seriesID]; exists {
				ordered = append(ordered, descriptor)
			}
		}
		return ordered
	}
	keys := make([]string, 0, len(descriptors))
	for key := range descriptors {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		first, second := descriptors[keys[left]].series, descriptors[keys[right]].series
		if first.Metric != second.Metric {
			return first.Metric < second.Metric
		}
		if first.EntityID != second.EntityID {
			return first.EntityID < second.EntityID
		}
		return first.SeriesID < second.SeriesID
	})
	if len(keys) > MaxMetricHistorySeries {
		keys = keys[:MaxMetricHistorySeries]
	}
	for _, key := range keys {
		ordered = append(ordered, descriptors[key])
	}
	return ordered
}

func cloneMetricValue(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func (s *Store) PruneSamples(ctx context.Context, before time.Time) (int, error) {
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return 0, err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.pruneSamplesSQL(ctx, before)
		}
		if phase == MonitoringStorageImporting {
			return 0, fmt.Errorf("sample pruning is paused during migration: %w", ErrConflict)
		}
	}
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
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return 0, err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.enforceSampleLimitSQL(ctx, maxSamples)
		}
		if phase == MonitoringStorageImporting {
			return 0, fmt.Errorf("sample limiting is paused during migration: %w", ErrConflict)
		}
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
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return 0, err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.cleanupTelemetrySQL(ctx, now)
		}
		if phase == MonitoringStorageImporting {
			return 0, fmt.Errorf("telemetry cleanup is paused during migration: %w", ErrConflict)
		}
	}
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
		underSampleThreshold := state.Workspace.MaxSamples <= 0 || len(state.Samples) < state.Workspace.MaxSamples*9/10
		underByteThreshold := telemetryBudgetBelowReleaseThreshold(memoryTelemetryBytes(*state), state.Workspace.TelemetryBudgetBytes)
		if state.Workspace.TelemetryBackpressure && underSampleThreshold && underByteThreshold {
			state.Workspace.TelemetryBackpressure = false
		}
		return nil
	})
	return removed, err
}

func (s *Store) SetTelemetryBackpressure(ctx context.Context, enabled bool) error {
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.setTelemetryBackpressureSQL(ctx, enabled)
		}
		if phase == MonitoringStorageImporting {
			return fmt.Errorf("telemetry backpressure is paused during migration: %w", ErrConflict)
		}
	}
	return s.mutate(ctx, func(state *State) error {
		state.Workspace.TelemetryBackpressure = enabled
		return nil
	})
}

func (s *Store) TelemetryStatus(ctx context.Context) (TelemetryStatus, error) {
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return TelemetryStatus{}, err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.telemetryStatusSQL(ctx)
		}
		if phase == MonitoringStorageImporting {
			return TelemetryStatus{}, fmt.Errorf("telemetry status is paused during migration: %w", ErrConflict)
		}
	}
	var result TelemetryStatus
	err := s.read(ctx, func(state *State) error {
		result = TelemetryStatus{Samples: len(state.Samples), DroppedSamples: state.Workspace.DroppedSamples, MaxSamples: state.Workspace.MaxSamples, UsedBytes: memoryTelemetryBytes(*state), BudgetBytes: state.Workspace.TelemetryBudgetBytes, Backpressure: state.Workspace.TelemetryBackpressure, RetentionHours: state.Workspace.RetentionHours, LastRetentionAt: cloneTime(state.Workspace.LastRetentionAt)}
		return nil
	})
	return result, err
}

func memoryTelemetryBytes(state State) int64 {
	encoded, err := json.Marshal(struct {
		Samples       []MetricSample         `json:"samples"`
		BatchReceipts map[string]string      `json:"batchReceipts"`
		Observations  map[string]Observation `json:"observations"`
	}{state.Samples, state.BatchReceipts, state.Observations})
	if err != nil {
		return 0
	}
	return int64(len(encoded))
}

func memoryTelemetryEstimate(sampleCount, observationCount int) int64 {
	return int64(sampleCount)*512 + int64(observationCount)*1024 + 512
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func (s *Store) StoreObservation(ctx context.Context, observation Observation) error {
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.storeObservationSQL(ctx, observation)
		}
		if phase == MonitoringStorageImporting {
			return fmt.Errorf("observation storage is paused during migration: %w", ErrConflict)
		}
	}
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
	if s.db != nil {
		phase, err := s.monitoringPhase(ctx)
		if err != nil {
			return nil, err
		}
		if phase == MonitoringStorageAuthoritative {
			return s.listObservationsSQL(ctx)
		}
		if phase == MonitoringStorageImporting {
			return nil, fmt.Errorf("observation history is paused during migration: %w", ErrConflict)
		}
	}
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
