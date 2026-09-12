package enrollment

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestDialSSHAuthenticatesWithPassword(t *testing.T) {
	listener, serverConfig := passwordSSHServer(t, "fixture-password")
	defer listener.Close()

	expectedFingerprint := ssh.FingerprintSHA256(serverConfig.hostKey.PublicKey())
	seenFingerprint := false
	transport, err := DialSSH(context.Background(), SSHOptions{
		Address:  listener.Addr().String(),
		Username: "fixture",
		Password: "fixture-password",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if ssh.FingerprintSHA256(key) != expectedFingerprint {
				return ErrHostKeyMismatch
			}
			seenFingerprint = true
			return nil
		},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("password SSH dial failed: %v", err)
	}
	if !seenFingerprint {
		t.Fatal("password SSH dial did not verify the host fingerprint")
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("close SSH transport: %v", err)
	}
}

func TestDialSSHRejectsWrongPassword(t *testing.T) {
	listener, _ := passwordSSHServer(t, "fixture-password")
	defer listener.Close()

	_, err := DialSSH(context.Background(), SSHOptions{
		Address:  listener.Addr().String(),
		Username: "fixture",
		Password: "wrong-password",
		HostKeyCallback: func(_ string, _ net.Addr, _ ssh.PublicKey) error {
			return nil
		},
		Timeout: time.Second,
	})
	if !errors.Is(err, ErrSSHAuthentication) {
		t.Fatalf("wrong password error = %v, want ErrSSHAuthentication", err)
	}
}

type passwordSSHServerConfig struct {
	hostKey ssh.Signer
}

func passwordSSHServer(t *testing.T, expectedPassword string) (net.Listener, passwordSSHServerConfig) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate SSH host key: %v", err)
	}
	hostKey, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("create SSH host signer: %v", err)
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(connection ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if connection.User() != "fixture" || string(password) != expectedPassword {
				return nil, errors.New("invalid fixture password")
			}
			return nil, nil
		},
	}
	config.AddHostKey(hostKey)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for SSH test server: %v", err)
	}
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		server, channels, requests, handshakeErr := ssh.NewServerConn(connection, config)
		if handshakeErr != nil {
			_ = connection.Close()
			return
		}
		go ssh.DiscardRequests(requests)
		for channel := range channels {
			_ = channel.Reject(ssh.Prohibited, "test does not open channels")
		}
		_ = server.Close()
	}()
	return listener, passwordSSHServerConfig{hostKey: hostKey}
}
