package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func collectorConfigKey(deviceID, collectorID string) string {
	return deviceID + "\x00" + collectorID
}

func (s *Store) PutCollectorConfig(ctx context.Context, config CollectorConfig, expectedRevision int64) (CollectorConfig, error) {
	if config.DeviceID == "" || config.CollectorID == "" || config.Provider == "" || len(config.Config) > 64 {
		return CollectorConfig{}, ErrInvalid
	}
	for key, value := range config.Config {
		if len(key) > 64 || len(value) > 256 || sensitiveCollectorConfigKey(key) {
			return CollectorConfig{}, ErrInvalid
		}
	}
	var result CollectorConfig
	err := s.mutate(ctx, func(state *State) error {
		if _, ok := state.Devices[config.DeviceID]; !ok {
			return ErrNotFound
		}
		key := collectorConfigKey(config.DeviceID, config.CollectorID)
		current, exists := state.CollectorConfigs[key]
		if exists && expectedRevision > 0 && current.Revision != expectedRevision {
			return ErrConflict
		}
		config.Revision = current.Revision + 1
		config.Config = cloneMap(config.Config)
		if config.Health == "" {
			config.Health = CollectorDetected
		}
		state.CollectorConfigs[key] = config
		result = cloneCollectorConfig(config)
		return nil
	})
	return result, err
}

func sensitiveCollectorConfigKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "key" || strings.Contains(key, "password") || strings.Contains(key, "secret") ||
		strings.Contains(key, "token") || strings.Contains(key, "private") || strings.Contains(key, "credential")
}

func (s *Store) GetCollectorConfig(ctx context.Context, deviceID, collectorID string) (CollectorConfig, error) {
	var result CollectorConfig
	err := s.read(ctx, func(state *State) error {
		item, ok := state.CollectorConfigs[collectorConfigKey(deviceID, collectorID)]
		if !ok {
			return ErrNotFound
		}
		result = cloneCollectorConfig(item)
		return nil
	})
	return result, err
}

func (s *Store) ListCollectorConfigs(ctx context.Context, deviceID string) ([]CollectorConfig, error) {
	result := []CollectorConfig{}
	err := s.read(ctx, func(state *State) error {
		for _, config := range state.CollectorConfigs {
			if deviceID != "" && config.DeviceID != deviceID {
				continue
			}
			result = append(result, cloneCollectorConfig(config))
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].DeviceID == result[j].DeviceID {
				return result[i].CollectorID < result[j].CollectorID
			}
			return result[i].DeviceID < result[j].DeviceID
		})
		return nil
	})
	return result, err
}

func (s *Store) PutServiceEntity(ctx context.Context, entity ServiceEntity) error {
	if entity.ID == "" || entity.Provider == "" || entity.Kind == "" || entity.Name == "" {
		return ErrInvalid
	}
	if len(entity.Labels) > 64 {
		return ErrBackpressure
	}
	return s.mutate(ctx, func(state *State) error {
		if entity.ObservedAt.IsZero() {
			entity.ObservedAt = s.now().UTC()
		}
		if entity.ExpiresAt.IsZero() {
			entity.ExpiresAt = entity.ObservedAt.Add(15 * time.Minute)
		}
		entity.Labels = cloneMap(entity.Labels)
		state.ServiceEntities[serviceEntityKey(entity)] = entity
		return nil
	})
}

func serviceEntityKey(entity ServiceEntity) string {
	return strings.Join([]string{entity.Provider, entity.ClusterID, entity.ID}, "\x00")
}

func (s *Store) ListServiceEntities(ctx context.Context, provider string, now time.Time) ([]ServiceEntity, error) {
	result := []ServiceEntity{}
	err := s.read(ctx, func(state *State) error {
		for _, entity := range state.ServiceEntities {
			if provider != "" && entity.Provider != provider {
				continue
			}
			if !entity.ExpiresAt.IsZero() && !now.Before(entity.ExpiresAt) {
				continue
			}
			entity.Labels = cloneMap(entity.Labels)
			result = append(result, entity)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
		return nil
	})
	return result, err
}

func (s *Store) DeleteExpiredServiceEntities(ctx context.Context, now time.Time) (int, error) {
	removed := 0
	err := s.mutate(ctx, func(state *State) error {
		for key, entity := range state.ServiceEntities {
			if !entity.ExpiresAt.IsZero() && !now.Before(entity.ExpiresAt) {
				delete(state.ServiceEntities, key)
				removed++
			}
		}
		return nil
	})
	return removed, err
}

func (s *Store) PutDeviceUpdatePolicy(ctx context.Context, policy DeviceUpdatePolicy) (DeviceUpdatePolicy, error) {
	if policy.DeviceID == "" {
		return DeviceUpdatePolicy{}, ErrInvalid
	}
	var result DeviceUpdatePolicy
	err := s.mutate(ctx, func(state *State) error {
		device, ok := state.Devices[policy.DeviceID]
		if !ok {
			return ErrNotFound
		}
		current := state.UpdatePolicies[policy.DeviceID]
		if policy.ExpectedRevision > 0 && current.Revision != policy.ExpectedRevision {
			return ErrConflict
		}
		switch policy.Mode {
		case "manual", "automatic", "pinned":
		default:
			return ErrInvalid
		}
		if policy.ReleaseID != "" {
			release, releaseOK := state.Releases[policy.ReleaseID]
			if !releaseOK || release.RevokedAt != nil {
				return ErrConflict
			}
			if release.Platform != device.Platform || release.Architecture != device.Architecture {
				return ErrConflict
			}
		}
		policy.DeviceID = device.ID
		policy.Revision = current.Revision + 1
		policy.UpdatedAt = s.now().UTC()
		state.UpdatePolicies[policy.DeviceID] = policy
		result = policy
		return nil
	})
	return result, err
}

func (s *Store) GetDeviceUpdatePolicy(ctx context.Context, deviceID string) (DeviceUpdatePolicy, error) {
	var result DeviceUpdatePolicy
	err := s.read(ctx, func(state *State) error {
		policy, ok := state.UpdatePolicies[deviceID]
		if !ok {
			return ErrNotFound
		}
		result = policy
		return nil
	})
	return result, err
}

func cloneCollectorConfig(config CollectorConfig) CollectorConfig {
	config.Config = cloneMap(config.Config)
	return config
}
