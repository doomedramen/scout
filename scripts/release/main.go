package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"scout.local/scout/internal/updates"
)

func main() {
	artifactPath := flag.String("artifact", "", "Linux agent artifact")
	privateKeyPath := flag.String("private-key", "", "publisher Ed25519 private key; never deploy this file")
	keyID := flag.String("key-id", "", "trusted publisher key identifier")
	version := flag.String("version", "", "semantic agent version")
	generation := flag.Int64("generation", 0, "monotonic release generation")
	architecture := flag.String("architecture", "amd64", "linux architecture")
	output := flag.String("output", "", "signed bundle output")
	flag.Parse()
	if *artifactPath == "" || *privateKeyPath == "" || *keyID == "" || *version == "" || *generation < 1 || *output == "" {
		flag.Usage()
		os.Exit(2)
	}
	artifact, err := os.ReadFile(filepath.Clean(*artifactPath))
	if err != nil {
		fatal(err)
	}
	privateKey, err := loadPrivateKey(filepath.Clean(*privateKeyPath))
	if err != nil {
		fatal(err)
	}
	manifest, err := updates.SignManifest(updates.Manifest{SchemaVersion: updates.ManifestSchemaVersion, Version: *version, Generation: *generation, Platform: "linux", Architecture: *architecture, Digest: updates.ArtifactDigest(artifact), Bytes: int64(len(artifact)), ProtocolMin: 1, ProtocolMax: 1}, privateKey, *keyID)
	if err != nil {
		fatal(err)
	}
	bundle, err := updates.EncodeBundle(manifest, artifact)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o700); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Clean(*output), bundle, 0o600); err != nil {
		fatal(err)
	}
	fmt.Printf("signed Scout release %s generation %d\n", manifest.Version, manifest.Generation)
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(data), nil
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("publisher key must be raw Ed25519 or PKCS8 PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("publisher key is not Ed25519")
	}
	return privateKey, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "scout-release:", err)
	os.Exit(1)
}
