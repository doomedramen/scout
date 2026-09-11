package docker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"scout.local/scout/internal/collector"
)

type Container struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

type Adapter struct {
	Client     *http.Client
	SocketPath string
	Now        func() time.Time
}

func New(socketPath string) (*Adapter, error) {
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}
	if !filepath.IsAbs(socketPath) {
		return nil, errors.New("Docker socket path must be absolute")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socketPath)
	}}
	return &Adapter{Client: &http.Client{Transport: transport, Timeout: 10 * time.Second}, SocketPath: socketPath}, nil
}

func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	data, status, err := a.get(ctx, "/version")
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("Docker version request returned HTTP %d", status)
	}
	var version struct {
		ApiVersion string `json:"ApiVersion"`
	}
	if err := json.Unmarshal(data, &version); err != nil || version.ApiVersion == "" {
		return false, errors.New("Docker version response was invalid")
	}
	return true, nil
}

func (a *Adapter) Collect(ctx context.Context) (collector.ServiceResult, error) {
	data, status, err := a.get(ctx, "/containers/json?all=0&limit=2000")
	if err != nil {
		return collector.ServiceResult{}, err
	}
	if status != http.StatusOK {
		return collector.ServiceResult{}, fmt.Errorf("Docker container request returned HTTP %d", status)
	}
	var containers []Container
	if err := json.Unmarshal(data, &containers); err != nil {
		return collector.ServiceResult{}, errors.New("Docker container response was invalid")
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	result := collector.ServiceResult{Entities: make([]collector.Entity, 0, len(containers))}
	for _, item := range containers {
		if item.ID == "" || len(result.Entities) >= 2000 {
			break
		}
		name := strings.TrimPrefix(first(item.Names), "/")
		if name == "" {
			name = item.ID[:min(12, len(item.ID))]
		}
		labels := map[string]string{}
		for key, value := range item.Labels {
			if len(labels) >= 64 || len(key) > 64 || len(value) > 256 {
				continue
			}
			labels[key] = value
		}
		result.Entities = append(result.Entities, collector.Entity{ID: item.ID, Kind: "container", Name: name, Status: item.State, Labels: labels, Observed: now, Expires: now.Add(2 * time.Minute)})
	}
	return result, nil
}

func (a *Adapter) get(ctx context.Context, path string) ([]byte, int, error) {
	client := a.Client
	if client == nil {
		return nil, 0, errors.New("Docker client is unavailable")
	}
	target, _ := url.Parse("http://docker" + path)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, response.StatusCode, err
	}
	return data, response.StatusCode, nil
}

func (a *Adapter) Close() error {
	if a != nil && a.Client != nil {
		if transport, ok := a.Client.Transport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
	}
	return nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
