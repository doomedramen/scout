package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSONDocument(t *testing.T, parts ...string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, parts...))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("%s: %v", filepath.Join(parts...), err)
	}
	return document
}

func schemaProperty(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties while looking for %q", name)
	}
	property, ok := properties[name].(map[string]any)
	if !ok {
		t.Fatalf("schema property %q missing", name)
	}
	return property
}

func assertNumber(t *testing.T, value any, want float64, label string) {
	t.Helper()
	got, ok := value.(float64)
	if !ok || got != want {
		t.Fatalf("%s: got %v, want %v", label, value, want)
	}
}

func TestMonitoringSchemasArePresentAndSelfDescribing(t *testing.T) {
	files := []string{
		"monitoring-common.json",
		"alert-rule-input.json",
		"alert-rule.json",
		"alert-rule-patch.json",
		"alert-rule-override-input.json",
		"alert-rule-override.json",
		"alert-rule-override-patch.json",
		"alert-rule-list.json",
		"alert-rule-override-list.json",
		"incident.json",
		"incident-list.json",
		"incident-transition.json",
		"incident-transition-list.json",
		"incident-acknowledgment.json",
		"notification-destination-input.json",
		"notification-destination-patch.json",
		"notification-destination.json",
		"notification-destination-list.json",
		"notification-delivery.json",
		"notification-delivery-list.json",
		"notification-test-response.json",
		"suppression-window-input.json",
		"suppression-window-patch.json",
		"suppression-window.json",
		"suppression-window-list.json",
		"monitoring-settings.json",
		"monitoring-settings-patch.json",
		"retention-preview-input.json",
		"retention-preview.json",
		"monitoring-notifications-resume.json",
		"monitoring-status.json",
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			document := readJSONDocument(t, "api", "schemas", name)
			if document["$schema"] == nil {
				t.Fatal("schema omits $schema")
			}
			if document["$id"] == nil {
				t.Fatal("schema omits stable $id")
			}
		})
	}
}

func TestMonitoringSchemasKeepSecurityAndResourceBounds(t *testing.T) {
	destination := readJSONDocument(t, "api", "schemas", "notification-destination-input.json")
	for _, name := range []string{"topic", "token"} {
		property := schemaProperty(t, destination, name)
		if property["writeOnly"] != true {
			t.Errorf("%s must be writeOnly", name)
		}
	}
	assertNumber(t, schemaProperty(t, destination, "topic")["maxLength"], 128, "topic maxLength")
	assertNumber(t, schemaProperty(t, destination, "token")["maxLength"], 4096, "token maxLength")

	metrics := readJSONDocument(t, "api", "schemas", "metrics.json")
	series := schemaProperty(t, metrics, "series")
	assertNumber(t, series["maxItems"], 16, "history series maxItems")
	seriesItems := series["items"].(map[string]any)
	points := schemaProperty(t, seriesItems, "points")
	assertNumber(t, points["maxItems"], 600, "history point maxItems")
	pointItems := points["items"].(map[string]any)
	coverage := schemaProperty(t, pointItems, "coverage")
	assertNumber(t, coverage["minimum"], 0, "coverage minimum")
	assertNumber(t, coverage["maximum"], 1, "coverage maximum")

	common := readJSONDocument(t, "api", "schemas", "monitoring-common.json")
	definitions := common["$defs"].(map[string]any)
	numeric := definitions["numericCondition"].(map[string]any)
	numericProperties := numeric["properties"].(map[string]any)
	triggerSeconds := numericProperties["triggerSeconds"].(map[string]any)
	clearSeconds := numericProperties["clearSeconds"].(map[string]any)
	assertNumber(t, triggerSeconds["maximum"], 86400, "numeric triggerSeconds maximum")
	assertNumber(t, clearSeconds["maximum"], 86400, "numeric clearSeconds maximum")
}

func TestMonitoringOpenAPIListsOnlyBoundedOwnerScopedOperations(t *testing.T) {
	data, err := os.ReadFile(repoPath(t, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, route := range []string{
		"/alert-rules:",
		"/alert-rules/{ruleId}:",
		"/alert-rules/{ruleId}/overrides:",
		"/incidents:",
		"/incidents/{incidentId}/acknowledgment:",
		"/notification-destinations:",
		"/notification-destinations/{destinationId}/test:",
		"/notification-deliveries:",
		"/suppression-windows:",
		"/monitoring/settings:",
		"/monitoring/retention-preview:",
		"/monitoring/notifications/resume:",
		"/monitoring/status:",
	} {
		if !strings.Contains(text, route) {
			t.Errorf("OpenAPI contract missing %q", route)
		}
	}
	for _, required := range []string{"x-requires-recent-mfa: true", "maximum: 600", "maxItems: 16", "Idempotency-Key", "writeOnly"} {
		if !strings.Contains(text, required) {
			t.Errorf("OpenAPI contract missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"remote-shell",
		"arbitraryCommand",
		"/incidents/{incidentId}/resolve",
		"/services/{serviceId}/start",
		"/services/{serviceId}/stop",
		"/gpu/{gpuId}/control",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("OpenAPI contract contains forbidden operation %q", forbidden)
		}
	}
}

func TestMonitoringContractFixturesCoverSafeAndRejectedShapes(t *testing.T) {
	for _, name := range []string{
		"alert-rule-input.valid.json",
		"notification-destination-input.valid.json",
		"notification-destination.redacted.json",
		"suppression-window-input.valid.json",
		"metrics-history.valid.json",
	} {
		document := readJSONDocument(t, "tests", "contracts", "fixtures", name)
		if len(document) == 0 {
			t.Fatalf("fixture %s is empty", name)
		}
	}
	invalid := readJSONDocument(t, "tests", "contracts", "fixtures", "alert-rule-input.invalid-expression.json")
	condition := invalid["condition"].(map[string]any)
	if _, exists := condition["expression"]; !exists {
		t.Fatal("negative rule fixture must exercise rejected executable expressions")
	}
	redacted := readJSONDocument(t, "tests", "contracts", "fixtures", "notification-destination.redacted.json")
	if _, exists := redacted["token"]; exists {
		t.Fatal("redacted destination fixture leaks token")
	}
	if _, exists := redacted["topic"]; exists {
		t.Fatal("redacted destination fixture leaks topic")
	}
}
