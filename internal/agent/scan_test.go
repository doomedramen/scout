package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/store"
)

func validScanAssignment(now time.Time) ScanAssignment {
	return ScanAssignment{
		RunID:               "run-1",
		LeaseEpoch:          1,
		ScopeID:             "scope-1",
		ScopeRevision:       2,
		AssignmentExpiresAt: now.Add(time.Minute),
		Ranges:              []string{"192.0.2.0/30"},
		Exclusions:          []string{"192.0.2.3"},
		EntryPoints: []store.ScanEntryPoint{{
			ID: "ssh-default", Name: "SSH", Transport: store.ScanTransportTCP, Port: 22, AccessMethod: store.ScanAccessSSH, Enabled: true,
		}},
		Limits: store.ScanLimits{ProbesPerSecond: 100, Concurrency: 2, TargetBudget: 4, AttemptBudget: 3, TimeoutMilliseconds: 1000, RunDeadlineSeconds: 30, ResultPageSize: 2},
	}
}

func TestValidateScanAssignmentRejectsExpiredAndUnsafeWork(t *testing.T) {
	now := time.Date(2026, 9, 12, 22, 0, 0, 0, time.UTC)
	assignment := validScanAssignment(now)
	if err := ValidateScanAssignment(assignment, now); err != nil {
		t.Fatal(err)
	}
	assignment.AssignmentExpiresAt = now
	if err := ValidateScanAssignment(assignment, now); err == nil {
		t.Fatal("expired scan assignment was accepted")
	}
	assignment = validScanAssignment(now)
	assignment.EntryPoints[0].Transport = "udp"
	if err := ValidateScanAssignment(assignment, now); err == nil {
		t.Fatal("unsupported scan transport was accepted")
	}
	assignment = validScanAssignment(now)
	assignment.Limits.AttemptBudget = 5
	if err := ValidateScanAssignment(assignment, now); err == nil {
		t.Fatal("attempt budget beyond bounded plan was accepted")
	}
}

func TestBuildScanResultPagesBoundsResultsAndFinalSummary(t *testing.T) {
	now := time.Date(2026, 9, 12, 22, 0, 0, 0, time.UTC)
	assignment := validScanAssignment(now)
	results := []discovery.ProbeResult{
		{Address: "192.0.2.0", Port: 22, Outcome: "open", Reachable: true},
		{Address: "192.0.2.1", Port: 22, Outcome: "closed", ReasonCode: "connection_refused"},
		{Address: "192.0.2.2", Port: 22, Outcome: "filtered", ReasonCode: "timeout"},
	}
	pages, err := BuildScanResultPages(assignment, "agent-1", results, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].Final || pages[0].Summary != nil || !pages[1].Final || pages[1].Summary == nil {
		t.Fatalf("unexpected page shape: %+v", pages)
	}
	if pages[1].Summary.AttemptsPlanned != 3 || pages[1].Summary.AttemptsCompleted != 3 || pages[1].Summary.TargetsPlanned != 3 {
		t.Fatalf("unexpected final summary: %+v", pages[1].Summary)
	}
	for _, page := range pages {
		if len(page.Results) > assignment.Limits.ResultPageSize {
			t.Fatalf("page exceeded configured size: %d", len(page.Results))
		}
	}
}

func TestStartScanIsNonBlockingAndSingleFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	runtime := &Runtime{scanExecutor: func(context.Context, ScanAssignment) error {
		close(started)
		<-release
		return nil
	}}
	assignment := validScanAssignment(time.Now().UTC())
	if !runtime.startScan(context.Background(), assignment) {
		t.Fatal("first scan was not started")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scan did not start asynchronously")
	}
	if runtime.startScan(context.Background(), assignment) {
		t.Fatal("second scan bypassed one-active-scan fence")
	}
	close(release)
}

func TestStartScanCancelsWhenDesiredAssignmentChanges(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	finished := make(chan struct{})
	runtime := &Runtime{scanExecutor: func(ctx context.Context, assignment ScanAssignment) error {
		if assignment.RunID != "run-1" {
			return nil
		}
		close(started)
		<-ctx.Done()
		close(cancelled)
		close(finished)
		return ctx.Err()
	}}
	first := validScanAssignment(time.Now().UTC())
	second := first
	second.ScopeRevision = 3
	if !runtime.startScan(context.Background(), first) {
		t.Fatal("first scan was not started")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first scan did not start")
	}
	if runtime.startScan(context.Background(), second) {
		t.Fatal("replacement scan started before the active scan released its fence")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("active scan was not cancelled after its assignment changed")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled scan did not finish")
	}
}

func TestScanResultSpoolIsSeparateAndSixteenMiBBounded(t *testing.T) {
	runtime, err := NewRuntime(Config{ServerURL: "http://127.0.0.1:1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(strings.Repeat("x", 1024*1024))
	for index := 0; index < 20; index++ {
		if err := runtime.scanSpool.Add(payload); err != nil {
			t.Fatal(err)
		}
	}
	items, err := runtime.scanSpool.Items()
	if err != nil {
		t.Fatal(err)
	}
	var total int
	for _, item := range items {
		total += len(item.Data)
	}
	if total > 16<<20 {
		t.Fatalf("scan spool exceeded 16 MiB: %d", total)
	}
	telemetryItems, err := runtime.spool.Items()
	if err != nil {
		t.Fatal(err)
	}
	if len(telemetryItems) != 0 {
		t.Fatalf("scan pages leaked into telemetry spool: %+v", telemetryItems)
	}
}

func TestScanResultPagesRetryThroughTheSeparateSpool(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/v1/scan-results" {
			http.NotFound(w, r)
			return
		}
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	runtime, err := NewRuntime(Config{ServerURL: server.URL, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	runtime.identity.AgentToken = "scan-token"
	page := discovery.ScanResultPage{ProtocolVersion: 1, RunID: "run-1", LeaseEpoch: 1, ScopeRevision: 1, PageOrdinal: 0, ObservedFrom: time.Now().UTC(), ObservedTo: time.Now().UTC(), Final: true}
	if err := runtime.postScanPages(context.Background(), []discovery.ScanResultPage{page}); err == nil {
		t.Fatal("failed scan page was not surfaced")
	}
	items, err := runtime.scanSpool.Items()
	if err != nil || len(items) != 1 {
		t.Fatalf("failed page was not spooled: %+v err=%v", items, err)
	}
	if err := runtime.flushScanSpool(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err = runtime.scanSpool.Items()
	if err != nil || len(items) != 0 || requests != 2 {
		t.Fatalf("spooled page was not retried exactly once: items=%+v requests=%d err=%v", items, requests, err)
	}
}

func TestReportOnceKeepsTelemetryAheadOfScanExecution(t *testing.T) {
	var eventMu sync.Mutex
	events := []string{}
	started := make(chan struct{})
	release := make(chan struct{})
	now := time.Now().UTC()
	assignment := validScanAssignment(now)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eventMu.Lock()
		events = append(events, r.URL.Path)
		eventMu.Unlock()
		if r.URL.Path == "/api/v1/agent/v1/desired-state" {
			_ = json.NewEncoder(w).Encode(map[string]any{"scanAssignment": assignment})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	runtime, err := NewRuntime(Config{ServerURL: server.URL, DataDir: t.TempDir(), Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	runtime.identity.AgentID = "agent-1"
	runtime.identity.AgentToken = "scan-token"
	runtime.scanExecutor = func(context.Context, ScanAssignment) error {
		close(started)
		<-release
		return nil
	}
	startedAt := time.Now()
	if err := runtime.ReportOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("telemetry waited for scan execution: %s", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scan execution did not start separately")
	}
	eventMu.Lock()
	eventsCopy := append([]string(nil), events...)
	eventMu.Unlock()
	batchIndex, heartbeatIndex, desiredIndex := -1, -1, -1
	for index, event := range eventsCopy {
		switch event {
		case "/api/v1/agent/v1/batches":
			if batchIndex == -1 {
				batchIndex = index
			}
		case "/api/v1/agent/v1/heartbeat":
			if heartbeatIndex == -1 {
				heartbeatIndex = index
			}
		case "/api/v1/agent/v1/desired-state":
			if desiredIndex == -1 {
				desiredIndex = index
			}
		}
	}
	if batchIndex < 0 || heartbeatIndex < 0 || desiredIndex < 0 || batchIndex > desiredIndex || heartbeatIndex > desiredIndex {
		t.Fatalf("telemetry/scan event order: %v", eventsCopy)
	}
	close(release)
}
