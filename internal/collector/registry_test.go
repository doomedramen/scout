package collector

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	result ServiceResult
	err    error
	panic  bool
}

func (f fakeSource) Detect(context.Context) (bool, error) { return true, nil }
func (f fakeSource) Collect(context.Context) (ServiceResult, error) {
	if f.panic {
		panic("fixture panic")
	}
	return f.result, f.err
}
func (f fakeSource) Close() error { return nil }

func TestRegistryContainsProviderFailure(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Descriptor{ID: "bad", Provider: "fixture", EntityLimit: 1, Deadline: time.Second}, fakeSource{panic: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Collect(context.Background(), "bad"); err == nil {
		t.Fatal("collector panic was swallowed without a degraded error")
	}
	states := registry.States()
	if len(states) != 1 || states[0].State != CollectorDegraded || states[0].Diagnostic == "" {
		t.Fatalf("panic state not recorded: %+v", states)
	}
}

func TestRegistryEnforcesEntityLimitAndSchedulerCancellation(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Descriptor{ID: "limited", Provider: "fixture", EntityLimit: 1, Deadline: time.Second}, fakeSource{result: ServiceResult{Entities: []Entity{{ID: "1"}, {ID: "2"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Collect(context.Background(), "limited"); err == nil {
		t.Fatal("entity limit was not enforced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	(&Scheduler{Registry: registry}).RunOnce(ctx, func(_ string, _ ServiceResult, err error) { called = errors.Is(err, context.Canceled) })
	if !called {
		t.Fatal("scheduler did not surface cancellation")
	}
}
