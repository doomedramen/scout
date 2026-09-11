package systemd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	"scout.local/scout/internal/collector"
)

type fixtureUnit struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LoadState   string `json:"loadState"`
	ActiveState string `json:"activeState"`
	SubState    string `json:"subState"`
}

type fakeManager struct {
	units       []dbus.UnitStatus
	err         error
	states      []string
	patterns    []string
	closeCalled bool
}

func (f *fakeManager) ListUnitsByPatternsContext(_ context.Context, states, patterns []string) ([]dbus.UnitStatus, error) {
	f.states = append([]string(nil), states...)
	f.patterns = append([]string(nil), patterns...)
	if f.err != nil {
		return nil, f.err
	}
	return append([]dbus.UnitStatus(nil), f.units...), nil
}

func (f *fakeManager) Close() {
	f.closeCalled = true
}

func TestAdapterTranslatesLoadedServiceStatesAndMustRunSelection(t *testing.T) {
	manager := &fakeManager{units: fixtureStatuses(t)}
	adapter, err := NewWithConnection(manager, Config{ExpectedRunning: []string{"backup.service", "scout-*.service"}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	adapter.now = func() time.Time { return at }

	if detected, err := adapter.Detect(context.Background()); err != nil || !detected {
		t.Fatalf("detect: %v %v", detected, err)
	}
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Partial || result.Diagnostic != "" || len(result.Entities) != 4 {
		t.Fatalf("inventory result = %+v", result)
	}
	if len(manager.states) != 0 || len(manager.patterns) != 1 || manager.patterns[0] != "*.service" {
		t.Fatalf("systemd query was not loaded-service-only: states=%v patterns=%v", manager.states, manager.patterns)
	}

	byID := map[string]collector.Entity{}
	for _, entity := range result.Entities {
		byID[entity.ID] = entity
		if !entity.Observed.Equal(at) || !entity.Expires.Equal(at.Add(90*time.Second)) {
			t.Fatalf("entity freshness = %+v", entity)
		}
	}
	for name, status := range map[string]string{
		"scout-agent.service": "active",
		"backup.service":      "inactive",
		"broken.service":      "failed",
		"starting.service":    "transitional",
	} {
		entity, ok := byID[name]
		if !ok || entity.Kind != "service" || entity.Status != status || entity.Labels["unit"] != name {
			t.Fatalf("translated %s = %+v", name, entity)
		}
	}
	if byID["backup.service"].Labels["mustRun"] != "true" || byID["scout-agent.service"].Labels["mustRun"] != "true" {
		t.Fatalf("must-run labels = backup:%v scout:%v", byID["backup.service"].Labels, byID["scout-agent.service"].Labels)
	}
	if _, ok := byID["not-loaded.service"]; ok {
		t.Fatal("unloaded unit entered loaded inventory")
	}
	if err := adapter.Close(); err != nil || !manager.closeCalled {
		t.Fatalf("close: %v called=%v", err, manager.closeCalled)
	}
}

func TestAdapterMarksPartialInventoryAtBound(t *testing.T) {
	units := make([]dbus.UnitStatus, maxSystemdUnits+1)
	for index := range units {
		units[index] = dbus.UnitStatus{Name: "fixture-" + pad(index) + ".service", LoadState: "loaded", ActiveState: "active", SubState: "running"}
	}
	adapter, err := NewWithConnection(&fakeManager{units: units}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Entities) != maxSystemdUnits || !strings.Contains(result.Diagnostic, "truncated") {
		t.Fatalf("partial result = partial:%v entities:%d diagnostic:%q", result.Partial, len(result.Entities), result.Diagnostic)
	}
}

func TestAdapterReportsDeniedAndUnavailableBusAccess(t *testing.T) {
	denied, err := NewWithConnection(&fakeManager{err: errors.New("org.freedesktop.DBus.Error.AccessDenied: permission denied")}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if detected, err := denied.Detect(context.Background()); detected || !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied detect = %v %v", detected, err)
	}
	if _, err := denied.Collect(context.Background()); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied collect = %v", err)
	}

	unavailable, err := NewWithConnection(&fakeManager{err: errors.New("system bus is not running")}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if detected, err := unavailable.Detect(context.Background()); detected || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable detect = %v %v", detected, err)
	}
}

func TestExpectedRunningPatternsAreBoundedAndGlobOnly(t *testing.T) {
	if err := ValidateExpectedRunning([]string{"scout-*.service", "backup.?.service"}); err != nil {
		t.Fatal(err)
	}
	for name, patterns := range map[string][]string{
		"regex":           {"(scout|backup).service"},
		"character class": {"scout[ab].service"},
		"path expansion":  {"../*.service"},
		"empty":           {"   "},
		"too long":        {strings.Repeat("a", maxPatternBytes+1)},
	} {
		if err := ValidateExpectedRunning(patterns); err == nil {
			t.Fatalf("accepted %s pattern", name)
		}
	}
	tooMany := make([]string, maxExpectedRunPatterns+1)
	for index := range tooMany {
		tooMany[index] = "unit.service"
	}
	if err := ValidateExpectedRunning(tooMany); err == nil {
		t.Fatal("accepted too many expected-running patterns")
	}
	if globMatch("scout-?.service", "scout-a.service") && globMatch("scout-*.service", "scout-agent.service") {
		return
	}
	t.Fatal("glob matching did not support only the contract wildcards")
}

func TestAdapterRegistersThroughSharedRegistry(t *testing.T) {
	manager := &fakeManager{units: []dbus.UnitStatus{{Name: "scout.service", LoadState: "loaded", ActiveState: "active", SubState: "running"}}}
	adapter, err := NewWithConnection(manager, Config{})
	if err != nil {
		t.Fatal(err)
	}
	registry := collector.NewRegistry()
	if err := Register(registry, adapter); err != nil {
		t.Fatal(err)
	}
	if descriptors := registry.Descriptors(); len(descriptors) != 1 || descriptors[0].ID != CollectorID || descriptors[0].Interval != unitInventoryInterval || descriptors[0].Deadline != unitInventoryDeadline {
		t.Fatalf("descriptor = %+v", descriptors)
	}
	var listed *collector.Descriptor
	for _, descriptor := range collector.DefaultDescriptors() {
		if descriptor.ID == CollectorID {
			copy := descriptor
			listed = &copy
			break
		}
	}
	if listed == nil || listed.Provider != Descriptor().Provider || listed.Version != Descriptor().Version || listed.EntityLimit != Descriptor().EntityLimit || listed.Interval != Descriptor().Interval || listed.Deadline != Descriptor().Deadline {
		t.Fatalf("default descriptor does not expose systemd contract: %+v", listed)
	}
	result, err := registry.Collect(context.Background(), CollectorID)
	if err != nil || len(result.Entities) != 1 || result.Entities[0].Status != "active" {
		t.Fatalf("registry collection = %+v err=%v", result, err)
	}
}

func fixtureStatuses(t *testing.T) []dbus.UnitStatus {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("fixture path unavailable")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../tests/fixtures/collectors/systemd-units.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixtureUnit
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	result := make([]dbus.UnitStatus, 0, len(fixtures))
	for _, fixture := range fixtures {
		result = append(result, dbus.UnitStatus{Name: fixture.Name, Description: fixture.Description, LoadState: fixture.LoadState, ActiveState: fixture.ActiveState, SubState: fixture.SubState})
	}
	return result
}

func pad(value int) string {
	return fmt.Sprintf("%04d", value)
}
