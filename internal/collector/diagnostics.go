package collector

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const HostSchemaVersion = 2

type diagnosticState struct {
	cpu   []uint64
	disks map[string]diskCounter
}

type diskCounter struct {
	identity     string
	readOps      uint64
	readSectors  uint64
	readMillis   uint64
	writeOps     uint64
	writeSectors uint64
	writeMillis  uint64
	ioMillis     uint64
	at           time.Time
}

type diskStats struct {
	major        uint64
	minor        uint64
	name         string
	readOps      uint64
	readSectors  uint64
	readMillis   uint64
	writeOps     uint64
	writeSectors uint64
	writeMillis  uint64
	ioMillis     uint64
}

type blockIdentity struct {
	ID     string
	Stable bool
	Source string
}

func collectDiagnostics(root string, now time.Time, previous diagnosticState) ([]Metric, diagnosticState, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cpuLine, err := readFirstLine(filepath.Join(root, "proc", "stat"), "cpu ")
	if err != nil {
		return nil, previous, err
	}
	cpu, err := parseUintFields(cpuLine)
	if err != nil || len(cpu) < 4 {
		return nil, previous, errors.New("cpu counters unavailable")
	}
	metrics := cpuMetrics(cpu, previous.cpu, now)
	memory, err := readMemory(filepath.Join(root, "proc", "meminfo"), now)
	if err != nil {
		return nil, previous, err
	}
	metrics = append(metrics, memory...)
	metrics = append(metrics, loadMetrics(filepath.Join(root, "proc", "loadavg"), now)...)

	disks, err := readDiskStats(filepath.Join(root, "proc", "diskstats"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, previous, err
	}
	next := diagnosticState{cpu: append([]uint64(nil), cpu...), disks: map[string]diskCounter{}}
	for _, disk := range disks {
		identity := resolveBlockIdentity(root, disk)
		diskMetrics, current := metricsForDisk(disk, identity, previous.disks, now)
		metrics = append(metrics, diskMetrics...)
		next.disks[fmt.Sprintf("%d:%d", disk.major, disk.minor)] = current
	}
	return metrics, next, nil
}

func parseUintFields(value string) ([]uint64, error) {
	fields := strings.Fields(value)
	result := make([]uint64, 0, len(fields))
	for _, field := range fields {
		parsed, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return nil, err
		}
		result = append(result, parsed)
	}
	return result, nil
}

func cpuMetrics(current, previous []uint64, now time.Time) []Metric {
	names := []string{"cpu.utilization", "cpu.user_percent", "cpu.system_percent", "cpu.iowait_percent", "cpu.steal_percent"}
	metrics := make([]Metric, 0, len(names))
	for _, name := range names {
		metrics = append(metrics, unavailableMetric("host", name, "percent", now, nil))
	}
	if len(current) < 4 || len(current) != len(previous) || len(current) < 8 {
		return metrics
	}
	delta := make([]uint64, len(current))
	for index, value := range current {
		if value < previous[index] {
			return metrics
		}
		delta[index] = value - previous[index]
	}
	total := uint64(0)
	for _, value := range delta[:8] {
		total += value
	}
	if total == 0 {
		return metrics
	}
	if delta[8] > delta[0] || len(delta) > 9 && delta[9] > delta[1] {
		return metrics
	}
	user := delta[0] - delta[8]
	utilization := 100 * float64(total-delta[3]) / float64(total)
	values := []float64{
		utilization,
		100 * float64(user) / float64(total),
		100 * float64(delta[2]) / float64(total),
		100 * float64(delta[4]) / float64(total),
		100 * float64(delta[7]) / float64(total),
	}
	for index, value := range values {
		metrics[index].Value = floatPtr(value)
		metrics[index].Availability = "current"
	}
	return metrics
}

func loadMetrics(path string, now time.Time) []Metric {
	names := []string{"load.1m", "load.5m", "load.15m"}
	metrics := make([]Metric, 0, len(names))
	for _, name := range names {
		metrics = append(metrics, unavailableMetric("host", name, "count", now, nil))
	}
	line, err := readFirstLine(path, "")
	if err != nil {
		return metrics
	}
	fields := strings.Fields(line)
	if len(fields) < len(names) {
		return metrics
	}
	for index, field := range fields[:len(names)] {
		value, err := strconv.ParseFloat(field, 64)
		if err != nil || value < 0 {
			return metrics
		}
		metrics[index].Value = floatPtr(value)
		metrics[index].Availability = "current"
	}
	return metrics
}

func readDiskStats(path string) ([]diskStats, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result := []diskStats{}
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue
		}
		major, majorErr := strconv.ParseUint(fields[0], 10, 64)
		minor, minorErr := strconv.ParseUint(fields[1], 10, 64)
		if majorErr != nil || minorErr != nil || fields[2] == "" {
			continue
		}
		key := fmt.Sprintf("%d:%d", major, minor)
		if _, exists := seen[key]; exists {
			continue
		}
		values := make([]uint64, 11)
		valid := true
		for index, fieldIndex := range []int{3, 5, 6, 7, 9, 10, 12} {
			value, parseErr := strconv.ParseUint(fields[fieldIndex], 10, 64)
			if parseErr != nil {
				valid = false
				break
			}
			values[index] = value
		}
		if !valid {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, diskStats{major: major, minor: minor, name: fields[2], readOps: values[0], readSectors: values[1], readMillis: values[2], writeOps: values[3], writeSectors: values[4], writeMillis: values[5], ioMillis: values[6]})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].major != result[right].major {
			return result[left].major < result[right].major
		}
		if result[left].minor != result[right].minor {
			return result[left].minor < result[right].minor
		}
		return result[left].name < result[right].name
	})
	return result, nil
}

func resolveBlockIdentity(root string, disk diskStats) blockIdentity {
	base := filepath.Join(root, "sys", "dev", "block", fmt.Sprintf("%d:%d", disk.major, disk.minor), "device")
	for _, candidate := range []struct {
		name   string
		source string
	}{{"wwid", "wwid"}, {"wwn", "wwn"}, {"serial", "serial"}, {"uuid", "uuid"}} {
		value, err := readBoundedText(filepath.Join(base, candidate.name), 256)
		if err != nil || value == "" {
			continue
		}
		return hashedBlockIdentity("stable|"+candidate.source+"|"+value, true, candidate.source)
	}
	return hashedBlockIdentity(fmt.Sprintf("unstable|%d:%d|%s", disk.major, disk.minor, disk.name), false, "kernel-device")
}

func hashedBlockIdentity(value string, stable bool, source string) blockIdentity {
	digest := sha256.Sum256([]byte(value))
	return blockIdentity{ID: "block-" + hex.EncodeToString(digest[:12]), Stable: stable, Source: source}
}

func readBoundedText(path string, limit int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := readAtMost(file, limit)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readAtMost(file *os.File, limit int64) ([]byte, error) {
	if limit < 1 {
		return nil, errors.New("invalid read limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("bounded value exceeded")
	}
	return data, nil
}

func metricsForDisk(disk diskStats, identity blockIdentity, previous map[string]diskCounter, now time.Time) ([]Metric, diskCounter) {
	labels := map[string]string{"device": disk.name, "major": strconv.FormatUint(disk.major, 10), "minor": strconv.FormatUint(disk.minor, 10), "identityStable": strconv.FormatBool(identity.Stable), "identitySource": identity.Source}
	metrics := []Metric{
		unavailableMetric(identity.ID, "disk.read_rate", "bytes_per_second", now, labels),
		unavailableMetric(identity.ID, "disk.write_rate", "bytes_per_second", now, labels),
		unavailableMetric(identity.ID, "disk.utilization", "percent", now, labels),
		unavailableMetric(identity.ID, "disk.read_latency", "milliseconds", now, labels),
		unavailableMetric(identity.ID, "disk.write_latency", "milliseconds", now, labels),
	}
	current := diskCounter{identity: identity.ID, readOps: disk.readOps, readSectors: disk.readSectors, readMillis: disk.readMillis, writeOps: disk.writeOps, writeSectors: disk.writeSectors, writeMillis: disk.writeMillis, ioMillis: disk.ioMillis, at: now}
	prior, ok := previous[fmt.Sprintf("%d:%d", disk.major, disk.minor)]
	if !ok || prior.identity != identity.ID {
		return metrics, current
	}
	elapsed := now.Sub(prior.at)
	if elapsed <= 0 || disk.readOps < prior.readOps || disk.readSectors < prior.readSectors || disk.readMillis < prior.readMillis || disk.writeOps < prior.writeOps || disk.writeSectors < prior.writeSectors || disk.writeMillis < prior.writeMillis || disk.ioMillis < prior.ioMillis {
		return metrics, current
	}
	seconds := elapsed.Seconds()
	readOps := disk.readOps - prior.readOps
	readSectors := disk.readSectors - prior.readSectors
	readMillis := disk.readMillis - prior.readMillis
	writeOps := disk.writeOps - prior.writeOps
	writeSectors := disk.writeSectors - prior.writeSectors
	writeMillis := disk.writeMillis - prior.writeMillis
	ioMillis := disk.ioMillis - prior.ioMillis
	metrics[0].Value = floatPtr(float64(readSectors) * 512 / seconds)
	metrics[0].Availability = "current"
	metrics[1].Value = floatPtr(float64(writeSectors) * 512 / seconds)
	metrics[1].Availability = "current"
	if elapsedMillis := seconds * 1000; float64(ioMillis) <= elapsedMillis {
		utilization := 100 * float64(ioMillis) / elapsedMillis
		metrics[2].Value = floatPtr(utilization)
		metrics[2].Availability = "current"
	}
	if readOps > 0 {
		metrics[3].Value = floatPtr(float64(readMillis) / float64(readOps))
		metrics[3].Availability = "current"
	}
	if writeOps > 0 {
		metrics[4].Value = floatPtr(float64(writeMillis) / float64(writeOps))
		metrics[4].Availability = "current"
	}
	return metrics, current
}

func unavailableMetric(entityID, name, unit string, now time.Time, labels map[string]string) Metric {
	return Metric{EntityID: entityID, Metric: name, Availability: "unavailable", Unit: unit, ObservedAt: now, Labels: cloneMetricLabels(labels)}
}

func cloneMetricLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}
