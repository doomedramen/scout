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
	Address         string
	Username        string
	Password        string
	PrivateKeyPEM   []byte
	KnownHostsFile  string
	HostKeyCallback ssh.HostKeyCallback
	Timeout         time.Duration
}

const (
	SSHAuthMethodPassword   = "password"
	SSHAuthMethodPrivateKey = "private_key"
)

var ErrHostKeyMismatch = errors.New("SSH host key does not match the trusted fingerprint")
var ErrSSHConnectivity = errors.New("SSH target could not be reached")
var ErrSSHAuthentication = errors.New("SSH authentication failed")

func NormalizeSSHAuthMethod(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return SSHAuthMethodPrivateKey, true
	}
	switch value {
	case SSHAuthMethodPassword, SSHAuthMethodPrivateKey:
		return value, true
	default:
		return "", false
	}
}

// FingerprintHostKeyCallback makes an owner-supplied fingerprint usable by
// the server-local enrollment worker without creating a known_hosts file.
func FingerprintHostKeyCallback(expected string) ssh.HostKeyCallback {
	expected = strings.TrimSpace(expected)
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if key == nil || expected == "" || !strings.EqualFold(ssh.FingerprintSHA256(key), expected) {
			return ErrHostKeyMismatch
		}
		return nil
	}
}

type sshTransport struct {
	client *ssh.Client
}

func DialSSH(ctx context.Context, options SSHOptions) (SSHTransport, error) {
	if err := ValidateTarget(options.Address); err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.Username) == "" || len(options.PrivateKeyPEM) == 0 && options.Password == "" || options.KnownHostsFile == "" && options.HostKeyCallback == nil {
		return nil, errors.New("SSH identity and host trust are required")
	}
	authMethods := make([]ssh.AuthMethod, 0, 2)
	if len(options.PrivateKeyPEM) > 0 {
		signer, err := ssh.ParsePrivateKey(options.PrivateKeyPEM)
		if err != nil {
			return nil, ErrSSHAuthentication
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if options.Password != "" {
		authMethods = append(authMethods, ssh.Password(options.Password))
	}
	var err error
	hostKeyCallback := options.HostKeyCallback
	var callbackErr error
	if hostKeyCallback != nil {
		callback := hostKeyCallback
		hostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			callbackErr = callback(hostname, remote, key)
			return callbackErr
		}
	}
	if hostKeyCallback == nil {
		hostKeyCallback, err = knownhosts.New(options.KnownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("load known_hosts: %w", err)
		}
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if timeout > 60*time.Second {
		return nil, errors.New("SSH timeout exceeds limit")
	}
	config := &ssh.ClientConfig{User: options.Username, Auth: authMethods, HostKeyCallback: hostKeyCallback, Timeout: timeout}
	dialer := net.Dialer{Timeout: timeout}
	connection, err := dialer.DialContext(ctx, "tcp", options.Address)
	if err != nil {
		return nil, ErrSSHConnectivity
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, options.Address, config)
	if err != nil {
		_ = connection.Close()
		if errors.Is(callbackErr, ErrHostKeyMismatch) {
			return nil, ErrHostKeyMismatch
		}
		if strings.Contains(strings.ToLower(err.Error()), "unable to authenticate") {
			return nil, ErrSSHAuthentication
		}
		return nil, errors.New("SSH handshake failed")
	}
	return &sshTransport{client: ssh.NewClient(clientConnection, channels, requests)}, nil
}

func (t *sshTransport) Upload(ctx context.Context, path string, data []byte) error {
	if t == nil || t.client == nil || (path != "/tmp/scout-agent.new" && path != "/tmp/scout-agent.service" && path != "/tmp/scout-agent.invitation") {
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
