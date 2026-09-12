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
	Invitation   []byte
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
		stagingPath    = "/tmp/scout-agent.new"
		unitPath       = "/tmp/scout-agent.service"
		invitationPath = "/tmp/scout-agent.invitation"
	)
	if err := transport.Upload(ctx, stagingPath, request.Artifact); err != nil {
		return InstallResult{}, err
	}
	if len(request.ServiceUnit) > 0 {
		if err := transport.Upload(ctx, unitPath, request.ServiceUnit); err != nil {
			return InstallResult{}, err
		}
	}
	if len(request.Invitation) > 0 {
		if err := transport.Upload(ctx, invitationPath, request.Invitation); err != nil {
			return InstallResult{}, err
		}
	}
	command, err := fixedInstallCommand(request.Version, stagingPath, unitPath, invitationPath, len(request.ServiceUnit) > 0, len(request.Invitation) > 0)
	if err != nil {
		return InstallResult{}, err
	}
	if _, err := transport.Run(ctx, command); err != nil {
		return InstallResult{}, fmt.Errorf("fixed agent installation failed: %w", err)
	}
	return InstallResult{Target: request.Target, Version: request.Version, Confirmed: true, Completed: time.Now().UTC()}, nil
}

func fixedInstallCommand(version, stagingPath, unitPath, invitationPath string, installUnit, installInvitation bool) (string, error) {
	if !safeVersion.MatchString(version) || stagingPath != "/tmp/scout-agent.new" || unitPath != "/tmp/scout-agent.service" || invitationPath != "/tmp/scout-agent.invitation" {
		return "", errors.New("unsafe fixed installation parameters")
	}
	unitStep := ""
	if installUnit {
		unitStep = "$as_root install -o root -g root -m 0644 /tmp/scout-agent.service /etc/systemd/system/scout-agent.service && "
	}
	invitationStep := ""
	if installInvitation {
		invitationStep = "$as_root install -o scout-agent -g scout-agent -m 0400 /tmp/scout-agent.invitation /var/lib/scout/agent/invitation && "
	}
	return fmt.Sprintf("set -eu; if [ \"$(id -u)\" -eq 0 ]; then as_root=; elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then as_root=sudo; else exit 77; fi; $as_root sh -c 'id scout-agent >/dev/null 2>&1 || useradd --system --user-group --home-dir /var/lib/scout/agent --shell /usr/sbin/nologin scout-agent'; $as_root install -d -o scout-agent -g scout-agent -m 0700 /var/lib/scout/agent; $as_root install -d -o root -g root -m 0755 /usr/local/libexec; $as_root test ! -e /var/lib/scout/agent/.install.lock || exit 73; $as_root mkdir /var/lib/scout/agent/.install.lock; trap '$as_root rmdir /var/lib/scout/agent/.install.lock; rm -f /tmp/scout-agent.new /tmp/scout-agent.service /tmp/scout-agent.invitation' EXIT; $as_root sh -c 'printf \"stage=installing version=%s\\n\" > /var/lib/scout/agent/install.journal'; test -s %s; $as_root install -o root -g root -m 0755 %s /usr/local/libexec/scout-agent; %s%s$as_root systemctl daemon-reload; $as_root systemctl enable --now scout-agent.service; $as_root systemctl is-active --quiet scout-agent.service; $as_root sh -c 'printf \"stage=confirmed version=%s\\n\" > /var/lib/scout/agent/install.journal'", version, stagingPath, stagingPath, invitationStep, unitStep, version), nil
}
