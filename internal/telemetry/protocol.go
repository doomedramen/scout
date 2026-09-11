package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"scout.local/scout/internal/collector"
	"scout.local/scout/internal/store"
)

const (
	ProtocolVersion      = 1
	MaxBatchBytes        = 1 << 20
	MaxBatchSamples      = 1000
	MaxBatchObservations = 1000
)

type CollectorRef struct {
	ID            string `json:"id"`
	SchemaVersion int    `json:"schemaVersion"`
}
type Sample struct {
	EntityID     string            `json:"entityId"`
	Metric       string            `json:"metric"`
	Labels       map[string]string `json:"labels,omitempty"`
	Value        *float64          `json:"value"`
	Availability string            `json:"availability"`
	Unit         string            `json:"unit"`
	ObservedAt   time.Time         `json:"observedAt"`
}
type RelationshipObservation struct {
	Kind       string         `json:"kind"`
	SubjectID  string         `json:"subjectId"`
	Payload    map[string]any `json:"payload"`
	ObservedAt time.Time      `json:"observedAt"`
	ExpiresAt  time.Time      `json:"expiresAt"`
	Confidence float64        `json:"confidence"`
}
type Batch struct {
	ProtocolVersion int                       `json:"protocolVersion"`
	BootID          string                    `json:"bootId"`
	BatchID         string                    `json:"batchId"`
	ObservedAt      time.Time                 `json:"observedAt"`
	Collector       CollectorRef              `json:"collector"`
	Samples         []Sample                  `json:"samples"`
	Observations    []RelationshipObservation `json:"observations"`
	DroppedCount    int                       `json:"droppedCount"`
}

func FromCollector(snapshot collector.HostSnapshot, bootID, batchID string) Batch {
	batch := Batch{ProtocolVersion: ProtocolVersion, BootID: bootID, BatchID: batchID, ObservedAt: snapshot.ObservedAt, Collector: CollectorRef{ID: "host", SchemaVersion: 1}, Samples: []Sample{}, Observations: []RelationshipObservation{}}
	for _, metric := range snapshot.Metrics {
		batch.Samples = append(batch.Samples, Sample{EntityID: metric.EntityID, Metric: metric.Metric, Value: metric.Value, Availability: metric.Availability, Unit: metric.Unit, ObservedAt: metric.ObservedAt, Labels: metric.Labels})
	}
	for _, iface := range snapshot.Interfaces {
		batch.Observations = append(batch.Observations, RelationshipObservation{Kind: "interface_membership", SubjectID: iface.Name, Payload: map[string]any{"addresses": iface.Addresses}, ObservedAt: snapshot.ObservedAt, ExpiresAt: snapshot.ObservedAt.Add(15 * time.Minute), Confidence: 1})
	}
	return batch
}

func ValidateBatch(batch Batch, now time.Time) error {
	if batch.ProtocolVersion != ProtocolVersion || batch.BootID == "" || batch.BatchID == "" || len(batch.BootID) > 128 || len(batch.BatchID) > 128 || batch.ObservedAt.IsZero() {
		return store.ErrInvalid
	}
	if batch.Collector.ID == "" || batch.Collector.SchemaVersion < 1 {
		return store.ErrInvalid
	}
	if len(batch.Samples) > MaxBatchSamples || len(batch.Observations) > MaxBatchObservations {
		return store.ErrBackpressure
	}
	if batch.DroppedCount < 0 {
		return store.ErrInvalid
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, sample := range batch.Samples {
		if sample.EntityID == "" || len(sample.EntityID) > 128 || sample.Metric == "" || len(sample.Metric) > 128 || sample.Unit == "" || len(sample.Unit) > 64 {
			return store.ErrInvalid
		}
		if sample.Value != nil && (math.IsNaN(*sample.Value) || math.IsInf(*sample.Value, 0)) {
			return store.ErrInvalid
		}
		if sample.Availability == "current" && sample.Value == nil {
			return store.ErrInvalid
		}
		if sample.Value != nil && strings.HasSuffix(sample.Metric, "percent") && (*sample.Value < 0 || *sample.Value > 100) {
			return store.ErrInvalid
		}
		switch sample.Availability {
		case "current", "unavailable", "unsupported", "missing", "stale":
		default:
			return store.ErrInvalid
		}
		if len(sample.Labels) > 64 {
			return store.ErrBackpressure
		}
		for key, value := range sample.Labels {
			if len(key) > 64 || len(value) > 256 {
				return store.ErrBackpressure
			}
		}
	}
	for _, observation := range batch.Observations {
		if observation.Kind == "" || observation.SubjectID == "" || observation.Confidence < 0 || observation.Confidence > 1 {
			return store.ErrInvalid
		}
		if len(observation.Payload) > 64 {
			return store.ErrBackpressure
		}
		if observation.ObservedAt.After(now.Add(5 * time.Minute)) {
			continue
		}
	}
	return nil
}

func CanonicalHash(batch Batch) (string, []byte, error) {
	data, err := json.Marshal(batch)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, nil
}
func ToStore(batch Batch, agentID string, receivedAt time.Time) ([]store.MetricSample, []store.Observation) {
	samples := make([]store.MetricSample, 0, len(batch.Samples))
	for _, sample := range batch.Samples {
		samples = append(samples, store.MetricSample{AgentID: agentID, CollectorID: batch.Collector.ID, EntityID: sample.EntityID, Metric: sample.Metric, Labels: sample.Labels, Value: sample.Value, Availability: store.Freshness(sample.Availability), Unit: sample.Unit, ObservedAt: sample.ObservedAt, ReceivedAt: receivedAt})
	}
	observations := make([]store.Observation, 0, len(batch.Observations))
	for _, item := range batch.Observations {
		observations = append(observations, store.Observation{ReporterID: agentID, CollectorID: batch.Collector.ID, SubjectID: item.SubjectID, Kind: item.Kind, Payload: item.Payload, ObservedAt: item.ObservedAt, ReceivedAt: receivedAt, ExpiresAt: item.ExpiresAt, Confidence: item.Confidence})
	}
	return samples, observations
}

func DecodeBatch(data []byte) (Batch, error) {
	if len(data) > MaxBatchBytes {
		return Batch{}, store.ErrBackpressure
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var batch Batch
	if err := decoder.Decode(&batch); err != nil {
		return Batch{}, fmt.Errorf("decode batch: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Batch{}, store.ErrInvalid
	}
	if err := ValidateBatch(batch, time.Now().UTC()); err != nil {
		return Batch{}, err
	}
	return batch, nil
}
