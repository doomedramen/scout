package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestAgentScanResultRouteUsesAuthenticatedAgentIdentity(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	now := time.Date(2026, 9, 12, 16, 0, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "agent-route"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{SiteID: site.ID, DisplayName: "agent-route-device", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	const agentID = "agent-route-1"
	const agentToken = "agent-route-token"
	if err := repository.CreateAgentIdentity(ctx, store.AgentIdentity{ID: agentID, DeviceID: device.ID, AuthTokenHash: store.HashToken(agentToken), ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := repository.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy.AgentIDs = []string{agentID}
	scanPolicy, err = repository.UpdateScanPolicy(ctx, scope.ID, scanPolicy.Revision, scanPolicy)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repository.CreateScanRun(ctx, store.ScanRun{ID: "agent-route-run", ScopeID: scope.ID, ScopeRevision: scanPolicy.Revision, ScannerKind: "agent", ScannerID: agentID, ScheduledAt: now.Add(-time.Minute), AssignmentExpiresAt: now.Add(time.Minute), PolicySnapshot: store.ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: scanPolicy.EntryPoints, Limits: scanPolicy.Limits}, TargetsPlanned: 1, AttemptsPlanned: 1})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := repository.LeaseScanRun(ctx, run.ID, agentID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartScanRun(ctx, run.ID, agentID, leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}

	page := map[string]any{
		"protocolVersion": 1,
		"runId":           run.ID,
		"leaseEpoch":      leased.LeaseEpoch,
		"scopeRevision":   scanPolicy.Revision,
		"pageOrdinal":     0,
		"observedFrom":    now.Add(-time.Second),
		"observedTo":      now,
		"final":           true,
		"results": []map[string]any{{
			"address": "192.0.2.10", "entryPointId": scanPolicy.EntryPoints[0].ID, "transport": "tcp", "port": 22, "outcome": "open", "observedAt": now,
		}},
		"summary": map[string]any{"targetsPlanned": 1, "attemptsPlanned": 1, "attemptsCompleted": 1, "partialReason": nil},
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/v1/scan-results", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+agentToken)
	response := httptest.NewRecorder()
	application, err := NewApp(repository, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		current, _ := repository.GetScanRun(ctx, run.ID)
		t.Fatalf("scan result response: %d %s (run=%+v)", response.Code, response.Body, current)
	}
	stored, err := repository.GetScanRun(ctx, run.ID)
	if err != nil || stored.State != store.ScanRunCompleted {
		t.Fatalf("route did not finalize run: %+v err=%v", stored, err)
	}
}
