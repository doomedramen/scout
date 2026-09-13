package control

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/store"
)

func TestScanVantageRunProjectionKeepsProgressAndNextSchedulePerVantage(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	schedule := 5 * time.Minute
	completedAt := now.Add(-2 * time.Minute)
	serverRun := store.ScanRun{
		ID: "server-complete", ScopeID: "scope-1", ScannerKind: "server", ScannerID: "control-server",
		State: store.ScanRunCompleted, ScheduledAt: completedAt.Add(-time.Minute), FinishedAt: &completedAt,
	}
	serverActiveAt := now.Add(-30 * time.Second)
	serverActive := store.ScanRun{
		ID: "server-active", ScopeID: "scope-1", ScannerKind: "server", ScannerID: "control-server",
		State: store.ScanRunRunning, ScheduledAt: serverActiveAt, AttemptsPlanned: 10, AttemptsCompleted: 4,
	}
	agentCompletedAt := now.Add(-time.Hour)
	agentRun := store.ScanRun{
		ID: "agent-complete", ScopeID: "scope-1", ScannerKind: "agent", ScannerID: "agent-1",
		State: store.ScanRunPartial, ScheduledAt: agentCompletedAt.Add(-time.Minute), FinishedAt: &agentCompletedAt,
		PartialReason: "scanner_unavailable",
	}
	agentNextAt := now.Add(2 * time.Minute)
	agentNext := store.ScanRun{
		ID: "agent-next", ScopeID: "scope-1", ScannerKind: "agent", ScannerID: "agent-1",
		State: store.ScanRunQueued, ScheduledAt: agentNextAt,
	}

	serverProjection := projectScanVantageRuns(
		[]store.ScanRun{serverRun, serverActive, agentRun, agentNext},
		"server",
		"control-server",
	)
	if serverProjection.active == nil || serverProjection.active.ID != serverActive.ID {
		t.Fatalf("server active run = %+v", serverProjection.active)
	}
	if serverProjection.latestTerminal == nil || serverProjection.latestTerminal.ID != serverRun.ID {
		t.Fatalf("server latest terminal = %+v", serverProjection.latestTerminal)
	}
	serverNext := nextScanVantageSchedule(serverProjection, now, "scope-1", "server", "control-server", schedule)
	if serverNext == nil || !serverNext.Equal(discovery.NextScanScheduleAt(serverActiveAt, "scope-1", "server", "control-server", schedule)) {
		t.Fatalf("server next schedule = %v", serverNext)
	}

	agentProjection := projectScanVantageRuns(
		[]store.ScanRun{serverRun, serverActive, agentRun, agentNext},
		"agent",
		"agent-1",
	)
	if agentProjection.active == nil || agentProjection.active.ID != agentNext.ID {
		t.Fatalf("agent active run = %+v", agentProjection.active)
	}
	if agentProjection.latestTerminal == nil || agentProjection.latestTerminal.ID != agentRun.ID {
		t.Fatalf("agent latest terminal = %+v", agentProjection.latestTerminal)
	}
	agentSchedule := nextScanVantageSchedule(agentProjection, now, "scope-1", "agent", "agent-1", schedule)
	if agentSchedule == nil || !agentSchedule.Equal(agentNextAt) {
		t.Fatalf("future queued agent run was not reported as next schedule: %v", agentSchedule)
	}
}

func TestCandidateFilterPaginationIsBoundedAndRejectsUnsafeLimits(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	items := []store.Candidate{
		{ID: "candidate-1", Address: "192.0.2.1", State: "discovered", LastSeen: now},
		{ID: "candidate-2", Address: "192.0.2.2", State: "discovered", LastSeen: now},
		{ID: "candidate-3", Address: "192.0.2.3", State: "discovered", LastSeen: now},
	}

	firstRequest := httptest.NewRequest(http.MethodGet, "/api/v1/candidates?limit=2", nil)
	first, err := filterCandidates(items, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.items) != 2 || first.nextCursor == nil || *first.nextCursor == "" {
		t.Fatalf("first candidate page = %+v", first)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/api/v1/candidates?limit=2&cursor="+*first.nextCursor, nil)
	second, err := filterCandidates(items, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.items) != 1 || second.items[0].ID != "candidate-3" || second.nextCursor != nil {
		t.Fatalf("second candidate page = %+v", second)
	}
	if first.items[0].ID == second.items[0].ID || first.items[1].ID == second.items[0].ID {
		t.Fatalf("candidate cursor repeated an item: first=%+v second=%+v", first.items, second.items)
	}

	unsafeLimit := httptest.NewRequest(http.MethodGet, "/api/v1/candidates?limit=501", nil)
	if _, err := filterCandidates(items, unsafeLimit); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("limit above API maximum returned err=%v", err)
	}
}
