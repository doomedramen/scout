package collector

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsCPUGuestAccountingAndLoadFixtures(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsFixture(t, root, diagnosticsFixture{
		stat:     "cpu  100 10 20 50 5 2 3 4 8 2\n",
		loadavg:  "1.50 0.75 0.25 1/100 1234\n",
		meminfo:  "MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n",
		diskstat: "8 0 sda 100 0 1000 100 50 0 2000 100 1 300 400\n",
		wwid:     "wwid-old\n",
	})

	firstAt := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	firstMetrics, previous, err := collectDiagnostics(root, firstAt, diagnosticState{})
	if err != nil {
		t.Fatal(err)
	}
	assertMetricUnavailableForDevice(t, firstMetrics, "sda", "disk.read_rate")
	assertMetricUnavailableForDevice(t, firstMetrics, "sda", "disk.read_latency")
	writeDiagnosticsFixture(t, root, diagnosticsFixture{
		stat:     "cpu  200 30 50 100 15 4 7 10 18 5\n",
		loadavg:  "1.50 0.75 0.25 1/100 1234\n",
		meminfo:  "MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n",
		diskstat: "8 0 sda 120 0 2000 150 60 0 4000 200 1 500 600\n",
		wwid:     "wwid-old\n",
	})

	metrics, _, err := collectDiagnostics(root, firstAt.Add(10*time.Second), previous)
	if err != nil {
		t.Fatal(err)
	}
	total := 222.0
	assertMetricValue(t, metrics, "host", "cpu.utilization", 100*(total-50)/total)
	assertMetricValue(t, metrics, "host", "cpu.user_percent", 100*90/total)
	assertMetricValue(t, metrics, "host", "cpu.system_percent", 100*30/total)
	assertMetricValue(t, metrics, "host", "cpu.iowait_percent", 100*10/total)
	assertMetricValue(t, metrics, "host", "cpu.steal_percent", 100*6/total)
	assertMetricValue(t, metrics, "host", "load.1m", 1.5)
	assertMetricValue(t, metrics, "host", "load.5m", 0.75)
	assertMetricValue(t, metrics, "host", "load.15m", 0.25)
	assertMetricValue(t, metrics, "host", "swap.capacity", 0)
	assertMetricValue(t, metrics, "host", "swap.used", 0)

	user := metricFor(metrics, "host", "cpu.user_percent")
	if user.Availability != "current" {
		t.Fatalf("guest-adjusted user metric is %q, want current", user.Availability)
	}
	if got := user.Value; got == nil || math.Abs(*got-100*90/total) > 0.000001 {
		t.Fatalf("guest-adjusted user value = %v", got)
	}
}

func TestDiagnosticsDiskRatesResetAndNoOperationsFixtures(t *testing.T) {
	root := t.TempDir()
	firstAt := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	writeDiagnosticsFixture(t, root, diagnosticsFixture{
		stat:     "cpu  100 0 100 800 0 0 0 0 0 0\n",
		loadavg:  "0.10 0.20 0.30 1/10 1\n",
		meminfo:  "MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 1024 kB\nSwapFree: 768 kB\n",
		diskstat: "8 0 sda 100 0 1000 100 50 0 2000 100 1 300 400\n8 16 sdb 10 0 100 10 5 0 200 5 0 20 30\n",
		wwid:     "wwid-old\n",
	})
	_, previous, err := collectDiagnostics(root, firstAt, diagnosticState{})
	if err != nil {
		t.Fatal(err)
	}
	writeDiagnosticsFixture(t, root, diagnosticsFixture{
		stat:     "cpu  110 0 110 900 0 0 0 0 0 0\n",
		loadavg:  "0.10 0.20 0.30 1/10 1\n",
		meminfo:  "MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 1024 kB\nSwapFree: 768 kB\n",
		diskstat: "8 0 sda 120 0 2000 150 60 0 4000 200 1 500 600\n8 16 sdb 10 0 100 10 5 0 200 5 0 20 30\n",
		wwid:     "wwid-old\n",
	})
	metrics, current, err := collectDiagnostics(root, firstAt.Add(10*time.Second), previous)
	if err != nil {
		t.Fatal(err)
	}
	assertMetricValueForDevice(t, metrics, "sdb", "disk.utilization", 0)
	assertMetricValueForDevice(t, metrics, "sda", "disk.read_rate", 51200)
	assertMetricValueForDevice(t, metrics, "sda", "disk.write_rate", 102400)
	assertMetricValueForDevice(t, metrics, "sda", "disk.utilization", 2)
	assertMetricValueForDevice(t, metrics, "sda", "disk.read_latency", 2.5)
	assertMetricValueForDevice(t, metrics, "sda", "disk.write_latency", 10)
	assertMetricValueForDevice(t, metrics, "sdb", "disk.read_rate", 0)
	assertMetricValueForDevice(t, metrics, "sdb", "disk.write_rate", 0)
	assertMetricUnavailableForDevice(t, metrics, "sdb", "disk.read_latency")
	assertMetricUnavailableForDevice(t, metrics, "sdb", "disk.write_latency")

	writeDiagnosticsFixture(t, root, diagnosticsFixture{
		stat:     "cpu  120 0 120 1000 0 0 0 0 0 0\n",
		loadavg:  "0.10 0.20 0.30 1/10 1\n",
		meminfo:  "MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 1024 kB\nSwapFree: 768 kB\n",
		diskstat: "8 0 sda 10 0 100 10 6 0 200 5 1 100 200\n8 16 sdb 12 0 120 15 7 0 220 10 0 30 40\n",
		wwid:     "wwid-old\n",
	})
	resetMetrics, _, err := collectDiagnostics(root, firstAt.Add(20*time.Second), current)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"disk.read_rate", "disk.write_rate", "disk.utilization", "disk.read_latency", "disk.write_latency"} {
		assertMetricUnavailableForDevice(t, resetMetrics, "sda", name)
	}
}

func TestDiagnosticsDeviceReplacementDoesNotMergeCounters(t *testing.T) {
	root := t.TempDir()
	firstAt := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	fixture := diagnosticsFixture{
		stat:     "cpu  100 0 100 800 0 0 0 0 0 0\n",
		loadavg:  "0.10 0.20 0.30 1/10 1\n",
		meminfo:  "MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n",
		diskstat: "8 0 sda 100 0 1000 100 50 0 2000 100 1 300 400\n",
		wwid:     "wwid-old\n",
	}
	writeDiagnosticsFixture(t, root, fixture)
	firstMetrics, previous, err := collectDiagnostics(root, firstAt, diagnosticState{})
	if err != nil {
		t.Fatal(err)
	}
	old := metricForDevice(firstMetrics, "sda", "disk.read_rate")
	if old.EntityID == "" {
		t.Fatal("first disk entity has no stable identity")
	}

	fixture.diskstat = "8 0 sda 120 0 2000 150 60 0 4000 200 1 500 600\n"
	fixture.wwid = "wwid-new\n"
	writeDiagnosticsFixture(t, root, fixture)
	replacementMetrics, _, err := collectDiagnostics(root, firstAt.Add(10*time.Second), previous)
	if err != nil {
		t.Fatal(err)
	}
	replacement := metricForDevice(replacementMetrics, "sda", "disk.read_rate")
	if replacement.EntityID == old.EntityID {
		t.Fatalf("device replacement reused entity identity %q", replacement.EntityID)
	}
	if replacement.Availability != "unavailable" || replacement.Value != nil {
		t.Fatalf("replacement rate = %#v, want unavailable without a value", replacement)
	}
	if replacement.Labels["identityStable"] != "true" {
		t.Fatalf("replacement identity labels = %#v", replacement.Labels)
	}
}

type diagnosticsFixture struct {
	stat     string
	loadavg  string
	meminfo  string
	diskstat string
	wwid     string
}

func writeDiagnosticsFixture(t *testing.T, root string, fixture diagnosticsFixture) {
	t.Helper()
	for _, directory := range []string{"proc", "proc/sys", "proc/net", "sys/dev/block/8:0/device"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/stat", fixture.stat)
	write("proc/loadavg", fixture.loadavg)
	write("proc/meminfo", fixture.meminfo)
	write("proc/diskstats", fixture.diskstat)
	write("sys/dev/block/8:0/device/wwid", fixture.wwid)
}

func metricFor(metrics []Metric, entityID, name string) Metric {
	for _, metric := range metrics {
		if metric.EntityID == entityID && metric.Metric == name {
			return metric
		}
	}
	return Metric{}
}

func metricForDevice(metrics []Metric, device, name string) Metric {
	for _, metric := range metrics {
		if metric.Metric == name && metric.Labels["device"] == device {
			return metric
		}
	}
	return Metric{}
}

func assertMetricValue(t *testing.T, metrics []Metric, entityID, name string, want float64) {
	t.Helper()
	metric := metricFor(metrics, entityID, name)
	if metric.Metric == "" || metric.Availability != "current" || metric.Value == nil {
		t.Fatalf("%s/%s = %#v, want current value %v", entityID, name, metric, want)
	}
	if math.Abs(*metric.Value-want) > 0.000001 {
		t.Fatalf("%s/%s = %v, want %v", entityID, name, *metric.Value, want)
	}
}

func assertMetricValueForDevice(t *testing.T, metrics []Metric, device, name string, want float64) {
	t.Helper()
	metric := metricForDevice(metrics, device, name)
	if metric.Metric == "" || metric.Availability != "current" || metric.Value == nil {
		t.Fatalf("%s/%s = %#v, want current value %v", device, name, metric, want)
	}
	if math.Abs(*metric.Value-want) > 0.000001 {
		t.Fatalf("%s/%s = %v, want %v", device, name, *metric.Value, want)
	}
}

func assertMetricUnavailableForDevice(t *testing.T, metrics []Metric, device, name string) {
	t.Helper()
	metric := metricForDevice(metrics, device, name)
	if metric.Metric == "" || metric.Availability != "unavailable" || metric.Value != nil {
		t.Fatalf("%s/%s = %#v, want unavailable without a value", device, name, metric)
	}
	if strings.TrimSpace(metric.Labels["device"]) != device {
		t.Fatalf("%s/%s lost device label: %#v", device, name, metric.Labels)
	}
}
