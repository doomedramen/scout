package store

import (
	"context"
	"sort"
	"strings"
)

func (s *Store) PutRelationship(ctx context.Context, relationship Relationship) error {
	if relationship.FromEntity == "" || relationship.ToEntity == "" || relationship.Type == "" || relationship.Confidence < 0 || relationship.Confidence > 1 {
		return ErrInvalid
	}
	return s.mutate(ctx, func(state *State) error {
		if relationship.ID == "" {
			relationship.ID = NewID()
		}
		if relationship.ObservedAt.IsZero() {
			relationship.ObservedAt = s.now().UTC()
		}
		state.Relationships[relationship.ID] = relationship
		return nil
	})
}

func (s *Store) ListRelationships(ctx context.Context, siteID string) ([]Relationship, error) {
	result := []Relationship{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Relationships {
			if !item.ExpiresAt.IsZero() && item.ExpiresAt.Before(s.now().UTC()) {
				continue
			}
			if siteID != "" {
				from := state.Devices[item.FromEntity]
				to := state.Devices[item.ToEntity]
				if from.SiteID != siteID && to.SiteID != siteID {
					continue
				}
			}
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].ObservedAt.After(result[j].ObservedAt) })
		return nil
	})
	return result, err
}

func (s *Store) PutCollector(ctx context.Context, deviceID string, descriptor CollectorDescriptorState) error {
	return s.mutate(ctx, func(state *State) error {
		device, ok := state.Devices[deviceID]
		if !ok {
			return ErrNotFound
		}
		key := deviceID + "\x00" + descriptor.ID
		state.Collectors[key] = descriptor
		found := false
		for i, item := range device.CollectorStates {
			if item.ID == descriptor.ID {
				device.CollectorStates[i] = descriptor
				found = true
				break
			}
		}
		if !found {
			device.CollectorStates = append(device.CollectorStates, descriptor)
		}
		state.Devices[deviceID] = device
		return nil
	})
}

func (s *Store) ListCollectors(ctx context.Context, deviceID string) ([]CollectorDescriptorState, error) {
	result := []CollectorDescriptorState{}
	err := s.read(ctx, func(state *State) error {
		for key, item := range state.Collectors {
			if deviceID == "" || strings.HasPrefix(key, deviceID+"\x00") {
				result = append(result, item)
			}
		}
		sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
		return nil
	})
	return result, err
}

func (s *Store) PutRelease(ctx context.Context, release Release) (Release, error) {
	if release.ManifestHash == "" || release.Digest == "" || release.Version == "" || release.Generation < 1 || release.Bytes < 1 {
		return Release{}, ErrInvalid
	}
	var result Release
	err := s.mutate(ctx, func(state *State) error {
		for _, item := range state.Releases {
			if item.ManifestHash == release.ManifestHash {
				return ErrConflict
			}
		}
		if release.ID == "" {
			release.ID = NewID()
		}
		state.Releases[release.ID] = release
		result = release
		return nil
	})
	return result, err
}
func (s *Store) GetRelease(ctx context.Context, id string) (Release, error) {
	var result Release
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Releases[id]
		if !ok {
			return ErrNotFound
		}
		result = item
		return nil
	})
	return result, err
}
func (s *Store) ListReleases(ctx context.Context, platform string) ([]Release, error) {
	result := []Release{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Releases {
			if platform != "" && item.Platform != platform {
				continue
			}
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Generation > result[j].Generation })
		return nil
	})
	return result, err
}
func (s *Store) RevokeRelease(ctx context.Context, id string) error {
	return s.mutate(ctx, func(state *State) error {
		item, ok := state.Releases[id]
		if !ok {
			return ErrNotFound
		}
		now := s.now().UTC()
		item.RevokedAt = &now
		state.Releases[id] = item
		return nil
	})
}

func (s *Store) PutAssignment(ctx context.Context, assignment Assignment) (Assignment, error) {
	if assignment.DeviceID == "" || assignment.DesiredRelease == "" || assignment.Generation < 1 {
		return Assignment{}, ErrInvalid
	}
	var result Assignment
	err := s.mutate(ctx, func(state *State) error {
		if _, ok := state.Devices[assignment.DeviceID]; !ok {
			return ErrNotFound
		}
		if assignment.ID == "" {
			assignment.ID = NewID()
		}
		state.Assignments[assignment.DeviceID] = assignment
		result = assignment
		return nil
	})
	return result, err
}
func (s *Store) Assignment(ctx context.Context, deviceID string) (Assignment, error) {
	var result Assignment
	err := s.read(ctx, func(state *State) error {
		item, ok := state.Assignments[deviceID]
		if !ok {
			return ErrNotFound
		}
		result = item
		return nil
	})
	return result, err
}
func (s *Store) ListAssignments(ctx context.Context) ([]Assignment, error) {
	result := []Assignment{}
	err := s.read(ctx, func(state *State) error {
		for _, item := range state.Assignments {
			result = append(result, item)
		}
		sort.Slice(result, func(i, j int) bool { return result[i].DeviceID < result[j].DeviceID })
		return nil
	})
	return result, err
}
func (s *Store) UpdateAssignment(ctx context.Context, deviceID string, update func(*Assignment) error) (Assignment, error) {
	var result Assignment
	err := s.mutate(ctx, func(state *State) error {
		item, ok := state.Assignments[deviceID]
		if !ok {
			return ErrNotFound
		}
		if err := update(&item); err != nil {
			return err
		}
		state.Assignments[deviceID] = item
		result = item
		return nil
	})
	return result, err
}

func (s *Store) DecommissionDevice(ctx context.Context, deviceID, reason string) (Device, error) {
	var result Device
	err := s.mutate(ctx, func(state *State) error {
		device, ok := state.Devices[deviceID]
		if !ok {
			return ErrNotFound
		}
		now := s.now().UTC()
		device.DecommissionedAt = &now
		device.Lifecycle = "decommissioned"
		device.Excluded = true
		device.Availability = AvailabilityRevoked
		state.Devices[deviceID] = device
		for agentID, item := range state.Agents {
			if item.DeviceID == deviceID {
				item.RevokedAt = &now
				state.Agents[agentID] = item
			}
		}
		for id, item := range state.Jobs {
			if item.DeviceID == deviceID && isActiveJob(item.State) {
				item.State = "excluded"
				item.Result = map[string]string{"code": "decommissioned", "reason": reason}
				item.LeaseExpiry = nil
				state.Jobs[id] = item
			}
		}
		result = copyDevice(device)
		return nil
	})
	return result, err
}

func (s *Store) ReenableDevice(ctx context.Context, deviceID string, expectedRevision int64) (Device, error) {
	var result Device
	err := s.mutate(ctx, func(state *State) error {
		device, ok := state.Devices[deviceID]
		if !ok {
			return ErrNotFound
		}
		if expectedRevision > 0 && device.Revision != expectedRevision {
			return ErrConflict
		}
		device.Excluded = false
		device.DecommissionedAt = nil
		device.Lifecycle = "candidate"
		device.Availability = AvailabilityConnecting
		device.Revision++
		state.Devices[deviceID] = device
		result = copyDevice(device)
		return nil
	})
	return result, err
}

func (s *Store) AddIdentifier(ctx context.Context, deviceID string, evidence IdentifierEvidence) error {
	return s.mutate(ctx, func(state *State) error {
		device, ok := state.Devices[deviceID]
		if !ok {
			return ErrNotFound
		}
		for i, item := range device.Identifiers {
			if item.Kind == evidence.Kind && item.Namespace == evidence.Namespace && item.Value == evidence.Value {
				item.LastSeen = evidence.LastSeen
				if item.LastSeen.IsZero() {
					item.LastSeen = s.now().UTC()
				}
				if evidence.Confidence > item.Confidence {
					item.Confidence = evidence.Confidence
				}
				device.Identifiers[i] = item
				state.Devices[deviceID] = device
				return nil
			}
		}
		if evidence.FirstSeen.IsZero() {
			evidence.FirstSeen = s.now().UTC()
		}
		if evidence.LastSeen.IsZero() {
			evidence.LastSeen = evidence.FirstSeen
		}
		device.Identifiers = append(device.Identifiers, evidence)
		state.Devices[deviceID] = device
		return nil
	})
}
