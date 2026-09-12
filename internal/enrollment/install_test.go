package enrollment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
)

type fakeSSH struct {
	uploads  map[string][]byte
	commands []string
	output   []byte
}

func (f *fakeSSH) Upload(_ context.Context, path string, data []byte) error {
	if f.uploads == nil {
		f.uploads = map[string][]byte{}
	}
	f.uploads[path] = append([]byte(nil), data...)
	return nil
}

func (f *fakeSSH) Run(_ context.Context, command string) ([]byte, error) {
	f.commands = append(f.commands, command)
	return f.output, nil
}

func (f *fakeSSH) Close() error { return nil }

func TestInstallUsesFixedPathsAndVerifiesArtifact(t *testing.T) {
	artifact := []byte("signed-agent")
	sum := sha256.Sum256(artifact)
	transport := &fakeSSH{}
	result, err := Install(context.Background(), transport, InstallRequest{Target: "192.0.2.10:22", Username: "scout", Version: "1.2.3", Artifact: artifact, ArtifactHash: "sha256:" + hex.EncodeToString(sum[:]), Invitation: []byte("one-time-invitation"), ServiceUnit: []byte("[Unit]\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Confirmed || len(transport.commands) != 1 || len(transport.uploads) != 3 {
		t.Fatalf("unexpected install result: %+v uploads=%d commands=%d", result, len(transport.uploads), len(transport.commands))
	}
	if strings.Contains(transport.commands[0], "192.0.2.10") || strings.Contains(transport.commands[0], "operator") {
		t.Fatal("untrusted connection parameters entered fixed remote command")
	}
	if !strings.Contains(transport.commands[0], "systemctl is-active --quiet scout-agent.service") {
		t.Fatal("post-install service confirmation missing")
	}
	if string(transport.uploads["/tmp/scout-agent.invitation"]) != "one-time-invitation" {
		t.Fatal("one-time invitation was not staged at the fixed path")
	}
}

func TestInstallRejectsUnsafeTargetAndTamperedArtifact(t *testing.T) {
	if err := (InstallRequest{Target: "https://192.0.2.10:22", Username: "scout", Version: "1.0.0", Artifact: []byte("x"), ArtifactHash: "sha256:" + strings.Repeat("0", 64)}).Validate(); err == nil {
		t.Fatal("URL target accepted")
	}
	if err := (InstallRequest{Target: "192.0.2.10:22", Username: "scout", Version: "1.0.0;rm", Artifact: []byte("x"), ArtifactHash: "sha256:" + strings.Repeat("0", 64)}).Validate(); err == nil {
		t.Fatal("command-injection version accepted")
	}
}

func TestFixedInstallCommandIsValidShell(t *testing.T) {
	command, err := fixedInstallCommand("1.2.3", "/tmp/scout-agent.new", "/tmp/scout-agent.service", "/tmp/scout-agent.invitation", true, true)
	if err != nil {
		t.Fatal(err)
	}
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("fixed install command is not valid bash: %v\n%s", err, output)
	}
}

func TestUninstallRequiresExplicitRemoteConfirmation(t *testing.T) {
	transport := &fakeSSH{output: []byte("scout-uninstall-confirmed\n")}
	result, err := Uninstall(context.Background(), transport, UninstallRequest{Target: "192.0.2.10:22", Username: "scout"})
	if err != nil || !result.Removed || !result.Confirmed {
		t.Fatalf("uninstall was not confirmed: %+v %v", result, err)
	}
	if !strings.Contains(transport.commands[0], "rm -f /usr/local/libexec/scout-agent") {
		t.Fatal("uninstall command was not fixed to the Scout executable")
	}
	transport = &fakeSSH{}
	if _, err := Uninstall(context.Background(), transport, UninstallRequest{Target: "192.0.2.10:22", Username: "scout"}); err == nil {
		t.Fatal("unconfirmed uninstall was reported as successful")
	}
}
