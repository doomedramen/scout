package discovery

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

type coordinatorTestScanner struct {
	scan func(context.Context, []string, ProbePolicy) ([]ProbeResult, error)
}

func (s coordinatorTestScanner) Scan(ctx context.Context, addresses []string, probePolicy ProbePolicy) ([]ProbeResult, error) {
	if s.scan == nil {
		return nil, nil
	}
	return s.scan(ctx, addresses, probePolicy)
}

func coordinatorFixture(t *testing.T) (*store.Store, store.Scope, store.ScanPolicy, *time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	repository := store.NewMemory()
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.10"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := repository.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy.ServerEnabled = true
	scanPolicy.ScheduleSeconds = 60
	scanPolicy.Limits.TargetBudget = 1
	scanPolicy.Limits.AttemptBudget = 1
	scanPolicy.Limits.RunDeadlineSeconds = 600
	scanPolicy, err = repository.UpdateScanPolicy(ctx, scope.ID, scanPolicy.Revision, scanPolicy)
	if err != nil {
		t.Fatal(err)
	}
	return repository, scope, scanPolicy, &now
}

func coordinatorFor(repository *store.Store, scanner Scanner) *Coordinator {
	return &Coordinator{Store: repository, Policy: &policy.Engine{Store: repository}, Scanner: scanner, ServerID: DefaultServerVantageID, LeaseDuration: time.Minute}
}

func finishCoordinatorRun(t *testing.T, repository *store.Store, run store.ScanRun) {
	t.Helper()
	ctx := context.Background()
	leased, err := repository.LeaseScanRun(ctx, run.ID, DefaultServerVantageID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartScanRun(ctx, run.ID, DefaultServerVantageID, leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FinalizeScanRun(ctx, run.ID, DefaultServerVantageID, leased.LeaseEpoch, store.ScanRunCompleted, "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorSchedulesWithStableJitterOnlyAfterTheInterval(t *testing.T) {
	ctx := context.Background()
	repository, scope, scanPolicy, now := coordinatorFixture(t)
	coordinator := coordinatorFor(repository, coordinatorTestScanner{})
	first, err := coordinator.ScheduleDue(ctx)
	if err != nil || len(first) != 1 {
		t.Fatalf("initial due run: %+v err=%v", first, err)
	}
	if !first[0].ScheduledAt.Equal(*now) {
		t.Fatalf("first scheduled run did not start immediately: %+v", first[0])
	}
	finishCoordinatorRun(t, repository, first[0])
	jitter := ScanScheduleJitter(scope.ID, "server", DefaultServerVantageID, time.Duration(scanPolicy.ScheduleSeconds)*time.Second)
	if jitter < 0 || jitter > 6*time.Second {
		t.Fatalf("jitter escaped bounded schedule window: %s", jitter)
	}
	if jitter != ScanScheduleJitter(scope.ID, "server", DefaultServerVantageID, time.Duration(scanPolicy.ScheduleSeconds)*time.Second) {
		t.Fatal("schedule jitter was not deterministic")
	}
	*now = first[0].ScheduledAt.Add(time.Duration(scanPolicy.ScheduleSeconds)*time.Second + jitter - time.Nanosecond)
	if due, err := coordinator.ScheduleDue(ctx); err != nil || len(due) != 0 {
		t.Fatalf("run became due before interval plus jitter: %+v err=%v", due, err)
	}
	*now = first[0].ScheduledAt.Add(time.Duration(scanPolicy.ScheduleSeconds)*time.Second + jitter)
	due, err := coordinator.ScheduleDue(ctx)
	if err != nil || len(due) != 1 {
		t.Fatalf("jittered run was not materialized: %+v err=%v", due, err)
	}
	wantScheduled := first[0].ScheduledAt.Add(time.Duration(scanPolicy.ScheduleSeconds)*time.Second + jitter)
	if !due[0].ScheduledAt.Equal(wantScheduled) {
		t.Fatalf("scheduledAt=%s want=%s", due[0].ScheduledAt, wantScheduled)
	}
}

func TestCoordinatorRequiresServerOptInAndKeepsOneActiveRun(t *testing.T) {
	ctx := context.Background()
	repository, scope, scanPolicy, _ := coordinatorFixture(t)
	scanPolicy.ServerEnabled = false
	updated, err := repository.UpdateScanPolicy(ctx, scope.ID, scanPolicy.Revision, scanPolicy)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := coordinatorFor(repository, coordinatorTestScanner{})
	if runs, err := coordinator.ScheduleDue(ctx); err != nil || len(runs) != 0 {
		t.Fatalf("server was scheduled without opt-in: %+v err=%v", runs, err)
	}
	updated.ServerEnabled = true
	if _, err := repository.UpdateScanPolicy(ctx, scope.ID, updated.Revision, updated); err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.ScheduleDue(ctx)
	if err != nil || len(first) != 1 {
		t.Fatalf("opted-in server was not scheduled: %+v err=%v", first, err)
	}
	second, err := coordinator.ScheduleDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("duplicate active run was scheduled: %+v", second)
	}
	page, err := repository.ListScanRuns(ctx, store.ScanRunQuery{ScopeID: scope.ID, ScannerID: DefaultServerVantageID, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("active run uniqueness was not preserved: %+v err=%v", page.Items, err)
	}
}

func TestCoordinatorReleasesExpiredLeaseAfterRestart(t *testing.T) {
	ctx := context.Background()
	repository, _, _, now := coordinatorFixture(t)
	firstCoordinator := coordinatorFor(repository, coordinatorTestScanner{})
	queued, err := firstCoordinator.ScheduleDue(ctx)
	if err != nil || len(queued) != 1 {
		t.Fatalf("scheduled run: %+v err=%v", queued, err)
	}
	leased, err := firstCoordinator.LeaseDue(ctx)
	if err != nil || len(leased) != 1 || leased[0].LeaseEpoch != 1 {
		t.Fatalf("initial lease: %+v err=%v", leased, err)
	}
	*now = now.Add(2 * time.Minute)
	restarted := coordinatorFor(repository, coordinatorTestScanner{})
	released, err := restarted.LeaseDue(ctx)
	if err != nil || len(released) != 1 || released[0].LeaseEpoch != 2 {
		t.Fatalf("restart did not re-lease expired run: %+v err=%v", released, err)
	}
	page, err := repository.ListScanRuns(ctx, store.ScanRunQuery{ScannerID: DefaultServerVantageID, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("restart duplicated active run: %+v err=%v", page.Items, err)
	}
}

func TestCoordinatorTurnsDeadlineAndScannerFailureIntoPartialRuns(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name         string
		scanner      Scanner
		wantReason   string
		wantAttempts int
	}{
		{name: "deadline", scanner: coordinatorTestScanner{scan: func(ctx context.Context, _ []string, _ ProbePolicy) ([]ProbeResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}, wantReason: "deadline"},
		{name: "scanner failure", scanner: coordinatorTestScanner{scan: func(context.Context, []string, ProbePolicy) ([]ProbeResult, error) {
			return nil, errors.New("fixture failure")
		}}, wantReason: "scanner_unavailable"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repository, _, _, _ := coordinatorFixture(t)
			coordinator := coordinatorFor(repository, testCase.scanner)
			coordinator.RunDeadline = 20 * time.Millisecond
			runs, err := coordinator.RunOnce(ctx)
			if err != nil || len(runs) != 1 {
				t.Fatalf("partial run execution: %+v err=%v", runs, err)
			}
			if runs[0].State != store.ScanRunPartial || runs[0].PartialReason != testCase.wantReason || runs[0].AttemptsCompleted != testCase.wantAttempts {
				t.Fatalf("partial state: %+v", runs[0])
			}
		})
	}
}

func TestCoordinatorExecutesControlledServerTCPScanAndPersistsEvidence(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	ctx := context.Background()
	now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	repository := store.NewMemory()
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "controlled-server-scan"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"127.0.0.1"}, AllowedMethods: []string{"tcp"}, Ports: []int{port}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := repository.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy.ServerEnabled = true
	scanPolicy.EntryPoints = []store.ScanEntryPoint{{ID: "ssh-fixture", Name: "SSH fixture", Transport: store.ScanTransportTCP, Port: port, AccessMethod: store.ScanAccessSSH, Enabled: true}}
	scanPolicy.Limits.TargetBudget = 1
	scanPolicy.Limits.AttemptBudget = 1
	scanPolicy, err = repository.UpdateScanPolicy(ctx, scope.ID, scanPolicy.Revision, scanPolicy)
	if err != nil {
		t.Fatal(err)
	}

	coordinator := coordinatorFor(repository, TCPScanner{})
	runs, err := coordinator.RunOnce(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("controlled scan: %+v err=%v", runs, err)
	}
	if runs[0].State != store.ScanRunCompleted || runs[0].OutcomeCounts.Open != 1 || runs[0].AttemptsCompleted != 1 {
		t.Fatalf("controlled scan state: %+v", runs[0])
	}
	observations, err := repository.ListEntryPointObservations(ctx, store.EntryPointObservationQuery{RunID: runs[0].ID, Limit: 10})
	if err != nil || len(observations.Items) != 1 || observations.Items[0].Outcome != "open" || observations.Items[0].Actionable {
		t.Fatalf("controlled scan evidence: %+v err=%v", observations.Items, err)
	}
}
