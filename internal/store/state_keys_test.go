package store

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPersistedStateKeysAreJSONSafe(t *testing.T) {
	keys := []string{
		alertWorkKey("lineage\x00id", "entity"),
		alertEvaluationKey("lineage", "entity\x00id"),
		collectorConfigKey("device", "collector"),
		safeCompositeKey("device", "collector"),
		serviceEntityKey(ServiceEntity{Provider: "provider", ClusterID: "cluster", ID: "entity"}),
		suppressionEpisodeKey("destination", "entity", 1),
		alertOverrideKey("lineage", "device", "device-id"),
	}

	for _, key := range keys {
		if strings.ContainsRune(key, '\x00') {
			t.Fatalf("persisted state key contains a NUL byte: %q", key)
		}
		encoded, err := json.Marshal(map[string]bool{key: true})
		if err != nil {
			t.Fatalf("marshal persisted state key %q: %v", key, err)
		}
		if bytes.Contains(encoded, []byte(`\u0000`)) {
			t.Fatalf("persisted state key contains a JSON NUL escape: %s", encoded)
		}
	}
}
