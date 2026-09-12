package sensors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/collector"
)

func TestAdapterConvertsNegativeTemperatureAndZeroFanAndSurvivesHwmonRename(t *testing.T) {
	root := newHwmonRoot(t)
	target := filepath.Join(root, "devices", "pci0", "k10temp")
	writeFiles(t, target, map[string]string{
		"name":        "k10temp\n",
		"temp1_input": "-55000\n",
		"temp1_label": "CPU package\n",
		"temp1_alarm": "0\n",
		"temp1_max":   "90000\n",
		"fan1_input":  "0\n",
		"fan1_label":  "Case fan\n",
		"fan1_alarm":  "0\n",
	})
	linkHwmon(t, root, "hwmon0", target)

	adapter, err := NewWithConfig(Config{HostID: "fixture-host", SysfsRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = fixedNow
	first, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Partial || len(first.Entities) != 2 {
		t.Fatalf("first result = %+v", first)
	}
	temp := entityByChannel(t, first.Entities, "temp1")
	fan := entityByChannel(t, first.Entities, "fan1")
	if temp.Status != "online" || temp.Name != "CPU package" || temp.Labels["identityStable"] != "true" || temp.Labels["capability.temperature"] != "current" || temp.Labels["capability.fault"] != "current" || temp.Labels["hardwareFault"] != "false" {
		t.Fatalf("temperature entity = %+v", temp)
	}
	if fan.Status != "online" || fan.Name != "Case fan" || fan.Labels["capability.fan_speed"] != "current" || fan.Labels["hardwareFault"] != "false" {
		t.Fatalf("fan entity = %+v", fan)
	}
	assertMetric(t, first.Metrics, temp.ID, temperatureMetric, -55, temperatureUnit)
	assertMetric(t, first.Metrics, fan.ID, fanSpeedMetric, 0, fanSpeedUnit)

	oldIDs := map[string]string{"temp1": temp.ID, "fan1": fan.ID}
	if err := os.Remove(filepath.Join(root, "class", "hwmon", "hwmon0")); err != nil {
		t.Fatal(err)
	}
	linkHwmon(t, root, "hwmon7", target)
	second, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Partial || len(second.Entities) != 2 {
		t.Fatalf("renamed result = %+v", second)
	}
	for _, entity := range second.Entities {
		if oldIDs[entity.Labels["channel"]] != entity.ID {
			t.Fatalf("hwmon rename changed %s identity: old=%q new=%q", entity.Labels["channel"], oldIDs[entity.Labels["channel"]], entity.ID)
		}
	}
}

func TestAdapterMapsOnlyExplicitAlarmToHardwareFault(t *testing.T) {
	root := newHwmonRoot(t)
	target := filepath.Join(root, "devices", "pci0", "coretemp")
	writeFiles(t, target, map[string]string{
		"name":        "coretemp\n",
		"temp1_input": "99000\n",
		"temp1_max":   "90000\n",
		"temp1_crit":  "100000\n",
	})
	linkHwmon(t, root, "hwmon0", target)
	adapter := mustAdapter(t, Config{HostID: "fixture-host", SysfsRoot: root})

	withoutAlarm, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entity := entityByChannel(t, withoutAlarm.Entities, "temp1")
	if entity.Status != "online" || entity.Labels["capability.fault"] != "unavailable" {
		t.Fatalf("threshold created fault: %+v", entity)
	}
	if _, exists := entity.Labels["hardwareFault"]; exists {
		t.Fatalf("missing alarm invented hardware fault: %+v", entity)
	}

	writeFiles(t, target, map[string]string{"temp1_alarm": "1\n"})
	withAlarm, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entity = entityByChannel(t, withAlarm.Entities, "temp1")
	if entity.Status != "fault" || entity.Labels["hardwareFault"] != "true" || entity.Labels["faultSource"] != identitySourceAlarm || entity.Labels["capability.fault"] != "current" {
		t.Fatalf("explicit alarm was not mapped: %+v", entity)
	}
}

func TestAdapterKeepsMalformedAndNegativeFieldsUnavailableWithoutClamping(t *testing.T) {
	root := newHwmonRoot(t)
	target := filepath.Join(root, "devices", "platform", "fixture")
	writeFiles(t, target, map[string]string{
		"name":        "fixture\n",
		"temp1_input": "-1250\n",
		"fan1_input":  "-1\n",
		"fan2_input":  "not-a-number\n",
	})
	linkHwmon(t, root, "hwmon0", target)
	adapter := mustAdapter(t, Config{HostID: "fixture-host", SysfsRoot: root})
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Entities) != 3 {
		t.Fatalf("malformed result = %+v", result)
	}
	temp := entityByChannel(t, result.Entities, "temp1")
	if temp.Status != "online" {
		t.Fatalf("negative temperature was rejected: %+v", temp)
	}
	assertMetric(t, result.Metrics, temp.ID, temperatureMetric, -1.25, temperatureUnit)
	for _, channel := range []string{"fan1", "fan2"} {
		entity := entityByChannel(t, result.Entities, channel)
		if entity.Status != "unavailable" || entity.Labels["capability.fault"] != "unavailable" {
			t.Fatalf("unavailable %s entity = %+v", channel, entity)
		}
		metric := metricFor(result.Metrics, entity.ID, fanSpeedMetric)
		if metric.Availability != "unavailable" || metric.Value != nil {
			t.Fatalf("unavailable %s metric = %+v", channel, metric)
		}
	}
}

func TestAdapterRejectsHwmonSymlinkOutsideConfiguredRoot(t *testing.T) {
	root := newHwmonRoot(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "secret-device")
	writeFiles(t, target, map[string]string{"name": "secret\n", "temp1_input": "12345\n"})
	linkHwmon(t, root, "hwmon0", target)
	adapter := mustAdapter(t, Config{HostID: "fixture-host", SysfsRoot: root})

	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Entities) != 0 || !strings.Contains(result.Diagnostic, "path rejected") || strings.Contains(result.Diagnostic, "12345") {
		t.Fatalf("unsafe symlink result = %+v", result)
	}
}

func TestAdapterAppliesExactStableExclusions(t *testing.T) {
	root := newHwmonRoot(t)
	target := filepath.Join(root, "devices", "platform", "fixture")
	writeFiles(t, target, map[string]string{"name": "fixture\n", "temp1_input": "25000\n", "temp1_alarm": "0\n"})
	linkHwmon(t, root, "hwmon0", target)
	adapter := mustAdapter(t, Config{HostID: "fixture-host", SysfsRoot: root})
	result, err := adapter.Collect(context.Background())
	if err != nil || len(result.Entities) != 1 {
		t.Fatalf("initial exclusion result = %+v, %v", result, err)
	}
	excluded := mustAdapter(t, Config{HostID: "fixture-host", SysfsRoot: root, ExcludedIDs: []string{result.Entities[0].ID}})
	filtered, err := excluded.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Entities) != 0 || len(filtered.Metrics) != 0 || filtered.Partial {
		t.Fatalf("exact exclusion did not suppress entity: %+v", filtered)
	}
}

func TestAdapterBoundsInventoryAndRegistersDescriptor(t *testing.T) {
	root := newHwmonRoot(t)
	for deviceIndex := 0; deviceIndex < 2; deviceIndex++ {
		target := filepath.Join(root, "devices", "platform", "fixture"+string(rune('a'+deviceIndex)))
		writeFiles(t, target, map[string]string{"name": "fixture" + string(rune('a'+deviceIndex)) + "\n"})
		for channelIndex := 1; channelIndex <= 200; channelIndex++ {
			writeFiles(t, target, map[string]string{"temp" + itoa(channelIndex) + "_input": "25000\n"})
		}
		linkHwmon(t, root, "hwmon"+itoa(deviceIndex), target)
	}
	adapter := mustAdapter(t, Config{HostID: "fixture-host", SysfsRoot: root})
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Entities) != maxSensors || len(result.Metrics) != maxSensors || !strings.Contains(result.Diagnostic, "truncated") {
		t.Fatalf("bounded result = entities=%d metrics=%d partial=%t diagnostic=%q", len(result.Entities), len(result.Metrics), result.Partial, result.Diagnostic)
	}

	registry := collector.NewRegistry()
	if err := Register(registry, adapter); err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 1 || descriptors[0].ID != CollectorID || descriptors[0].EntityLimit != maxSensors || descriptors[0].Interval != collectionInterval || descriptors[0].Deadline != collectionDeadline {
		t.Fatalf("registered descriptor = %+v", descriptors)
	}
	var listed *collector.Descriptor
	for _, descriptor := range collector.DefaultDescriptors() {
		if descriptor.ID == CollectorID {
			copy := descriptor
			listed = &copy
			break
		}
	}
	if listed == nil || listed.Version != "hwmon-sysfs-v1" || listed.RequiredPermission[0] != "sysfs:hwmon-read" || listed.ConfigSchema["excludedIds"] == "" {
		t.Fatalf("default sensor descriptor = %+v", listed)
	}
}

func TestAdapterDetectsEmptyClassAndReportsMissingRoot(t *testing.T) {
	root := newHwmonRoot(t)
	adapter := mustAdapter(t, Config{SysfsRoot: root})
	detected, err := adapter.Detect(context.Background())
	if err != nil || !detected {
		t.Fatalf("empty hwmon detect = %t, %v", detected, err)
	}
	empty, err := adapter.Collect(context.Background())
	if err != nil || empty.Partial || len(empty.Entities) != 0 || len(empty.Metrics) != 0 {
		t.Fatalf("empty hwmon collect = %+v, %v", empty, err)
	}

	missing, err := NewWithConfig(Config{SysfsRoot: filepath.Join(root, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.Collect(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing hwmon root error = %v", err)
	}
}

func newHwmonRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "class", "hwmon"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
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

func linkHwmon(t *testing.T, root, name, target string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(root, "class", "hwmon", name)); err != nil {
		t.Fatal(err)
	}
}

func mustAdapter(t *testing.T, config Config) *Adapter {
	t.Helper()
	adapter, err := NewWithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = fixedNow
	return adapter
}

func fixedNow() time.Time {
	return time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
}

func entityByChannel(t *testing.T, entities []collector.Entity, channel string) collector.Entity {
	t.Helper()
	for _, entity := range entities {
		if entity.Labels["channel"] == channel {
			return entity
		}
	}
	t.Fatalf("channel %q not found in %+v", channel, entities)
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
	if metric.Availability != "current" || metric.Value == nil || *metric.Value != want || metric.Unit != unit {
		t.Fatalf("metric = %+v, want %s=%v %s", metric, name, want, unit)
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
