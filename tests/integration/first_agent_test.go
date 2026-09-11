package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scout.local/scout/internal/agent"
	"scout.local/scout/internal/control"
	"scout.local/scout/internal/store"
)

func TestRuntimeEnrollmentPersistenceAndReporting(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	application, err := control.NewApp(repository, nil, control.Config{SetupToken: "integration-setup-token"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	site, err := repository.CreateSite(ctx, store.Site{Name: "integration-lab"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{DisplayName: "first-agent", SiteID: site.ID, Platform: "linux", Architecture: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	invitation := "integration-one-time-invitation"
	if err := repository.ReserveInvitation(ctx, store.BootstrapInvitation{TokenHash: store.HashToken(invitation), DeviceID: device.ID, ExpiresAt: time.Now().UTC().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	invitationFile := filepath.Join(t.TempDir(), "invitation")
	if err := os.WriteFile(invitationFile, []byte(invitation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	first, err := agent.NewRuntime(agent.Config{ServerURL: server.URL, InvitationFile: invitationFile, DataDir: dataDir, HTTPClient: server.Client(), Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	firstDevice, err := repository.GetDevice(ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstDevice.AgentID == "" || firstDevice.LastHeartbeat == nil {
		t.Fatalf("agent did not report: %+v", firstDevice)
	}
	series, err := repository.QueryMetrics(ctx, store.MetricQuery{DeviceID: device.ID, MaxPoints: 600})
	if err != nil {
		t.Fatal(err)
	}
	if len(series) == 0 {
		t.Fatal("agent batch did not create metric history")
	}

	identityPath := filepath.Join(dataDir, "identity.json")
	identityData, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	var identity map[string]any
	if err := json.Unmarshal(identityData, &identity); err != nil || identity["agentId"] == "" {
		t.Fatalf("persisted identity is invalid: %s", identityData)
	}
	identityInfo, err := os.Stat(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	if identityInfo.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions=%o", identityInfo.Mode().Perm())
	}

	second, err := agent.NewRuntime(agent.Config{ServerURL: server.URL, DataDir: dataDir, HTTPClient: server.Client(), Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	secondDevice, err := repository.GetDevice(ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondDevice.AgentID != firstDevice.AgentID {
		t.Fatalf("restart created a different agent identity: first=%s second=%s", firstDevice.AgentID, secondDevice.AgentID)
	}

	lastHeartbeat := *secondDevice.LastHeartbeat
	value := 42.0
	if _, err := repository.IngestBatch(ctx, secondDevice.AgentID, "freshness-test", "batch-1", "hash-1", []store.MetricSample{{EntityID: "host", Metric: "test.metric", Value: &value, Availability: store.FreshnessCurrent, Unit: "count", ObservedAt: lastHeartbeat, ReceivedAt: lastHeartbeat}}, nil, 0); err != nil {
		t.Fatal(err)
	}
	repository.SetClock(func() time.Time { return lastHeartbeat.Add(2 * time.Minute) })
	devices, err := repository.ListDevices(ctx, store.DeviceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Availability != store.AvailabilityOffline {
		t.Fatalf("lost contact was not projected as offline: %+v", devices)
	}
	if devices[0].MetricFreshness["test.metric"] != store.FreshnessStale {
		t.Fatalf("metric freshness did not become stale: %+v", devices[0].MetricFreshness)
	}
}
