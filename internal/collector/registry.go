package collector

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"scout.local/scout/internal/store"
)

type Descriptor struct {
	ID                 string            `json:"id"`
	Provider           string            `json:"provider"`
	Version            string            `json:"version"`
	RequiredPermission []string          `json:"requiredPermissions"`
	ConfigSchema       map[string]string `json:"configSchema"`
	EntityLimit        int               `json:"entityLimit"`
	Interval           time.Duration     `json:"interval"`
	Deadline           time.Duration     `json:"deadline"`
}

type CollectorState = store.CollectorState

const (
	CollectorDetected = store.CollectorDetected
	CollectorEnabled  = store.CollectorEnabled
	CollectorDegraded = store.CollectorDegraded
)

type Entity struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	Labels   map[string]string `json:"labels,omitempty"`
	Observed time.Time         `json:"observedAt"`
	Expires  time.Time         `json:"expiresAt"`
}

type ServiceResult struct {
	Entities   []Entity
	Metrics    []Metric
	Partial    bool
	Diagnostic string
}

type HostSource struct {
	Collector *HostCollector
}

func (h HostSource) Detect(context.Context) (bool, error) { return h.Collector != nil, nil }

func (h HostSource) Collect(ctx context.Context) (ServiceResult, error) {
	if h.Collector == nil {
		return ServiceResult{}, errors.New("host collector unavailable")
	}
	snapshot, err := h.Collector.Collect(ctx)
	if err != nil {
		return ServiceResult{}, err
	}
	return ServiceResult{Entities: []Entity{{ID: "host", Kind: "host", Name: snapshot.Hostname, Status: "online", Observed: snapshot.ObservedAt, Expires: snapshot.ObservedAt.Add(45 * time.Second)}}, Metrics: snapshot.Metrics}, nil
}

func (h HostSource) Close() error { return nil }

type Source interface {
	Detect(context.Context) (bool, error)
	Collect(context.Context) (ServiceResult, error)
	Close() error
}

type Entry struct {
	Descriptor  Descriptor
	Source      Source
	State       store.CollectorState
	Diagnostic  string
	LastSuccess time.Time
}

type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

func NewRegistry() *Registry { return &Registry{entries: map[string]Entry{}} }

func (r *Registry) Register(descriptor Descriptor, source Source) error {
	if r == nil || source == nil || descriptor.ID == "" || descriptor.Provider == "" || descriptor.EntityLimit < 1 || descriptor.EntityLimit > 2000 {
		return errors.New("invalid collector descriptor")
	}
	if descriptor.Interval <= 0 {
		descriptor.Interval = 30 * time.Second
	}
	if descriptor.Deadline <= 0 || descriptor.Deadline > descriptor.Interval {
		descriptor.Deadline = 10 * time.Second
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[descriptor.ID]; exists {
		return errors.New("collector already registered")
	}
	r.entries[descriptor.ID] = Entry{Descriptor: descriptor, Source: source, State: store.CollectorDetected}
	return nil
}

func (r *Registry) Descriptors() []Descriptor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Descriptor, 0, len(r.entries))
	for _, entry := range r.entries {
		result = append(result, entry.Descriptor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (r *Registry) States() []Entry {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		entry.Source = nil
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Descriptor.ID < result[j].Descriptor.ID })
	return result
}

func (r *Registry) Collect(ctx context.Context, id string) (result ServiceResult, err error) {
	if r == nil {
		return ServiceResult{}, errors.New("collector registry unavailable")
	}
	r.mu.RLock()
	entry, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok || entry.Source == nil {
		return ServiceResult{}, errors.New("collector not found")
	}
	deadline := entry.Descriptor.Deadline
	if deadline <= 0 {
		deadline = 10 * time.Second
	}
	collectContext, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			result = ServiceResult{}
			err = fmt.Errorf("collector panic: %v", recovered)
		}
		r.mu.Lock()
		current := r.entries[id]
		if err != nil {
			current.State = store.CollectorDegraded
			current.Diagnostic = safeDiagnostic(err)
		} else {
			current.State = store.CollectorEnabled
			current.Diagnostic = ""
			current.LastSuccess = time.Now().UTC()
		}
		r.entries[id] = current
		r.mu.Unlock()
	}()
	result, err = entry.Source.Collect(collectContext)
	if err != nil {
		return ServiceResult{}, err
	}
	if len(result.Entities) > entry.Descriptor.EntityLimit {
		return ServiceResult{}, errors.New("collector entity limit exceeded")
	}
	return result, nil
}

func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	entries := make([]Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	r.mu.Unlock()
	for _, entry := range entries {
		if entry.Source != nil {
			if err := entry.Source.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func safeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 256 {
		message = message[:256]
	}
	return message
}

func DefaultDescriptors() []Descriptor {
	return []Descriptor{
		{ID: "host", Provider: "host", Version: runtime.Version(), RequiredPermission: []string{"proc:read", "sysfs:read"}, ConfigSchema: map[string]string{}, EntityLimit: 1, Interval: 15 * time.Second, Deadline: 5 * time.Second},
		{ID: "systemd", Provider: "systemd", Version: "systemd-v1", RequiredPermission: []string{"systemd:read"}, ConfigSchema: map[string]string{"expectedRunning": "comma-separated service globs; * and ? only; max 100 patterns"}, EntityLimit: 1000, Interval: 30 * time.Second, Deadline: 5 * time.Second},
		{ID: "docker", Provider: "docker", Version: "engine-api-v1", RequiredPermission: []string{"docker:read"}, ConfigSchema: map[string]string{"socketPath": "local unix socket"}, EntityLimit: 2000, Interval: 30 * time.Second, Deadline: 10 * time.Second},
		{ID: "proxmox", Provider: "proxmox", Version: "api-v2", RequiredPermission: []string{"proxmox:cluster-read"}, ConfigSchema: map[string]string{"baseUrl": "https URL", "clusterId": "scoped cluster"}, EntityLimit: 2000, Interval: 30 * time.Second, Deadline: 10 * time.Second},
		{ID: "smart", Provider: "smart", Version: "smartctl-json-v1", RequiredPermission: []string{"smartctl:read", "block-device:read"}, ConfigSchema: map[string]string{"standbyPolicy": "never wake; smartctl -n standby,0"}, EntityLimit: 64, Interval: 5 * time.Minute, Deadline: 60 * time.Second},
		{ID: "zfs", Provider: "zfs", Version: "openzfs-fixed-v1", RequiredPermission: []string{"zpool:read", "zfs:read", "zfs:kstat-read"}, ConfigSchema: map[string]string{}, EntityLimit: 1032, Interval: 60 * time.Second, Deadline: 20 * time.Second},
		{ID: "sensors", Provider: "sensors", Version: "hwmon-sysfs-v1", RequiredPermission: []string{"sysfs:hwmon-read"}, ConfigSchema: map[string]string{"excludedIds": "comma-separated stable sensor IDs; exact match; max 256"}, EntityLimit: 256, Interval: 30 * time.Second, Deadline: 5 * time.Second},
		{ID: "gpu", Provider: "gpu", Version: "gpu-fixed-v1", RequiredPermission: []string{"nvidia-smi:read", "sysfs:drm-read", "sysfs:hwmon-read"}, ConfigSchema: map[string]string{"sysfsRoot": "host sysfs mount; production default /sys"}, EntityLimit: 32, Interval: 30 * time.Second, Deadline: 10 * time.Second},
	}
}
