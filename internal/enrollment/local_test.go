package enrollment

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

type localWorkerSSH struct {
	repository *store.Store
	deviceID   string
	uploads    map[string][]byte
	commands   []string
}

func (s *localWorkerSSH) Upload(_ context.Context, path string, data []byte) error {
	if s.uploads == nil {
		s.uploads = map[string][]byte{}
	}
	s.uploads[path] = append([]byte(nil), data...)
	return nil
}

func (s *localWorkerSSH) Run(_ context.Context, command string) ([]byte, error) {
	s.commands = append(s.commands, command)
	if command == "uname -m" {
		return []byte("x86_64\n"), nil
	}
	if strings.Contains(command, "systemctl enable --now scout-agent.service") {
		now := s.repository.Now()
		_, err := s.repository.UpdateDevice(context.Background(), s.deviceID, func(device *store.Device) error {
			device.AgentID = "fixture-agent"
			device.LastHeartbeat = &now
			device.Availability = store.AvailabilityOnline
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (s *localWorkerSSH) Close() error { return nil }

func TestLocalWorkerInstallsAgentFromOwnerBoundAccess(t *testing.T) {
	fixture := newLocalWorkerFixture(t)
	transport := &localWorkerSSH{repository: fixture.repository, deviceID: fixture.job.DeviceID}
	worker := NewLocalWorker(fixture.repository, &Access{Store: fixture.repository}, fixture.broker, LocalWorkerConfig{
		PublicOrigin: "https://scout.example.test",
		ArtifactDir:  fixture.artifactDir,
		ServiceUnit:  []byte("Environment=SCOUT_SERVER_URL=__SCOUT_SERVER_URL__\n"),
		AgentVersion: "0.1.0",
		PollInterval: time.Millisecond,
		Dial: func(_ context.Context, options SSHOptions) (SSHTransport, error) {
			if options.Address != fixture.job.Destination || options.Username != "fixture" || options.HostKeyCallback == nil {
				t.Fatalf("unexpected SSH options: %+v", options)
			}
			return transport, nil
		},
	})

	handled, err := worker.RunOnce(context.Background())
	if !handled || err != nil {
		t.Fatalf("local worker run = handled %t err %v", handled, err)
	}
	job, err := fixture.repository.Job(context.Background(), fixture.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "enrolled" || job.Result["code"] != "agent_confirmed" {
		t.Fatalf("job was not enrolled: %+v", job)
	}
	candidate, err := fixture.repository.CandidateByDeviceID(context.Background(), fixture.job.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.State != "enrolled" {
		t.Fatalf("candidate state = %q", candidate.State)
	}
	if got := string(transport.uploads["/tmp/scout-agent.invitation"]); got == "" {
		t.Fatal("worker did not upload a one-time invitation")
	}
	if got := string(transport.uploads["/tmp/scout-agent.service"]); !strings.Contains(got, "https://scout.example.test") {
		t.Fatalf("service unit was not bound to Scout: %q", got)
	}
	if !strings.Contains(strings.Join(transport.commands, "\n"), "sudo -n true") {
		t.Fatal("fixed install command did not check non-interactive privilege")
	}
}

func TestLocalWorkerProjectsHostKeyFailureWithoutLeakingCredential(t *testing.T) {
	fixture := newLocalWorkerFixture(t)
	worker := NewLocalWorker(fixture.repository, &Access{Store: fixture.repository}, fixture.broker, LocalWorkerConfig{
		PublicOrigin: "https://scout.example.test",
		ArtifactDir:  fixture.artifactDir,
		ServiceUnit:  []byte("__SCOUT_SERVER_URL__"),
		Dial: func(context.Context, SSHOptions) (SSHTransport, error) {
			return nil, ErrHostKeyMismatch
		},
	})

	handled, err := worker.RunOnce(context.Background())
	if !handled || !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("host key failure = handled %t err %v", handled, err)
	}
	job, err := fixture.repository.Job(context.Background(), fixture.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "failed" || job.Result["code"] != "host_key_mismatch" {
		t.Fatalf("host key result = %+v", job)
	}
	candidate, err := fixture.repository.CandidateByDeviceID(context.Background(), fixture.job.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.State != "needs_host_trust" {
		t.Fatalf("candidate state = %q", candidate.State)
	}
}

type localWorkerFixture struct {
	repository  *store.Store
	broker      *secrets.Broker
	job         store.Job
	artifactDir string
}

func newLocalWorkerFixture(t *testing.T) localWorkerFixture {
	t.Helper()
	ctx := context.Background()
	repository := store.NewMemory()
	site, err := repository.CreateSite(ctx, store.Site{ID: "site-local", Name: "Local", AddressContext: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	credentialID := "credential-local"
	trustID := "trust-local"
	scope, err := repository.CreateScope(ctx, store.Scope{ID: "scope-local", SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, CredentialRef: credentialID, TrustRef: trustID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	keyRing, err := secrets.NewKeyRing(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := keyRing.EncryptSecret(credentialID, "ssh", []byte("fixture-private-key"))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := repository.PutCredential(ctx, store.CredentialRef{ID: credentialID, Kind: "ssh", AllowedUse: []string{"enrollment"}, Targets: []string{"192.0.2.10:22"}, Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, WrappedDataKey: envelope.WrappedDataKey, KeyVersion: envelope.KeyVersion, Metadata: map[string]string{"username": "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PutTrust(ctx, store.TrustRecord{ID: trustID, ScopeID: scope.ID, Endpoint: "192.0.2.10:22", Host: "192.0.2.10", Fingerprint: "SHA256:fixture"}); err != nil {
		t.Fatal(err)
	}
	candidate, err := repository.UpsertCandidate(ctx, store.Candidate{ID: "candidate-local", ScopeID: scope.ID, SiteID: site.ID, Address: "192.0.2.10", Source: "fixture", State: "discovered", PreferredAccessMethod: store.ScanAccessSSH, EntryPointIDs: []string{"ssh-default"}})
	if err != nil {
		t.Fatal(err)
	}
	_, job, err := repository.QueueCandidateEnrollment(ctx, candidate.ID, store.Job{Kind: "enrollment", ScopeID: scope.ID, ScopeRevision: scope.Revision, CredentialVersion: credential.Revision, Destination: "192.0.2.10:22", TrustRef: trustID, State: "queued", Deadline: timePtrForTest(repository.Now().Add(5 * time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "scout-agent-linux-amd64"), []byte("signed-agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	return localWorkerFixture{repository: repository, broker: &secrets.Broker{Store: repository, KeyRing: keyRing}, job: job, artifactDir: artifactDir}
}

func timePtrForTest(value time.Time) *time.Time { return &value }
