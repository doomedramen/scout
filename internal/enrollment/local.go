package enrollment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

const (
	DefaultLocalWorkerID = "server-enrollment"
	DefaultAgentVersion  = "0.1.0"
)

// LocalWorkerConfig contains server-owned enrollment settings. The server
// keeps the worker in-process while the SSH transport remains a narrow,
// owner-authorized boundary.
type LocalWorkerConfig struct {
	PublicOrigin       string
	ArtifactDir        string
	ArtifactFile       string
	ServiceUnitFile    string
	ServiceUnit        []byte
	AgentVersion       string
	Username           string
	PollInterval       time.Duration
	SSHTimeout         time.Duration
	InvitationLifetime time.Duration
	WorkerID           string
	Dial               func(context.Context, SSHOptions) (SSHTransport, error)
	Logf               func(string, ...any)
}

type LocalWorker struct {
	Store  *store.Store
	Access *Access
	Broker *secrets.Broker
	Config LocalWorkerConfig
}

func NewLocalWorker(repository *store.Store, access *Access, broker *secrets.Broker, config LocalWorkerConfig) *LocalWorker {
	if config.ArtifactDir == "" {
		config.ArtifactDir = "/usr/local/share/scout/agent"
	}
	if config.AgentVersion == "" {
		config.AgentVersion = DefaultAgentVersion
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
	}
	if config.SSHTimeout <= 0 {
		config.SSHTimeout = 15 * time.Second
	}
	if config.InvitationLifetime <= 0 {
		config.InvitationLifetime = 5 * time.Minute
	}
	if config.WorkerID == "" {
		config.WorkerID = DefaultLocalWorkerID
	}
	return &LocalWorker{Store: repository, Access: access, Broker: broker, Config: config}
}

// Run keeps polling until the server shuts down. A failed target is recorded
// on its job and does not stop enrollment for other discovered devices.
func (w *LocalWorker) Run(ctx context.Context) error {
	if w == nil || w.Store == nil {
		return store.ErrInvalid
	}
	for {
		handled, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			w.logf("local enrollment: %v", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if handled && err == nil {
			continue
		}
		timer := time.NewTimer(w.pollInterval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// RunOnce claims and processes at most one enrollment job. The boolean is
// false when the queue is empty, allowing callers and tests to avoid sleeps.
func (w *LocalWorker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || w.Store == nil {
		return false, store.ErrInvalid
	}
	sites, err := w.Store.ListSites(ctx)
	if err != nil {
		return false, err
	}
	siteIDs := make([]string, 0, len(sites))
	for _, site := range sites {
		siteIDs = append(siteIDs, site.ID)
	}
	job, err := w.Store.ClaimWorkerJob(ctx, store.WorkerIdentity{ID: w.workerID(), Name: "Scout server enrollment", Kind: "enroller", SiteIDs: siteIDs})
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, w.execute(ctx, job)
}

func (w *LocalWorker) execute(ctx context.Context, job store.Job) error {
	device, err := w.Store.GetDevice(ctx, job.DeviceID)
	if err != nil {
		return w.fail(ctx, job, "server_connectivity", err)
	}
	if device.AgentID != "" {
		if device.LastHeartbeat != nil && device.Availability == store.AvailabilityOnline {
			return w.report(ctx, job, "enrolled", map[string]string{"code": "agent_already_confirmed"})
		}
		if err := w.report(ctx, job, "verifying", map[string]string{"code": "agent_identity_exists"}); err != nil {
			return err
		}
		return w.waitForConfirmation(ctx, job)
	}
	if err := w.report(ctx, job, "connecting", nil); err != nil {
		return err
	}

	scope, err := w.Store.GetScope(ctx, job.ScopeID)
	if err != nil || !scope.Enabled || scope.Revision != job.ScopeRevision {
		if err == nil {
			err = store.ErrConflict
		}
		return w.fail(ctx, job, "scope_revision_changed", err)
	}
	if scope.CredentialRef == "" || w.Broker == nil {
		return w.fail(ctx, job, "credential_unavailable", store.ErrForbidden)
	}
	credential, err := w.Store.Credential(ctx, scope.CredentialRef)
	if err != nil {
		return w.fail(ctx, job, "credential_unavailable", err)
	}
	if credential.RevokedAt != nil || credential.Kind != "ssh" || job.CredentialVersion > 0 && credential.Revision != job.CredentialVersion {
		return w.fail(ctx, job, "invalid_credentials", store.ErrForbidden)
	}
	trust, err := w.Store.ScopeTrustRecord(ctx, scope.ID, job.Destination)
	if err != nil {
		return w.fail(ctx, job, "host_trust_required", err)
	}
	secret, err := w.Broker.RedeemForTarget(ctx, credential.ID, "enrollment", job.Destination)
	if err != nil {
		return w.fail(ctx, job, "credential_unavailable", err)
	}
	username := strings.TrimSpace(credential.Metadata["username"])
	if username == "" {
		username = strings.TrimSpace(w.Config.Username)
	}
	if username == "" {
		username = "scout"
	}
	if !validSSHUsername(username) {
		return w.fail(ctx, job, "invalid_credentials", store.ErrInvalid)
	}

	dial := w.Config.Dial
	if dial == nil {
		dial = DialSSH
	}
	transport, err := dial(ctx, SSHOptions{Address: job.Destination, Username: username, PrivateKeyPEM: secret, HostKeyCallback: FingerprintHostKeyCallback(trust.Fingerprint), Timeout: w.sshTimeout()})
	if err != nil {
		code := "connectivity"
		switch {
		case errors.Is(err, ErrHostKeyMismatch):
			code = "host_key_mismatch"
		case errors.Is(err, ErrSSHAuthentication):
			code = "invalid_credentials"
		}
		return w.fail(ctx, job, code, err)
	}

	artifact, version, err := w.loadArtifact(ctx, transport, device)
	if err != nil {
		_ = transport.Close()
		return w.fail(ctx, job, "unsupported_platform", err)
	}
	serviceUnit, err := w.loadServiceUnit()
	if err != nil {
		_ = transport.Close()
		return w.fail(ctx, job, "worker_not_configured", err)
	}
	serviceUnit, err = RenderServiceUnit(serviceUnit, w.Config.PublicOrigin)
	if err != nil {
		_ = transport.Close()
		return w.fail(ctx, job, "worker_not_configured", err)
	}
	invit, err := w.reserveInvitation(ctx, job)
	if err != nil {
		_ = transport.Close()
		return w.fail(ctx, job, "server_connectivity", err)
	}
	if err := w.report(ctx, job, "installing", nil); err != nil {
		_ = transport.Close()
		return err
	}
	sum := sha256.Sum256(artifact)
	if _, err := Install(ctx, transport, InstallRequest{Target: job.Destination, Username: username, Version: version, Artifact: artifact, ArtifactHash: "sha256:" + hex.EncodeToString(sum[:]), Invitation: []byte(invit), ServiceUnit: serviceUnit}); err != nil {
		return w.fail(ctx, job, "installation", err)
	}
	if err := w.report(ctx, job, "verifying", nil); err != nil {
		return err
	}
	return w.waitForConfirmation(ctx, job)
}

func (w *LocalWorker) waitForConfirmation(ctx context.Context, job store.Job) error {
	deadline := w.Store.Now().UTC().Add(60 * time.Second)
	renew := time.NewTicker(15 * time.Second)
	defer renew.Stop()
	check := time.NewTicker(2 * time.Second)
	defer check.Stop()
	for {
		device, err := w.Store.GetDevice(ctx, job.DeviceID)
		if err == nil && device.AgentID != "" && device.LastHeartbeat != nil && device.Availability == store.AvailabilityOnline {
			return w.report(ctx, job, "enrolled", map[string]string{"code": "agent_confirmed"})
		}
		if !w.Store.Now().UTC().Before(deadline) {
			return w.fail(ctx, job, "agent_confirmation_timeout", errors.New("installed agent did not confirm"))
		}
		select {
		case <-ctx.Done():
			return w.fail(ctx, job, "server_connectivity", ctx.Err())
		case <-renew.C:
			if _, err := w.Store.RenewJob(ctx, job.ID, w.workerID(), job.Epoch); err != nil {
				return err
			}
		case <-check.C:
		}
	}
}

func (w *LocalWorker) loadArtifact(ctx context.Context, transport SSHTransport, device store.Device) ([]byte, string, error) {
	path := strings.TrimSpace(w.Config.ArtifactFile)
	if path == "" {
		architecture := normalizeArchitecture(device.Architecture)
		if architecture == "" {
			output, err := transport.Run(ctx, "uname -m")
			if err != nil {
				return nil, "", err
			}
			architecture = normalizeArchitecture(string(output))
		}
		if architecture == "" {
			return nil, "", errors.New("target Linux architecture is unsupported")
		}
		path = filepath.Join(w.Config.ArtifactDir, "scout-agent-linux-"+architecture)
	}
	artifact, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, "", fmt.Errorf("read verified agent artifact: %w", err)
	}
	if len(artifact) == 0 {
		return nil, "", errors.New("verified agent artifact is empty")
	}
	version := strings.TrimSpace(w.Config.AgentVersion)
	if version == "" {
		version = DefaultAgentVersion
	}
	if !safeVersion.MatchString(version) {
		return nil, "", errors.New("agent version is invalid")
	}
	return artifact, version, nil
}

func (w *LocalWorker) loadServiceUnit() ([]byte, error) {
	if len(w.Config.ServiceUnit) > 0 {
		return append([]byte(nil), w.Config.ServiceUnit...), nil
	}
	path := strings.TrimSpace(w.Config.ServiceUnitFile)
	if path == "" {
		path = filepath.Join(w.Config.ArtifactDir, "agent.service")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) || path == "packaging/linux/agent.service" {
		return nil, err
	}
	return os.ReadFile("packaging/linux/agent.service")
}

func (w *LocalWorker) reserveInvitation(ctx context.Context, job store.Job) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := w.Store.Now().UTC()
	expires := now.Add(w.invitationLifetime())
	if job.Deadline != nil && job.Deadline.Before(expires) {
		expires = job.Deadline.UTC()
	}
	if !now.Before(expires) {
		return "", store.ErrExpired
	}
	if err := w.Store.ReserveInvitation(ctx, store.BootstrapInvitation{TokenHash: store.HashToken(token), DeviceID: job.DeviceID, EnrollmentJobID: job.ID, ExpiresAt: expires}); err != nil {
		return "", err
	}
	return token, nil
}

func (w *LocalWorker) report(ctx context.Context, job store.Job, state string, result map[string]string) error {
	if err := w.Store.ReportJob(ctx, job.ID, w.workerID(), job.Epoch, state, result); err != nil {
		return err
	}
	candidate, err := w.Store.CandidateByDeviceID(ctx, job.DeviceID)
	if err == nil {
		switch state {
		case "connecting", "installing", "verifying":
			_, _ = w.Store.UpdateCandidateState(ctx, candidate.ID, "enrolling")
		case "enrolled":
			_, _ = w.Store.UpdateCandidateState(ctx, candidate.ID, "enrolled")
		case "failed":
			code := "server_connectivity"
			if result != nil && result["code"] != "" {
				code = result["code"]
			}
			if w.Access != nil {
				_ = w.Access.RecordEnrollmentOutcome(ctx, candidate.ID, code)
			}
		}
	}
	if state == "failed" || state == "paused" || state == "enrolled" {
		_, _ = w.Store.AcknowledgePause(ctx, w.workerID())
	}
	return nil
}

func (w *LocalWorker) fail(ctx context.Context, job store.Job, code string, cause error) error {
	reportErr := w.report(ctx, job, "failed", map[string]string{"code": code})
	if reportErr != nil {
		return errors.Join(cause, reportErr)
	}
	return cause
}

func (w *LocalWorker) workerID() string {
	if value := strings.TrimSpace(w.Config.WorkerID); value != "" {
		return value
	}
	return DefaultLocalWorkerID
}

func (w *LocalWorker) pollInterval() time.Duration {
	if w.Config.PollInterval > 0 {
		return w.Config.PollInterval
	}
	return 5 * time.Second
}

func (w *LocalWorker) sshTimeout() time.Duration {
	if w.Config.SSHTimeout > 0 && w.Config.SSHTimeout <= 60*time.Second {
		return w.Config.SSHTimeout
	}
	return 15 * time.Second
}

func (w *LocalWorker) invitationLifetime() time.Duration {
	if w.Config.InvitationLifetime > 0 && w.Config.InvitationLifetime <= 15*time.Minute {
		return w.Config.InvitationLifetime
	}
	return 5 * time.Minute
}

func (w *LocalWorker) logf(format string, args ...any) {
	if w.Config.Logf != nil {
		w.Config.Logf(format, args...)
	}
}

func normalizeArchitecture(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(fields[0])) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

func validSSHUsername(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || value == "." || value == ".." || strings.ContainsAny(value, "\x00\r\n \t/\\:") {
		return false
	}
	return true
}
