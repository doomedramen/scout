package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinuxCollectorReportsRealValuesAndCounterResetAsUnavailable(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc", "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, data string) {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/stat", "cpu  100 0 100 800 0 0 0 0 0 0\n")
	write("proc/meminfo", "MemTotal:       1024 kB\nMemAvailable:    256 kB\n")
	write("proc/uptime", "42.5 1.0\n")
	write("proc/net/dev", "Inter-| Receive | Transmit\n eth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n")
	collector := NewHostCollector(root)
	collector.previousCPU = []uint64{100, 0, 100, 800, 0, 0, 0, 0, 0, 0}
	collector.previousNetwork = map[string]counter{"eth0": {received: 50, sent: 100, at: time.Now().Add(-time.Second)}}
	snapshot, _, _, err := collectLinux(root, time.Now().UTC(), collector.previousCPU, collector.previousNetwork)
	if err != nil {
		t.Fatal(err)
	}
	foundRate := false
	for _, metric := range snapshot {
		if metric.Metric == "network.receive_rate" {
			foundRate = true
			if metric.Availability != "current" {
				t.Fatalf("expected rate")
			}
		}
	}
	if !foundRate {
		t.Fatal("network rate missing")
	}
	write("proc/net/dev", "Inter-| Receive | Transmit\n eth0: 10 0 0 0 0 0 0 0 20 0 0 0 0 0 0 0\n")
	snapshot, _, _, err = collectLinux(root, time.Now().UTC(), collector.previousCPU, collector.previousNetwork)
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range snapshot {
		if metric.Metric == "network.receive_rate" && metric.Availability != "unavailable" {
			t.Fatal("counter reset treated as a valid rate")
		}
	}
}
