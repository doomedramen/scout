package gpu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"scout.local/scout/internal/collector"
)

const (
	CollectorID         = "gpu"
	Provider            = "gpu"
	maxGPUs             = 32
	maxDirectoryEntries = 128
	maxHwmonEntries     = 32
	maxReadBytes        = 4096
	maxOutputBytes      = 1 << 20
	maxDiagnosticBytes  = 1024
	maxHostIDBytes      = 128
	collectionInterval  = 30 * time.Second
	collectionDeadline  = 10 * time.Second
	defaultSysfsRoot    = "/sys"
	defaultNvidiaPath   = "/usr/bin/nvidia-smi"
	vendorAMD           = "1002"
	vendorIntel         = "8086"

	nvidiaQuery = "index,uuid,pci.bus_id,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,driver_version"
)

var (
	ErrUnavailable  = errors.New("GPU collector unavailable")
	ErrAccessDenied = errors.New("GPU collector access denied")
	ErrUnsafePath   = errors.New("GPU path escapes configured sysfs root")
	ErrReadLimit    = errors.New("GPU attribute exceeds read limit")
)

// Config contains bounded, non-secret GPU settings. SysfsRoot is injectable
// for fixtures and host mounts; production uses /sys.
type Config struct {
	HostID    string
	SysfsRoot string
}

// CommandResult retains bounded nvidia-smi output and its process status.
type CommandResult struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	Truncated bool
}

// Runner is the fixed command surface used by the NVIDIA adapter. It has no
// shell and does not allow caller-supplied utility arguments.
type Runner interface {
	Run(context.Context, ...string) (CommandResult, error)
}

// Adapter combines independent NVIDIA and DRM/sysfs collection. A failed
// optional source never discards a valid source from the same host.
type Adapter struct {
	runner Runner
	root   string
	hostID string
	now    func() time.Time

	mu       sync.Mutex
	previous map[string]energySnapshot
}

type energySnapshot struct {
	microjoules uint64
	at          time.Time
}

// New uses the packaged nvidia-smi path and the host sysfs mount.
func New(nvidiaPath, sysfsRoot string) (*Adapter, error) {
	if nvidiaPath == "" {
		nvidiaPath = defaultNvidiaPath
	}
	if !filepath.IsAbs(nvidiaPath) || len(nvidiaPath) > 4096 {
		return nil, errors.New("nvidia-smi path must be absolute and bounded")
	}
	adapter, err := NewWithRunner(processRunner{path: filepath.Clean(nvidiaPath)}, Config{SysfsRoot: sysfsRoot})
	if err != nil {
		return nil, err
	}
	return adapter, nil
}

// NewWithRunner constructs a GPU adapter around a fixed NVIDIA runner. A nil
// runner is valid for AMD/Intel-only hosts and fixture tests.
func NewWithRunner(runner Runner, config Config) (*Adapter, error) {
	root := strings.TrimSpace(config.SysfsRoot)
	if root == "" {
		root = defaultSysfsRoot
	}
	if !filepath.IsAbs(root) || len(root) > 4096 {
		return nil, errors.New("GPU sysfs root must be absolute and bounded")
	}
	hostID := strings.TrimSpace(config.HostID)
	if hostID == "" {
		hostID = "host"
	}
	if len(hostID) > maxHostIDBytes {
		return nil, errors.New("GPU host identity is out of bounds")
	}
	return &Adapter{runner: runner, root: filepath.Clean(root), hostID: hostID, now: func() time.Time { return time.Now().UTC() }, previous: map[string]energySnapshot{}}, nil
}

// Descriptor describes the bounded NVIDIA and AMD/Intel GPU collector.
func Descriptor() collector.Descriptor {
	return collector.Descriptor{
		ID:                 CollectorID,
		Provider:           Provider,
		Version:            "gpu-fixed-v1",
		RequiredPermission: []string{"nvidia-smi:read", "sysfs:drm-read", "sysfs:hwmon-read"},
		ConfigSchema: map[string]string{
			"sysfsRoot": "host sysfs mount; production default /sys",
		},
		EntityLimit: maxGPUs,
		Interval:    collectionInterval,
		Deadline:    collectionDeadline,
	}
}

// Register adds the adapter to the shared collector registry.
func Register(registry *collector.Registry, adapter *Adapter) error {
	if registry == nil || adapter == nil {
		return errors.New("GPU registry and adapter are required")
	}
	return registry.Register(Descriptor(), adapter)
}

// Detect checks either fixed NVIDIA querying or readable DRM sysfs. Empty
// successful inventories are valid on hosts without a supported GPU.
func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	if a == nil {
		return false, ErrUnavailable
	}
	var firstErr error
	if a.runner != nil {
		command, err := a.runNvidia(ctx)
		if err == nil && (command.ExitCode == 0 || len(bytes.TrimSpace(command.Stdout)) > 0) {
			return true, nil
		}
		if err != nil {
			firstErr = err
		}
	}
	if _, _, _, err := a.discoverDRMCards(); err == nil {
		return true, nil
	} else if firstErr == nil {
		firstErr = err
	}
	if firstErr == nil {
		firstErr = ErrUnavailable
	}
	return false, firstErr
}

// Collect keeps source failures independent: NVIDIA and DRM/sysfs results
// are merged only when their PCI identity does not identify the same device.
func (a *Adapter) Collect(ctx context.Context) (collector.ServiceResult, error) {
	if a == nil {
		return collector.ServiceResult{}, ErrUnavailable
	}
	collectCtx, cancel := context.WithTimeout(ctx, collectionDeadline)
	defer cancel()
	now := a.currentTime()
	result := collector.ServiceResult{Entities: []collector.Entity{}, Metrics: []collector.Metric{}}
	seenIDs := map[string]struct{}{}
	seenPCI := map[string]struct{}{}
	diagnostics := []string{}
	sourceAvailable := false
	var firstErr error
	var nvidiaErr error

	if a.runner != nil {
		nvidia, err := a.collectNVIDIA(collectCtx, now, maxGPUs, seenIDs, seenPCI)
		result.Entities = append(result.Entities, nvidia.entities...)
		result.Metrics = append(result.Metrics, nvidia.metrics...)
		if nvidia.partial {
			result.Partial = true
		}
		diagnostics = append(diagnostics, nvidia.diagnostics...)
		if err == nil {
			sourceAvailable = true
		} else {
			nvidiaErr = err
			firstErr = err
		}
	}

	if len(result.Entities) < maxGPUs {
		sysfs, available, err := a.collectSysfs(collectCtx, now, maxGPUs-len(result.Entities), seenIDs, seenPCI)
		result.Entities = append(result.Entities, sysfs.entities...)
		result.Metrics = append(result.Metrics, sysfs.metrics...)
		if sysfs.partial {
			result.Partial = true
		}
		diagnostics = append(diagnostics, sysfs.diagnostics...)
		if available {
			sourceAvailable = true
			if nvidiaErr != nil {
				result.Partial = true
			}
		} else if firstErr == nil {
			firstErr = err
		}
	}

	result.Diagnostic = joinDiagnostics(diagnostics)
	if !sourceAvailable {
		if firstErr == nil {
			firstErr = ErrUnavailable
		}
		return collector.ServiceResult{}, firstErr
	}
	return result, nil
}

func (a *Adapter) collectNVIDIA(ctx context.Context, now time.Time, limit int, seenIDs, seenPCI map[string]struct{}) (gpuResult, error) {
	command, err := a.runNvidia(ctx)
	if err != nil {
		return gpuResult{diagnostics: []string{"NVIDIA query unavailable"}}, err
	}
	readings, partial, diagnostics := parseNVIDIA(command.Stdout)
	result := gpuResult{partial: partial, diagnostics: diagnostics}
	if command.ExitCode != 0 {
		result.partial = true
		result.diagnostics = append(result.diagnostics, fmt.Sprintf("NVIDIA query exited with status %d", command.ExitCode))
	}
	for ordinal, reading := range readings {
		if len(result.entities) >= limit {
			result.partial = true
			result.diagnostics = append(result.diagnostics, fmt.Sprintf("GPU inventory truncated at %d devices", maxGPUs))
			break
		}
		id, stable, source := gpuIdentity(a.hostID, reading.uuid, reading.pciSlot, "nvidia", reading.evidenceKey(), ordinal, seenIDs)
		if reading.pciSlot != "" {
			seenPCI[canonicalPCI(reading.pciSlot)] = struct{}{}
		}
		appendGPUReading(&result, reading.toGPUReading(id, stable, source), now, seenIDs)
	}
	return result, nil
}

func (a *Adapter) runNvidia(ctx context.Context) (CommandResult, error) {
	if a.runner == nil {
		return CommandResult{}, ErrUnavailable
	}
	result, err := a.runner.Run(ctx, "--query-gpu="+nvidiaQuery, "--format=csv,noheader,nounits")
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("%w: nvidia-smi execution failed", ErrUnavailable)
	}
	if result.Truncated {
		return result, ErrReadLimit
	}
	if result.ExitCode != 0 && len(bytes.TrimSpace(result.Stdout)) == 0 {
		return result, fmt.Errorf("%w: nvidia-smi query failed", ErrUnavailable)
	}
	return result, nil
}

type nvidiaReading struct {
	index       string
	uuid        string
	pciSlot     string
	name        string
	driver      string
	fields      gpuFields
	diagnostics []string
}

func parseNVIDIA(data []byte) ([]nvidiaReading, bool, []string) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	readings := []nvidiaReading{}
	partial := false
	diagnostics := []string{}
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			partial = true
			diagnostics = append(diagnostics, "NVIDIA query contained malformed CSV")
			continue
		}
		if len(row) == 0 || allEmpty(row) {
			continue
		}
		if len(row) != 10 {
			partial = true
			diagnostics = append(diagnostics, "NVIDIA query row had an unexpected field count")
			continue
		}
		reading := nvidiaReading{
			index:   normalizedText(row[0]),
			uuid:    normalizedText(row[1]),
			pciSlot: normalizedText(row[2]),
			name:    sanitizeText(row[3], 128),
			driver:  sanitizeText(row[9], 64),
		}
		if reading.name == "" {
			reading.name = "NVIDIA GPU"
		}
		if value, err := parseNvidiaNumber(row[4], 1, percentOK); err != nil {
			reading.diagnostics = append(reading.diagnostics, "utilization unavailable")
			partial = true
		} else {
			reading.fields.utilization = value
		}
		if value, err := parseNvidiaNumber(row[5], 1024*1024, nonNegative); err != nil {
			reading.diagnostics = append(reading.diagnostics, "memory used unavailable")
			partial = true
		} else {
			reading.fields.memoryUsed = value
		}
		if value, err := parseNvidiaNumber(row[6], 1024*1024, nonNegative); err != nil {
			reading.diagnostics = append(reading.diagnostics, "memory capacity unavailable")
			partial = true
		} else {
			reading.fields.memoryCapacity = value
		}
		if value, err := parseNvidiaNumber(row[7], 1, finiteNumber); err != nil {
			reading.diagnostics = append(reading.diagnostics, "temperature unavailable")
			partial = true
		} else {
			reading.fields.temperature = value
		}
		if value, err := parseNvidiaNumber(row[8], 1, nonNegative); err != nil {
			reading.diagnostics = append(reading.diagnostics, "power unavailable")
			partial = true
		} else {
			reading.fields.power = value
		}
		if len(reading.diagnostics) > 0 {
			reading.fields.diagnostics = append(reading.fields.diagnostics, reading.diagnostics...)
		}
		readings = append(readings, reading)
		if len(readings) >= maxGPUs && reader != nil {
			// Continue reading only through the bounded command output so a
			// later row can prove truncation without retaining it.
			for {
				_, readErr := reader.Read()
				if errors.Is(readErr, io.EOF) {
					return readings, partial, diagnostics
				}
				partial = true
				diagnostics = append(diagnostics, fmt.Sprintf("GPU inventory truncated at %d devices", maxGPUs))
				return readings, partial, diagnostics
			}
		}
	}
	return readings, partial, diagnostics
}

func (r nvidiaReading) evidenceKey() string {
	return strings.Join([]string{r.name, r.driver, strconv.FormatFloat(valueOrZero(r.fields.memoryCapacity), 'f', 0, 64)}, "\x00")
}

func (r nvidiaReading) toGPUReading(id string, stable bool, source string) gpuReading {
	labels := map[string]string{
		"vendor":         "nvidia",
		"identityStable": strconv.FormatBool(stable),
		"identitySource": source,
		"cardIndex":      r.index,
	}
	if r.uuid != "" {
		labels["uuid"] = r.uuid
	}
	if r.pciSlot != "" {
		labels["pciSlot"] = r.pciSlot
	}
	if r.driver != "" {
		labels["driver"] = r.driver
	}
	labels["powerScope"] = "gpu-board"
	labels["powerSource"] = "nvidia-smi"
	return gpuReading{id: id, name: r.name, labels: labels, fields: r.fields}
}

type gpuFields struct {
	utilization    *float64
	memoryUsed     *float64
	memoryCapacity *float64
	temperature    *float64
	power          *float64
	powerScope     string
	powerSource    string
	diagnostics    []string
}

type gpuReading struct {
	id     string
	name   string
	labels map[string]string
	fields gpuFields
}

type gpuResult struct {
	entities    []collector.Entity
	metrics     []collector.Metric
	partial     bool
	diagnostics []string
}

func appendGPUReading(result *gpuResult, reading gpuReading, now time.Time, seenIDs map[string]struct{}) {
	if _, exists := seenIDs[reading.id]; exists {
		result.partial = true
		result.diagnostics = append(result.diagnostics, "duplicate GPU identity suppressed")
		return
	}
	seenIDs[reading.id] = struct{}{}
	labels := cloneLabels(reading.labels)
	fields := []gpuMetric{
		{name: "gpu.utilization", unit: "percent", value: reading.fields.utilization},
		{name: "gpu.memory.used", unit: "bytes", value: reading.fields.memoryUsed},
		{name: "gpu.memory.capacity", unit: "bytes", value: reading.fields.memoryCapacity},
		{name: "gpu.temperature", unit: "celsius", value: reading.fields.temperature},
		{name: "gpu.power", unit: "watts", value: reading.fields.power},
	}
	currentFields := 0
	metrics := make([]collector.Metric, 0, len(fields))
	for _, field := range fields {
		capability := strings.TrimPrefix(field.name, "gpu.")
		if field.value != nil {
			currentFields++
			labels["capability."+capability] = "current"
		} else {
			labels["capability."+capability] = "unavailable"
		}
		metricLabels := map[string]string{
			"vendor":         labels["vendor"],
			"identityStable": labels["identityStable"],
		}
		if labels["pciSlot"] != "" {
			metricLabels["pciSlot"] = labels["pciSlot"]
		}
		if labels["powerScope"] != "" && field.name == "gpu.power" {
			metricLabels["powerScope"] = labels["powerScope"]
			metricLabels["powerSource"] = labels["powerSource"]
		}
		metrics = append(metrics, collector.Metric{EntityID: reading.id, Metric: field.name, Value: field.value, Availability: availability(field.value), Unit: field.unit, ObservedAt: now, Labels: metricLabels})
	}
	if reading.fields.powerScope != "" {
		labels["powerScope"] = reading.fields.powerScope
		labels["powerSource"] = reading.fields.powerSource
	}
	status := "unavailable"
	if currentFields > 0 {
		status = "online"
	}
	result.entities = append(result.entities, collector.Entity{ID: reading.id, Kind: "gpu", Name: reading.name, Status: status, Labels: labels, Observed: now, Expires: now.Add(3 * collectionInterval)})
	result.metrics = append(result.metrics, metrics...)
	if len(reading.fields.diagnostics) > 0 {
		result.partial = true
		result.diagnostics = append(result.diagnostics, reading.fields.diagnostics...)
	}
}

type gpuMetric struct {
	name  string
	unit  string
	value *float64
}

func (a *Adapter) collectSysfs(ctx context.Context, now time.Time, limit int, seenIDs, seenPCI map[string]struct{}) (gpuResult, bool, error) {
	cards, available, diagnostics, err := a.discoverDRMCards()
	if err != nil {
		return gpuResult{diagnostics: diagnostics}, false, err
	}
	result := gpuResult{diagnostics: diagnostics, partial: len(diagnostics) > 0}
	for ordinal, card := range cards {
		if err := ctx.Err(); err != nil {
			result.partial = true
			result.diagnostics = append(result.diagnostics, "GPU sysfs collection deadline exceeded")
			break
		}
		if len(result.entities) >= limit {
			result.partial = true
			result.diagnostics = append(result.diagnostics, fmt.Sprintf("GPU inventory truncated at %d devices", maxGPUs))
			break
		}
		if card.pciSlot != "" {
			pci := canonicalPCI(card.pciSlot)
			if _, exists := seenPCI[pci]; exists {
				continue
			}
			seenPCI[pci] = struct{}{}
		}
		id, stable, source := gpuIdentity(a.hostID, "", card.pciSlot, card.vendor, card.devicePath, ordinal, seenIDs)
		reading := a.readSysfsGPU(ctx, card, id, stable, source, now)
		appendGPUReading(&result, reading, now, seenIDs)
	}
	return result, available, nil
}

type drmCard struct {
	index      int
	cardName   string
	cardPath   string
	devicePath string
	vendor     string
	pciSlot    string
	driver     string
	name       string
}

func (a *Adapter) discoverDRMCards() ([]drmCard, bool, []string, error) {
	root, err := a.resolveRoot()
	if err != nil {
		return nil, false, nil, classifyPathError(err)
	}
	drmRoot, err := resolveWithin(root, filepath.Join(root, "class", "drm"))
	if err != nil {
		return nil, false, nil, classifyPathError(err)
	}
	entries, truncated, err := readDirectory(drmRoot, maxDirectoryEntries)
	if err != nil {
		return nil, false, nil, classifyPathError(err)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	diagnostics := []string{}
	if truncated {
		diagnostics = append(diagnostics, "DRM card inventory truncated")
	}
	cards := []drmCard{}
	for _, entry := range entries {
		index, ok := parseCardName(entry.Name())
		if !ok {
			continue
		}
		cardPath, err := resolveWithin(root, filepath.Join(drmRoot, entry.Name()))
		if err != nil {
			diagnostics = append(diagnostics, "DRM card path rejected")
			continue
		}
		devicePath, err := resolveWithin(root, filepath.Join(cardPath, "device"))
		if err != nil {
			diagnostics = append(diagnostics, "DRM device path unavailable")
			continue
		}
		vendor, present, err := readOptionalAttribute(root, devicePath, "vendor")
		if err != nil || !present {
			diagnostics = append(diagnostics, "DRM vendor unavailable")
			continue
		}
		vendor = normalizeVendor(vendor)
		if vendor != vendorAMD && vendor != vendorIntel {
			continue
		}
		pciSlot, _, pciErr := readOptionalAttribute(root, devicePath, "uevent")
		if pciErr != nil {
			// A missing PCI slot is allowed, but permissions remain visible as
			// an unstable identity rather than becoming a card-index identity.
			diagnostics = append(diagnostics, "DRM PCI identity unavailable")
		}
		pciSlot = sanitizeText(parseUeventValue(pciSlot, "PCI_SLOT_NAME"), 64)
		driver := resolveDriver(root, devicePath)
		name := firstAvailableText(root, devicePath, "product_name", "name", "device_name")
		if name == "" {
			name = vendorDisplayName(vendor) + " GPU"
		}
		cards = append(cards, drmCard{index: index, cardName: entry.Name(), cardPath: cardPath, devicePath: devicePath, vendor: vendor, pciSlot: pciSlot, driver: driver, name: name})
		if len(cards) >= maxGPUs {
			if len(entries) > len(cards) {
				diagnostics = append(diagnostics, fmt.Sprintf("GPU inventory truncated at %d devices", maxGPUs))
			}
			break
		}
	}
	return cards, true, diagnostics, nil
}

func (a *Adapter) readSysfsGPU(ctx context.Context, card drmCard, id string, stable bool, source string, now time.Time) gpuReading {
	fields := gpuFields{}
	labels := map[string]string{
		"vendor":         vendorDisplayName(card.vendor),
		"identityStable": strconv.FormatBool(stable),
		"identitySource": source,
		"cardIndex":      strconv.Itoa(card.index),
	}
	if card.pciSlot != "" {
		labels["pciSlot"] = card.pciSlot
	}
	if card.driver != "" {
		labels["driver"] = card.driver
	}
	if card.driver == "" {
		fields.diagnostics = append(fields.diagnostics, "GPU driver unavailable")
	}

	if card.vendor == vendorAMD {
		fields.utilization, fields.diagnostics = readSysfsNumber(ctx, a.root, card.devicePath, []string{"gpu_busy_percent"}, percentOK, fields.diagnostics)
	} else {
		fields.utilization, fields.diagnostics = readSysfsNumber(ctx, a.root, card.devicePath, []string{"gt_busy_percent", "gpu_busy_percent"}, percentOK, fields.diagnostics)
	}
	memoryUsedNames := []string{"mem_info_vram_used"}
	memoryCapacityNames := []string{"mem_info_vram_total"}
	if card.vendor == vendorIntel {
		memoryUsedNames = []string{"mem_info_vram_used", "mem_info_lmem_used", "mem_info_local_mem_used"}
		memoryCapacityNames = []string{"mem_info_vram_total", "mem_info_lmem_total", "mem_info_local_mem_total"}
	}
	fields.memoryUsed, fields.diagnostics = readSysfsNumber(ctx, a.root, card.devicePath, memoryUsedNames, nonNegative, fields.diagnostics)
	fields.memoryCapacity, fields.diagnostics = readSysfsNumber(ctx, a.root, card.devicePath, memoryCapacityNames, nonNegative, fields.diagnostics)

	hwmon := a.readGPUHwmon(ctx, card.devicePath, card.vendor, id, now)
	fields.temperature = hwmon.temperature
	fields.power = hwmon.power
	fields.powerScope = hwmon.powerScope
	fields.powerSource = hwmon.powerSource
	fields.diagnostics = append(fields.diagnostics, hwmon.diagnostics...)

	labels["driverCapability"] = driverCapability(card.vendor, card.driver)
	return gpuReading{id: id, name: card.name, labels: labels, fields: fields}
}

type hwmonReading struct {
	temperature *float64
	power       *float64
	powerScope  string
	powerSource string
	diagnostics []string
}

func (a *Adapter) readGPUHwmon(ctx context.Context, devicePath, vendor, id string, now time.Time) hwmonReading {
	result := hwmonReading{}
	root, err := a.resolveRoot()
	if err != nil {
		return result
	}
	hwmonRoot, err := resolveWithin(root, filepath.Join(devicePath, "hwmon"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return result
		}
		result.diagnostics = append(result.diagnostics, "GPU hwmon path unavailable")
		return result
	}
	entries, truncated, err := readDirectory(hwmonRoot, maxHwmonEntries)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			result.diagnostics = append(result.diagnostics, "GPU hwmon inventory unavailable")
		}
		return result
	}
	if truncated {
		result.diagnostics = append(result.diagnostics, "GPU hwmon inventory truncated")
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	for _, entry := range entries {
		if !isHwmonName(entry.Name()) {
			continue
		}
		hwmonPath, resolveErr := resolveWithin(root, filepath.Join(hwmonRoot, entry.Name()))
		if resolveErr != nil {
			result.diagnostics = append(result.diagnostics, "GPU hwmon path rejected")
			continue
		}
		if result.temperature == nil {
			result.temperature, result.diagnostics = readSysfsNumber(ctx, root, hwmonPath, []string{"temp1_input", "temp2_input"}, finiteNumber, result.diagnostics)
			if result.temperature != nil {
				result.temperature = scaleValue(result.temperature, 0.001)
			}
		}
		if vendor == vendorAMD && result.power == nil {
			result.power, result.diagnostics = readSysfsNumber(ctx, root, hwmonPath, []string{"power1_average", "power1_input"}, nonNegative, result.diagnostics)
			if result.power != nil {
				result.power = scaleValue(result.power, 0.000001)
				result.powerScope = "gpu-board"
				result.powerSource = "hwmon-power"
			}
		}
		if vendor == vendorIntel && result.power == nil {
			energy, present, energyErr := readOptionalAttribute(root, hwmonPath, "energy1_input")
			if energyErr != nil {
				result.diagnostics = append(result.diagnostics, "GPU energy counter unavailable")
			} else if present {
				result.powerScope = "package"
				result.powerSource = "hwmon-energy"
				microjoules, parseErr := strconv.ParseUint(strings.TrimSpace(energy), 10, 64)
				if parseErr != nil {
					result.diagnostics = append(result.diagnostics, "GPU energy counter malformed")
				} else {
					power, available, reset := a.energyPower(id, microjoules, now)
					if reset {
						result.diagnostics = append(result.diagnostics, "GPU energy counter reset")
					}
					if available {
						result.power = power
					}
				}
			}
		}
		if result.temperature != nil && result.power != nil {
			break
		}
	}
	return result
}

func (a *Adapter) energyPower(id string, microjoules uint64, now time.Time) (*float64, bool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	previous, exists := a.previous[id]
	a.previous[id] = energySnapshot{microjoules: microjoules, at: now}
	if !exists || microjoules < previous.microjoules || now.Sub(previous.at) <= 0 {
		return nil, false, exists && microjoules < previous.microjoules
	}
	elapsed := now.Sub(previous.at).Seconds()
	power := float64(microjoules-previous.microjoules) / 1_000_000 / elapsed
	return &power, true, false
}

func (a *Adapter) resolveRoot() (string, error) {
	return filepath.EvalSymlinks(a.root)
}

func gpuIdentity(hostID, uuid, pciSlot, vendor, evidence string, ordinal int, seenIDs map[string]struct{}) (string, bool, string) {
	stable := true
	source := "nvidia-uuid"
	key := "uuid:" + strings.ToLower(strings.TrimSpace(uuid))
	if strings.TrimSpace(uuid) == "" {
		if strings.TrimSpace(pciSlot) != "" {
			key = "pci:" + canonicalPCI(pciSlot)
			source = "pci-slot"
		} else if strings.TrimSpace(evidence) != "" {
			key = "evidence:" + evidence + "\x00" + strconv.Itoa(ordinal)
			stable = false
			source = "device-evidence"
		} else {
			key = "ordinal:" + strconv.Itoa(ordinal)
			stable = false
			source = "ambiguous-ordinal"
		}
	}
	base := "gpu\x00" + bounded(hostID, maxHostIDBytes) + "\x00" + strings.ToLower(strings.TrimSpace(vendor)) + "\x00" + key
	sum := sha256.Sum256([]byte(base))
	id := "gpu-" + hex.EncodeToString(sum[:])
	if _, exists := seenIDs[id]; exists {
		stable = false
		source = "ambiguous-identity"
		sum = sha256.Sum256([]byte(base + "\x00duplicate\x00" + strconv.Itoa(ordinal)))
		id = "gpu-" + hex.EncodeToString(sum[:])
	}
	return id, stable, source
}

func readSysfsNumber(ctx context.Context, root, directory string, names []string, validator func(float64) bool, diagnostics []string) (*float64, []string) {
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, append(diagnostics, "GPU sysfs collection deadline exceeded")
		}
		raw, present, err := readOptionalAttribute(root, directory, name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, append(diagnostics, "GPU sysfs field unavailable")
		}
		if !present {
			continue
		}
		value, parseErr := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if parseErr != nil || !finiteNumber(value) || (validator != nil && !validator(value)) {
			return nil, append(diagnostics, "GPU sysfs field malformed")
		}
		return &value, diagnostics
	}
	return nil, diagnostics
}

func readOptionalAttribute(root, directory, name string) (string, bool, error) {
	value, err := readBoundedWithin(root, directory, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", true, err
	}
	return strings.TrimSpace(value), true, nil
}

func readBoundedWithin(root, directory, name string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", ErrUnsafePath
	}
	path := filepath.Join(directory, name)
	resolved, err := resolveWithin(root, path)
	if err != nil {
		return "", err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxReadBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxReadBytes {
		return "", ErrReadLimit
	}
	return string(data), nil
}

func resolveWithin(root, path string) (string, error) {
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	pathResolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootResolved, pathResolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrUnsafePath
	}
	return pathResolved, nil
}

func readDirectory(path string, limit int) ([]os.DirEntry, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	entries, err := file.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	truncated := len(entries) > limit
	if truncated {
		entries = entries[:limit]
	}
	return entries, truncated, nil
}

func classifyPathError(err error) error {
	if err == nil {
		return ErrUnavailable
	}
	if errors.Is(err, ErrUnsafePath) || errors.Is(err, ErrReadLimit) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, fs.ErrPermission) {
		return ErrAccessDenied
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, safeError(err))
}

func parseCardName(name string) (int, bool) {
	if !strings.HasPrefix(name, "card") {
		return 0, false
	}
	rest := strings.TrimPrefix(name, "card")
	if rest == "" || strings.Contains(rest, "-") {
		return 0, false
	}
	index, err := strconv.Atoi(rest)
	return index, err == nil && index >= 0 && index < maxDirectoryEntries
}

func parseUeventValue(raw, key string) string {
	for _, line := range strings.Split(raw, "\n") {
		name, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(name) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func resolveDriver(root, devicePath string) string {
	resolved, err := resolveWithin(root, filepath.Join(devicePath, "driver"))
	if err != nil {
		return ""
	}
	return sanitizeText(filepath.Base(resolved), 64)
}

func firstAvailableText(root, directory string, names ...string) string {
	for _, name := range names {
		value, present, err := readOptionalAttribute(root, directory, name)
		if err == nil && present {
			if value = sanitizeText(value, 128); value != "" {
				return value
			}
		}
	}
	return ""
}

func parseNvidiaNumber(raw string, scale float64, validator func(float64) bool) (*float64, error) {
	raw = normalizedText(raw)
	if raw == "" || strings.EqualFold(raw, "N/A") {
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || !finiteNumber(value) || (validator != nil && !validator(value)) {
		return nil, errors.New("invalid NVIDIA numeric field")
	}
	value *= scale
	return &value, nil
}

func percentOK(value float64) bool { return value >= 0 && value <= 100 }

func nonNegative(value float64) bool { return value >= 0 }

func finiteNumber(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func scaleValue(value *float64, scale float64) *float64 {
	if value == nil {
		return nil
	}
	scaled := *value * scale
	return &scaled
}

func availability(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return "current"
}

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func normalizedText(value string) string {
	value = strings.TrimSpace(value)
	if value == "[N/A]" || value == "N/A" || value == "-" {
		return ""
	}
	return value
}

func normalizeVendor(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.TrimPrefix(value, "0x")
}

func canonicalPCI(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func vendorDisplayName(vendor string) string {
	switch vendor {
	case vendorAMD:
		return "AMD"
	case vendorIntel:
		return "Intel"
	default:
		return "GPU"
	}
}

func driverCapability(vendor, driver string) string {
	driver = strings.ToLower(strings.TrimSpace(driver))
	if vendor == vendorAMD && driver == "amdgpu" || vendor == vendorIntel && (driver == "i915" || driver == "xe") {
		return "supported"
	}
	if driver == "" {
		return "unavailable"
	}
	return "unsupported"
}

func isHwmonName(name string) bool {
	if !strings.HasPrefix(name, "hwmon") {
		return false
	}
	value := strings.TrimPrefix(name, "hwmon")
	index, err := strconv.Atoi(value)
	return value != "" && err == nil && index >= 0 && index < maxHwmonEntries
}

func allEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func cloneLabels(labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}

func joinDiagnostics(parts []string) string {
	message := strings.Join(parts, "; ")
	if len(message) > maxDiagnosticBytes {
		return message[:maxDiagnosticBytes]
	}
	return message
}

func sanitizeText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		value = value[:limit]
	}
	var builder strings.Builder
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			builder.WriteByte(' ')
			continue
		}
		builder.WriteRune(character)
	}
	return strings.TrimSpace(builder.String())
}

func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := sanitizeText(err.Error(), 128)
	if message == "" {
		return "unknown error"
	}
	return message
}

func (a *Adapter) currentTime() time.Time {
	if a.now == nil {
		return time.Now().UTC()
	}
	return a.now().UTC()
}

func (a *Adapter) Close() error { return nil }

type processRunner struct {
	path string
}

func (r processRunner) Run(ctx context.Context, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, r.path, args...)
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
	return result, fmt.Errorf("%w: nvidia-smi executable unavailable", ErrUnavailable)
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
