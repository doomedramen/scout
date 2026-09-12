package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	runtimeagent "scout.local/scout/internal/agent"
	"scout.local/scout/internal/collector"
	"scout.local/scout/internal/store"
)

func main() {
	if len(os.Args) == 1 {
		printSnapshot()
		return
	}
	daemon := flag.Bool("daemon", false, "run the persistent monitoring agent")
	server := flag.String("server", os.Getenv("SCOUT_SERVER_URL"), "Scout server URL")
	invitationFile := flag.String("invitation-file", os.Getenv("SCOUT_INVITATION_FILE"), "file containing the one-time bootstrap invitation")
	dataDir := flag.String("data-dir", os.Getenv("SCOUT_AGENT_DATA_DIR"), "agent data directory")
	root := flag.String("root", "/", "host filesystem root, primarily for tests")
	interval := flag.Duration("interval", 15*time.Second, "report interval")
	releaseTrustFile := flag.String("release-trust-file", os.Getenv("SCOUT_RELEASE_TRUST_FILE"), "trusted release public keys")
	scanProtocolVersion := flag.Int("scan-protocol-version", 1, "supported Scout scan protocol version")
	scanTransport := flag.String("scan-transport", store.ScanTransportTCP, "supported Scout scan transport")
	flag.Parse()
	if !*daemon {
		printSnapshot()
		return
	}
	runtime, err := runtimeagent.NewRuntime(runtimeagent.Config{ServerURL: *server, InvitationFile: *invitationFile, DataDir: *dataDir, Root: *root, Interval: *interval, ReleaseTrustFile: *releaseTrustFile, ScanCapabilities: store.ScanCapabilities{ScanProtocolVersions: []int{*scanProtocolVersion}, ScanTransports: []string{*scanTransport}}})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runtime.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func printSnapshot() {
	snapshot, err := collector.Collect()
	if err != nil {
		log.Fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		log.Fatal(err)
	}
}
