package zfs

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"scout.local/scout/internal/collector"
)

const (
	CollectorID          = "zfs"
	Provider             = "zfs"
	maxPools             = 32
	maxDatasets          = 1000
	maxOutputBytes       = 1 << 20
	collectionInterval   = time.Minute
	commandDeadline      = 10 * time.Second
	totalCollectionLimit = 20 * time.Second
	procZFSRoot          = "/proc/spl/kstat/zfs"
)

var (
	ErrUnavailable  = errors.New("ZFS collector unavailable")
	ErrAccessDenied = errors.New("ZFS read permission denied")
	ErrCounters     = errors.New("ZFS I/O counters unavailable")
)

// CommandResult retains bounded output and the process exit code. ZFS status
// may use a non-zero exit for a pool fault, so callers inspect both fields.
type CommandResult struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	Truncated bool
}

// Runner is the small command surface used by the adapter. Production uses
// fixed zpool/zfs paths; fixture runners never need a shell or a real pool.
type Runner interface {
	Run(context.Context, string, ...string) (CommandResult, error)
}

// Counter is a cumulative logical I/O snapshot. Values reset on pool import,
// so the adapter deliberately emits an unavailable gap across a reset.
type Counter struct {
	Name       string
	ReadBytes  uint64
	WriteBytes uint64
}

// CounterReader reads the fixed, read-only Linux ZFS kstat view. It is
// injectable only to make deterministic reset and permission tests possible.
type CounterReader interface {
	Read(context.Context) (map[string]Counter, error)
}

// Config contains non-secret ZFS collector settings. There are no arbitrary
// command or path settings; CounterReader exists for tests and lab adapters.
type Config struct {
	HostID        string
	CounterReader CounterReader
}

// Adapter reads ZFS capacity, state, scrub and cumulative I/O data using
// fixed, read-only commands and a fixed Linux kstat path.
type Adapter struct {
	runner  Runner
	counter CounterReader
	hostID  string
	now     func() time.Time

	mu       sync.Mutex
	previous map[string]previousCounter
}

type previousCounter struct {
	readBytes  uint64
	writeBytes uint64
	at         time.Time
}

// New uses the packaged zpool and zfs paths. The binaries are optional on a
// host; Detect and Collect make their absence a visible capability state.
func New(zpoolPath, zfsPath string) (*Adapter, error) {
	if zpoolPath == "" {
		zpoolPath = "/usr/sbin/zpool"
	}
	if zfsPath == "" {
		zfsPath = "/usr/sbin/zfs"
	}
	if !filepath.IsAbs(zpoolPath) || !filepath.IsAbs(zfsPath) {
		return nil, errors.New("ZFS utility paths must be absolute")
	}
	return NewWithRunner(&processRunner{zpoolPath: zpoolPath, zfsPath: zfsPath}, Config{})
}

// NewWithRunner constructs an adapter around fixed command and counter
// readers. The default counter reader is the fixed Linux kstat path.
func NewWithRunner(runner Runner, config Config) (*Adapter, error) {
	if runner == nil {
		return nil, ErrUnavailable
	}
	hostID := strings.TrimSpace(config.HostID)
	if hostID == "" {
		hostID = "host"
	}
	if len(hostID) > 128 {
		return nil, errors.New("ZFS host identity is out of bounds")
	}
	counter := config.CounterReader
	if counter == nil {
		counter = procCounterReader{root: procZFSRoot}
	}
	return &Adapter{
		runner:   runner,
		counter:  counter,
		hostID:   hostID,
		now:      func() time.Time { return time.Now().UTC() },
		previous: map[string]previousCounter{},
	}, nil
}

// Descriptor describes the bounded ZFS collector to the shared registry.
func Descriptor() collector.Descriptor {
	return collector.Descriptor{
		ID:                 CollectorID,
		Provider:           Provider,
		Version:            "openzfs-fixed-v1",
		RequiredPermission: []string{"zpool:read", "zfs:read", "zfs:kstat-read"},
		ConfigSchema:       map[string]string{},
		EntityLimit:        maxPools + maxDatasets,
		Interval:           collectionInterval,
		Deadline:           totalCollectionLimit,
	}
}

// Register adds the adapter to the shared collector registry.
func Register(registry *collector.Registry, adapter *Adapter) error {
	if registry == nil || adapter == nil {
		return errors.New("ZFS registry and adapter are required")
	}
	return registry.Register(Descriptor(), adapter)
}

// Detect verifies that the fixed pool inventory query executes. An empty
// successful inventory is detection success: a host may simply have no pools.
func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	if a == nil || a.runner == nil {
		return false, ErrUnavailable
	}
	_, _, _, err := a.readPools(ctx)
	if err != nil {
		return false, err
	}
	return true, nil
}

// Collect performs one bounded, non-overlapping snapshot. Pool enumeration is
// authoritative only when complete; failed optional queries retain the valid
// portion and mark the result partial so callers never retire missing data.
func (a *Adapter) Collect(ctx context.Context) (collector.ServiceResult, error) {
	if a == nil || a.runner == nil {
		return collector.ServiceResult{}, ErrUnavailable
	}
	collectCtx, cancel := context.WithTimeout(ctx, totalCollectionLimit)
	defer cancel()

	now := a.currentTime()
	pools, poolPartial, diagnostics, err := a.readPools(collectCtx)
	if err != nil {
		return collector.ServiceResult{}, err
	}
	partial := poolPartial

	datasets, datasetPartial, datasetDiagnostic := a.readDatasets(collectCtx)
	partial = partial || datasetPartial
	if datasetDiagnostic != "" {
		diagnostics = append(diagnostics, datasetDiagnostic)
	}

	statuses, statusPartial, statusDiagnostic := a.readStatuses(collectCtx)
	partial = partial || statusPartial
	if statusDiagnostic != "" {
		diagnostics = append(diagnostics, statusDiagnostic)
	}

	counters, counterDiagnostic := a.readCounters(collectCtx)
	if counterDiagnostic != "" {
		diagnostics = append(diagnostics, counterDiagnostic)
		partial = true
	}
	rates := a.rates(pools, counters, now)

	poolIdentityCounts := countPoolIdentities(pools)
	datasetIdentityCounts := countDatasetIdentities(datasets)
	result := collector.ServiceResult{
		Entities: make([]collector.Entity, 0, len(pools)+len(datasets)),
		Metrics:  make([]collector.Metric, 0, len(pools)*4+len(datasets)*2),
		Partial:  partial,
	}

	for _, pool := range pools {
		status, found := statuses[strings.ToLower(pool.Name)]
		if !found {
			status = poolStatus{Name: pool.Name, Health: pool.Health, ScrubState: "unavailable"}
			if pool.Health == "" {
				status.Health = "unavailable"
			}
			if pool.Name != "" {
				partial = true
				result.Partial = true
			}
		}
		if status.Health == "" {
			status.Health = pool.Health
		}
		entityID, stable, source := stablePoolID(a.hostID, pool, poolIdentityCounts[poolIdentityKey(pool)] > 1)
		labels := map[string]string{
			"capacityScope":  "physical",
			"healthState":    normalizedState(status.Health),
			"scrubState":     scrubState(status.ScrubState),
			"scrubErrors":    strconv.FormatUint(status.ScrubErrors, 10),
			"dataErrors":     strconv.FormatUint(status.DataErrors, 10),
			"hardwareFault":  strconv.FormatBool(poolFault(status.Health, status.ScrubErrors, status.DataErrors)),
			"identityStable": strconv.FormatBool(stable),
			"identitySource": source,
		}
		if pool.GUID != "" {
			labels["guid"] = pool.GUID
		}
		if status.ScrubProgress != "" {
			labels["scrubProgress"] = status.ScrubProgress
		}
		entityStatus := poolEntityStatus(status.Health, status.ScrubState, status.ScrubErrors, status.DataErrors)
		result.Entities = append(result.Entities, collector.Entity{ID: entityID, Kind: "zfs_pool", Name: pool.Name, Status: entityStatus, Labels: labels, Observed: now, Expires: now.Add(3 * collectionInterval)})
		result.Metrics = append(result.Metrics, poolMetrics(entityID, pool, rates[poolRateKey(pool)], now)...)
	}

	for _, dataset := range datasets {
		entityID, stable, source := stableDatasetID(a.hostID, dataset, datasetIdentityCounts[datasetIdentityKey(dataset)] > 1)
		labels := map[string]string{
			"capacityScope":  "usable",
			"pool":           datasetPoolName(dataset.Name),
			"identityStable": strconv.FormatBool(stable),
			"identitySource": source,
		}
		if dataset.GUID != "" {
			labels["guid"] = dataset.GUID
		}
		result.Entities = append(result.Entities, collector.Entity{ID: entityID, Kind: "zfs_dataset", Name: dataset.Name, Status: "online", Labels: labels, Observed: now, Expires: now.Add(3 * collectionInterval)})
		result.Metrics = append(result.Metrics, datasetMetrics(entityID, dataset, now)...)
	}

	result.Diagnostic = boundedJoin(diagnostics, 512)
	return result, nil
}

func (a *Adapter) readPools(ctx context.Context) ([]poolRecord, bool, []string, error) {
	command, err := a.runFixed(ctx, "zpool", "list", "-H", "-p", "-o", "name,guid,size,allocated,health")
	if err != nil {
		if errors.Is(err, ErrAccessDenied) {
			return nil, false, nil, err
		}
		return nil, false, nil, fmt.Errorf("%w: pool enumeration failed", ErrUnavailable)
	}
	pools, partial, diagnostic := parsePoolList(command.Stdout)
	if len(pools) == 0 && len(bytes.TrimSpace(command.Stdout)) > 0 {
		return nil, false, nil, fmt.Errorf("%w: pool inventory was invalid", ErrUnavailable)
	}
	diagnostics := []string{}
	if diagnostic != "" {
		diagnostics = append(diagnostics, diagnostic)
	}
	return pools, partial, diagnostics, nil
}

func (a *Adapter) readDatasets(ctx context.Context) ([]datasetRecord, bool, string) {
	command, err := a.runFixed(ctx, "zfs", "list", "-H", "-p", "-o", "name,guid,used,available")
	if err != nil {
		return nil, true, classifyCommandDiagnostic("dataset enumeration", err)
	}
	datasets, partial, diagnostic := parseDatasetList(command.Stdout)
	if len(datasets) == 0 && len(bytes.TrimSpace(command.Stdout)) > 0 {
		return nil, true, "dataset inventory was invalid"
	}
	return datasets, partial, diagnostic
}

func (a *Adapter) readStatuses(ctx context.Context) (map[string]poolStatus, bool, string) {
	command, err := a.runFixed(ctx, "zpool", "status", "-p")
	if err != nil {
		return map[string]poolStatus{}, true, classifyCommandDiagnostic("pool status", err)
	}
	statuses, partial, diagnostic := parseStatus(command.Stdout)
	result := make(map[string]poolStatus, len(statuses))
	for _, status := range statuses {
		if status.Name != "" {
			result[strings.ToLower(status.Name)] = status
		}
	}
	return result, partial, diagnostic
}

func (a *Adapter) readCounters(ctx context.Context) (map[string]Counter, string) {
	if a.counter == nil {
		return map[string]Counter{}, ErrCounters.Error()
	}
	counters, err := a.counter.Read(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return map[string]Counter{}, "ZFS I/O counters timed out"
		}
		return map[string]Counter{}, ErrCounters.Error()
	}
	if len(counters) > maxPools {
		return counters, fmt.Sprintf("ZFS I/O counter inventory truncated at %d pools", maxPools)
	}
	return counters, ""
}

func (a *Adapter) rates(pools []poolRecord, counters map[string]Counter, now time.Time) map[string]poolRates {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make(map[string]poolRates, len(pools))
	seen := make(map[string]struct{}, len(pools))
	for _, pool := range pools {
		key := poolRateKey(pool)
		seen[key] = struct{}{}
		current, ok := counters[strings.ToLower(pool.Name)]
		if !ok {
			delete(a.previous, key)
			continue
		}
		previous, hadPrevious := a.previous[key]
		if hadPrevious {
			elapsed := now.Sub(previous.at).Seconds()
			if elapsed > 0 && current.ReadBytes >= previous.readBytes && current.WriteBytes >= previous.writeBytes {
				readRate := float64(current.ReadBytes-previous.readBytes) / elapsed
				writeRate := float64(current.WriteBytes-previous.writeBytes) / elapsed
				result[key] = poolRates{read: floatPtr(readRate), write: floatPtr(writeRate)}
			}
		}
		a.previous[key] = previousCounter{readBytes: current.ReadBytes, writeBytes: current.WriteBytes, at: now}
	}
	for key := range a.previous {
		if _, ok := seen[key]; !ok {
			delete(a.previous, key)
		}
	}
	return result
}

func (a *Adapter) currentTime() time.Time {
	if a.now != nil {
		return a.now().UTC()
	}
	return time.Now().UTC()
}

func (a *Adapter) Close() error { return nil }

type poolRecord struct {
	Name   string
	GUID   string
	Size   uint64
	Used   uint64
	Health string
}

type datasetRecord struct {
	Name      string
	GUID      string
	Used      uint64
	Available uint64
}

type poolStatus struct {
	Name          string
	Health        string
	ScrubState    string
	ScrubProgress string
	ScrubErrors   uint64
	DataErrors    uint64
}

type poolRates struct {
	read  *float64
	write *float64
}

func parsePoolList(data []byte) ([]poolRecord, bool, string) {
	result := make([]poolRecord, 0, maxPools)
	partial := false
	invalid := 0
	forEachLine(data, func(line string) bool {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || strings.TrimSpace(fields[0]) == "" {
			partial = true
			invalid++
			return len(result) < maxPools+1
		}
		size, sizeOK := parseUint(fields[2])
		used, usedOK := parseUint(fields[3])
		if !sizeOK || !usedOK {
			partial = true
			invalid++
			return len(result) < maxPools+1
		}
		result = append(result, poolRecord{Name: boundedValue(fields[0], 128), GUID: normalizeGUID(fields[1]), Size: size, Used: used, Health: normalizedState(fields[4])})
		return len(result) < maxPools+1
	})
	if len(result) > maxPools {
		result = result[:maxPools]
		partial = true
	}
	if invalid > 0 {
		return result, partial, fmt.Sprintf("ZFS pool inventory contains %d invalid rows", invalid)
	}
	return result, partial, ""
}

func parseDatasetList(data []byte) ([]datasetRecord, bool, string) {
	result := make([]datasetRecord, 0, min(maxDatasets, 64))
	partial := false
	invalid := 0
	forEachLine(data, func(line string) bool {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || strings.TrimSpace(fields[0]) == "" {
			partial = true
			invalid++
			return len(result) < maxDatasets+1
		}
		used, usedOK := parseUint(fields[2])
		available, availableOK := parseUint(fields[3])
		if !usedOK || !availableOK {
			partial = true
			invalid++
			return len(result) < maxDatasets+1
		}
		result = append(result, datasetRecord{Name: boundedValue(fields[0], 256), GUID: normalizeGUID(fields[1]), Used: used, Available: available})
		return len(result) < maxDatasets+1
	})
	if len(result) > maxDatasets {
		result = result[:maxDatasets]
		partial = true
	}
	if invalid > 0 {
		return result, partial, fmt.Sprintf("ZFS dataset inventory contains %d invalid rows", invalid)
	}
	return result, partial, ""
}

var (
	scrubProgressPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%\s+done`)
	scrubErrorsPattern   = regexp.MustCompile(`with\s+([0-9]+)\s+errors?`)
	dataErrorsPattern    = regexp.MustCompile(`([0-9]+)\s+data\s+errors?`)
)

func parseStatus(data []byte) ([]poolStatus, bool, string) {
	result := make([]poolStatus, 0, maxPools)
	current := -1
	partial := false
	forEachLine(data, func(line string) bool {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "pool:"):
			name := boundedValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "pool:")), 128)
			if name == "" || len(result) >= maxPools+1 {
				partial = true
				return len(result) < maxPools+1
			}
			result = append(result, poolStatus{Name: name, ScrubState: "unavailable"})
			current = len(result) - 1
		case current < 0:
			if trimmed != "" {
				partial = true
			}
		case strings.HasPrefix(trimmed, "state:"):
			result[current].Health = normalizedState(strings.TrimSpace(strings.TrimPrefix(trimmed, "state:")))
		case strings.HasPrefix(trimmed, "scan:"):
			result[current].ScrubState, result[current].ScrubProgress, result[current].ScrubErrors = parseScanLine(trimmed)
		case strings.HasPrefix(trimmed, "errors:"):
			if match := dataErrorsPattern.FindStringSubmatch(strings.ToLower(trimmed)); len(match) == 2 {
				if value, ok := parseUint(match[1]); ok {
					result[current].DataErrors = value
				}
			}
		case result[current].ScrubState == "SCANNING":
			if match := scrubProgressPattern.FindStringSubmatch(trimmed); len(match) == 2 {
				result[current].ScrubProgress = match[1] + "%"
			}
		}
		return len(result) < maxPools+1
	})
	if len(result) > maxPools {
		result = result[:maxPools]
		partial = true
	}
	for _, status := range result {
		if status.Health == "" {
			partial = true
		}
	}
	if partial {
		return result, true, "ZFS pool status was partial or unparseable"
	}
	return result, false, ""
}

func parseScanLine(line string) (string, string, uint64) {
	lower := strings.ToLower(line)
	state := "NONE"
	switch {
	case strings.Contains(lower, "in progress"):
		state = "SCANNING"
	case strings.Contains(lower, "canceled"):
		state = "CANCELED"
	case strings.Contains(lower, "repaired"), strings.Contains(lower, "resilvered"):
		state = "FINISHED"
	case strings.Contains(lower, "none"), strings.Contains(lower, "never"):
		state = "NONE"
	default:
		state = "unavailable"
	}
	progress := ""
	if match := scrubProgressPattern.FindStringSubmatch(line); len(match) == 2 {
		progress = match[1] + "%"
	}
	errors := uint64(0)
	if match := scrubErrorsPattern.FindStringSubmatch(lower); len(match) == 2 {
		errors, _ = parseUint(match[1])
	}
	return state, progress, errors
}

func poolMetrics(entityID string, pool poolRecord, rates poolRates, now time.Time) []collector.Metric {
	return []collector.Metric{
		{EntityID: entityID, Metric: "zfs.pool.used", Value: floatPtr(float64(pool.Used)), Availability: "current", Unit: "bytes", ObservedAt: now, Labels: map[string]string{"capacityScope": "physical"}},
		{EntityID: entityID, Metric: "zfs.pool.capacity", Value: floatPtr(float64(pool.Size)), Availability: "current", Unit: "bytes", ObservedAt: now, Labels: map[string]string{"capacityScope": "physical"}},
		{EntityID: entityID, Metric: "zfs.pool.read_rate", Value: cloneFloat(rates.read), Availability: rateAvailability(rates.read), Unit: "bytes_per_second", ObservedAt: now, Labels: map[string]string{"counterSource": "zfs-kstat"}},
		{EntityID: entityID, Metric: "zfs.pool.write_rate", Value: cloneFloat(rates.write), Availability: rateAvailability(rates.write), Unit: "bytes_per_second", ObservedAt: now, Labels: map[string]string{"counterSource": "zfs-kstat"}},
	}
}

func datasetMetrics(entityID string, dataset datasetRecord, now time.Time) []collector.Metric {
	labels := map[string]string{"capacityScope": "usable"}
	return []collector.Metric{
		{EntityID: entityID, Metric: "zfs.dataset.used", Value: floatPtr(float64(dataset.Used)), Availability: "current", Unit: "bytes", ObservedAt: now, Labels: labels},
		{EntityID: entityID, Metric: "zfs.dataset.available", Value: floatPtr(float64(dataset.Available)), Availability: "current", Unit: "bytes", ObservedAt: now, Labels: map[string]string{"capacityScope": "usable"}},
	}
}

func rateAvailability(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return "current"
}

func poolEntityStatus(health, scrub string, scrubErrors, dataErrors uint64) string {
	if poolFault(health, scrubErrors, dataErrors) {
		return "fault"
	}
	if strings.EqualFold(scrub, "SCANNING") {
		return "scrubbing"
	}
	if normalizedState(health) == "ONLINE" {
		return "online"
	}
	return "unavailable"
}

func poolFault(health string, scrubErrors, dataErrors uint64) bool {
	switch normalizedState(health) {
	case "DEGRADED", "FAULTED", "UNAVAIL", "SUSPENDED":
		return true
	default:
		return scrubErrors > 0 || dataErrors > 0
	}
}

func scrubState(value string) string {
	if value == "" {
		return "unavailable"
	}
	return boundedValue(value, 32)
}

func countPoolIdentities(pools []poolRecord) map[string]int {
	result := make(map[string]int, len(pools))
	for _, pool := range pools {
		result[poolIdentityKey(pool)]++
	}
	return result
}

func countDatasetIdentities(datasets []datasetRecord) map[string]int {
	result := make(map[string]int, len(datasets))
	for _, dataset := range datasets {
		result[datasetIdentityKey(dataset)]++
	}
	return result
}

func poolIdentityKey(pool poolRecord) string {
	if pool.GUID != "" {
		return "guid:" + strings.ToLower(pool.GUID)
	}
	return "name:" + strings.ToLower(pool.Name)
}

func datasetIdentityKey(dataset datasetRecord) string {
	if dataset.GUID != "" {
		return "guid:" + strings.ToLower(dataset.GUID)
	}
	return "name:" + strings.ToLower(dataset.Name)
}

func poolRateKey(pool poolRecord) string {
	if pool.GUID != "" {
		return "guid:" + strings.ToLower(pool.GUID)
	}
	return "name:" + strings.ToLower(pool.Name)
}

func stablePoolID(hostID string, pool poolRecord, ambiguous bool) (string, bool, string) {
	key := poolIdentityKey(pool)
	stable := pool.GUID != ""
	source := "guid"
	if pool.GUID == "" {
		source = "unstable-name"
	}
	if ambiguous {
		key = "name:" + strings.ToLower(pool.Name)
		stable = false
		source = "ambiguous-name"
	}
	return hashID("zfs-pool", hostID, key), stable, source
}

func stableDatasetID(hostID string, dataset datasetRecord, ambiguous bool) (string, bool, string) {
	key := datasetIdentityKey(dataset)
	stable := dataset.GUID != ""
	source := "guid"
	if dataset.GUID == "" {
		source = "unstable-name"
	}
	if ambiguous {
		key = "name:" + strings.ToLower(dataset.Name)
		stable = false
		source = "ambiguous-name"
	}
	return hashID("zfs-dataset", hostID, key), stable, source
}

func hashID(kind, hostID, key string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + boundedValue(hostID, 128) + "\x00" + key))
	return kind + "-" + hex.EncodeToString(sum[:])
}

func datasetPoolName(name string) string {
	if index := strings.IndexByte(name, '/'); index >= 0 {
		return name[:index]
	}
	return name
}

func normalizeGUID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" || value == "0" {
		return ""
	}
	if _, ok := parseUint(value); !ok {
		return ""
	}
	return value
}

func normalizedState(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" || value == "-" {
		return ""
	}
	for _, character := range value {
		if (character < 'A' || character > 'Z') && character != '_' && character != '-' {
			return "unavailable"
		}
	}
	return boundedValue(value, 32)
}

func parseUint(value string) (uint64, bool) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return parsed, err == nil
}

func forEachLine(data []byte, visit func(string) bool) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), 16<<10)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !visit(line) {
			return
		}
	}
}

func runErrorDiagnostic(err error) string {
	switch {
	case errors.Is(err, ErrAccessDenied):
		return ErrAccessDenied.Error()
	case errors.Is(err, ErrCounters):
		return ErrCounters.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "collection timed out"
	default:
		return ErrUnavailable.Error()
	}
}

func classifyCommandDiagnostic(label string, err error) string {
	diagnostic := runErrorDiagnostic(err)
	if diagnostic == ErrUnavailable.Error() {
		return "ZFS " + label + " unavailable"
	}
	return "ZFS " + label + ": " + diagnostic
}

func boundedValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

func boundedJoin(values []string, limit int) string {
	var builder strings.Builder
	for _, value := range values {
		value = boundedValue(value, limit)
		if value == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("; ")
		}
		if builder.Len()+len(value) > limit {
			remaining := limit - builder.Len()
			if remaining > 0 {
				builder.WriteString(value[:remaining])
			}
			break
		}
		builder.WriteString(value)
	}
	return builder.String()
}

func floatPtr(value float64) *float64 { return &value }

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (a *Adapter) runFixed(ctx context.Context, program string, args ...string) (CommandResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, commandDeadline)
	defer cancel()
	result, err := a.runner.Run(commandCtx, program, args...)
	if err != nil {
		if commandCtx.Err() != nil {
			return result, commandCtx.Err()
		}
		return result, fmt.Errorf("%w: %s command unavailable", ErrUnavailable, program)
	}
	if result.Truncated {
		return result, fmt.Errorf("%w: %s output exceeded the collector bound", ErrUnavailable, program)
	}
	if result.ExitCode == 0 {
		return result, nil
	}
	message := strings.ToLower(boundedValue(string(result.Stderr), 512))
	if strings.Contains(message, "permission") || strings.Contains(message, "not permitted") || strings.Contains(message, "access denied") {
		return result, ErrAccessDenied
	}
	return result, fmt.Errorf("%w: %s command failed", ErrUnavailable, program)
}

type processRunner struct {
	zpoolPath string
	zfsPath   string
}

func (r *processRunner) Run(ctx context.Context, program string, args ...string) (CommandResult, error) {
	path := ""
	switch program {
	case "zpool":
		path = r.zpoolPath
	case "zfs":
		path = r.zfsPath
	default:
		return CommandResult{}, errors.New("unallowlisted ZFS command")
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	stdout := &limitedBuffer{limit: maxOutputBytes}
	stderr := &limitedBuffer{limit: maxOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Truncated: stdout.truncated || stderr.truncated}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, fmt.Errorf("%w: ZFS executable unavailable", ErrUnavailable)
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if b.limit <= b.Len() {
		b.truncated = true
		return len(value), nil
	}
	remaining := b.limit - b.Len()
	if len(value) > remaining {
		_, _ = b.Buffer.Write(value[:remaining])
		b.truncated = true
		return len(value), nil
	}
	return b.Buffer.Write(value)
}

type procCounterReader struct {
	root string
}

func (r procCounterReader) Read(ctx context.Context) (map[string]Counter, error) {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return nil, ErrCounters
	}
	result := make(map[string]Counter, min(len(entries), maxPools))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || entry.Name() == "" || strings.ContainsAny(entry.Name(), "/\\") || len(result) >= maxPools {
			continue
		}
		readBytes, writeBytes, err := readKstatCounters(filepath.Join(r.root, entry.Name(), "io"))
		if err != nil {
			continue
		}
		result[strings.ToLower(entry.Name())] = Counter{Name: entry.Name(), ReadBytes: readBytes, WriteBytes: writeBytes}
	}
	if len(result) == 0 {
		return nil, ErrCounters
	}
	return result, nil
}

func readKstatCounters(path string) (uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxOutputBytes))
	var readBytes, writeBytes uint64
	var foundRead, foundWrite bool
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, ok := firstNumeric(fields[1:])
		if !ok {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "nread":
			readBytes, foundRead = value, true
		case "nwritten":
			writeBytes, foundWrite = value, true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if !foundRead || !foundWrite {
		return 0, 0, ErrCounters
	}
	return readBytes, writeBytes, nil
}

func firstNumeric(fields []string) (uint64, bool) {
	for _, field := range fields {
		if value, ok := parseUint(field); ok {
			return value, true
		}
	}
	return 0, false
}
