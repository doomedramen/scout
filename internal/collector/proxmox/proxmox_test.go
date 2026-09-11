package proxmox

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

func TestAdapterTranslatesTypedResources(t *testing.T) {
	adapter := &Adapter{BaseURL: "https://pve.example", ClusterID: "cluster-a", Token: "token", Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "PVEAPIToken=token" {
			t.Fatal("Proxmox token header missing")
		}
		body := string(readFixture("proxmox-resources.json"))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	result, err := adapter.Collect(context.Background())
	if err != nil || len(result.Entities) != 1 {
		t.Fatalf("collect: %+v %v", result, err)
	}
	if result.Entities[0].Name != "router" || result.Entities[0].Labels["cluster"] != "cluster-a" {
		t.Fatalf("resource translation: %+v", result.Entities[0])
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

func TestNewRequiresHTTPSAndScopedCredentials(t *testing.T) {
	if _, err := New("http://pve.example", "cluster", "token", "", ""); err == nil {
		t.Fatal("HTTP Proxmox endpoint accepted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
