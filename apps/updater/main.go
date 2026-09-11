package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"scout.local/scout/internal/updates"
)

func main() {
	root := flag.String("root", "/var/lib/scout/updater", "private updater state directory")
	artifactPath := flag.String("artifact", "", "verified artifact path to stage")
	manifestPath := flag.String("manifest", "", "signed manifest JSON path")
	trustPath := flag.String("trust-file", "", "release public-key trust file")
	recover := flag.Bool("recover", false, "recover an interrupted switch")
	ready := flag.Bool("ready", false, "mark the active slot ready after health checks")
	rollback := flag.Bool("rollback", false, "perform the single bounded rollback")
	flag.Parse()

	installer, err := updates.NewInstaller(filepath.Clean(*root))
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()
	switch {
	case *recover:
		state, recoverErr := installer.Recover(ctx)
		if recoverErr != nil {
			fatal(recoverErr)
		}
		printJSON(state)
	case *ready:
		if err := installer.MarkReady(ctx); err != nil {
			fatal(err)
		}
	case *rollback:
		state, rollbackErr := installer.Rollback(ctx)
		if rollbackErr != nil {
			fatal(rollbackErr)
		}
		printJSON(state)
	case *artifactPath != "" || *manifestPath != "" || *trustPath != "":
		if *artifactPath == "" || *manifestPath == "" || *trustPath == "" {
			fatal(fmt.Errorf("artifact, manifest, and trust-file must be supplied together"))
		}
		artifact, readErr := os.ReadFile(filepath.Clean(*artifactPath))
		if readErr != nil {
			fatal(readErr)
		}
		manifestData, readErr := os.ReadFile(filepath.Clean(*manifestPath))
		if readErr != nil {
			fatal(readErr)
		}
		var manifest updates.Manifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			fatal(err)
		}
		trust, err := updates.LoadTrustFile(filepath.Clean(*trustPath))
		if err != nil {
			fatal(err)
		}
		if err := manifest.Verify(artifact, trust); err != nil {
			fatal(err)
		}
		if err := installer.Stage(ctx, manifest, artifact); err != nil {
			fatal(err)
		}
		state, err := installer.State()
		if err != nil {
			fatal(err)
		}
		printJSON(state)
	default:
		state, stateErr := installer.State()
		if stateErr != nil {
			fatal(stateErr)
		}
		printJSON(state)
	}
}

func printJSON(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "scout-updater:", err)
	os.Exit(1)
}
