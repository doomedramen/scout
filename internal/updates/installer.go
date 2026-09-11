package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

type SlotState struct {
	ActiveSlot         string    `json:"activeSlot"`
	ActiveVersion      string    `json:"activeVersion"`
	ActiveGeneration   int64     `json:"activeGeneration"`
	ActiveDigest       string    `json:"activeDigest"`
	PreviousSlot       string    `json:"previousSlot,omitempty"`
	PreviousVersion    string    `json:"previousVersion,omitempty"`
	PreviousGeneration int64     `json:"previousGeneration,omitempty"`
	PreviousDigest     string    `json:"previousDigest,omitempty"`
	Ready              bool      `json:"ready"`
	RollbackCount      int       `json:"rollbackCount"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type journal struct {
	Stage            string `json:"stage"`
	TargetSlot       string `json:"targetSlot"`
	TargetVersion    string `json:"targetVersion"`
	TargetGeneration int64  `json:"targetGeneration"`
}

type Installer struct {
	Root string
}

func NewInstaller(root string) (*Installer, error) {
	if strings.TrimSpace(root) == "" {
		return nil, store.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Join(root, "slots"), 0o700); err != nil {
		return nil, err
	}
	return &Installer{Root: root}, nil
}

func (i *Installer) State() (SlotState, error) {
	if i == nil {
		return SlotState{}, store.ErrInvalid
	}
	data, err := os.ReadFile(filepath.Join(i.Root, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return SlotState{ActiveSlot: "a", Ready: true}, nil
	}
	if err != nil {
		return SlotState{}, err
	}
	var state SlotState
	if err := json.Unmarshal(data, &state); err != nil || (state.ActiveSlot != "a" && state.ActiveSlot != "b") {
		return SlotState{}, store.ErrInvalid
	}
	return state, nil
}

func (i *Installer) Stage(ctx context.Context, manifest Manifest, artifact []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	if int64(len(artifact)) != manifest.Bytes || ArtifactDigest(artifact) != manifest.Digest {
		return ErrTampered
	}
	state, err := i.State()
	if err != nil {
		return err
	}
	if IsDowngrade(manifest.Generation, state.ActiveGeneration) || manifest.Generation == state.ActiveGeneration && manifest.Digest != state.ActiveDigest {
		return store.ErrConflict
	}
	target := "b"
	if state.ActiveSlot == "b" {
		target = "a"
	}
	if err := writeJournal(i.Root, journal{Stage: "staging", TargetSlot: target, TargetVersion: manifest.Version, TargetGeneration: manifest.Generation}); err != nil {
		return err
	}
	if err := writeSlot(i.Root, target, artifact); err != nil {
		return err
	}
	if err := writeJournal(i.Root, journal{Stage: "staged", TargetSlot: target, TargetVersion: manifest.Version, TargetGeneration: manifest.Generation}); err != nil {
		return err
	}
	state = SlotState{ActiveSlot: target, ActiveVersion: manifest.Version, ActiveGeneration: manifest.Generation, ActiveDigest: manifest.Digest, PreviousSlot: state.ActiveSlot, PreviousVersion: state.ActiveVersion, PreviousGeneration: state.ActiveGeneration, PreviousDigest: state.ActiveDigest, Ready: false, RollbackCount: 0, UpdatedAt: time.Now().UTC()}
	if err := writeJSON(filepath.Join(i.Root, "state.json"), state); err != nil {
		return err
	}
	return writeJournal(i.Root, journal{Stage: "switched", TargetSlot: target, TargetVersion: manifest.Version, TargetGeneration: manifest.Generation})
}

func (i *Installer) MarkReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := i.State()
	if err != nil {
		return err
	}
	if state.ActiveVersion == "" || state.ActiveGeneration < 1 || !slotExists(i.Root, state.ActiveSlot) {
		return store.ErrInvalid
	}
	state.Ready = true
	state.UpdatedAt = time.Now().UTC()
	if err := writeJSON(filepath.Join(i.Root, "state.json"), state); err != nil {
		return err
	}
	return clearJournal(i.Root)
}

func (i *Installer) Recover(ctx context.Context) (SlotState, error) {
	if err := ctx.Err(); err != nil {
		return SlotState{}, err
	}
	state, err := i.State()
	if err != nil {
		return SlotState{}, err
	}
	data, err := os.ReadFile(filepath.Join(i.Root, "journal.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return SlotState{}, err
	}
	var entry journal
	if err := json.Unmarshal(data, &entry); err != nil {
		return SlotState{}, store.ErrInvalid
	}
	switch entry.Stage {
	case "staging", "staged":
		_ = os.Remove(slotPath(i.Root, entry.TargetSlot) + ".partial")
	case "switched":
		if !state.Ready && state.PreviousGeneration > 0 && slotExists(i.Root, state.PreviousSlot) {
			state.ActiveSlot = state.PreviousSlot
			state.ActiveVersion = state.PreviousVersion
			state.ActiveGeneration = state.PreviousGeneration
			state.ActiveDigest = state.PreviousDigest
			state.Ready = true
			state.RollbackCount++
			state.UpdatedAt = time.Now().UTC()
			if err := writeJSON(filepath.Join(i.Root, "state.json"), state); err != nil {
				return SlotState{}, err
			}
		}
	}
	if err := clearJournal(i.Root); err != nil {
		return SlotState{}, err
	}
	return state, nil
}

func (i *Installer) Rollback(ctx context.Context) (SlotState, error) {
	if err := ctx.Err(); err != nil {
		return SlotState{}, err
	}
	state, err := i.State()
	if err != nil {
		return SlotState{}, err
	}
	if state.RollbackCount >= 1 || state.PreviousGeneration < 1 || !slotExists(i.Root, state.PreviousSlot) {
		return SlotState{}, store.ErrConflict
	}
	state.ActiveSlot, state.PreviousSlot = state.PreviousSlot, state.ActiveSlot
	state.ActiveVersion, state.PreviousVersion = state.PreviousVersion, state.ActiveVersion
	state.ActiveGeneration, state.PreviousGeneration = state.PreviousGeneration, state.ActiveGeneration
	state.ActiveDigest, state.PreviousDigest = state.PreviousDigest, state.ActiveDigest
	state.Ready = true
	state.RollbackCount++
	state.UpdatedAt = time.Now().UTC()
	if err := writeJSON(filepath.Join(i.Root, "state.json"), state); err != nil {
		return SlotState{}, err
	}
	if err := clearJournal(i.Root); err != nil {
		return SlotState{}, err
	}
	return state, nil
}

func (i *Installer) ActivePath() (string, error) {
	state, err := i.State()
	if err != nil {
		return "", err
	}
	if !slotExists(i.Root, state.ActiveSlot) {
		return "", fmt.Errorf("active slot unavailable: %w", store.ErrNotFound)
	}
	return slotPath(i.Root, state.ActiveSlot), nil
}

func slotPath(root, slot string) string { return filepath.Join(root, "slots", slot, "scout-agent") }

func slotExists(root, slot string) bool {
	if slot != "a" && slot != "b" {
		return false
	}
	info, err := os.Stat(slotPath(root, slot))
	return err == nil && !info.IsDir()
}

func writeSlot(root, slot string, artifact []byte) error {
	if slot != "a" && slot != "b" {
		return store.ErrInvalid
	}
	directory := filepath.Dir(slotPath(root, slot))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".agent-*.partial")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o700); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(artifact); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, slotPath(root, slot)); err != nil {
		return err
	}
	return syncDir(directory)
}

func writeJournal(root string, entry journal) error {
	return writeJSON(filepath.Join(root, "journal.json"), entry)
}

func clearJournal(root string) error {
	if err := os.Remove(filepath.Join(root, "journal.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeJSON(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".json-*.partial")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := json.NewEncoder(temporary).Encode(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDir(directory)
}

func syncDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
