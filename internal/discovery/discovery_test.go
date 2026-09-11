package discovery

import (
	"context"
	"errors"
	"net"
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
