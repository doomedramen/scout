package proxmox

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"scout.local/scout/internal/collector"
)

type Resource struct {
	VMID     int    `json:"vmid"`
	Type     string `json:"type"`
	Node     string `json:"node"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Tags     string `json:"tags"`
	Pool     string `json:"pool"`
	Template int    `json:"template"`
}

type Adapter struct {
	Client    *http.Client
	BaseURL   string
	ClusterID string
	Token     string
	Now       func() time.Time
}

func New(baseURL, clusterID, token, caFile, fingerprint string) (*Adapter, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || clusterID == "" || token == "" {
		return nil, errors.New("Proxmox requires an HTTPS endpoint, cluster, and token")
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		data, readErr := os.ReadFile(caFile)
		if readErr != nil || !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("Proxmox CA file is invalid")
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool}}
	if fingerprint != "" {
		transport.TLSClientConfig.VerifyConnection = verifyFingerprint(fingerprint)
	}
	return &Adapter{Client: &http.Client{Transport: transport, Timeout: 10 * time.Second}, BaseURL: parsed.String(), ClusterID: clusterID, Token: token}, nil
}

func verifyFingerprint(expected string) func(tls.ConnectionState) error {
	expected = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(expected), "sha256:"))
	return func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("Proxmox peer certificate missing")
		}
		sum := sha256.Sum256(state.PeerCertificates[0].Raw)
		if hex.EncodeToString(sum[:]) != expected {
			return errors.New("Proxmox peer certificate fingerprint mismatch")
		}
		return nil
	}
}

func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	data, status, err := a.get(ctx, "/api2/json/version")
	if err != nil {
		return false, err
	}
	if status != http.StatusOK || !json.Valid(data) {
		return false, fmt.Errorf("Proxmox version request returned HTTP %d", status)
	}
	return true, nil
}

func (a *Adapter) Collect(ctx context.Context) (collector.ServiceResult, error) {
	data, status, err := a.get(ctx, "/api2/json/cluster/resources?type=vm")
	if err != nil {
		return collector.ServiceResult{}, err
	}
	if status != http.StatusOK {
		return collector.ServiceResult{}, fmt.Errorf("Proxmox resource request returned HTTP %d", status)
	}
	var envelope struct {
		Data []Resource `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return collector.ServiceResult{}, errors.New("Proxmox resource response was invalid")
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	result := collector.ServiceResult{Entities: make([]collector.Entity, 0, len(envelope.Data))}
	for _, item := range envelope.Data {
		if item.VMID < 1 || len(result.Entities) >= 2000 {
			break
		}
		name := item.Name
		if name == "" {
			name = fmt.Sprintf("%s-%d", item.Type, item.VMID)
		}
		result.Entities = append(result.Entities, collector.Entity{ID: fmt.Sprintf("%d", item.VMID), Kind: item.Type, Name: name, Status: item.Status, Labels: map[string]string{"node": item.Node, "pool": item.Pool, "cluster": a.ClusterID}, Observed: now, Expires: now.Add(2 * time.Minute)})
	}
	return result, nil
}

func (a *Adapter) get(ctx context.Context, path string) ([]byte, int, error) {
	if a == nil || a.Client == nil {
		return nil, 0, errors.New("Proxmox client is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BaseURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "PVEAPIToken="+a.Token)
	response, err := a.Client.Do(request)
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
