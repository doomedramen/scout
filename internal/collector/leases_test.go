package collector

import (
	"testing"
	"time"
)

func TestLeaseManagerFencesDuplicateOwnersAndStaleEpochs(t *testing.T) {
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	manager := NewLeaseManager()
	manager.SetClock(func() time.Time { return now })
	first, err := manager.Acquire("docker", "worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire("docker", "worker-b", time.Minute); err == nil {
		t.Fatal("second owner acquired active lease")
	}
	if _, err := manager.Renew("docker", "worker-a", first.Epoch+1, time.Minute); err == nil {
		t.Fatal("stale epoch was accepted")
	}
	now = now.Add(2 * time.Minute)
	second, err := manager.Acquire("docker", "worker-b", time.Minute)
	if err != nil || second.Epoch <= first.Epoch {
		t.Fatalf("expired lease did not fail over: %+v %v", second, err)
	}
}
