package docker

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestAdapterTranslatesContainersWithoutSecrets(t *testing.T) {
	adapter, err := New("/tmp/scout-docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	adapter.Client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := string(readFixture("docker-containers.json"))
		if strings.HasSuffix(request.URL.Path, "/version") {
			body = string(readFixture("docker-version.json"))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	ok, err := adapter.Detect(context.Background())
	if err != nil || !ok {
		t.Fatalf("detect: %v %v", ok, err)
	}
	result, err := adapter.Collect(context.Background())
	if err != nil || len(result.Entities) != 1 {
		t.Fatalf("collect: %+v %v", result, err)
	}
	if result.Entities[0].Name != "web" || result.Entities[0].Labels["app"] != "web" {
		t.Fatalf("container translation: %+v", result.Entities[0])
	}
}

func readFixture(name string) []byte {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("test fixture path unavailable")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../tests/fixtures/collectors", name))
	if err != nil {
		panic(err)
	}
	return data
}

func TestAdapterRejectsRelativeSocketPath(t *testing.T) {
	if _, err := New("relative.sock"); err == nil {
		t.Fatal("relative Docker socket path accepted")
	}
}
