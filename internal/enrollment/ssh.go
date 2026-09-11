package enrollment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SSHOptions struct {
	Address        string
	Username       string
	PrivateKeyPEM  []byte
	KnownHostsFile string
	Timeout        time.Duration
}

type sshTransport struct {
	client *ssh.Client
}

func DialSSH(ctx context.Context, options SSHOptions) (SSHTransport, error) {
	if err := ValidateTarget(options.Address); err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.Username) == "" || len(options.PrivateKeyPEM) == 0 || options.KnownHostsFile == "" {
		return nil, errors.New("SSH identity and known_hosts are required")
	}
	signer, err := ssh.ParsePrivateKey(options.PrivateKeyPEM)
	if err != nil {
		return nil, errors.New("SSH private key is invalid")
	}
	hostKeyCallback, err := knownhosts.New(options.KnownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if timeout > 60*time.Second {
		return nil, errors.New("SSH timeout exceeds limit")
	}
	config := &ssh.ClientConfig{User: options.Username, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: hostKeyCallback, Timeout: timeout}
	dialer := net.Dialer{Timeout: timeout}
	connection, err := dialer.DialContext(ctx, "tcp", options.Address)
	if err != nil {
		return nil, errors.New("SSH connection failed")
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, options.Address, config)
	if err != nil {
		_ = connection.Close()
		return nil, errors.New("SSH handshake failed")
	}
	return &sshTransport{client: ssh.NewClient(clientConnection, channels, requests)}, nil
}

func (t *sshTransport) Upload(ctx context.Context, path string, data []byte) error {
	if t == nil || t.client == nil || (path != "/var/lib/scout/agent/scout-agent.new" && path != "/etc/systemd/system/scout-agent.service") {
		return errors.New("upload path is not permitted")
	}
	session, err := t.client.NewSession()
	if err != nil {
		return errors.New("SSH session unavailable")
	}
	defer session.Close()
	var stderr bytes.Buffer
	session.Stderr = &stderr
	stdin, err := session.StdinPipe()
	if err != nil {
		return errors.New("SSH upload unavailable")
	}
	command := "umask 077; cat > " + shellQuote(path)
	if err := session.Start(command); err != nil {
		return errors.New("SSH upload start failed")
	}
	result := make(chan error, 1)
	go func() {
		_, writeErr := stdin.Write(data)
		_ = stdin.Close()
		waitErr := session.Wait()
		if writeErr != nil {
			result <- writeErr
			return
		}
		result <- waitErr
	}()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	case err := <-result:
		if err != nil {
			return errors.New("SSH upload failed")
		}
		return nil
	}
}

func (t *sshTransport) Run(ctx context.Context, command string) ([]byte, error) {
	if t == nil || t.client == nil || strings.TrimSpace(command) == "" {
		return nil, errors.New("SSH command is required")
	}
	session, err := t.client.NewSession()
	if err != nil {
		return nil, errors.New("SSH session unavailable")
	}
	defer session.Close()
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Start(command); err != nil {
		return nil, errors.New("SSH command start failed")
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return nil, ctx.Err()
	case err := <-done:
		if err != nil {
			return nil, errors.New("SSH command failed")
		}
		return stdout.Bytes(), nil
	}
}

func (t *sshTransport) Close() error {
	if t == nil || t.client == nil {
		return nil
	}
	return t.client.Close()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func LoadPrivateKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
