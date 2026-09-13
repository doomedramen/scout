package contracts

import (
	"os"
	"regexp"
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
		`${SCOUT_BIND_ADDRESS:-127.0.0.1}:${SCOUT_PORT:-8080}:8080`,
		`SCOUT_LISTEN: "127.0.0.1:8081"`,
		`SCOUT_API_ORIGIN: http://127.0.0.1:8081`,
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
		`${SCOUT_BIND_ADDRESS:-127.0.0.1}:${SCOUT_PORT:-8443}:8443`,
		`SCOUT_LISTEN: "0.0.0.0:8443"`,
	} {
		if !strings.Contains(productionText, required) {
			t.Errorf("production compose missing %q", required)
		}
	}

	for name, text := range map[string]string{"quickstart": quickstartText, "production": productionText} {
		if strings.Contains(text, "SCOUT_SERVER_BIND") {
			t.Errorf("%s compose retains the obsolete full host:port variable", name)
		}
		if !strings.Contains(text, "pull_policy: always") {
			t.Errorf("%s compose must pull the configured server image before starting", name)
		}
		for _, required := range []string{"restart: unless-stopped", "read_only: true", "no-new-privileges:true", "cap_drop:"} {
			if !strings.Contains(text, required) {
				t.Errorf("%s compose missing runtime hardening %q", name, required)
			}
		}
	}

	readme, err := os.ReadFile(repoPath(t, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "SCOUT_PORT=18080 docker compose up -d") {
		t.Fatal("README omits the single-variable host-port override")
	}
	for _, required := range []string{"SCOUT_PORT=8041", "SCOUT_BIND_ADDRESS=0.0.0.0", "http://<server-address>:8041"} {
		if !strings.Contains(string(readme), required) {
			t.Fatalf("README omits easy .env deployment instruction %q", required)
		}
	}

	composeExample := regexp.MustCompile("(?s)The equivalent Compose file, for direct copy/paste, is:\\n\\n```yaml\\n(.*?)```").FindStringSubmatch(string(readme))
	if len(composeExample) != 2 {
		t.Fatal("README Compose example not found")
	}
	if composeExample[1] != quickstartText {
		t.Fatal("README Compose example differs from compose.quickstart.yaml")
	}
}
