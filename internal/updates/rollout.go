package updates

import (
	"sort"
	"time"

	"scout.local/scout/internal/store"
)

func ValidateRollout(rollout store.Rollout, release store.Release) error {
	if rollout.ReleaseID == "" || rollout.ReleaseID != release.ID || rollout.Concurrency < 1 || rollout.Concurrency > 100 || rollout.Canaries < 0 || rollout.Canaries > rollout.Concurrency || rollout.FailureThreshold < 1 || rollout.FailureThreshold > 100 {
		return store.ErrInvalid
	}
	switch rollout.Mode {
	case "manual", "automatic", "pinned":
	default:
		return store.ErrInvalid
	}
	if rollout.WindowStart != nil && rollout.WindowEnd != nil && !rollout.WindowStart.Before(*rollout.WindowEnd) {
		return store.ErrInvalid
	}
	return nil
}

func WithinMaintenanceWindow(rollout store.Rollout, now time.Time) bool {
	if rollout.WindowStart == nil || rollout.WindowEnd == nil {
		return true
	}
	return !now.Before(*rollout.WindowStart) && now.Before(*rollout.WindowEnd)
}

func PlanAssignments(rollout store.Rollout, release store.Release, devices []store.Device, now time.Time) ([]store.Assignment, error) {
	if err := ValidateRollout(rollout, release); err != nil {
		return nil, err
	}
	if rollout.Paused || rollout.Mode == "automatic" && !WithinMaintenanceWindow(rollout, now) {
		return []store.Assignment{}, nil
	}
	targets := map[string]bool{}
	for _, id := range rollout.Targets {
		targets[id] = true
	}
	eligible := make([]store.Device, 0, len(devices))
	for _, device := range devices {
		if len(targets) > 0 && !targets[device.ID] {
			continue
		}
		if device.Excluded || device.Lifecycle == "decommissioned" || device.Platform != release.Platform || device.Architecture != release.Architecture {
			continue
		}
		eligible = append(eligible, device)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })
	assignments := make([]store.Assignment, 0, len(eligible))
	expires := now.Add(24 * time.Hour)
	for index, device := range eligible {
		state := "queued"
		if rollout.Canaries > 0 && index < rollout.Canaries {
			state = "canary"
		}
		assignments = append(assignments, store.Assignment{RolloutID: rollout.ID, DeviceID: device.ID, DesiredRelease: release.ID, Generation: release.Generation, ExpiresAt: expires, State: state})
	}
	return assignments, nil
}

func RecordRolloutFailure(rollout *store.Rollout) bool {
	if rollout == nil {
		return false
	}
	rollout.FailureCount++
	if rollout.FailureCount >= rollout.FailureThreshold {
		rollout.Paused = true
		return true
	}
	return false
}
