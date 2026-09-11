package systemd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/coreos/go-systemd/v22/dbus"
	"scout.local/scout/internal/collector"
)

const (
	CollectorID            = "systemd"
	Provider               = "systemd"
	maxSystemdUnits        = 1000
	maxExpectedRunPatterns = 100
	maxPatternBytes        = 128
	unitInventoryInterval  = 30 * time.Second
	unitInventoryDeadline  = 5 * time.Second
)

var (
	ErrUnavailable  = errors.New("systemd service inventory unavailable")
	ErrAccessDenied = errors.New("systemd service inventory access denied")
)

// Config contains only non-secret, bounded systemd collector settings.
type Config struct {
	ExpectedRunning []string
}

// unitLister is the read-only systemd API used by the adapter. Keeping the
// narrow interface here makes fixture tests independent of a host D-Bus.
type unitLister interface {
	ListUnitsByPatternsContext(context.Context, []string, []string) ([]dbus.UnitStatus, error)
}

type unitConnection interface {
	unitLister
	Close()
}

// Adapter discovers loaded .service units from the system D-Bus. It never
// starts, stops, reloads, or otherwise mutates a unit.
type Adapter struct {
	connection      unitLister
	closeConnection func() error
	expectedRunning []string
	now             func() time.Time
}

// New opens the host system D-Bus and returns a read-only systemd adapter.
func New(ctx context.Context, config Config) (*Adapter, error) {
	if err := ValidateExpectedRunning(config.ExpectedRunning); err != nil {
		return nil, err
	}
	connection, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", classifyBusError(err), safeDiagnostic(err))
	}
	return newAdapter(connection, config), nil
}

// NewWithConnection constructs an adapter around a caller-owned test or
// already-established connection. Production callers should use New.
func NewWithConnection(connection unitConnection, config Config) (*Adapter, error) {
	if connection == nil {
		return nil, ErrUnavailable
	}
	if err := ValidateExpectedRunning(config.ExpectedRunning); err != nil {
		return nil, err
	}
	return newAdapter(connection, config), nil
}

func newAdapter(connection unitConnection, config Config) *Adapter {
	return &Adapter{
		connection: connection,
		closeConnection: func() error {
			connection.Close()
			return nil
		},
		expectedRunning: append([]string(nil), config.ExpectedRunning...),
		now:             func() time.Time { return time.Now().UTC() },
	}
}

// Descriptor describes the collector to the shared 001 registry.
func Descriptor() collector.Descriptor {
	return collector.Descriptor{
		ID:                 CollectorID,
		Provider:           Provider,
		Version:            "systemd-v1",
		RequiredPermission: []string{"systemd:read"},
		ConfigSchema: map[string]string{
			"expectedRunning": "comma-separated service globs; * and ? only; max 100 patterns",
		},
		EntityLimit: maxSystemdUnits,
		Interval:    unitInventoryInterval,
		Deadline:    unitInventoryDeadline,
	}
}

// Register adds the adapter to the shared collector registry.
func Register(registry *collector.Registry, adapter *Adapter) error {
	if registry == nil || adapter == nil {
		return errors.New("systemd registry and adapter are required")
	}
	return registry.Register(Descriptor(), adapter)
}

// ValidateExpectedRunning enforces the contract's glob-only selector syntax.
func ValidateExpectedRunning(patterns []string) error {
	if len(patterns) > maxExpectedRunPatterns {
		return fmt.Errorf("expected-running patterns exceed %d", maxExpectedRunPatterns)
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || len(pattern) > maxPatternBytes {
			return fmt.Errorf("expected-running pattern must be 1-%d bytes", maxPatternBytes)
		}
		for _, character := range pattern {
			if character == '*' || character == '?' || isAllowedUnitPatternCharacter(character) {
				continue
			}
			return fmt.Errorf("expected-running pattern contains unsupported syntax")
		}
	}
	return nil
}

func isAllowedUnitPatternCharacter(character rune) bool {
	if unicode.IsLetter(character) || unicode.IsDigit(character) {
		return true
	}
	switch character {
	case '.', '-', '_', '@', ':', '%':
		return true
	default:
		return false
	}
}

// Detect verifies that the systemd manager can be queried within the caller's
// context. The empty result is still a valid detected systemd installation.
func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	if a == nil || a.connection == nil {
		return false, ErrUnavailable
	}
	_, err := a.listUnits(ctx)
	if err != nil {
		return false, err
	}
	return true, nil
}

// Collect returns loaded service units with bounded freshness and state
// labels. A full systemd result is never inferred from a truncated response.
func (a *Adapter) Collect(ctx context.Context) (collector.ServiceResult, error) {
	if a == nil || a.connection == nil {
		return collector.ServiceResult{}, ErrUnavailable
	}
	units, err := a.listUnits(ctx)
	if err != nil {
		return collector.ServiceResult{}, err
	}
	now := time.Now().UTC()
	if a.now != nil {
		now = a.now().UTC()
	}
	sort.SliceStable(units, func(left, right int) bool { return units[left].Name < units[right].Name })
	partial := len(units) > maxSystemdUnits
	if partial {
		units = units[:maxSystemdUnits]
	}
	entities := make([]collector.Entity, 0, len(units))
	for _, unit := range units {
		if unit.LoadState != "loaded" || !strings.HasSuffix(unit.Name, ".service") {
			continue
		}
		if strings.TrimSpace(unit.Name) == "" {
			partial = true
			continue
		}
		status := normalizeActiveState(unit.ActiveState)
		labels := map[string]string{
			"unit":        sanitizeUnitValue(unit.Name, maxPatternBytes),
			"loadState":   "loaded",
			"activeState": sanitizeUnitValue(unit.ActiveState, 64),
			"subState":    sanitizeUnitValue(unit.SubState, 64),
		}
		if description := sanitizeUnitValue(unit.Description, 256); description != "" {
			labels["description"] = description
		}
		if matchesAnyGlob(a.expectedRunning, unit.Name) {
			labels["mustRun"] = "true"
		}
		entities = append(entities, collector.Entity{
			ID:       unit.Name,
			Kind:     "service",
			Name:     unit.Name,
			Status:   status,
			Labels:   labels,
			Observed: now,
			Expires:  now.Add(3 * unitInventoryInterval),
		})
	}
	result := collector.ServiceResult{Entities: entities, Partial: partial}
	if partial {
		result.Diagnostic = fmt.Sprintf("systemd service inventory truncated at %d loaded units", maxSystemdUnits)
	}
	return result, nil
}

func (a *Adapter) listUnits(ctx context.Context) ([]dbus.UnitStatus, error) {
	units, err := a.connection.ListUnitsByPatternsContext(ctx, []string{}, []string{"*.service"})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", classifyBusError(err), safeDiagnostic(err))
	}
	return units, nil
}

func classifyBusError(err error) error {
	if err == nil {
		return ErrUnavailable
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "permission") || strings.Contains(message, "access denied") || strings.Contains(message, "not authorized") {
		return ErrAccessDenied
	}
	return ErrUnavailable
}

func normalizeActiveState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "active":
		return "active"
	case "inactive":
		return "inactive"
	case "failed":
		return "failed"
	case "activating", "deactivating", "reloading":
		return "transitional"
	default:
		return "unavailable"
	}
}

func sanitizeUnitValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit < 1 {
		return ""
	}
	result := strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func matchesAnyGlob(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if globMatch(strings.TrimSpace(pattern), value) {
			return true
		}
	}
	return false
}

// globMatch implements only the contract's two wildcard characters. It does
// not interpret character classes, alternation, regex, or filesystem paths.
func globMatch(pattern, value string) bool {
	patternRunes := []rune(pattern)
	valueRunes := []rune(value)
	previous := make([]bool, len(valueRunes)+1)
	previous[0] = true
	for _, patternRune := range patternRunes {
		current := make([]bool, len(valueRunes)+1)
		switch patternRune {
		case '*':
			current[0] = previous[0]
			for index := 1; index <= len(valueRunes); index++ {
				current[index] = previous[index] || current[index-1]
			}
		case '?':
			for index := 1; index <= len(valueRunes); index++ {
				current[index] = previous[index-1]
			}
		default:
			for index := 1; index <= len(valueRunes); index++ {
				current[index] = previous[index-1] && patternRune == valueRunes[index-1]
			}
		}
		previous = current
	}
	return previous[len(valueRunes)]
}

func (a *Adapter) Close() error {
	if a == nil || a.closeConnection == nil {
		return nil
	}
	return a.closeConnection()
}

func safeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, strings.TrimSpace(err.Error()))
	if len(message) > 256 {
		return message[:256]
	}
	return message
}
