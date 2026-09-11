package collector

import (
	"errors"
	"sync"
	"time"
)

type Lease struct {
	CollectorID string    `json:"collectorId"`
	Owner       string    `json:"owner"`
	Epoch       int64     `json:"epoch"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type LeaseManager struct {
	mu    sync.Mutex
	now   func() time.Time
	items map[string]Lease
}

func NewLeaseManager() *LeaseManager {
	return &LeaseManager{now: func() time.Time { return time.Now().UTC() }, items: map[string]Lease{}}
}

func (m *LeaseManager) clock() time.Time {
	if m.now != nil {
		return m.now().UTC()
	}
	return time.Now().UTC()
}

func (m *LeaseManager) SetClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now != nil {
		m.now = now
	}
}

func (m *LeaseManager) Acquire(collectorID, owner string, duration time.Duration) (Lease, error) {
	if m == nil || collectorID == "" || owner == "" {
		return Lease{}, errors.New("collector and owner are required")
	}
	if duration <= 0 || duration > 5*time.Minute {
		duration = time.Minute
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	if current, ok := m.items[collectorID]; ok && now.Before(current.ExpiresAt) && current.Owner != owner {
		return Lease{}, errors.New("collector lease is held")
	}
	current := m.items[collectorID]
	current.CollectorID = collectorID
	current.Owner = owner
	current.Epoch++
	current.ExpiresAt = now.Add(duration)
	m.items[collectorID] = current
	return current, nil
}

func (m *LeaseManager) Renew(collectorID, owner string, epoch int64, duration time.Duration) (Lease, error) {
	if m == nil {
		return Lease{}, errors.New("lease manager unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[collectorID]
	if !ok || current.Owner != owner || current.Epoch != epoch || !m.clock().Before(current.ExpiresAt) {
		return Lease{}, errors.New("collector lease is stale")
	}
	if duration <= 0 || duration > 5*time.Minute {
		duration = time.Minute
	}
	current.ExpiresAt = m.clock().Add(duration)
	m.items[collectorID] = current
	return current, nil
}

func (m *LeaseManager) Release(collectorID, owner string, epoch int64) error {
	if m == nil {
		return errors.New("lease manager unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[collectorID]
	if !ok || current.Owner != owner || current.Epoch != epoch {
		return errors.New("collector lease is stale")
	}
	delete(m.items, collectorID)
	return nil
}
