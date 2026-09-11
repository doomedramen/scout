package collector

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Metric struct {
	EntityID     string
	Metric       string
	Value        *float64
	Availability string
	Unit         string
	ObservedAt   time.Time
	Labels       map[string]string
}

type HostSnapshot struct {
	ObservedAt    time.Time
	SchemaVersion int
	Hostname      string
	OS            string
	Architecture  string
	CPUs          int
	Interfaces    []Interface
	Metrics       []Metric
}

type counter struct {
	received uint64
	sent     uint64
	at       time.Time
}

type HostCollector struct {
	Root                string
	mu                  sync.Mutex
	previousCPU         []uint64
	previousNetwork     map[string]counter
	previousDiagnostics diagnosticState
}

func NewHostCollector(root string) *HostCollector {
	if root == "" {
		root = "/"
	}
	return &HostCollector{Root: root, previousNetwork: map[string]counter{}, previousDiagnostics: diagnosticState{disks: map[string]diskCounter{}}}
}

func (c *HostCollector) Collect(ctx context.Context) (HostSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return HostSnapshot{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	hostname, _ := os.Hostname()
	snapshot := HostSnapshot{ObservedAt: now, SchemaVersion: 1, Hostname: hostname, OS: runtime.GOOS, Architecture: runtime.GOARCH, CPUs: runtime.NumCPU(), Interfaces: []Interface{}, Metrics: []Metric{}}
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, device := range interfaces {
			if device.Flags&net.FlagLoopback != 0 || device.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, _ := device.Addrs()
			item := Interface{Name: device.Name, Addresses: []string{}}
			for _, addr := range addrs {
				item.Addresses = append(item.Addresses, addr.String())
			}
			snapshot.Interfaces = append(snapshot.Interfaces, item)
		}
	}
	if runtime.GOOS == "linux" {
		previous := c.previousDiagnostics
		if len(previous.cpu) == 0 {
			previous.cpu = c.previousCPU
		}
		metrics, cpu, network, diagnostics, readErr := collectLinuxState(c.Root, now, c.previousNetwork, previous)
		if readErr != nil {
			return snapshot, readErr
		}
		c.previousCPU = cpu
		c.previousNetwork = network
		c.previousDiagnostics = diagnostics
		snapshot.Metrics = metrics
		snapshot.SchemaVersion = HostSchemaVersion
	} else {
		snapshot.Metrics = append(snapshot.Metrics, Metric{EntityID: "host", Metric: "cpu.utilization", Availability: "unsupported", Unit: "percent", ObservedAt: now}, Metric{EntityID: "host", Metric: "memory.used", Availability: "unsupported", Unit: "bytes", ObservedAt: now}, Metric{EntityID: "host", Metric: "filesystem.used", Availability: "unsupported", Unit: "bytes", ObservedAt: now})
	}
	return snapshot, nil
}

func collectLinux(root string, now time.Time, previousCPU []uint64, previousNetwork map[string]counter) ([]Metric, []uint64, map[string]counter, error) {
	metrics, cpu, network, _, err := collectLinuxState(root, now, previousNetwork, diagnosticState{cpu: previousCPU})
	return metrics, cpu, network, err
}

func collectLinuxState(root string, now time.Time, previousNetwork map[string]counter, previousDiagnostics diagnosticState) ([]Metric, []uint64, map[string]counter, diagnosticState, error) {
	metrics := []Metric{}
	diagnosticMetrics, diagnostics, err := collectDiagnostics(root, now, previousDiagnostics)
	if err != nil {
		return metrics, nil, previousNetwork, previousDiagnostics, err
	}
	metrics = append(metrics, diagnosticMetrics...)
	uptime, err := readFirstLine(filepath.Join(root, "proc", "uptime"), "")
	if err == nil {
		fields := strings.Fields(uptime)
		if len(fields) > 0 {
			seconds, _ := strconv.ParseFloat(fields[0], 64)
			metric := Metric{EntityID: "host", Metric: "host.uptime", Unit: "seconds", Availability: "current", ObservedAt: now}
			metric.Value = &seconds
			metrics = append(metrics, metric)
		}
	} else {
		metrics = append(metrics, Metric{EntityID: "host", Metric: "host.uptime", Unit: "seconds", Availability: "unavailable", ObservedAt: now})
	}
	fsMetrics, err := filesystemMetrics(root, now)
	if err == nil {
		metrics = append(metrics, fsMetrics...)
	} else {
		metrics = append(metrics, Metric{EntityID: "host", Metric: "filesystem.used", Unit: "bytes", Availability: "unavailable", ObservedAt: now}, Metric{EntityID: "host", Metric: "filesystem.capacity", Unit: "bytes", Availability: "unavailable", ObservedAt: now})
	}
	network, err := readNetwork(filepath.Join(root, "proc", "net", "dev"), now, previousNetwork)
	if err == nil {
		metrics = append(metrics, network.metrics...)
		return metrics, diagnostics.cpu, network.counters, diagnostics, nil
	}
	return metrics, diagnostics.cpu, previousNetwork, diagnostics, nil
}

func readFirstLine(path, prefix string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if prefix == "" || strings.HasPrefix(line, prefix) {
			if prefix != "" {
				line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
			}
			return line, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("line not found")
}

func readMemory(path string, now time.Time) ([]Metric, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values := map[string]float64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		number, parseErr := strconv.ParseFloat(fields[1], 64)
		if parseErr != nil {
			continue
		}
		if len(fields) > 2 && fields[2] == "kB" {
			number *= 1024
		}
		values[strings.TrimSuffix(fields[0], ":")] = number
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	total, ok := values["MemTotal"]
	if !ok || total <= 0 {
		return nil, errors.New("memory total unavailable")
	}
	available := values["MemAvailable"]
	used := total - available
	if used < 0 {
		used = 0
	}
	percent := 100 * used / total
	swapTotal, hasSwapTotal := values["SwapTotal"]
	swapFree, hasSwapFree := values["SwapFree"]
	if !hasSwapTotal || !hasSwapFree {
		return []Metric{{EntityID: "host", Metric: "memory.used", Value: &used, Availability: "current", Unit: "bytes", ObservedAt: now}, {EntityID: "host", Metric: "memory.capacity", Value: &total, Availability: "current", Unit: "bytes", ObservedAt: now}, {EntityID: "host", Metric: "memory.used_percent", Value: &percent, Availability: "current", Unit: "percent", ObservedAt: now}, {EntityID: "host", Metric: "swap.used", Availability: "unavailable", Unit: "bytes", ObservedAt: now}, {EntityID: "host", Metric: "swap.capacity", Availability: "unavailable", Unit: "bytes", ObservedAt: now}}, nil
	}
	if swapFree > swapTotal {
		swapFree = swapTotal
	}
	swapUsed := swapTotal - swapFree
	return []Metric{{EntityID: "host", Metric: "memory.used", Value: &used, Availability: "current", Unit: "bytes", ObservedAt: now}, {EntityID: "host", Metric: "memory.capacity", Value: &total, Availability: "current", Unit: "bytes", ObservedAt: now}, {EntityID: "host", Metric: "memory.used_percent", Value: &percent, Availability: "current", Unit: "percent", ObservedAt: now}, {EntityID: "host", Metric: "swap.used", Value: &swapUsed, Availability: "current", Unit: "bytes", ObservedAt: now}, {EntityID: "host", Metric: "swap.capacity", Value: &swapTotal, Availability: "current", Unit: "bytes", ObservedAt: now}}, nil
}

type networkReading struct {
	metrics  []Metric
	counters map[string]counter
}

func readNetwork(path string, now time.Time, previous map[string]counter) (networkReading, error) {
	file, err := os.Open(path)
	if err != nil {
		return networkReading{}, err
	}
	defer file.Close()
	reading := networkReading{counters: map[string]counter{}}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		name := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		received, err1 := strconv.ParseUint(fields[0], 10, 64)
		sent, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		current := counter{received: received, sent: sent, at: now}
		reading.counters[name] = current
		prev, ok := previous[name]
		if !ok || current.received < prev.received || current.sent < prev.sent || !current.at.After(prev.at) {
			reading.metrics = append(reading.metrics, Metric{EntityID: name, Metric: "network.receive_rate", Unit: "bytes_per_second", Availability: "unavailable", ObservedAt: now, Labels: map[string]string{"interface": name}}, Metric{EntityID: name, Metric: "network.transmit_rate", Unit: "bytes_per_second", Availability: "unavailable", ObservedAt: now, Labels: map[string]string{"interface": name}})
			continue
		}
		seconds := current.at.Sub(prev.at).Seconds()
		if seconds <= 0 {
			continue
		}
		rx := float64(current.received-prev.received) / seconds
		tx := float64(current.sent-prev.sent) / seconds
		reading.metrics = append(reading.metrics, Metric{EntityID: name, Metric: "network.receive_rate", Value: &rx, Availability: "current", Unit: "bytes_per_second", ObservedAt: now, Labels: map[string]string{"interface": name}}, Metric{EntityID: name, Metric: "network.transmit_rate", Value: &tx, Availability: "current", Unit: "bytes_per_second", ObservedAt: now, Labels: map[string]string{"interface": name}})
	}
	return reading, scanner.Err()
}
