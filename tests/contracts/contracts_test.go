package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "..", "..", filepath.Join(parts...))
}

func TestContractSchemasAreValidJSON(t *testing.T) {
	entries, err := os.ReadDir(repoPath(t, "api", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 10 {
		t.Fatalf("expected executable schema set, got %d files", len(entries))
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(repoPath(t, "api", "schemas", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if document["$schema"] == nil {
			t.Errorf("%s omits $schema", entry.Name())
		}
	}
}

func TestOpenAPIContractKeepsSecurityAndBoundedRoutes(t *testing.T) {
	data, err := os.ReadFile(repoPath(t, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{"openapi: 3.1.0", "/agent/v1/batches:", "/agent/v1/heartbeat:", "/worker/v1/claim:", "scoutSession", "maximum: 500", "maximum: 600", "writeOnly"} {
		if !strings.Contains(text, required) {
			t.Errorf("contract missing %q", required)
		}
	}
	if strings.Contains(text, "remote-shell") || strings.Contains(text, "arbitraryCommand") {
		t.Fatal("contract contains an arbitrary command endpoint")
	}
}
