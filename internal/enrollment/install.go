package enrollment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

var safeVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type SSHTransport interface {
	Upload(context.Context, string, []byte) error
	Run(context.Context, string) ([]byte, error)
	Close() error
}

type InstallRequest struct {
	Target       string
	Username     string
	Version      string
	Artifact     []byte
	ArtifactHash string
	ServiceUnit  []byte
}

type InstallResult struct {
	Target    string    `json:"target"`
	Version   string    `json:"version"`
	Confirmed bool      `json:"confirmed"`
	Completed time.Time `json:"completedAt"`
}

func ValidateTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" || strings.ContainsAny(target, "/\\?#% \t\r\n") {
		return errors.New("target must be a host and port")
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || port == "" {
		return errors.New("target must include an explicit port")
	}
	if address, addressErr := netip.ParseAddr(host); addressErr == nil && !address.IsValid() {
		return errors.New("target address is invalid")
	}
	return nil
}

func VerifyArtifact(artifact []byte, expected string) error {
	if len(artifact) == 0 || !strings.HasPrefix(expected, "sha256:") {
		return errors.New("artifact digest is required")
	}
	digest := strings.TrimPrefix(expected, "sha256:")
	if len(digest) != sha256.Size*2 {
		return errors.New("artifact digest is invalid")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return errors.New("artifact digest is invalid")
	}
	sum := sha256.Sum256(artifact)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), digest) {
		return errors.New("artifact digest does not match")
	}
	return nil
}

func (r InstallRequest) Validate() error {
	if err := ValidateTarget(r.Target); err != nil {
		return err
	}
	if strings.TrimSpace(r.Username) == "" || !safeVersion.MatchString(r.Version) || len(r.ServiceUnit) > 128<<10 {
		return errors.New("install request is invalid")
	}
	return VerifyArtifact(r.Artifact, r.ArtifactHash)
}

func Install(ctx context.Context, transport SSHTransport, request InstallRequest) (InstallResult, error) {
	if transport == nil {
		return InstallResult{}, errors.New("SSH transport is required")
	}
	if err := request.Validate(); err != nil {
		return InstallResult{}, err
	}
	defer transport.Close()
	const (
		stagingPath = "/var/lib/scout/agent/scout-agent.new"
		unitPath    = "/etc/systemd/system/scout-agent.service"
	)
	if err := transport.Upload(ctx, stagingPath, request.Artifact); err != nil {
		return InstallResult{}, err
	}
	if len(request.ServiceUnit) > 0 {
		if err := transport.Upload(ctx, unitPath, request.ServiceUnit); err != nil {
			return InstallResult{}, err
		}
	}
	command, err := fixedInstallCommand(request.Version, stagingPath, unitPath, len(request.ServiceUnit) > 0)
	if err != nil {
		return InstallResult{}, err
	}
	if _, err := transport.Run(ctx, command); err != nil {
		return InstallResult{}, fmt.Errorf("fixed agent installation failed: %w", err)
	}
	return InstallResult{Target: request.Target, Version: request.Version, Confirmed: true, Completed: time.Now().UTC()}, nil
}

func fixedInstallCommand(version, stagingPath, unitPath string, installUnit bool) (string, error) {
	if !safeVersion.MatchString(version) || stagingPath != "/var/lib/scout/agent/scout-agent.new" || unitPath != "/etc/systemd/system/scout-agent.service" {
		return "", errors.New("unsafe fixed installation parameters")
	}
	unitStep := ""
	if installUnit {
		unitStep = "install -o root -g root -m 0644 /etc/systemd/system/scout-agent.service /etc/systemd/system/scout-agent.service && "
	}
	return fmt.Sprintf("set -eu; test ! -e /var/lib/scout/agent/.install.lock || exit 73; install -d -o root -g root -m 0755 /var/lib/scout/agent; mkdir /var/lib/scout/agent/.install.lock; trap 'rmdir /var/lib/scout/agent/.install.lock' EXIT; printf 'stage=installing version=%s\\n' '%s' > /var/lib/scout/agent/install.journal; test -s %s; install -o root -g root -m 0755 %s /usr/local/libexec/scout-agent; %ssystemctl daemon-reload; systemctl enable --now scout-agent.service; systemctl is-active --quiet scout-agent.service; printf 'stage=confirmed version=%s\\n' '%s' > /var/lib/scout/agent/install.journal", version, version, stagingPath, stagingPath, unitStep, version, version), nil
}
