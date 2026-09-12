package smart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scout.local/scout/internal/collector"
)

type fixtureCommand struct {
	output   string
	exitCode int
}

type fixtureRunner struct {
	commands []fixtureCommand
	calls    [][]string
}

func (r *fixtureRunner) Run(_ context.Context, args ...string) (CommandResult, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(r.commands) == 0 {
		return CommandResult{}, errors.New("fixture command queue exhausted")
	}
	command := r.commands[0]
	r.commands = r.commands[1:]
	return CommandResult{Stdout: []byte(command.output), ExitCode: command.exitCode}, nil
}

func TestAdapterTranslatesSataSasAndNvmeFixtures(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		deviceType  string
		fixture     string
		exitCode    int
		temperature float64
		wear        *float64
		errors      float64
		status      string
		fault       string
	}{
		{name: "sata health failure retains metrics", path: "/dev/sda", deviceType: "sat", fixture: "smart-sata.json", exitCode: 8, temperature: 35, wear: floatPtr(7), errors: 0, status: "fault", fault: "true"},
		{name: "sas", path: "/dev/sdb", deviceType: "scsi", fixture: "smart-sas.json", exitCode: 0, temperature: 31, errors: 2, status: "online", fault: "false"},
		{name: "nvme", path: "/dev/nvme0", deviceType: "nvme", fixture: "smart-nvme.json", exitCode: 0, temperature: 36, wear: floatPtr(7), errors: 3, status: "online", fault: "false"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			runner := &fixtureRunner{commands: []fixtureCommand{{output: string(readFixture(t, test.fixture)), exitCode: test.exitCode}}}
			adapter, err := NewWithRunner(runner, Config{HostID: "fixture-host", Targets: []Target{{Path: test.path, Type: test.deviceType}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Partial || len(result.Entities) != 1 {
				t.Fatalf("result = %+v", result)
			}
			entity := result.Entities[0]
			if entity.Status != test.status || entity.Labels["hardwareFault"] != test.fault || entity.Labels["identityStable"] != "true" {
				t.Fatalf("entity = %+v", entity)
			}
			assertMetricValue(t, result.Metrics, entity.ID, "smart.temperature", test.temperature)
			assertMetricValue(t, result.Metrics, entity.ID, "smart.error_count", test.errors)
			wear := metricFor(result.Metrics, entity.ID, "smart.wear_percent")
			if test.wear == nil {
				if wear.Availability != "unavailable" || wear.Value != nil {
					t.Fatalf("missing wear = %+v", wear)
				}
			} else {
				assertMetricValue(t, result.Metrics, entity.ID, "smart.wear_percent", *test.wear)
			}
		})
	}
}

func TestAdapterPreservesStandbyAndPermissionAsUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		fixture string
		want    string
	}{
		{name: "standby", path: "/dev/sdc", fixture: "smart-standby.json", want: "standby"},
		{name: "permission", path: "/dev/sdd", fixture: "smart-permission.json", want: "permission"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			runner := &fixtureRunner{commands: []fixtureCommand{{output: string(readFixture(t, test.fixture)), exitCode: 2}}}
			adapter, err := NewWithRunner(runner, Config{HostID: "fixture-host", Targets: []Target{{Path: test.path, Type: "sat"}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Entities) != 1 || result.Entities[0].Status != "unavailable" || !strings.Contains(strings.ToLower(result.Diagnostic), test.want) {
				t.Fatalf("result = %+v", result)
			}
			for _, metric := range result.Metrics {
				if metric.Availability != "unavailable" || metric.Value != nil {
					t.Fatalf("unavailable fixture emitted a value: %+v", metric)
				}
			}
			if len(runner.calls) != 1 || !containsArgs(runner.calls[0], "-n", "standby,0") || containsAnyArg(runner.calls[0], "-t", "--test") {
				t.Fatalf("smartctl query was not bounded and non-waking: %v", runner.calls)
			}
		})
	}
}

func TestSmartIdentitySurvivesPathChangesAndSeparatesReplacementOrAmbiguity(t *testing.T) {
	wwn := smartDevice{Path: "/dev/sda", Type: "sat", Model: "Scout", Serial: "serial-a", WWN: "naa.5000c50012345678"}
	pathChanged := wwn
	pathChanged.Path = "/dev/disk/by-id/wwn-0x5000c50012345678"
	oldID, oldStable, oldSource := stableEntityID("host-a", wwn, false)
	newID, newStable, newSource := stableEntityID("host-a", pathChanged, false)
	if oldID != newID || !oldStable || !newStable || oldSource != "wwn" || newSource != "wwn" {
		t.Fatalf("path change changed stable identity: old=%q/%t/%q new=%q/%t/%q", oldID, oldStable, oldSource, newID, newStable, newSource)
	}

	replacement := wwn
	replacement.WWN = "naa.5000c50087654321"
	replacementID, replacementStable, _ := stableEntityID("host-a", replacement, false)
	if replacementID == oldID || !replacementStable {
		t.Fatalf("replacement was merged into old identity: old=%q replacement=%q stable=%t", oldID, replacementID, replacementStable)
	}

	duplicate := smartDevice{Path: "/dev/sdb", Type: "sat", Model: "Scout", Serial: "serial-duplicate"}
	duplicateID, duplicateStable, source := stableEntityID("host-a", duplicate, true)
	if duplicateID == "" || duplicateStable || source != "ambiguous-path" {
		t.Fatalf("ambiguous identity was advertised stable: id=%q stable=%t source=%q", duplicateID, duplicateStable, source)
	}
	otherPath := duplicate
	otherPath.Path = "/dev/sdc"
	otherID, otherStable, _ := stableEntityID("host-a", otherPath, true)
	if duplicateID == otherID || otherStable {
		t.Fatalf("ambiguous paths were merged: first=%q second=%q", duplicateID, otherID)
	}
}

func TestAdapterUsesFixedScanAndSkipsUnknownTargets(t *testing.T) {
	scan := `{"devices":[{"name":"/dev/sda","type":"sat"},{"name":"relative","type":"sat"},{"name":"/dev/unknown","type":"megaraid"}]}`
	runner := &fixtureRunner{commands: []fixtureCommand{
		{output: scan},
		{output: string(readFixture(t, "smart-sata.json")), exitCode: 8},
	}}
	adapter, err := NewWithRunner(runner, Config{HostID: "fixture-host"})
	if err != nil {
		t.Fatal(err)
	}
	if detected, err := adapter.Detect(context.Background()); err != nil || !detected {
		t.Fatalf("detect = %t, %v", detected, err)
	}
	result, err := adapter.Collect(context.Background())
	if err != nil || len(result.Entities) != 1 {
		t.Fatalf("collect = %+v, %v", result, err)
	}
	if len(runner.calls) != 2 || !containsArgs(runner.calls[0], "--scan", "-j") || containsAnyArg(runner.calls[0], "-t", "--test") {
		t.Fatalf("scan was not fixed and read-only: %v", runner.calls)
	}
	if !containsArgs(runner.calls[1], "-n", "standby,0") || !containsAnyArg(runner.calls[1], "--", "/dev/sda") {
		t.Fatalf("device query was not fixed and non-waking: %v", runner.calls)
	}
}

func TestAdapterRejectsUnboundedOrUnallowlistedTargets(t *testing.T) {
	if _, err := NewWithRunner(&fixtureRunner{}, Config{Targets: []Target{{Path: "relative", Type: "sat"}}}); err == nil {
		t.Fatal("relative SMART device path accepted")
	}
	if _, err := NewWithRunner(&fixtureRunner{}, Config{Targets: []Target{{Path: "/dev/sda", Type: "megaraid"}}}); err == nil {
		t.Fatal("unallowlisted SMART bridge type accepted")
	}
	tooMany := make([]Target, maxDevices+1)
	for index := range tooMany {
		tooMany[index] = Target{Path: "/dev/sd" + string(rune('a'+index)), Type: "sat"}
	}
	if _, err := NewWithRunner(&fixtureRunner{}, Config{Targets: tooMany}); err == nil {
		t.Fatal("oversized SMART target list accepted")
	}
}

func TestAdapterRegistersWithBoundedSMARTDescriptor(t *testing.T) {
	adapter, err := NewWithRunner(&fixtureRunner{}, Config{Targets: []Target{{Path: "/dev/sda", Type: "sat"}}})
	if err != nil {
		t.Fatal(err)
	}
	registry := collector.NewRegistry()
	if err := Register(registry, adapter); err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 1 || descriptors[0].ID != CollectorID || descriptors[0].Interval != collectionInterval || descriptors[0].Deadline != totalCollectionLimit || descriptors[0].EntityLimit != maxDevices {
		t.Fatalf("descriptor = %+v", descriptors)
	}
	var listed *collector.Descriptor
	for _, descriptor := range collector.DefaultDescriptors() {
		if descriptor.ID == CollectorID {
			copy := descriptor
			listed = &copy
			break
		}
	}
	if listed == nil || listed.RequiredPermission[0] != "smartctl:read" || listed.ConfigSchema["standbyPolicy"] == "" || listed.Deadline != totalCollectionLimit {
		t.Fatalf("default SMART descriptor = %+v", listed)
	}
}

func TestAdapterReportsMissingSmartctlAsUnavailable(t *testing.T) {
	runner := &fixtureRunner{}
	adapter, err := NewWithRunner(runner, Config{HostID: "fixture-host"})
	if err != nil {
		t.Fatal(err)
	}
	if detected, err := adapter.Detect(context.Background()); detected || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing smartctl detect = %t, %v", detected, err)
	}
	if _, err := adapter.Collect(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing smartctl collect = %v", err)
	}
}

func assertMetricValue(t *testing.T, metrics []collector.Metric, entityID, name string, want float64) {
	t.Helper()
	metric := metricFor(metrics, entityID, name)
	if metric.Metric == "" || metric.Availability != "current" || metric.Value == nil || *metric.Value != want {
		t.Fatalf("%s/%s = %+v, want current value %v", entityID, name, metric, want)
	}
}

func metricFor(metrics []collector.Metric, entityID, name string) collector.Metric {
	for _, metric := range metrics {
		if metric.EntityID == entityID && metric.Metric == name {
			return metric
		}
	}
	return collector.Metric{}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("fixture path unavailable")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../tests/fixtures/collectors", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func floatPtr(value float64) *float64 { return &value }

func containsArgs(args []string, values ...string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == values[0] && args[index+1] == values[1] {
			return true
		}
	}
	return false
}

func containsAnyArg(args []string, values ...string) bool {
	for _, arg := range args {
		for _, value := range values {
			if arg == value {
				return true
			}
		}
	}
	return false
}
