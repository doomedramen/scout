package sensors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"scout.local/scout/internal/collector"
)

const (
	CollectorID              = "sensors"
	Provider                 = "sensors"
	maxSensors               = 256
	maxDirectoryEntries      = 512
	maxReadBytes             = 4096
	maxDiagnosticBytes       = 1024
	maxHostIDBytes           = 128
	maxExclusionIDBytes      = 128
	collectionInterval       = 30 * time.Second
	collectionDeadline       = 5 * time.Second
	defaultSysfsRoot         = "/sys"
	sensorKindTemperature    = "temperature"
	sensorKindFan            = "fan"
	temperatureMetric        = "sensor.temperature"
	fanSpeedMetric           = "sensor.fan_speed"
	temperatureUnit          = "celsius"
	fanSpeedUnit             = "rpm"
	identitySourceChannel    = "hwmon-entry+channel"
	identitySourceDevicePath = "device-path+channel"
	identitySourceDeviceChip = "device-path+chip+channel"
	identitySourceAlarm      = "hwmon-alarm"
)

var (
	ErrUnavailable  = errors.New("hwmon sensor collector unavailable")
	ErrAccessDenied = errors.New("hwmon sensor access denied")
	ErrUnsafePath   = errors.New("hwmon path escapes configured sysfs root")
	ErrReadLimit    = errors.New("hwmon attribute exceeds read limit")
)

// Config contains non-secret, bounded hwmon settings. SysfsRoot is primarily
// useful for a host mount or fixture; production uses /sys.
type Config struct {
	HostID      string
	SysfsRoot   string
	ExcludedIDs []string
}

// Adapter reads the fixed Linux hwmon ABI without invoking utilities or
// following paths outside the configured sysfs root.
type Adapter struct {
	root     string
	hostID   string
	excluded map[string]struct{}
	now      func() time.Time
}

// New constructs an adapter using the host's /sys mount.
func New(sysfsRoot string) (*Adapter, error) {
	return NewWithConfig(Config{SysfsRoot: sysfsRoot})
}

// NewWithConfig constructs an adapter with fixture-safe root and exclusions.
func NewWithConfig(config Config) (*Adapter, error) {
	root := strings.TrimSpace(config.SysfsRoot)
	if root == "" {
		root = defaultSysfsRoot
	}
	if !filepath.IsAbs(root) || len(root) > 4096 {
		return nil, errors.New("hwmon sysfs root must be absolute and bounded")
	}
	hostID := strings.TrimSpace(config.HostID)
	if hostID == "" {
		hostID = "host"
	}
	if len(hostID) > maxHostIDBytes {
		return nil, errors.New("hwmon host identity is out of bounds")
	}
	excluded := make(map[string]struct{}, len(config.ExcludedIDs))
	if len(config.ExcludedIDs) > maxSensors {
		return nil, fmt.Errorf("hwmon exclusions exceed %d entries", maxSensors)
	}
	for _, id := range config.ExcludedIDs {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > maxExclusionIDBytes {
			return nil, errors.New("hwmon exclusion ID must be non-empty and bounded")
		}
		excluded[id] = struct{}{}
	}
	return &Adapter{root: filepath.Clean(root), hostID: hostID, excluded: excluded, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Descriptor describes the bounded read-only hwmon collector.
func Descriptor() collector.Descriptor {
	return collector.Descriptor{
		ID:                 CollectorID,
		Provider:           Provider,
		Version:            "hwmon-sysfs-v1",
		RequiredPermission: []string{"sysfs:hwmon-read"},
		ConfigSchema: map[string]string{
			"excludedIds": "comma-separated stable sensor IDs; exact match; max 256",
		},
		EntityLimit: maxSensors,
		Interval:    collectionInterval,
		Deadline:    collectionDeadline,
	}
}

// Register adds the adapter to the shared collector registry.
func Register(registry *collector.Registry, adapter *Adapter) error {
	if registry == nil || adapter == nil {
		return errors.New("hwmon registry and adapter are required")
	}
	return registry.Register(Descriptor(), adapter)
}

// Detect verifies that the hwmon class is readable. An empty class is valid:
// the host may simply expose no temperature or fan channels.
func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	if a == nil {
		return false, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	root, err := a.resolveRoot()
	if err != nil {
		return false, classifyPathError(err)
	}
	hwmonRoot, err := resolveWithin(root, filepath.Join(root, "class", "hwmon"))
	if err != nil {
		return false, classifyPathError(err)
	}
	file, err := os.Open(hwmonRoot)
	if err != nil {
		return false, classifyPathError(err)
	}
	defer file.Close()
	_, err = file.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, classifyPathError(err)
	}
	return true, nil
}

// Collect returns one entity per exposed temp/fan input file. Missing optional
// attributes remain unavailable; no value is invented from a threshold or an
// absent alarm file.
func (a *Adapter) Collect(ctx context.Context) (collector.ServiceResult, error) {
	if a == nil {
		return collector.ServiceResult{}, ErrUnavailable
	}
	collectCtx, cancel := context.WithTimeout(ctx, collectionDeadline)
	defer cancel()
	root, err := a.resolveRoot()
	if err != nil {
		return collector.ServiceResult{}, classifyPathError(err)
	}
	hwmonRoot, err := resolveWithin(root, filepath.Join(root, "class", "hwmon"))
	if err != nil {
		return collector.ServiceResult{}, classifyPathError(err)
	}
	entries, truncated, err := readDirectory(hwmonRoot, maxDirectoryEntries)
	if err != nil {
		return collector.ServiceResult{}, classifyPathError(err)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })

	now := a.currentTime()
	result := collector.ServiceResult{Entities: []collector.Entity{}, Metrics: []collector.Metric{}}
	diagnostics := []string{}
	if truncated {
		result.Partial = true
		diagnostics = append(diagnostics, fmt.Sprintf("hwmon device inventory truncated at %d entries", maxDirectoryEntries))
	}
	seenIDs := make(map[string]struct{})
	for _, entry := range entries {
		if err := collectCtx.Err(); err != nil {
			result.Partial = true
			diagnostics = append(diagnostics, "hwmon collection deadline exceeded")
			break
		}
		entryPath := filepath.Join(hwmonRoot, entry.Name())
		resolvedDevicePath, resolveErr := resolveWithin(root, entryPath)
		if resolveErr != nil {
			result.Partial = true
			diagnostics = append(diagnostics, "hwmon device path rejected")
			continue
		}
		info, statErr := os.Stat(resolvedDevicePath)
		if statErr != nil {
			result.Partial = true
			diagnostics = append(diagnostics, "hwmon device disappeared during collection")
			continue
		}
		if !info.IsDir() {
			continue
		}
		deviceResult := a.collectDevice(collectCtx, root, entry, entryPath, resolvedDevicePath, now, maxSensors-len(result.Entities), seenIDs)
		result.Entities = append(result.Entities, deviceResult.entities...)
		result.Metrics = append(result.Metrics, deviceResult.metrics...)
		if deviceResult.partial {
			result.Partial = true
		}
		diagnostics = append(diagnostics, deviceResult.diagnostics...)
		if len(result.Entities) >= maxSensors {
			if hasMoreDeviceEntries(entries, entry.Name()) || truncated {
				result.Partial = true
				diagnostics = append(diagnostics, fmt.Sprintf("hwmon sensor inventory truncated at %d sensors", maxSensors))
			}
			break
		}
	}
	result.Diagnostic = joinDiagnostics(diagnostics)
	return result, nil
}

func (a *Adapter) collectDevice(ctx context.Context, root string, entry os.DirEntry, entryPath, resolvedDevicePath string, now time.Time, limit int, seenIDs map[string]struct{}) deviceResult {
	result := deviceResult{}
	files, truncated, err := readDirectory(resolvedDevicePath, maxDirectoryEntries)
	if err != nil {
		result.partial = true
		result.diagnostics = append(result.diagnostics, "hwmon device files unavailable")
		return result
	}
	if truncated {
		result.partial = true
		result.diagnostics = append(result.diagnostics, "hwmon device file inventory truncated")
	}
	channels := make([]channelRef, 0, len(files))
	for _, file := range files {
		kind, index, ok := parseChannelFile(file.Name())
		if !ok {
			continue
		}
		channels = append(channels, channelRef{kind: kind, index: index, inputName: file.Name()})
	}
	sort.Slice(channels, func(left, right int) bool {
		if channels[left].kind != channels[right].kind {
			return channels[left].kind < channels[right].kind
		}
		return channels[left].index < channels[right].index
	})
	if len(channels) == 0 {
		return result
	}

	chip, chipPresent, chipErr := readOptionalAttribute(root, resolvedDevicePath, "name")
	if chipErr != nil {
		result.partial = true
		result.diagnostics = append(result.diagnostics, "hwmon chip name unavailable")
	}
	chip = sanitizeText(chip, 128)
	identityStable, identitySource := identityMetadata(entry, entryPath, resolvedDevicePath, chip, chipPresent && chip != "")
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			result.partial = true
			result.diagnostics = append(result.diagnostics, "hwmon collection deadline exceeded")
			break
		}
		if len(result.entities) >= limit {
			result.partial = true
			result.diagnostics = append(result.diagnostics, fmt.Sprintf("hwmon sensor inventory truncated at %d sensors", maxSensors))
			break
		}
		id := stableEntityID(a.hostID, resolvedDevicePath, chip, channel.kind, channel.index)
		if _, excluded := a.excluded[id]; excluded {
			continue
		}
		if _, duplicate := seenIDs[id]; duplicate {
			result.partial = true
			result.diagnostics = append(result.diagnostics, "duplicate hwmon sensor identity suppressed")
			continue
		}
		seenIDs[id] = struct{}{}

		inputValue, inputErr := readBoundedWithin(root, resolvedDevicePath, channel.inputName)
		parsedValue, parseErr := parseSensorValue(channel.kind, inputValue)
		inputCurrent := inputErr == nil && parseErr == nil
		if inputErr != nil || parseErr != nil {
			result.partial = true
			result.diagnostics = append(result.diagnostics, sensorDiagnostic(channel, "input unavailable"))
		}

		label, labelPresent, labelErr := readOptionalAttribute(root, resolvedDevicePath, channel.attributeName("label"))
		if labelErr != nil && labelPresent {
			result.partial = true
			result.diagnostics = append(result.diagnostics, sensorDiagnostic(channel, "label unavailable"))
		}
		label = sanitizeText(label, 128)
		if label == "" {
			label = fmt.Sprintf("%s %d", channel.kind, channel.index)
		}

		fault, faultAvailability, alarm, alarmErr := readAlarm(root, resolvedDevicePath, channel)
		if alarmErr != nil {
			result.partial = true
			result.diagnostics = append(result.diagnostics, sensorDiagnostic(channel, "fault alarm unavailable"))
		}

		labels := map[string]string{
			"sensorType":     channel.kind,
			"channel":        channel.baseName(),
			"devicePath":     resolvedDevicePath,
			"sourcePath":     filepath.Join(resolvedDevicePath, channel.inputName),
			"identityStable": strconv.FormatBool(identityStable),
			"identitySource": identitySource,
			"capability." + channel.metricCapability(): availabilityForInput(inputCurrent),
			"capability.fault":                         faultAvailability,
		}
		if chip != "" {
			labels["chip"] = chip
		}
		if alarm != "" {
			labels["alarm"] = alarm
		}
		if fault != nil {
			labels["hardwareFault"] = strconv.FormatBool(*fault)
			labels["faultSource"] = identitySourceAlarm
		}

		status := "online"
		if fault != nil && *fault {
			status = "fault"
		} else if !inputCurrent {
			status = "unavailable"
		}
		result.entities = append(result.entities, collector.Entity{ID: id, Kind: "sensor", Name: label, Status: status, Labels: labels, Observed: now, Expires: now.Add(3 * collectionInterval)})
		result.metrics = append(result.metrics, collector.Metric{
			EntityID:     id,
			Metric:       channel.metricName(),
			Value:        parsedValue,
			Availability: availabilityForInput(inputCurrent),
			Unit:         channel.unit(),
			ObservedAt:   now,
			Labels: map[string]string{
				"sensorType":     channel.kind,
				"channel":        channel.baseName(),
				"identityStable": strconv.FormatBool(identityStable),
			},
		})
	}
	return result
}

type deviceResult struct {
	entities    []collector.Entity
	metrics     []collector.Metric
	partial     bool
	diagnostics []string
}

type channelRef struct {
	kind      string
	index     int
	inputName string
}

func (c channelRef) baseName() string {
	prefix := "temp"
	if c.kind == sensorKindFan {
		prefix = "fan"
	}
	return prefix + strconv.Itoa(c.index)
}

func (c channelRef) attributeName(attribute string) string {
	return c.baseName() + "_" + attribute
}

func (c channelRef) metricName() string {
	if c.kind == sensorKindFan {
		return fanSpeedMetric
	}
	return temperatureMetric
}

func (c channelRef) metricCapability() string {
	if c.kind == sensorKindFan {
		return "fan_speed"
	}
	return "temperature"
}

func (c channelRef) unit() string {
	if c.kind == sensorKindFan {
		return fanSpeedUnit
	}
	return temperatureUnit
}

func parseChannelFile(name string) (string, int, bool) {
	kind := ""
	remaining := ""
	switch {
	case strings.HasPrefix(name, "temp"):
		kind, remaining = sensorKindTemperature, strings.TrimPrefix(name, "temp")
	case strings.HasPrefix(name, "fan"):
		kind, remaining = sensorKindFan, strings.TrimPrefix(name, "fan")
	default:
		return "", 0, false
	}
	if !strings.HasSuffix(remaining, "_input") {
		return "", 0, false
	}
	remaining = strings.TrimSuffix(remaining, "_input")
	if remaining == "" {
		return "", 0, false
	}
	index, err := strconv.Atoi(remaining)
	if err != nil || index < 1 || index > maxSensors {
		return "", 0, false
	}
	return kind, index, true
}

func parseSensorValue(kind, raw string) (*float64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return nil, err
	}
	if kind == sensorKindFan && value < 0 {
		return nil, errors.New("fan speed cannot be negative")
	}
	parsed := float64(value)
	if kind == sensorKindTemperature {
		parsed /= 1000
	}
	return &parsed, nil
}

func readAlarm(root, devicePath string, channel channelRef) (*bool, string, string, error) {
	raw, present, err := readOptionalAttribute(root, devicePath, channel.attributeName("alarm"))
	if err != nil {
		return nil, "unavailable", "", err
	}
	if !present {
		return nil, "unavailable", "", nil
	}
	value, parseErr := strconv.Atoi(strings.TrimSpace(raw))
	if parseErr != nil || (value != 0 && value != 1) {
		return nil, "unavailable", "", errors.New("invalid hwmon alarm")
	}
	fault := value == 1
	return &fault, "current", strconv.Itoa(value), nil
}

func identityMetadata(entry os.DirEntry, entryPath, resolvedDevicePath, chip string, chipAvailable bool) (bool, string) {
	if chipAvailable {
		return true, identitySourceDeviceChip
	}
	if entry.Type()&os.ModeSymlink != 0 || filepath.Clean(entryPath) != filepath.Clean(resolvedDevicePath) {
		return true, identitySourceDevicePath
	}
	return false, identitySourceChannel
}

func stableEntityID(hostID, devicePath, chip, kind string, index int) string {
	key := strings.Join([]string{"sensor", bounded(hostID, maxHostIDBytes), devicePath, chip, kind, strconv.Itoa(index)}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return "sensor-" + hex.EncodeToString(sum[:])
}

func (a *Adapter) resolveRoot() (string, error) {
	return filepath.EvalSymlinks(a.root)
}

func readDirectory(path string, limit int) ([]os.DirEntry, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("invalid directory limit")
	}
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

func readOptionalAttribute(root, devicePath, name string) (string, bool, error) {
	value, err := readBoundedWithin(root, devicePath, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", true, err
	}
	return strings.TrimSpace(value), true, nil
}

func readBoundedWithin(root, devicePath, name string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", ErrUnsafePath
	}
	path := filepath.Join(devicePath, name)
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

func classifyPathError(err error) error {
	if err == nil {
		return ErrUnavailable
	}
	if errors.Is(err, ErrUnsafePath) || errors.Is(err, ErrReadLimit) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, fs.ErrPermission) {
		return ErrAccessDenied
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, safeError(err))
}

func availabilityForInput(current bool) string {
	if current {
		return "current"
	}
	return "unavailable"
}

func hasMoreDeviceEntries(entries []os.DirEntry, current string) bool {
	for _, entry := range entries {
		if entry.Name() > current {
			return true
		}
	}
	return false
}

func sensorDiagnostic(channel channelRef, message string) string {
	return channel.baseName() + ": " + message
}

func joinDiagnostics(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
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
