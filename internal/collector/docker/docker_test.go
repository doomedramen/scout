package docker

import (
	"context"
	"io"
	"net/http"
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
		body := `[{"Id":"container-id","Names":["/web"],"Image":"nginx","State":"running","Status":"Up","Labels":{"app":"web"}}]`
		if strings.HasSuffix(request.URL.Path, "/version") {
			body = `{"ApiVersion":"1.43"}`
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

func TestAdapterRejectsRelativeSocketPath(t *testing.T) {
	if _, err := New("relative.sock"); err == nil {
		t.Fatal("relative Docker socket path accepted")
	}
}
