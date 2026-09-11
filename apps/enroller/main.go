package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"scout.local/scout/internal/enrollment"
)

func main() {
	server := flag.String("server", os.Getenv("SCOUT_SERVER_URL"), "Scout server URL")
	token := flag.String("worker-token", os.Getenv("SCOUT_WORKER_TOKEN"), "worker token; prefer SCOUT_WORKER_TOKEN")
	username := flag.String("user", "scout", "target SSH user")
	knownHosts := flag.String("known-hosts", os.Getenv("SCOUT_KNOWN_HOSTS_FILE"), "known_hosts file")
	artifactPath := flag.String("agent-artifact", os.Getenv("SCOUT_AGENT_ARTIFACT_FILE"), "verified agent artifact")
	servicePath := flag.String("service-unit", "packaging/linux/agent.service", "agent systemd unit")
	version := flag.String("agent-version", os.Getenv("SCOUT_AGENT_VERSION"), "artifact version")
	once := flag.Bool("once", false, "claim at most one job")
	interval := flag.Duration("poll-interval", 5*time.Second, "idle claim interval")
	flag.Parse()
	if *server == "" || *token == "" || *knownHosts == "" || *artifactPath == "" || *version == "" {
		fatal(errors.New("server, worker token, known_hosts, signed agent artifact, and version are required"))
	}
	artifact, err := os.ReadFile(*artifactPath)
	if err != nil {
		fatal(fmt.Errorf("read signed agent artifact: %w", err))
	}
	sum := sha256.Sum256(artifact)
	serviceUnit, err := os.ReadFile(*servicePath)
	if err != nil {
		fatal(fmt.Errorf("read agent service unit: %w", err))
	}
	serviceUnit, err = renderServiceUnit(serviceUnit, *server)
	if err != nil {
		fatal(fmt.Errorf("render agent service unit: %w", err))
	}
	worker := &enrollment.RemoteWorker{ServerURL: *server, Token: *token}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		job, claimErr := worker.Claim(ctx)
		cancel()
		if claimErr != nil {
			fatal(claimErr)
		}
		if job != nil {
			if err := execute(context.Background(), worker, *job, *username, *knownHosts, *version, artifact, "sha256:"+hex.EncodeToString(sum[:]), serviceUnit); err != nil {
				fatal(err)
			}
			if *once {
				return
			}
			continue
		}
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

func renderServiceUnit(template []byte, server string) ([]byte, error) {
	parsed, err := url.Parse(server)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.ContainsAny(server, "\r\n\"") {
		return nil, errors.New("server URL must be an http(s) URL without control characters or quotes")
	}
	if !strings.Contains(string(template), "__SCOUT_SERVER_URL__") {
		return nil, errors.New("agent service unit is missing the server URL placeholder")
	}
	return []byte(strings.ReplaceAll(string(template), "__SCOUT_SERVER_URL__", server)), nil
}

func execute(ctx context.Context, worker *enrollment.RemoteWorker, job enrollment.ClaimedJob, username, knownHosts, version string, artifact []byte, artifactHash string, serviceUnit []byte) error {
	if err := worker.Progress(ctx, job, "connecting", map[string]string{}); err != nil {
		return err
	}
	credential, err := worker.Redeem(ctx, job)
	if err != nil {
		_ = worker.Progress(ctx, job, "failed", map[string]string{"code": "credential_unavailable"})
		return err
	}
	transport, err := enrollment.DialSSH(ctx, enrollment.SSHOptions{Address: job.Destination, Username: username, PrivateKeyPEM: []byte(credential), KnownHostsFile: knownHosts})
	if err != nil {
		_ = worker.Progress(ctx, job, "failed", map[string]string{"code": "connectivity"})
		return err
	}
	if err := worker.Progress(ctx, job, "installing", map[string]string{}); err != nil {
		transport.Close()
		return err
	}
	if _, err := enrollment.Install(ctx, transport, enrollment.InstallRequest{Target: job.Destination, Username: username, Version: version, Artifact: artifact, ArtifactHash: artifactHash, ServiceUnit: serviceUnit}); err != nil {
		_ = worker.Progress(ctx, job, "failed", map[string]string{"code": "installation"})
		return err
	}
	if err := worker.Progress(ctx, job, "verifying", map[string]string{}); err != nil {
		return err
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		confirmed, confirmErr := worker.Confirmed(ctx, job)
		if confirmErr == nil && confirmed {
			return worker.Progress(ctx, job, "enrolled", map[string]string{"code": "agent_confirmed"})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	_ = worker.Progress(ctx, job, "failed", map[string]string{"code": "agent_confirmation_timeout"})
	return errors.New("installed agent did not confirm")
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "scout-enroller:", err)
	os.Exit(1)
}
