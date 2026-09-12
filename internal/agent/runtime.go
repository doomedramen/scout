package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"scout.local/scout/internal/collector"
	"scout.local/scout/internal/store"
	"scout.local/scout/internal/telemetry"
	"scout.local/scout/internal/updates"
)

const AgentVersion = "0.1.0"

type Config struct {
	ServerURL        string
	InvitationFile   string
	DataDir          string
	Root             string
	Interval         time.Duration
	HTTPClient       *http.Client
	Version          string
	ReleaseTrustFile string
}

type persistedIdentity struct {
	DeviceID       string    `json:"deviceId"`
	AgentID        string    `json:"agentId"`
	AgentToken     string    `json:"agentToken"`
	CertificatePEM string    `json:"certificatePem"`
	CABundlePEM    string    `json:"caBundlePem"`
	PrivateKeyPEM  string    `json:"privateKeyPem"`
	ExpiresAt      time.Time `json:"expiresAt"`
	Version        string    `json:"version"`
	Generation     int64     `json:"generation"`
}

type Runtime struct {
	Config       Config
	identity     persistedIdentity
	collector    *collector.HostCollector
	spool        *Spool
	scanSpool    *Spool
	bootID       string
	client       *http.Client
	mu           sync.Mutex
	scanMu       sync.Mutex
	scanStateMu  sync.Mutex
	scanCancel   context.CancelFunc
	scanRunID    string
	scanEpoch    int64
	scanRevision int64
	scanExecutor func(context.Context, ScanAssignment) error
	startedAt    time.Time
}

func NewRuntime(config Config) (*Runtime, error) {
	if config.ServerURL == "" {
		return nil, fmt.Errorf("server URL is required")
	}
	if config.DataDir == "" {
		config.DataDir = "/var/lib/scout/agent"
	}
	if config.Root == "" {
		config.Root = "/"
	}
	if config.Interval <= 0 {
		config.Interval = 15 * time.Second
	}
	if config.Version == "" {
		config.Version = AgentVersion
	}
	if err := os.MkdirAll(config.DataDir, 0o700); err != nil {
		return nil, err
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Runtime{Config: config, collector: collector.NewHostCollector(config.Root), spool: NewSpool(filepath.Join(config.DataDir, "spool"), 64<<20, time.Hour), scanSpool: NewSpool(filepath.Join(config.DataDir, "scan-spool"), 16<<20, time.Hour), bootID: store.NewID(), client: client, startedAt: time.Now().UTC()}, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	if err := r.loadOrEnroll(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(r.Config.Interval)
	defer ticker.Stop()
	if err := r.ReportOnce(ctx); err != nil && ctx.Err() != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.ReportOnce(ctx); err != nil && ctx.Err() != nil {
				return err
			}
		}
	}
}

// RunOnce enrolls the agent when needed and sends one batch plus heartbeat.
// It supports supervised service checks and deterministic integration tests.
func (r *Runtime) RunOnce(ctx context.Context) error {
	if err := r.loadOrEnroll(ctx); err != nil {
		return err
	}
	return r.ReportOnce(ctx)
}

func (r *Runtime) ReportOnce(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.flushSpool(ctx)
	snapshot, err := r.collector.Collect(ctx)
	if err != nil {
		return err
	}
	snapshot.IntervalSeconds = int(r.Config.Interval / time.Second)
	if snapshot.IntervalSeconds < 1 {
		snapshot.IntervalSeconds = 1
	}
	batch := telemetry.FromCollector(snapshot, r.bootID, store.NewID())
	batch.DroppedCount = r.spool.Dropped()
	data, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	if err := r.post(ctx, "/api/v1/agent/v1/batches", data); err != nil {
		if spoolErr := r.spool.Add(data); spoolErr != nil {
			return errors.Join(err, spoolErr)
		}
	}
	uptime := int64(time.Since(r.startedAt).Seconds())
	if uptime < 0 {
		uptime = 0
	}
	heartbeat, _ := json.Marshal(map[string]any{"bootId": r.bootID, "installedVersion": r.identity.Version, "uptimeSeconds": uptime, "collectorStates": []any{}, "updateState": map[string]string{}, "capabilities": map[string]any{"scanProtocolVersions": []int{1}, "scanTransports": []string{store.ScanTransportTCP}}})
	_ = r.post(ctx, "/api/v1/agent/v1/heartbeat", heartbeat)
	_ = r.syncScan(ctx)
	_ = r.syncUpdate(ctx)
	return nil
}

func (r *Runtime) syncUpdate(ctx context.Context) error {
	if r.Config.ReleaseTrustFile == "" {
		return nil
	}
	data, err := r.getResponse(ctx, "/api/v1/agent/v1/desired-state", r.identity.AgentToken)
	if err != nil {
		return err
	}
	var desired struct {
		UpdateAssignment *struct {
			Assignment store.Assignment `json:"assignment"`
			Release    updates.Manifest `json:"release"`
			Artifact   string           `json:"artifactPath"`
		} `json:"updateAssignment"`
	}
	if err := json.Unmarshal(data, &desired); err != nil {
		return err
	}
	if desired.UpdateAssignment == nil {
		return nil
	}
	assignment := desired.UpdateAssignment.Assignment
	manifest := desired.UpdateAssignment.Release
	if assignment.ExpiresAt.Before(time.Now().UTC()) || assignment.State == "paused" || assignment.State == "failed" {
		return nil
	}
	if updates.IsDowngrade(manifest.Generation, r.identity.Generation) || manifest.Generation == r.identity.Generation && manifest.Version != r.identity.Version {
		return r.reportUpdateResult(ctx, assignment, "rejected", "downgrade")
	}
	if err := updates.Compatible(manifest, "linux", runtimeArch(), telemetry.ProtocolVersion); err != nil {
		return r.reportUpdateResult(ctx, assignment, "rejected", "incompatible")
	}
	trust, err := updates.LoadTrustFile(filepath.Clean(r.Config.ReleaseTrustFile))
	if err != nil {
		return err
	}
	artifactPath := filepath.Join(r.Config.DataDir, "updates", strings.TrimPrefix(manifest.Digest, "sha256:"))
	if err := updates.Download(ctx, r.client, r.serverPath(desired.UpdateAssignment.Artifact), artifactPath, manifest.Digest, manifest.Bytes, r.identity.AgentToken); err != nil {
		return r.reportUpdateResult(ctx, assignment, "failed", "download")
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	if err := manifest.Verify(artifact, trust); err != nil {
		return r.reportUpdateResult(ctx, assignment, "rejected", "verification")
	}
	installer, err := updates.NewInstaller(filepath.Join(r.Config.DataDir, "updater"))
	if err != nil {
		return err
	}
	if err := installer.Stage(ctx, manifest, artifact); err != nil {
		return r.reportUpdateResult(ctx, assignment, "failed", "staging")
	}
	return r.reportUpdateResult(ctx, assignment, "staged", "")
}

func (r *Runtime) reportUpdateResult(ctx context.Context, assignment store.Assignment, state, code string) error {
	body, err := json.Marshal(map[string]any{"assignmentId": assignment.ID, "generation": assignment.Generation, "state": state, "installedVersion": r.identity.Version, "errorCode": code})
	if err != nil {
		return err
	}
	return r.post(ctx, "/api/v1/agent/v1/update-results", body)
}

func (r *Runtime) loadOrEnroll(ctx context.Context) error {
	path := filepath.Join(r.Config.DataDir, "identity.json")
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &r.identity); err != nil {
			return fmt.Errorf("decode agent identity: %w", err)
		}
		if r.identity.AgentToken == "" || r.identity.AgentID == "" {
			return fmt.Errorf("agent identity is incomplete")
		}
		if err := r.configureTLS(); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if r.Config.InvitationFile == "" {
		return fmt.Errorf("agent invitation file is required for first enrollment")
	}
	invitation, err := os.ReadFile(filepath.Clean(r.Config.InvitationFile))
	if err != nil {
		return err
	}
	if err := r.enroll(ctx, strings.TrimSpace(string(invitation)), path); err != nil {
		return err
	}
	if err := removeConsumedInvitation(r.Config.InvitationFile); err != nil {
		return err
	}
	return nil
}

func removeConsumedInvitation(path string) error {
	if err := os.Remove(filepath.Clean(path)); err != nil {
		if errors.Is(err, os.ErrNotExist) || os.IsPermission(err) || errors.Is(err, syscall.EROFS) {
			return nil
		}
		return fmt.Errorf("remove consumed invitation: %w", err)
	}
	return nil
}

func (r *Runtime) enroll(ctx context.Context, invitation, path string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "scout-agent"}}, key)
	if err != nil {
		return err
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	request := map[string]string{"invitation": invitation, "csrPem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), "agentVersion": r.Config.Version, "platform": "linux", "architecture": runtimeArch()}
	body, _ := json.Marshal(request)
	resultData, err := r.postResponse(ctx, "/api/v1/agent/v1/enroll", body, "")
	if err != nil {
		return err
	}
	var result struct {
		DeviceID       string    `json:"deviceId"`
		AgentID        string    `json:"agentId"`
		AgentToken     string    `json:"agentToken"`
		CertificatePEM string    `json:"certificatePem"`
		CABundlePEM    string    `json:"caBundlePem"`
		ExpiresAt      time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(resultData, &result); err != nil {
		return err
	}
	if result.AgentID == "" || result.AgentToken == "" {
		return fmt.Errorf("enrollment response omitted identity")
	}
	r.identity = persistedIdentity{DeviceID: result.DeviceID, AgentID: result.AgentID, AgentToken: result.AgentToken, CertificatePEM: result.CertificatePEM, CABundlePEM: result.CABundlePEM, PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})), ExpiresAt: result.ExpiresAt, Version: r.Config.Version}
	encoded, _ := json.Marshal(r.identity)
	temporary := path + ".new"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	if err := r.configureTLS(); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) configureTLS() error {
	if r.identity.CertificatePEM == "" || r.identity.PrivateKeyPEM == "" || r.identity.CABundlePEM == "" {
		return nil
	}
	certificate, err := tls.X509KeyPair([]byte(r.identity.CertificatePEM), []byte(r.identity.PrivateKeyPEM))
	if err != nil {
		return fmt.Errorf("load persisted agent certificate: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(r.identity.CABundlePEM)) {
		return fmt.Errorf("load persisted agent CA: invalid certificate bundle")
	}
	transport, ok := r.client.Transport.(*http.Transport)
	if !ok && r.client.Transport != nil {
		// Preserve a caller-owned RoundTripper, especially for embedded clients
		// and tests that intentionally intercept requests.
		return nil
	}
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport)
	}
	clone := transport.Clone()
	if clone.TLSClientConfig == nil {
		clone.TLSClientConfig = &tls.Config{}
	} else {
		clone.TLSClientConfig = clone.TLSClientConfig.Clone()
	}
	clone.TLSClientConfig.MinVersion = tls.VersionTLS13
	clone.TLSClientConfig.RootCAs = pool
	clone.TLSClientConfig.Certificates = []tls.Certificate{certificate}
	r.client = &http.Client{Transport: clone, Timeout: r.client.Timeout, CheckRedirect: r.client.CheckRedirect, Jar: r.client.Jar}
	return nil
}

func runtimeArch() string { return runtime.GOARCH }

func (r *Runtime) post(ctx context.Context, path string, body []byte) error {
	_, err := r.postResponse(ctx, path, body, r.identity.AgentToken)
	return err
}
func (r *Runtime) postResponse(ctx context.Context, path string, body []byte, token string) ([]byte, error) {
	return r.requestResponse(ctx, http.MethodPost, path, body, token)
}

func (r *Runtime) getResponse(ctx context.Context, path, token string) ([]byte, error) {
	return r.requestResponse(ctx, http.MethodGet, path, nil, token)
}

func (r *Runtime) requestResponse(ctx context.Context, method, path string, body []byte, token string) ([]byte, error) {
	base, err := url.Parse(r.Config.ServerURL)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	request, err := http.NewRequestWithContext(ctx, method, base.String(), reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Scout API returned HTTP %d", response.StatusCode)
	}
	return data, nil
}

func (r *Runtime) serverPath(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimRight(r.Config.ServerURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func (r *Runtime) flushSpool(ctx context.Context) error {
	items, err := r.spool.Items()
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := r.post(ctx, "/api/v1/agent/v1/batches", item.Data); err != nil {
			return err
		}
		if err := r.spool.Remove(item.Path); err != nil {
			return err
		}
	}
	return nil
}

type SpoolItem struct {
	Path      string
	Data      []byte
	CreatedAt time.Time
}
type Spool struct {
	dir      string
	maxBytes int64
	maxAge   time.Duration
	mu       sync.Mutex
	dropped  int
}

func NewSpool(dir string, maxBytes int64, maxAge time.Duration) *Spool {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	return &Spool{dir: dir, maxBytes: maxBytes, maxAge: maxAge}
}
func (s *Spool) Add(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.batch", time.Now().UnixNano(), store.NewID())
	if err := os.WriteFile(filepath.Join(s.dir, name), data, 0o600); err != nil {
		return err
	}
	s.enforceLocked()
	return nil
}
func (s *Spool) enforceLocked() {
	entries, _ := os.ReadDir(s.dir)
	items := []SpoolItem{}
	var total int64
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".batch") {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > s.maxAge {
			s.dropped++
			_ = os.Remove(path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		total += int64(len(data))
		items = append(items, SpoolItem{Path: path, Data: data, CreatedAt: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	for total > s.maxBytes && len(items) > 0 {
		item := items[0]
		items = items[1:]
		total -= int64(len(item.Data))
		s.dropped++
		_ = os.Remove(item.Path)
	}
}
func (s *Spool) Items() ([]SpoolItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	s.enforceLocked()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	result := []SpoolItem{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".batch") {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		info, _ := entry.Info()
		result = append(result, SpoolItem{Path: path, Data: data, CreatedAt: info.ModTime()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}
func (s *Spool) Remove(path string) error { s.mu.Lock(); defer s.mu.Unlock(); return os.Remove(path) }
func (s *Spool) Dropped() int             { s.mu.Lock(); defer s.mu.Unlock(); return s.dropped }
