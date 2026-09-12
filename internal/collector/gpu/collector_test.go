package gpu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/collector"
)

type fixtureRunner struct {
	result CommandResult
	err    error
	calls  [][]string
}

func (r *fixtureRunner) Run(_ context.Context, args ...string) (CommandResult, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	return r.result, r.err
}

func TestNVIDIAUsesFixedQueryAndMapsFieldsAndNA(t *testing.T) {
	runner := &fixtureRunner{result: CommandResult{Stdout: []byte("0, GPU-UUID-A, 0000:01:00.0, NVIDIA RTX, 12.5, 1024, 8192, 45, 75.25, 550.54\n1, [N/A], [N/A], NVIDIA Compute, 48, 10, 4096, [N/A], [N/A], 550.54\n")}}
	root := t.TempDir()
	adapter := mustAdapter(t, runner, Config{HostID: "fixture-host", SysfsRoot: root})
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Partial || len(result.Entities) != 2 || len(result.Metrics) != 10 {
		t.Fatalf("NVIDIA result = %+v", result)
	}
	if len(runner.calls) != 1 || len(runner.calls[0]) != 2 || runner.calls[0][0] != "--query-gpu="+nvidiaQuery || runner.calls[0][1] != "--format=csv,noheader,nounits" {
		t.Fatalf("NVIDIA query was not fixed: %v", runner.calls)
	}
	first := entityByName(t, result.Entities, "NVIDIA RTX")
	if first.Labels["identityStable"] != "true" || first.Labels["identitySource"] != "nvidia-uuid" || first.Labels["uuid"] != "GPU-UUID-A" || first.Labels["powerScope"] != "gpu-board" || first.Observed.IsZero() {
		t.Fatalf("NVIDIA identity/metadata = %+v", first)
	}
	assertMetric(t, result.Metrics, first.ID, "gpu.utilization", 12.5, "percent")
	assertMetric(t, result.Metrics, first.ID, "gpu.memory.used", 1024*1024*1024, "bytes")
	assertMetric(t, result.Metrics, first.ID, "gpu.memory.capacity", 8192*1024*1024, "bytes")
	assertMetric(t, result.Metrics, first.ID, "gpu.temperature", 45, "celsius")
	assertMetric(t, result.Metrics, first.ID, "gpu.power", 75.25, "watts")

	second := entityByName(t, result.Entities, "NVIDIA Compute")
	if second.Labels["identityStable"] != "false" || second.Labels["identitySource"] != "device-evidence" || second.Labels["capability.temperature"] != "unavailable" || second.Labels["capability.power"] != "unavailable" {
		t.Fatalf("N/A NVIDIA fields = %+v", second)
	}
	if metric := metricFor(result.Metrics, second.ID, "gpu.temperature"); metric.Availability != "unavailable" || metric.Value != nil {
		t.Fatalf("N/A temperature metric = %+v", metric)
	}
}

func TestNVIDIAKeepsRowsOnMalformedFieldAndSeparatesDuplicateEvidence(t *testing.T) {
	runner := &fixtureRunner{result: CommandResult{Stdout: []byte("0, [N/A], [N/A], NVIDIA A, bad, 1, 2, 40, 10, 550\n1, [N/A], [N/A], NVIDIA A, 10, 1, 2, 40, 10, 550\n")}}
	adapter := mustAdapter(t, runner, Config{HostID: "fixture-host", SysfsRoot: t.TempDir()})
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Entities) != 2 || !strings.Contains(result.Diagnostic, "utilization unavailable") {
		t.Fatalf("malformed NVIDIA result = %+v", result)
	}
	first := entityByName(t, result.Entities, "NVIDIA A")
	if first.Labels["identityStable"] != "false" {
		t.Fatalf("evidence identity was advertised stable: %+v", first)
	}
	if metric := metricFor(result.Metrics, first.ID, "gpu.utilization"); metric.Availability != "unavailable" || metric.Value != nil {
		t.Fatalf("malformed utilization metric = %+v", metric)
	}
	if result.Entities[0].ID == result.Entities[1].ID {
		t.Fatal("duplicate evidence rows were merged")
	}
}

func TestAMDReadsBoundedSysfsFieldsAndGPUHwmonPower(t *testing.T) {
	root := newDRMRoot(t)
	card := addDRMCard(t, root, "card0", "1002", "0000:03:00.0", "amdgpu", map[string]string{
		"product_name":        "Radeon Fixture",
		"gpu_busy_percent":    "32.5\n",
		"mem_info_vram_used":  "1073741824\n",
		"mem_info_vram_total": "8589934592\n",
	})
	writeFiles(t, filepath.Join(card, "hwmon", "hwmon0"), map[string]string{
		"name":           "amdgpu\n",
		"temp1_input":    "65000\n",
		"power1_average": "12345000\n",
	})
	adapter := mustAdapter(t, nil, Config{HostID: "fixture-host", SysfsRoot: root})
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Partial || len(result.Entities) != 1 {
		t.Fatalf("AMD result = %+v", result)
	}
	entity := result.Entities[0]
	if entity.Name != "Radeon Fixture" || entity.Labels["vendor"] != "AMD" || entity.Labels["driver"] != "amdgpu" || entity.Labels["identityStable"] != "true" || entity.Labels["identitySource"] != "pci-slot" || entity.Labels["powerScope"] != "gpu-board" {
		t.Fatalf("AMD entity = %+v", entity)
	}
	assertMetric(t, result.Metrics, entity.ID, "gpu.utilization", 32.5, "percent")
	assertMetric(t, result.Metrics, entity.ID, "gpu.memory.used", 1073741824, "bytes")
	assertMetric(t, result.Metrics, entity.ID, "gpu.memory.capacity", 8589934592, "bytes")
	assertMetric(t, result.Metrics, entity.ID, "gpu.temperature", 65, "celsius")
	assertMetric(t, result.Metrics, entity.ID, "gpu.power", 12.345, "watts")
}

func TestIntelEnergyPowerHasFirstSampleGapAndReset(t *testing.T) {
	root := newDRMRoot(t)
	card := addDRMCard(t, root, "card0", "8086", "0000:00:02.0", "i915", map[string]string{
		"product_name":        "Intel Fixture",
		"gt_busy_percent":     "20\n",
		"mem_info_lmem_used":  "4096\n",
		"mem_info_lmem_total": "8192\n",
	})
	hwmon := filepath.Join(card, "hwmon", "hwmon0")
	writeFiles(t, hwmon, map[string]string{"name": "i915\n", "temp1_input": "42000\n", "energy1_input": "1000000\n"})
	adapter := mustAdapter(t, nil, Config{HostID: "fixture-host", SysfsRoot: root})
	current := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	adapter.now = func() time.Time { return current }

	first, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entity := first.Entities[0]
	if entity.Labels["powerScope"] != "package" || entity.Labels["capability.power"] != "unavailable" {
		t.Fatalf("first Intel power capability = %+v", entity)
	}
	if metric := metricFor(first.Metrics, entity.ID, "gpu.power"); metric.Availability != "unavailable" || metric.Value != nil {
		t.Fatalf("first Intel power metric = %+v", metric)
	}

	current = current.Add(time.Second)
	writeFiles(t, hwmon, map[string]string{"energy1_input": "2000000\n"})
	second, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entity = second.Entities[0]
	assertMetric(t, second.Metrics, entity.ID, "gpu.power", 1, "watts")

	current = current.Add(time.Second)
	writeFiles(t, hwmon, map[string]string{"energy1_input": "500000\n"})
	reset, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reset.Partial || !strings.Contains(reset.Diagnostic, "energy counter reset") {
		t.Fatalf("reset result = %+v", reset)
	}
	if metric := metricFor(reset.Metrics, reset.Entities[0].ID, "gpu.power"); metric.Availability != "unavailable" || metric.Value != nil {
		t.Fatalf("reset power metric = %+v", metric)
	}
}

func TestSysfsAndNVIDIASourcesRemainIndependentAndPCIIdentityDeduplicates(t *testing.T) {
	root := newDRMRoot(t)
	card := addDRMCard(t, root, "card0", "1002", "0000:03:00.0", "amdgpu", map[string]string{"gpu_busy_percent": "10\n"})
	writeFiles(t, filepath.Join(card, "hwmon", "hwmon0"), map[string]string{"temp1_input": "50000\n"})
	runner := &fixtureRunner{result: CommandResult{Stdout: []byte("0, NVIDIA-UUID, 0000:03:00.0, NVIDIA duplicate, 20, 1, 2, 40, 10, 550\n")}}
	adapter := mustAdapter(t, runner, Config{HostID: "fixture-host", SysfsRoot: root})
	result, err := adapter.Collect(context.Background())
	if err != nil || len(result.Entities) != 1 {
		t.Fatalf("cross-source result = %+v, %v", result, err)
	}
	if result.Entities[0].Labels["vendor"] != "nvidia" {
		t.Fatalf("sysfs duplicate was not suppressed: %+v", result.Entities)
	}
}

func TestGPUBoundsAndDescriptor(t *testing.T) {
	rows := make([]string, 0, maxGPUs+1)
	for index := 0; index < maxGPUs+1; index++ {
		rows = append(rows, ""+itoa(index)+", GPU-"+itoa(index)+", 0000:00:"+hexByte(index)+".0, GPU, 1, 1, 2, 30, 1, 550")
	}
	runner := &fixtureRunner{result: CommandResult{Stdout: []byte(strings.Join(rows, "\n"))}}
	adapter := mustAdapter(t, runner, Config{HostID: "fixture-host", SysfsRoot: t.TempDir()})
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Entities) != maxGPUs || len(result.Metrics) != maxGPUs*5 || !strings.Contains(result.Diagnostic, "truncated") {
		t.Fatalf("bounded GPU result = entities=%d metrics=%d partial=%t diagnostic=%q", len(result.Entities), len(result.Metrics), result.Partial, result.Diagnostic)
	}
	registry := collector.NewRegistry()
	if err := Register(registry, adapter); err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 1 || descriptors[0].ID != CollectorID || descriptors[0].EntityLimit != maxGPUs || descriptors[0].Interval != collectionInterval || descriptors[0].Deadline != collectionDeadline {
		t.Fatalf("GPU descriptor = %+v", descriptors)
	}
	var listed *collector.Descriptor
	for _, descriptor := range collector.DefaultDescriptors() {
		if descriptor.ID == CollectorID {
			copy := descriptor
			listed = &copy
			break
		}
	}
	if listed == nil || listed.RequiredPermission[0] != "nvidia-smi:read" || listed.ConfigSchema["sysfsRoot"] == "" {
		t.Fatalf("default GPU descriptor = %+v", listed)
	}
}

func TestGPUReportsUnavailableWhenNoSourceExists(t *testing.T) {
	adapter := mustAdapter(t, nil, Config{HostID: "fixture-host", SysfsRoot: filepath.Join(t.TempDir(), "missing")})
	if _, err := adapter.Collect(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no source error = %v", err)
	}
}

func mustAdapter(t *testing.T, runner Runner, config Config) *Adapter {
	t.Helper()
	adapter, err := NewWithRunner(runner, config)
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = func() time.Time { return time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC) }
	return adapter
}

func newDRMRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "class", "drm"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func addDRMCard(t *testing.T, root, cardName, vendor, pciSlot, driver string, files map[string]string) string {
	t.Helper()
	device := filepath.Join(root, "devices", pciSlot)
	writeFiles(t, device, files)
	writeFiles(t, filepath.Join(root, "bus", "pci", "drivers", driver), map[string]string{".keep": ""})
	if err := os.WriteFile(filepath.Join(device, "vendor"), []byte(vendor+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(device, "uevent"), []byte("PCI_SLOT_NAME="+pciSlot+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "bus", "pci", "drivers", driver), filepath.Join(device, "driver")); err != nil {
		t.Fatal(err)
	}
	drmCard := filepath.Join(root, "devices", pciSlot, "drm", cardName)
	if err := os.MkdirAll(drmCard, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(device, filepath.Join(drmCard, "device")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(drmCard, filepath.Join(root, "class", "drm", cardName)); err != nil {
		t.Fatal(err)
	}
	return device
}

func writeFiles(t *testing.T, directory string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func entityByName(t *testing.T, entities []collector.Entity, name string) collector.Entity {
	t.Helper()
	for _, entity := range entities {
		if entity.Name == name {
			return entity
		}
	}
	t.Fatalf("GPU %q not found in %+v", name, entities)
	return collector.Entity{}
}

func metricFor(metrics []collector.Metric, entityID, name string) collector.Metric {
	for _, metric := range metrics {
		if metric.EntityID == entityID && metric.Metric == name {
			return metric
		}
	}
	return collector.Metric{}
}

func assertMetric(t *testing.T, metrics []collector.Metric, entityID, name string, want float64, unit string) {
	t.Helper()
	metric := metricFor(metrics, entityID, name)
	if metric.Availability != "current" || metric.Value == nil || math.Abs(*metric.Value-want) > 1e-9 || metric.Unit != unit || metric.ObservedAt.IsZero() {
		t.Fatalf("metric = %+v, want %s=%v %s", metric, name, want, unit)
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func hexByte(value int) string {
	return fmt.Sprintf("%02x", value)
}
