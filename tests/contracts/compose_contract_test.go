package contracts

import (
	"os"
	"strings"
	"testing"
)

func TestComposeKeepsListenAndPortMappingConfigurable(t *testing.T) {
	quickstart, err := os.ReadFile(repoPath(t, "compose.quickstart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	quickstartText := string(quickstart)
	for _, required := range []string{
		`${SCOUT_BIND_ADDRESS:-127.0.0.1}:${SCOUT_PORT:-8080}:${SCOUT_CONTAINER_PORT:-8080}`,
		`SCOUT_LISTEN: "0.0.0.0:${SCOUT_CONTAINER_PORT:-8080}"`,
	} {
		if !strings.Contains(quickstartText, required) {
			t.Errorf("quickstart compose missing %q", required)
		}
	}

	production, err := os.ReadFile(repoPath(t, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	productionText := string(production)
	for _, required := range []string{
		`${SCOUT_BIND_ADDRESS:-127.0.0.1}:${SCOUT_PORT:-8443}:${SCOUT_CONTAINER_PORT:-8443}`,
		`SCOUT_LISTEN: "0.0.0.0:${SCOUT_CONTAINER_PORT:-8443}"`,
	} {
		if !strings.Contains(productionText, required) {
			t.Errorf("production compose missing %q", required)
		}
	}

	for name, text := range map[string]string{"quickstart": quickstartText, "production": productionText} {
		if strings.Contains(text, "SCOUT_SERVER_BIND") {
			t.Errorf("%s compose retains the obsolete full host:port variable", name)
		}
	}

	readme, err := os.ReadFile(repoPath(t, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "SCOUT_PORT=18080 docker compose up -d") {
		t.Fatal("README omits the single-variable host-port override")
	}
	if !strings.Contains(string(readme), "SCOUT_CONTAINER_PORT") {
		t.Fatal("README omits the optional container-port override")
	}
}
