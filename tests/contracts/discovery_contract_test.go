package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type schemaValidator struct {
	root string
}

func (v schemaValidator) validateFile(schemaName string, value any) error {
	path := filepath.Join(v.root, schemaName)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return err
	}
	return v.validate(schema, value, path, "$")
}

func (v schemaValidator) validate(schema map[string]any, value any, basePath, path string) error {
	if rawRef, ok := schema["$ref"].(string); ok {
		resolved, resolvedPath, err := v.resolveRef(basePath, rawRef)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return v.validate(resolved, value, resolvedPath, path)
	}
	if rawAllOf, ok := schema["allOf"].([]any); ok {
		for index, raw := range rawAllOf {
			part, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s allOf[%d] is not an object", path, index)
			}
			if err := v.validate(part, value, basePath, path); err != nil {
				return err
			}
		}
	}
	if rawAnyOf, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, raw := range rawAnyOf {
			part, ok := raw.(map[string]any)
			if ok && v.validate(part, value, basePath, path) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s matches none of anyOf", path)
		}
	}
	if rawOneOf, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, raw := range rawOneOf {
			part, ok := raw.(map[string]any)
			if ok && v.validate(part, value, basePath, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s matches %d oneOf branches", path, matches)
		}
	}
	if rawIf, ok := schema["if"].(map[string]any); ok {
		if v.validate(rawIf, value, basePath, path) == nil {
			if rawThen, ok := schema["then"].(map[string]any); ok {
				if err := v.validate(rawThen, value, basePath, path); err != nil {
					return err
				}
			}
		} else if rawElse, ok := schema["else"].(map[string]any); ok {
			if err := v.validate(rawElse, value, basePath, path); err != nil {
				return err
			}
		}
	}
	if rawNot, ok := schema["not"].(map[string]any); ok && v.validate(rawNot, value, basePath, path) == nil {
		return fmt.Errorf("%s matches forbidden not schema", path)
	}

	if rawType, ok := schema["type"]; ok && !matchesType(rawType, value) {
		return fmt.Errorf("%s has type %T, schema requires %v", path, value, rawType)
	}
	if expected, ok := schema["const"]; ok && !reflect.DeepEqual(expected, value) {
		return fmt.Errorf("%s is %v, expected %v", path, value, expected)
	}
	if rawEnum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range rawEnum {
			if reflect.DeepEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s value %v is outside enum", path, value)
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		if rawRequired, ok := schema["required"].([]any); ok {
			for _, rawName := range rawRequired {
				name, ok := rawName.(string)
				if !ok {
					return fmt.Errorf("%s has non-string required name", path)
				}
				if _, exists := typed[name]; !exists {
					return fmt.Errorf("%s is missing required property %q", path, name)
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for name, child := range typed {
			property, declared := properties[name]
			if declared {
				propertySchema, ok := property.(map[string]any)
				if !ok {
					return fmt.Errorf("%s.%s has invalid property schema", path, name)
				}
				if err := v.validate(propertySchema, child, basePath, path+"."+name); err != nil {
					return err
				}
				continue
			}
			additional, hasAdditional := schema["additionalProperties"]
			if !hasAdditional {
				continue
			}
			if allowed, ok := additional.(bool); ok && !allowed {
				return fmt.Errorf("%s contains unknown property %q", path, name)
			}
			if additionalSchema, ok := additional.(map[string]any); ok {
				if err := v.validate(additionalSchema, child, basePath, path+"."+name); err != nil {
					return err
				}
			}
		}
	case []any:
		if minimum, ok := number(schema["minItems"]); ok && float64(len(typed)) < minimum {
			return fmt.Errorf("%s has %d items, minimum is %v", path, len(typed), minimum)
		}
		if maximum, ok := number(schema["maxItems"]); ok && float64(len(typed)) > maximum {
			return fmt.Errorf("%s has %d items, maximum is %v", path, len(typed), maximum)
		}
		if unique, ok := schema["uniqueItems"].(bool); ok && unique {
			seen := map[string]bool{}
			for index, item := range typed {
				encoded, err := json.Marshal(item)
				if err != nil {
					return fmt.Errorf("%s[%d] cannot be canonicalized: %w", path, index, err)
				}
				key := string(encoded)
				if seen[key] {
					return fmt.Errorf("%s contains duplicate item at index %d", path, index)
				}
				seen[key] = true
			}
		}
		if rawItems, ok := schema["items"].(map[string]any); ok {
			for index, item := range typed {
				if err := v.validate(rawItems, item, basePath, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	case string:
		if minimum, ok := number(schema["minLength"]); ok && float64(len(typed)) < minimum {
			return fmt.Errorf("%s is shorter than %v characters", path, minimum)
		}
		if maximum, ok := number(schema["maxLength"]); ok && float64(len(typed)) > maximum {
			return fmt.Errorf("%s is longer than %v characters", path, maximum)
		}
		if rawPattern, ok := schema["pattern"].(string); ok {
			matched, err := regexp.MatchString(rawPattern, typed)
			if err != nil {
				return fmt.Errorf("%s has invalid schema pattern: %w", path, err)
			}
			if !matched {
				return fmt.Errorf("%s does not match schema pattern", path)
			}
		}
	case float64:
		if minimum, ok := number(schema["minimum"]); ok && typed < minimum {
			return fmt.Errorf("%s is below minimum %v", path, minimum)
		}
		if maximum, ok := number(schema["maximum"]); ok && typed > maximum {
			return fmt.Errorf("%s is above maximum %v", path, maximum)
		}
	}
	return nil
}

func matchesType(rawType any, value any) bool {
	if types, ok := rawType.([]any); ok {
		for _, item := range types {
			if typeName, ok := item.(string); ok && matchesType(typeName, value) {
				return true
			}
		}
		return false
	}
	typeName, ok := rawType.(string)
	if !ok {
		return true
	}
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func number(value any) (float64, bool) {
	result, ok := value.(float64)
	return result, ok
}

func (v schemaValidator) resolveRef(basePath, ref string) (map[string]any, string, error) {
	parts := strings.SplitN(ref, "#", 2)
	path := basePath
	if parts[0] != "" {
		if strings.HasPrefix(parts[0], "https://") || strings.HasPrefix(parts[0], "http://") {
			return nil, "", fmt.Errorf("remote schema reference is not allowed: %s", ref)
		}
		path = filepath.Join(filepath.Dir(basePath), parts[0])
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", path, err)
	}
	if len(parts) == 1 || parts[1] == "" {
		return document, path, nil
	}
	node := any(document)
	for _, token := range strings.Split(strings.TrimPrefix(parts[1], "/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := node.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("reference %s traverses a non-object", ref)
		}
		node, ok = object[token]
		if !ok {
			return nil, "", fmt.Errorf("reference %s points to missing %q", ref, token)
		}
	}
	resolved, ok := node.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("reference %s does not point to a schema object", ref)
	}
	return resolved, path, nil
}

func walkSchemaRefs(value any, visit func(string) error) error {
	switch typed := value.(type) {
	case map[string]any:
		if ref, ok := typed["$ref"].(string); ok {
			if err := visit(ref); err != nil {
				return err
			}
		}
		for _, child := range typed {
			if err := walkSchemaRefs(child, visit); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkSchemaRefs(child, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func readContractFixture(t *testing.T, name string) (map[string]any, []byte) {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, "tests", "contracts", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return document, data
}

func TestActiveScanSchemaReferencesResolve(t *testing.T) {
	root := repoPath(t, "api", "schemas")
	validator := schemaValidator{root: root}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasPrefix(entry.Name(), "scan-") && !strings.HasPrefix(entry.Name(), "candidate") && !strings.HasPrefix(entry.Name(), "entry-point")) {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			document := readJSONDocument(t, "api", "schemas", entry.Name())
			if err := walkSchemaRefs(document, func(ref string) error {
				_, _, err := validator.resolveRef(filepath.Join(root, entry.Name()), ref)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestActiveScanFixturesValidatePositiveAndNegativeShapes(t *testing.T) {
	validator := schemaValidator{root: repoPath(t, "api", "schemas")}
	valid := map[string]string{
		"scan-policy.valid.json":                     "scan-policy-input.json",
		"scan-assignment.valid.json":                 "scan-assignment.json",
		"scan-result-page.valid.json":                "scan-result-page.json",
		"candidate.valid.json":                       "candidate.json",
		"scan-run-create.invalid-wrong-scanner.json": "scan-run-create.json",
	}
	for fixture, schema := range valid {
		document, _ := readContractFixture(t, fixture)
		if err := validator.validateFile(schema, document); err != nil {
			t.Errorf("valid fixture %s rejected: %v", fixture, err)
		}
	}
	invalid := map[string]string{
		"scan-policy.invalid-unknown-field.json":        "scan-policy-input.json",
		"scan-assignment.invalid-bounds.json":           "scan-assignment.json",
		"scan-result-page.invalid-transport.json":       "scan-result-page.json",
		"scan-result-page.invalid-remote-identity.json": "scan-result-page.json",
	}
	for fixture, schema := range invalid {
		document, _ := readContractFixture(t, fixture)
		if err := validator.validateFile(schema, document); err == nil {
			t.Errorf("invalid fixture %s was accepted", fixture)
		}
	}
}

func TestActiveScanBoundsAndPolicyFences(t *testing.T) {
	common := readJSONDocument(t, "api", "schemas", "scan-common.json")
	definitions := common["$defs"].(map[string]any)
	limits := definitions["limits"].(map[string]any)["properties"].(map[string]any)
	assertBound := func(name string, minimum, maximum float64) {
		t.Helper()
		property := limits[name].(map[string]any)
		assertNumber(t, property["minimum"], minimum, name+" minimum")
		assertNumber(t, property["maximum"], maximum, name+" maximum")
	}
	assertBound("probesPerSecond", 1, 1000)
	assertBound("concurrency", 1, 16)
	assertBound("targetBudget", 1, 4096)
	assertBound("attemptBudget", 1, 16384)
	assertBound("timeoutMilliseconds", 100, 10000)
	assertBound("runDeadlineSeconds", 30, 900)
	assertBound("resultPageSize", 1, 1000)
	entryPoint := readJSONDocument(t, "api", "schemas", "scan-policy-input.json")["properties"].(map[string]any)["entryPoints"].(map[string]any)
	assertNumber(t, entryPoint["maxItems"], 64, "entry point maxItems")
	assignment := readJSONDocument(t, "api", "schemas", "scan-assignment.json")["properties"].(map[string]any)
	assertNumber(t, assignment["ranges"].(map[string]any)["maxItems"], 128, "assignment range maxItems")
	assertNumber(t, assignment["exclusions"].(map[string]any)["maxItems"], 128, "assignment exclusion maxItems")

	policy, _ := readContractFixture(t, "scan-assignment.valid.json")
	if excludedByPolicy("192.0.2.10", []string{"192.0.2.0/24"}, []string{"192.0.2.9"}) {
		t.Fatal("in-scope non-excluded address was unexpectedly denied")
	}
	excluded, _ := readContractFixture(t, "scan-result-page.invalid-excluded-target.json")
	results := excluded["results"].([]any)
	address := results[0].(map[string]any)["address"].(string)
	if !excludedByPolicy(address, stringSlice(policy["ranges"]), stringSlice(policy["exclusions"])) {
		t.Fatal("excluded target fixture did not match exclusion policy")
	}

	stale, _ := readContractFixture(t, "scan-result-page.invalid-stale-revision.json")
	if stale["scopeRevision"].(float64) >= 9 {
		t.Fatal("stale revision fixture is not stale")
	}
	wrongScanner, _ := readContractFixture(t, "scan-run-create.invalid-wrong-scanner.json")
	scanner := wrongScanner["scanner"].(map[string]any)
	if scanner["id"].(string) == "assigned-agent" {
		t.Fatal("wrong-scanner fixture names the assigned scanner")
	}

	_, replayA := readContractFixture(t, "scan-result-page.replay-a.json")
	_, replayConflict := readContractFixture(t, "scan-result-page.replay-conflict.json")
	if hashBytes(replayA) == hashBytes(replayConflict) {
		t.Fatal("conflicting replay fixtures have the same content hash")
	}
	if !sameRunAndPage(replayA, replayConflict) {
		t.Fatal("conflicting replay fixtures do not target the same run page")
	}
}

func stringSlice(value any) []string {
	items := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.(string))
	}
	return result
}

func excludedByPolicy(address string, ranges, exclusions []string) bool {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return true
	}
	inside := false
	for _, raw := range ranges {
		if prefix, prefixErr := netip.ParsePrefix(raw); prefixErr == nil && prefix.Contains(parsed) {
			inside = true
		}
		if literal, literalErr := netip.ParseAddr(raw); literalErr == nil && literal == parsed {
			inside = true
		}
	}
	if !inside {
		return true
	}
	for _, raw := range exclusions {
		if literal, literalErr := netip.ParseAddr(raw); literalErr == nil && literal == parsed {
			return true
		}
		if prefix, prefixErr := netip.ParsePrefix(raw); prefixErr == nil && prefix.Contains(parsed) {
			return true
		}
	}
	return false
}

func hashBytes(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func sameRunAndPage(first, second []byte) bool {
	var left, right map[string]any
	if json.Unmarshal(first, &left) != nil || json.Unmarshal(second, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left["runId"], right["runId"]) && reflect.DeepEqual(left["pageOrdinal"], right["pageOrdinal"])
}
