package smart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"scout.local/scout/internal/collector"
)

const (
	CollectorID          = "smart"
	Provider             = "smart"
	maxDevices           = 64
	maxOutputBytes       = 1 << 20
	collectionInterval   = 5 * time.Minute
	collectionDeadline   = 10 * time.Second
	totalCollectionLimit = 60 * time.Second
)

var (
	ErrUnavailable  = errors.New("SMART collector unavailable")
	ErrAccessDenied = errors.New("SMART device permission denied")
	ErrStandby      = errors.New("SMART device is in standby")
)

// Target is a device discovered by smartctl --scan. It is intentionally a
// small allowlist rather than a bag of smartctl arguments.
type Target struct {
	Path string
	Type string
}

// Config contains non-secret SMART collector settings. Targets are primarily
// useful for tests and a caller that has already completed the fixed scan;
// normal construction leaves them empty and scans on collection.
type Config struct {
	HostID  string
	Targets []Target
}

// CommandResult retains bounded command output and the process exit bitmask.
// smartctl uses exit bits to report health and access conditions even when its
// JSON payload contains useful readings.
type CommandResult struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	Truncated bool
}

// Runner is deliberately narrower than os/exec. It makes fixture tests
// independent of smartmontools while production still invokes it directly,
// without a shell or owner-supplied arguments.
type Runner interface {
	Run(context.Context, ...string) (CommandResult, error)
}

// Adapter reads SMART data with fixed, read-only, non-waking commands.
type Adapter struct {
	mu      sync.Mutex
	runner  Runner
	hostID  string
	targets []Target
	now     func() time.Time
}

// New uses the packaged smartctl path. The executable is checked by Detect or
// Collect so an absent optional utility becomes a visible capability state.
func New(smartctlPath string) (*Adapter, error) {
	if smartctlPath == "" {
		smartctlPath = "/usr/sbin/smartctl"
	}
	if !filepath.IsAbs(smartctlPath) {
		return nil, errors.New("smartctl path must be absolute")
	}
	return NewWithRunner(processRunner{path: smartctlPath}, Config{})
}

// NewWithRunner constructs an adapter around a fixed command runner.
func NewWithRunner(runner Runner, config Config) (*Adapter, error) {
	if runner == nil {
		return nil, ErrUnavailable
	}
	hostID := strings.TrimSpace(config.HostID)
	if hostID == "" {
		hostID = "host"
	}
	if len(hostID) > 128 || len(config.Targets) > maxDevices {
		return nil, errors.New("SMART collector configuration is out of bounds")
	}
	targets := make([]Target, 0, len(config.Targets))
	seen := map[string]struct{}{}
	for _, target := range config.Targets {
		target.Path = filepath.Clean(strings.TrimSpace(target.Path))
		target.Type = normalizeDeviceType(target.Type)
		if err := validateTarget(target); err != nil {
			return nil, err
		}
		key := target.Type + "\x00" + target.Path
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	return &Adapter{runner: runner, hostID: hostID, targets: targets, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Descriptor describes the fixed SMART collector to the shared registry.
func Descriptor() collector.Descriptor {
	return collector.Descriptor{
		ID:                 CollectorID,
		Provider:           Provider,
		Version:            "smartctl-json-v1",
		RequiredPermission: []string{"smartctl:read", "block-device:read"},
		ConfigSchema: map[string]string{
			"standbyPolicy": "never wake; smartctl -n standby,0",
		},
		EntityLimit: maxDevices,
		Interval:    collectionInterval,
		Deadline:    totalCollectionLimit,
	}
}

// Register adds the adapter to the common registry.
func Register(registry *collector.Registry, adapter *Adapter) error {
	if registry == nil || adapter == nil {
		return errors.New("SMART registry and adapter are required")
	}
	return registry.Register(Descriptor(), adapter)
}

// Detect verifies that the fixed scan can be executed. An empty valid scan is
// still detection success: a host without SMART-visible disks is not a failed
// smartmontools installation.
func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	if a == nil || a.runner == nil {
		return false, ErrUnavailable
	}
	if len(a.copyTargets()) > 0 {
		return true, nil
	}
	_, err := a.scan(ctx)
	if err != nil {
		return false, err
	}
	return true, nil
}

// Collect runs a bounded scan and one fixed non-waking read per target. A
// failed optional disk never prevents the remaining disks from being read.
func (a *Adapter) Collect(ctx context.Context) (collector.ServiceResult, error) {
	if a == nil || a.runner == nil {
		return collector.ServiceResult{}, ErrUnavailable
	}
	collectCtx, cancel := context.WithTimeout(ctx, totalCollectionLimit)
	defer cancel()

	targets := a.copyTargets()
	partial := false
	diagnosticParts := []string{}
	if len(targets) == 0 {
		var err error
		targets, err = a.scanWithContext(collectCtx)
		if err != nil {
			return collector.ServiceResult{}, err
		}
	}
	if len(targets) > maxDevices {
		targets = targets[:maxDevices]
		partial = true
		diagnosticParts = append(diagnosticParts, fmt.Sprintf("SMART device inventory truncated at %d devices", maxDevices))
	}

	readings := make([]smartReading, 0, len(targets))
	for _, target := range targets {
		if err := collectCtx.Err(); err != nil {
			partial = true
			diagnosticParts = append(diagnosticParts, "SMART collection deadline exceeded")
			break
		}
		reading := a.readTarget(collectCtx, target)
		if reading.diagnostic != "" {
			partial = true
			diagnosticParts = append(diagnosticParts, target.Path+": "+reading.diagnostic)
		}
		readings = append(readings, reading)
	}

	identityCounts := map[string]int{}
	for _, reading := range readings {
		identityCounts[identityBaseKey(reading.device)]++
	}
	now := a.currentTime()
	result := collector.ServiceResult{Entities: make([]collector.Entity, 0, len(readings)), Metrics: make([]collector.Metric, 0, len(readings)*3), Partial: partial}
	for _, reading := range readings {
		ambiguous := identityCounts[identityBaseKey(reading.device)] > 1
		entityID, stable, source := stableEntityID(a.hostID, reading.device, ambiguous)
		labels := reading.labels()
		labels["identityStable"] = fmt.Sprintf("%t", stable)
		labels["identitySource"] = source
		labels["hardwareFault"] = fmt.Sprintf("%t", reading.hardwareFault)
		status := "online"
		if reading.diagnostic != "" {
			status = "unavailable"
		} else if reading.hardwareFault {
			status = "fault"
		}
		result.Entities = append(result.Entities, collector.Entity{ID: entityID, Kind: "storage", Name: reading.displayName(), Status: status, Labels: labels, Observed: now, Expires: now.Add(3 * collectionInterval)})
		result.Metrics = append(result.Metrics, reading.metrics(entityID, now)...)
	}
	result.Diagnostic = boundedJoin(diagnosticParts, 512)
	return result, nil
}

func (a *Adapter) copyTargets() []Target {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Target(nil), a.targets...)
}

func (a *Adapter) currentTime() time.Time {
	if a.now != nil {
		return a.now().UTC()
	}
	return time.Now().UTC()
}

func (a *Adapter) Close() error { return nil }

func (a *Adapter) scan(ctx context.Context) ([]Target, error) {
	targets, err := a.scanWithContext(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.targets = append([]Target(nil), targets...)
	a.mu.Unlock()
	return targets, nil
}

func (a *Adapter) scanWithContext(ctx context.Context) ([]Target, error) {
	command, err := a.runner.Run(ctx, "--scan", "-j")
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: smartctl scan failed", ErrUnavailable)
	}
	if command.Truncated {
		return nil, fmt.Errorf("%w: smartctl scan exceeded output limit", ErrUnavailable)
	}
	var payload scanResponse
	if err := json.Unmarshal(command.Stdout, &payload); err != nil {
		return nil, fmt.Errorf("%w: smartctl scan response was invalid", ErrUnavailable)
	}
	seen := map[string]struct{}{}
	targets := make([]Target, 0, len(payload.Devices))
	for _, device := range payload.Devices {
		target := Target{Path: filepath.Clean(strings.TrimSpace(device.Name)), Type: normalizeDeviceType(device.Type)}
		if validateTarget(target) != nil {
			continue
		}
		key := target.Type + "\x00" + target.Path
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
		if len(targets) == maxDevices+1 {
			break
		}
	}
	return targets, nil
}

func (a *Adapter) readTarget(ctx context.Context, target Target) smartReading {
	queryCtx, cancel := context.WithTimeout(ctx, collectionDeadline)
	defer cancel()
	command, err := a.runner.Run(queryCtx, "-j", "-n", "standby,0", "-d", target.Type, "--", target.Path)
	reading := smartReading{device: smartDevice{Path: target.Path, Type: target.Type}}
	if err != nil {
		if errors.Is(queryCtx.Err(), context.Canceled) || errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
			reading.diagnostic = "collection timed out"
		} else {
			reading.diagnostic = "smartctl execution unavailable"
		}
		return reading
	}
	if command.Truncated {
		reading.diagnostic = "smartctl output exceeded the collector bound"
		return reading
	}
	parsed, parseErr := parseResponse(command.Stdout, target, command.ExitCode)
	if parseErr != nil {
		reading.diagnostic = classifyDiagnostic(parseErr)
		return reading
	}
	reading = parsed
	message := strings.ToLower(parsed.rawMessage + " " + boundedValue(string(command.Stderr), 512))
	if message != "" {
		if strings.Contains(message, "standby") || strings.Contains(message, "sleep") {
			reading.diagnostic = ErrStandby.Error()
		} else if strings.Contains(message, "permission") || strings.Contains(message, "access denied") || strings.Contains(message, "operation not permitted") {
			reading.diagnostic = ErrAccessDenied.Error()
		}
	}
	if reading.diagnostic == "" && command.ExitCode&exitDeviceOpen != 0 && !reading.hasAnyMetric() {
		reading.diagnostic = ErrAccessDenied.Error()
	}
	if reading.diagnostic == "" && command.ExitCode&exitCommandFailure != 0 && !reading.hasAnyMetric() {
		reading.diagnostic = ErrUnavailable.Error()
	}
	if reading.diagnostic == "" && reading.powerMode == "standby" {
		reading.diagnostic = ErrStandby.Error()
	}
	return reading
}

type scanResponse struct {
	Devices []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"devices"`
}

type smartReading struct {
	device        smartDevice
	temperature   *float64
	wearPercent   *float64
	errorCount    *float64
	hardwareFault bool
	healthPassed  *bool
	powerMode     string
	rawMessage    string
	diagnostic    string
}

type smartDevice struct {
	Path     string
	Type     string
	Protocol string
	Model    string
	Serial   string
	WWN      string
}

type smartResponse struct {
	Device struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Protocol string `json:"protocol"`
	} `json:"device"`
	ModelName    string          `json:"model_name"`
	Vendor       string          `json:"vendor"`
	Product      string          `json:"product"`
	SerialNumber string          `json:"serial_number"`
	WWN          json.RawMessage `json:"wwn"`
	Smartctl     struct {
		Messages []struct {
			String string `json:"string"`
		} `json:"messages"`
	} `json:"smartctl"`
	SmartStatus *struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current *float64 `json:"current"`
	} `json:"temperature"`
	PowerMode json.RawMessage `json:"power_mode"`
	ATA       *struct {
		Table []struct {
			ID    int      `json:"id"`
			Name  string   `json:"name"`
			Value *float64 `json:"value"`
			Raw   struct {
				Value *float64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	SCSI *struct {
		Read   smartErrorCounter `json:"read"`
		Write  smartErrorCounter `json:"write"`
		Verify smartErrorCounter `json:"verify"`
	} `json:"scsi_error_counter_log"`
	NVMe *struct {
		CriticalWarning *float64 `json:"critical_warning"`
		Temperature     *float64 `json:"temperature"`
		PercentageUsed  *float64 `json:"percentage_used"`
		MediaErrors     *float64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
}

type smartErrorCounter struct {
	TotalErrors *float64 `json:"total_errors"`
}

const (
	exitCommandLine    = 1
	exitDeviceOpen     = 2
	exitCommandFailure = 4
	exitHealthFailure  = 8
)

func parseResponse(data []byte, target Target, exitCode int) (smartReading, error) {
	var payload smartResponse
	if err := json.Unmarshal(data, &payload); err != nil {
		return smartReading{}, ErrUnavailable
	}
	device := smartDevice{
		Path:     target.Path,
		Type:     normalizeDeviceType(payload.Device.Type),
		Protocol: strings.TrimSpace(payload.Device.Protocol),
		Model:    boundedValue(payload.ModelName, 256),
		Serial:   boundedValue(payload.SerialNumber, 256),
		WWN:      boundedValue(normalizeWWN(payload.WWN), 256),
	}
	if device.Type == "" {
		device.Type = target.Type
	}
	if payload.Device.Name != "" && filepath.IsAbs(payload.Device.Name) {
		device.Path = filepath.Clean(payload.Device.Name)
	}
	if device.Model == "" {
		device.Model = boundedValue(strings.TrimSpace(strings.TrimSpace(payload.Vendor)+" "+strings.TrimSpace(payload.Product)), 256)
	}
	reading := smartReading{device: device, powerMode: normalizePowerMode(payload.PowerMode), rawMessage: responseMessages(payload.Smartctl.Messages)}
	if payload.SmartStatus != nil {
		reading.healthPassed = payload.SmartStatus.Passed
		if payload.SmartStatus.Passed != nil && !*payload.SmartStatus.Passed {
			reading.hardwareFault = true
		}
	}
	if exitCode&exitHealthFailure != 0 {
		reading.hardwareFault = true
	}
	if payload.NVMe != nil {
		reading.temperature = cloneFloat(payload.NVMe.Temperature)
		reading.wearPercent = cloneFloat(payload.NVMe.PercentageUsed)
		reading.errorCount = cloneFloat(payload.NVMe.MediaErrors)
	}
	if payload.Temperature != nil && payload.Temperature.Current != nil {
		reading.temperature = cloneFloat(payload.Temperature.Current)
	}
	if payload.ATA != nil {
		var errors float64
		hasErrors := false
		for _, attribute := range payload.ATA.Table {
			name := strings.ToLower(attribute.Name)
			if reading.temperature == nil && (attribute.ID == 190 || attribute.ID == 194 || strings.Contains(name, "temperature")) && attribute.Raw.Value != nil {
				reading.temperature = cloneFloat(attribute.Raw.Value)
			}
			if reading.wearPercent == nil && (strings.Contains(name, "percentage_used") || strings.Contains(name, "percent_used")) {
				reading.wearPercent = cloneFloat(attribute.Value)
			}
			if isATAErrorAttribute(attribute.ID, name) && attribute.Raw.Value != nil {
				errors += *attribute.Raw.Value
				hasErrors = true
			}
		}
		if hasErrors {
			reading.errorCount = cloneFloat(&errors)
		}
	}
	if payload.SCSI != nil {
		var errors float64
		hasErrors := false
		for _, counter := range []smartErrorCounter{payload.SCSI.Read, payload.SCSI.Write, payload.SCSI.Verify} {
			if counter.TotalErrors != nil {
				errors += *counter.TotalErrors
				hasErrors = true
			}
		}
		if hasErrors {
			reading.errorCount = cloneFloat(&errors)
		}
	}
	if reading.powerMode == "standby" {
		reading.diagnostic = ErrStandby.Error()
	} else if containsPermission(reading.rawMessage) {
		reading.diagnostic = ErrAccessDenied.Error()
	} else if exitCode&(exitCommandLine|exitDeviceOpen|exitCommandFailure) != 0 && !reading.hasAnyMetric() {
		reading.diagnostic = ErrUnavailable.Error()
	} else if !reading.hasAnyMetric() && !reading.hardwareFault {
		reading.diagnostic = ErrUnavailable.Error()
	}
	return reading, nil
}

func (r smartReading) hasAnyMetric() bool {
	return r.temperature != nil || r.wearPercent != nil || r.errorCount != nil
}

func (r smartReading) labels() map[string]string {
	labels := map[string]string{
		"devicePath": r.device.Path,
		"deviceType": r.device.Type,
	}
	if r.device.Protocol != "" {
		labels["protocol"] = boundedValue(r.device.Protocol, 64)
	}
	if r.device.Model != "" {
		labels["model"] = boundedValue(r.device.Model, 128)
	}
	if r.device.Serial != "" {
		labels["serial"] = boundedValue(r.device.Serial, 128)
	}
	if r.device.WWN != "" {
		labels["wwn"] = boundedValue(r.device.WWN, 128)
	}
	if r.powerMode != "" {
		labels["powerMode"] = r.powerMode
	}
	if r.healthPassed != nil {
		labels["smartPassed"] = fmt.Sprintf("%t", *r.healthPassed)
	}
	if r.temperature != nil {
		labels["capability.temperature"] = "current"
	} else {
		labels["capability.temperature"] = "unavailable"
	}
	if r.wearPercent != nil {
		labels["capability.wear"] = "current"
	} else {
		labels["capability.wear"] = "unavailable"
	}
	if r.errorCount != nil {
		labels["capability.errors"] = "current"
	} else {
		labels["capability.errors"] = "unavailable"
	}
	return labels
}

func (r smartReading) metrics(entityID string, now time.Time) []collector.Metric {
	return []collector.Metric{
		{EntityID: entityID, Metric: "smart.temperature", Value: cloneFloat(r.temperature), Availability: availability(r.temperature, r.diagnostic), Unit: "celsius", ObservedAt: now},
		{EntityID: entityID, Metric: "smart.wear_percent", Value: cloneFloat(r.wearPercent), Availability: availability(r.wearPercent, r.diagnostic), Unit: "percent", ObservedAt: now},
		{EntityID: entityID, Metric: "smart.error_count", Value: cloneFloat(r.errorCount), Availability: availability(r.errorCount, r.diagnostic), Unit: "count", ObservedAt: now},
	}
}

func (r smartReading) displayName() string {
	if r.device.Model != "" {
		return r.device.Model
	}
	return r.device.Path
}

func availability(value *float64, diagnostic string) string {
	if value == nil || diagnostic != "" {
		return "unavailable"
	}
	return "current"
}

func stableEntityID(hostID string, device smartDevice, ambiguous bool) (string, bool, string) {
	hostID = boundedValue(strings.TrimSpace(hostID), 128)
	if hostID == "" {
		hostID = "host"
	}
	key := identityBaseKey(device)
	stable := true
	source := "wwn"
	if ambiguous {
		key = "path:" + filepath.Clean(device.Path)
		stable = false
		source = "ambiguous-path"
	} else if device.WWN == "" {
		switch {
		case device.Serial != "" && device.Model != "":
			source = "serial-model"
		case device.Serial != "":
			source = "serial"
		default:
			stable = false
			source = "path"
		}
	}
	sum := sha256.Sum256([]byte("smart\x00" + hostID + "\x00" + key))
	return "smart-" + hex.EncodeToString(sum[:]), stable, source
}

func identityBaseKey(device smartDevice) string {
	if device.WWN != "" {
		return "wwn:" + strings.ToLower(strings.TrimSpace(device.WWN))
	}
	if device.Serial != "" && device.Model != "" {
		return "serial-model:" + strings.ToLower(strings.TrimSpace(device.Serial)) + "\x00" + strings.ToLower(strings.TrimSpace(device.Model))
	}
	if device.Serial != "" {
		return "serial:" + strings.ToLower(strings.TrimSpace(device.Serial))
	}
	return "path:" + filepath.Clean(device.Path)
}

func validateTarget(target Target) error {
	if target.Path == "" || !filepath.IsAbs(target.Path) || len(target.Path) > 4096 {
		return errors.New("SMART device path must be absolute and bounded")
	}
	switch target.Type {
	case "sat", "scsi", "nvme":
		return nil
	default:
		return errors.New("SMART device type is not allowlisted")
	}
}

func normalizeDeviceType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "ata" {
		return "sat"
	}
	return value
}

func normalizePowerMode(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.ToLower(strings.TrimSpace(value))
	}
	var object struct {
		Value  string `json:"value"`
		String string `json:"string"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return strings.ToLower(strings.TrimSpace(firstNonEmpty(object.Value, object.String)))
	}
	return ""
}

func normalizeWWN(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.ToLower(strings.TrimSpace(value))
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.Trim(strings.TrimSpace(string(fields[key])), `"`)
		if value != "" && value != "null" {
			parts = append(parts, strings.ToLower(key)+"="+strings.ToLower(value))
		}
	}
	return strings.Join(parts, "/")
}

func responseMessages(messages []struct {
	String string `json:"string"`
}) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if value := boundedValue(message.String, 256); value != "" {
			parts = append(parts, value)
		}
	}
	return boundedJoin(parts, 512)
}

func isATAErrorAttribute(id int, name string) bool {
	if id == 5 || id == 187 || id == 196 || id == 198 {
		return true
	}
	return strings.Contains(name, "error") || strings.Contains(name, "reallocated")
}

func containsPermission(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "permission") || strings.Contains(value, "access denied") || strings.Contains(value, "operation not permitted")
}

func classifyDiagnostic(err error) string {
	if errors.Is(err, ErrAccessDenied) {
		return ErrAccessDenied.Error()
	}
	if errors.Is(err, ErrStandby) {
		return ErrStandby.Error()
	}
	return ErrUnavailable.Error()
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

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
	return result, fmt.Errorf("%w: smartctl executable unavailable", ErrUnavailable)
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
