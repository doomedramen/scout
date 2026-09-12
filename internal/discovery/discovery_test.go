package discovery

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestExpandTargetsIsFiniteAndExclusionsWin(t *testing.T) {
	targets, err := ExpandTargets([]string{"192.0.2.0/24", "2001:db8::/64"}, []string{"192.0.2.1", "192.0.2.4/30"}, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 9 {
		t.Fatalf("target budget was not enforced: %d", len(targets))
	}
	for _, item := range targets {
		if item == "192.0.2.1" || item == "192.0.2.4" {
			t.Fatalf("excluded target returned: %s", item)
		}
	}
}

func TestExpandTargetsRejectsInvalidRanges(t *testing.T) {
	if _, err := ExpandTargets([]string{"not-an-address"}, nil, 2); err == nil {
		t.Fatal("invalid target range was accepted")
	}
	if _, err := ExpandTargets([]string{"192.0.2.0/24"}, nil, 5000); err == nil {
		t.Fatal("unbounded target budget was accepted")
	}
}

func TestExpandTargetsExclusionsTakePrecedenceForLiteralsAndPrefixes(t *testing.T) {
	targets, err := ExpandTargets([]string{"192.0.2.0/29"}, []string{"192.0.2.1", "192.0.2.4/30"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.0.2.0", "192.0.2.2", "192.0.2.3"}
	if len(targets) != len(want) {
		t.Fatalf("excluded targets were retained: got=%v want=%v", targets, want)
	}
	for index, target := range want {
		if targets[index] != target {
			t.Fatalf("target %d=%q want %q; all=%v", index, targets[index], target, targets)
		}
	}
}

func TestProbeHonorsCancellationAndReportsClosedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results, err := Probe(ctx, []string{"127.0.0.1"}, ProbePolicy{Ports: []int{port, port + 1}, Concurrency: 2, ProbesPerSecond: 100, TargetBudget: 2, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !results[0].Reachable && !results[1].Reachable {
		t.Fatalf("probe did not record reachable listener: %+v", results)
	}
	if _, err := Probe(context.Background(), []string{"127.0.0.1"}, ProbePolicy{ProbesPerSecond: 0, Timeout: 11 * time.Second}); err == nil {
		t.Fatal("excessive timeout was accepted")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("probe exceeded its bounded timeout")
	}
}

type probeTimeoutError struct{}

func (probeTimeoutError) Error() string   { return "probe timeout" }
func (probeTimeoutError) Timeout() bool   { return true }
func (probeTimeoutError) Temporary() bool { return true }

func TestProbeClassifiesTCPOutcomesWithoutApplicationPayload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	openPort := listener.Addr().(*net.TCPAddr).Port
	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	closedPort := closedListener.Addr().(*net.TCPAddr).Port
	closedListener.Close()
	bytesRead := make(chan int, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			bytesRead <- -1
			return
		}
		buffer := make([]byte, 32)
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		count, _ := connection.Read(buffer)
		bytesRead <- count
		connection.Close()
	}()
	defer listener.Close()

	results, err := Probe(context.Background(), []string{"127.0.0.1"}, ProbePolicy{Ports: []int{openPort, closedPort}, Concurrency: 1, ProbesPerSecond: 1000, TargetBudget: 1, AttemptBudget: 2, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	byPort := map[int]ProbeResult{}
	for _, result := range results {
		byPort[result.Port] = result
	}
	if byPort[openPort].Outcome != "open" || !byPort[openPort].Reachable {
		t.Fatalf("open TCP outcome: %+v", byPort[openPort])
	}
	if byPort[closedPort].Outcome != "closed" || byPort[closedPort].Reachable {
		t.Fatalf("closed TCP outcome: %+v", byPort[closedPort])
	}
	if count := <-bytesRead; count != 0 {
		t.Fatalf("probe sent application payload: %d bytes", count)
	}
}

func TestProbeHonorsAttemptBudgetAndEmitsSkippedOutcomes(t *testing.T) {
	var attempts atomic.Int32
	results, err := ProbeWithOptions(context.Background(), []string{"192.0.2.10", "192.0.2.11"}, ProbePolicy{Ports: []int{22, 80, 443}, Concurrency: 1, ProbesPerSecond: 1000, TargetBudget: 2, AttemptBudget: 2, Timeout: 100 * time.Millisecond}, ProbeOptions{DialContext: func(context.Context, string, string) (net.Conn, error) {
		attempts.Add(1)
		return nil, syscall.ECONNREFUSED
	}})
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("dial attempts=%d, want 2", attempts.Load())
	}
	skipped := 0
	for _, result := range results {
		if result.Outcome == "skipped" {
			skipped++
		}
	}
	if len(results) != 6 || skipped != 4 {
		t.Fatalf("attempt-budget outcomes: len=%d skipped=%d results=%+v", len(results), skipped, results)
	}
	if _, err := Probe(context.Background(), []string{"192.0.2.10"}, ProbePolicy{Ports: []int{22, 80}, TargetBudget: 1, AttemptBudget: 3}); err == nil {
		t.Fatal("attempt budget exceeded target and entry-point multiplication")
	}
}

func TestProbeClassifiesTimeoutAndStopsCleanlyOnCancellation(t *testing.T) {
	results, err := ProbeWithOptions(context.Background(), []string{"192.0.2.10"}, ProbePolicy{Ports: []int{22}, Concurrency: 1, ProbesPerSecond: 1000, TargetBudget: 1, AttemptBudget: 1, Timeout: 100 * time.Millisecond}, ProbeOptions{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, probeTimeoutError{}
	}})
	if err != nil || len(results) != 1 || results[0].Outcome != "filtered" || results[0].ReasonCode != "timeout" {
		t.Fatalf("timeout outcome: %+v err=%v", results, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var attempts atomic.Int32
	results, err = ProbeWithOptions(ctx, []string{"192.0.2.10", "192.0.2.11"}, ProbePolicy{Ports: []int{22}, Concurrency: 1, ProbesPerSecond: 1000, TargetBudget: 2, AttemptBudget: 2, Timeout: time.Second}, ProbeOptions{DialContext: func(context.Context, string, string) (net.Conn, error) {
		attempts.Add(1)
		cancel()
		return nil, context.Canceled
	}})
	if !errors.Is(err, context.Canceled) || attempts.Load() != 1 {
		t.Fatalf("cancellation outcome: attempts=%d results=%+v err=%v", attempts.Load(), results, err)
	}
}

func TestProbeClassifiesUnreachableAndScannerErrorsWithoutRemoteText(t *testing.T) {
	errorsToReturn := []error{syscall.ENETUNREACH, errors.New("remote response must not escape")}
	index := 0
	results, err := ProbeWithOptions(context.Background(), []string{"192.0.2.10"}, ProbePolicy{Ports: []int{22, 80}, Concurrency: 1, ProbesPerSecond: 1000, TargetBudget: 1, AttemptBudget: 2, Timeout: 100 * time.Millisecond}, ProbeOptions{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dialErr := errorsToReturn[index]
		index++
		return nil, dialErr
	}})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Outcome != "unreachable" || results[0].ReasonCode != "network_unreachable" || results[1].Outcome != "scanner_error" || results[1].ReasonCode != "scanner_error" {
		t.Fatalf("classified outcomes: %+v", results)
	}
	if strings.Contains(results[1].Reason, "remote response") {
		t.Fatalf("remote error text escaped: %+v", results[1])
	}
}
