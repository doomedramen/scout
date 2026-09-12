package zfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/collector"
)

type fixtureCommand struct {
	output    string
	stderr    string
	exitCode  int
	truncated bool
}

type fixtureRunner struct {
	commands []fixtureCommand
	calls    [][]string
}

func (r *fixtureRunner) Run(_ context.Context, program string, args ...string) (CommandResult, error) {
	call := append([]string{program}, args...)
	r.calls = append(r.calls, call)
	if len(r.commands) == 0 {
		return CommandResult{}, errors.New("fixture command queue exhausted")
	}
	command := r.commands[0]
	r.commands = r.commands[1:]
	return CommandResult{Stdout: []byte(command.output), Stderr: []byte(command.stderr), ExitCode: command.exitCode, Truncated: command.truncated}, nil
}

type fixtureCounterReader struct {
	snapshots []map[string]Counter
	calls     int
	err       error
}

func (r *fixtureCounterReader) Read(_ context.Context) (map[string]Counter, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	if len(r.snapshots) == 0 {
		return map[string]Counter{}, nil
	}
	snapshot := r.snapshots[0]
	r.snapshots = r.snapshots[1:]
	return snapshot, nil
}

func TestAdapterCollectsCapacityHealthAndScrubWithoutConflatingScopes(t *testing.T) {
	counters := &fixtureCounterReader{snapshots: []map[string]Counter{readCountersFixture(t, "zfs-counters-first.txt")}}
	runner := &fixtureRunner{commands: []fixtureCommand{
		{output: string(readFixture(t, "zfs-pools.txt"))},
		{output: string(readFixture(t, "zfs-datasets.txt"))},
		{output: string(readFixture(t, "zfs-status.txt"))},
	}}
	adapter, err := NewWithRunner(runner, Config{HostID: "fixture-host", CounterReader: counters})
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = func() time.Time { return time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC) }
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Partial || len(result.Entities) != 6 {
		t.Fatalf("result = %+v", result)
	}

	var onlinePool, degradedPool collector.Entity
	for _, entity := range result.Entities {
		switch entity.Name {
		case "tank":
			if entity.Kind == "zfs_pool" {
				onlinePool = entity
			}
		case "rpool":
			if entity.Kind == "zfs_pool" {
				degradedPool = entity
			}
		}
	}
	if onlinePool.Status != "online" || onlinePool.Labels["healthState"] != "ONLINE" || onlinePool.Labels["scrubState"] != "FINISHED" || onlinePool.Labels["hardwareFault"] != "false" {
		t.Fatalf("online pool = %+v", onlinePool)
	}
	if degradedPool.Status != "fault" || degradedPool.Labels["healthState"] != "DEGRADED" || degradedPool.Labels["scrubState"] != "SCANNING" || degradedPool.Labels["scrubProgress"] != "10.00%" || degradedPool.Labels["scrubErrors"] != "0" || degradedPool.Labels["dataErrors"] != "1" || degradedPool.Labels["hardwareFault"] != "true" {
		t.Fatalf("degraded pool = %+v", degradedPool)
	}

	assertMetric(t, result.Metrics, onlinePool.ID, "zfs.pool.used", 12000000000000, "bytes", "physical", "current")
	assertMetric(t, result.Metrics, onlinePool.ID, "zfs.pool.capacity", 23999000000000, "bytes", "physical", "current")
	assertMetric(t, result.Metrics, onlinePool.ID, "zfs.pool.read_rate", 0, "bytes_per_second", "", "unavailable")
	assertMetric(t, result.Metrics, onlinePool.ID, "zfs.pool.write_rate", 0, "bytes_per_second", "", "unavailable")

	var dataset collector.Entity
	for _, entity := range result.Entities {
		if entity.Kind == "zfs_dataset" && entity.Name == "tank/apps" {
			dataset = entity
		}
	}
	if dataset.ID == "" || dataset.Labels["capacityScope"] != "usable" || dataset.Labels["pool"] != "tank" || dataset.Labels["identityStable"] != "true" {
		t.Fatalf("dataset = %+v", dataset)
	}
	assertMetric(t, result.Metrics, dataset.ID, "zfs.dataset.used", 1000000000000, "bytes", "usable", "current")
	assertMetric(t, result.Metrics, dataset.ID, "zfs.dataset.available", 11999000000000, "bytes", "usable", "current")
}

func TestAdapterCalculatesRatesAndCreatesGapOnCounterReset(t *testing.T) {
	first := readCountersFixture(t, "zfs-counters-first.txt")
	second := readCountersFixture(t, "zfs-counters-second.txt")
	reset := readCountersFixture(t, "zfs-counters-reset.txt")
	runner := &fixtureRunner{commands: repeatedCommands(t, 3)}
	counters := &fixtureCounterReader{snapshots: []map[string]Counter{first, second, reset}}
	adapter, err := NewWithRunner(runner, Config{HostID: "fixture-host", CounterReader: counters})
	if err != nil {
		t.Fatal(err)
	}
	current := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	adapter.now = func() time.Time { return current }

	firstResult, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tankID := entityID(t, firstResult.Entities, "zfs_pool", "tank")
	assertMetric(t, firstResult.Metrics, tankID, "zfs.pool.read_rate", 0, "bytes_per_second", "", "unavailable")

	current = current.Add(time.Minute)
	secondResult, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertMetric(t, secondResult.Metrics, tankID, "zfs.pool.read_rate", 10000000, "bytes_per_second", "", "current")
	assertMetric(t, secondResult.Metrics, tankID, "zfs.pool.write_rate", 10000000, "bytes_per_second", "", "current")

	current = current.Add(time.Minute)
	resetResult, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertMetric(t, resetResult.Metrics, tankID, "zfs.pool.read_rate", 0, "bytes_per_second", "", "unavailable")
	assertMetric(t, resetResult.Metrics, tankID, "zfs.pool.write_rate", 0, "bytes_per_second", "", "unavailable")
}

func TestAdapterMarksPartialRowsAndPermissionWithoutInventingHealth(t *testing.T) {
	runner := &fixtureRunner{commands: []fixtureCommand{
		{output: string(readFixture(t, "zfs-pools.txt"))},
		{output: string(readFixture(t, "zfs-datasets-partial.txt"))},
		{output: string(readFixture(t, "zfs-status-partial.txt"))},
	}}
	counters := &fixtureCounterReader{err: errors.New("kstat denied")}
	adapter, err := NewWithRunner(runner, Config{HostID: "fixture-host", CounterReader: counters})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Entities) != 4 || !strings.Contains(result.Diagnostic, "partial") || !strings.Contains(result.Diagnostic, "counters unavailable") {
		t.Fatalf("partial result = %+v", result)
	}
	tank := entityByName(t, result.Entities, "zfs_pool", "tank")
	if tank.Labels["healthState"] != "ONLINE" || tank.Labels["scrubState"] != "unavailable" || tank.Labels["hardwareFault"] != "false" {
		t.Fatalf("partial tank health was invented or lost: %+v", tank)
	}

	permissionRunner := &fixtureRunner{commands: []fixtureCommand{
		{output: string(readFixture(t, "zfs-pools.txt"))},
		{stderr: "zfs: permission denied", exitCode: 1},
		{output: string(readFixture(t, "zfs-status.txt"))},
	}}
	permissionAdapter, err := NewWithRunner(permissionRunner, Config{HostID: "fixture-host", CounterReader: &fixtureCounterReader{snapshots: []map[string]Counter{{}}}})
	if err != nil {
		t.Fatal(err)
	}
	permissionResult, err := permissionAdapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !permissionResult.Partial || !strings.Contains(strings.ToLower(permissionResult.Diagnostic), "permission") {
		t.Fatalf("permission result = %+v", permissionResult)
	}
	if len(permissionResult.Entities) != 2 {
		t.Fatalf("permission result discarded valid pool/status data: %+v", permissionResult)
	}
}

func TestAdapterRejectsFailedEnumerationAndEmptyEnumerationIsAbsent(t *testing.T) {
	permissionRunner := &fixtureRunner{commands: []fixtureCommand{{stderr: "operation not permitted", exitCode: 1}}}
	adapter, err := NewWithRunner(permissionRunner, Config{HostID: "fixture-host"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Collect(context.Background()); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("pool permission error = %v", err)
	}

	emptyRunner := &fixtureRunner{commands: []fixtureCommand{{}, {}, {}}}
	emptyAdapter, err := NewWithRunner(emptyRunner, Config{HostID: "fixture-host", CounterReader: &fixtureCounterReader{snapshots: []map[string]Counter{{}}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := emptyAdapter.Collect(context.Background())
	if err != nil || result.Partial || len(result.Entities) != 0 || len(result.Metrics) != 0 {
		t.Fatalf("empty successful inventory = %+v, %v", result, err)
	}
}

func TestZFSIdentitySeparatesReplacementAndMarksUnstableOrAmbiguousNames(t *testing.T) {
	old := poolRecord{Name: "tank", GUID: "100", Size: 1}
	pathChanged := old
	pathChanged.Name = "tank-renamed"
	oldID, oldStable, oldSource := stablePoolID("host-a", old, false)
	newID, newStable, newSource := stablePoolID("host-a", pathChanged, false)
	if oldID != newID || !oldStable || !newStable || oldSource != "guid" || newSource != "guid" {
		t.Fatalf("pool name change changed GUID identity: old=%q new=%q", oldID, newID)
	}
	replacement := old
	replacement.GUID = "200"
	replacementID, replacementStable, _ := stablePoolID("host-a", replacement, false)
	if replacementID == oldID || !replacementStable {
		t.Fatalf("pool replacement merged into old identity: old=%q replacement=%q", oldID, replacementID)
	}

	unstable := poolRecord{Name: "rpool"}
	unstableID, stable, source := stablePoolID("host-a", unstable, false)
	if unstableID == "" || stable || source != "unstable-name" {
		t.Fatalf("missing GUID was advertised stable: id=%q stable=%t source=%q", unstableID, stable, source)
	}
	first, firstStable, firstSource := stablePoolID("host-a", poolRecord{Name: "one", GUID: "300"}, true)
	second, secondStable, _ := stablePoolID("host-a", poolRecord{Name: "two", GUID: "300"}, true)
	if first == second || firstStable || secondStable || firstSource != "ambiguous-name" {
		t.Fatalf("ambiguous pool GUIDs were merged or advertised stable: %q/%t and %q/%t", first, firstStable, second, secondStable)
	}
}

func TestAdapterUsesOnlyFixedReadOnlyQueriesAndBoundedOutput(t *testing.T) {
	runner := &fixtureRunner{commands: []fixtureCommand{
		{output: string(readFixture(t, "zfs-pools.txt"))},
		{output: string(readFixture(t, "zfs-datasets.txt"))},
		{output: string(readFixture(t, "zfs-status.txt"))},
	}}
	adapter, err := NewWithRunner(runner, Config{CounterReader: &fixtureCounterReader{snapshots: []map[string]Counter{{}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"zpool", "list", "-H", "-p", "-o", "name,guid,size,allocated,health"},
		{"zfs", "list", "-H", "-p", "-o", "name,guid,used,available"},
		{"zpool", "status", "-p"},
	}
	if len(runner.calls) != len(want) {
		t.Fatalf("calls = %v", runner.calls)
	}
	for index := range want {
		if strings.Join(runner.calls[index], "\x00") != strings.Join(want[index], "\x00") {
			t.Fatalf("call %d = %v, want %v", index, runner.calls[index], want[index])
		}
		for _, argument := range runner.calls[index] {
			if argument == "destroy" || argument == "set" || argument == "scrub" || argument == "import" || argument == "export" || argument == "repair" {
				t.Fatalf("mutating argument in fixed query: %v", runner.calls[index])
			}
		}
	}

	truncatedRunner := &fixtureRunner{commands: []fixtureCommand{{truncated: true}}}
	truncatedAdapter, err := NewWithRunner(truncatedRunner, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := truncatedAdapter.Collect(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("truncated pool output = %v", err)
	}
}

func TestAdapterRegistersBoundedDescriptor(t *testing.T) {
	adapter, err := NewWithRunner(&fixtureRunner{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registry := collector.NewRegistry()
	if err := Register(registry, adapter); err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 1 || descriptors[0].ID != CollectorID || descriptors[0].Interval != collectionInterval || descriptors[0].Deadline != totalCollectionLimit || descriptors[0].EntityLimit != maxPools+maxDatasets {
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
	if listed == nil || listed.Version != "openzfs-fixed-v1" || listed.RequiredPermission[0] != "zpool:read" || listed.Deadline != totalCollectionLimit {
		t.Fatalf("default ZFS descriptor = %+v", listed)
	}
}

func assertMetric(t *testing.T, metrics []collector.Metric, entityID, name string, want float64, unit, scope, availability string) {
	t.Helper()
	metric := metricFor(metrics, entityID, name)
	if metric.Metric == "" || metric.Unit != unit || metric.Availability != availability {
		t.Fatalf("%s/%s metadata = %+v", entityID, name, metric)
	}
	if scope != "" && metric.Labels["capacityScope"] != scope {
		t.Fatalf("%s/%s scope = %+v", entityID, name, metric.Labels)
	}
	if availability == "unavailable" {
		if metric.Value != nil {
			t.Fatalf("%s/%s unavailable value = %v", entityID, name, *metric.Value)
		}
		return
	}
	if metric.Value == nil || *metric.Value != want {
		t.Fatalf("%s/%s value = %v, want %v", entityID, name, metric.Value, want)
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

func entityID(t *testing.T, entities []collector.Entity, kind, name string) string {
	t.Helper()
	return entityByName(t, entities, kind, name).ID
}

func entityByName(t *testing.T, entities []collector.Entity, kind, name string) collector.Entity {
	t.Helper()
	for _, entity := range entities {
		if entity.Kind == kind && entity.Name == name {
			return entity
		}
	}
	t.Fatalf("entity %s/%s not found in %+v", kind, name, entities)
	return collector.Entity{}
}

func repeatedCommands(t *testing.T, cycles int) []fixtureCommand {
	t.Helper()
	commands := make([]fixtureCommand, 0, cycles*3)
	for range cycles {
		commands = append(commands,
			fixtureCommand{output: string(readFixture(t, "zfs-pools.txt"))},
			fixtureCommand{output: string(readFixture(t, "zfs-datasets.txt"))},
			fixtureCommand{output: string(readFixture(t, "zfs-status.txt"))},
		)
	}
	return commands
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

func readCountersFixture(t *testing.T, name string) map[string]Counter {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(readFixture(t, name))), "\n")
	result := make(map[string]Counter, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			t.Fatalf("invalid counter fixture line %q", line)
		}
		readBytes, ok := parseUint(fields[1])
		if !ok {
			t.Fatalf("invalid read counter %q", line)
		}
		writeBytes, ok := parseUint(fields[2])
		if !ok {
			t.Fatalf("invalid write counter %q", line)
		}
		result[strings.ToLower(fields[0])] = Counter{Name: fields[0], ReadBytes: readBytes, WriteBytes: writeBytes}
	}
	return result
}
