package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"scout.local/scout/internal/store"
)

func Download(ctx context.Context, client *http.Client, endpoint, destination, digest string, bytes int64, bearer string) error {
	if client == nil {
		client = &http.Client{}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return store.ErrInvalid
	}
	if bytes < 1 || bytes > MaxArtifactBytes || !strings.HasPrefix(digest, "sha256:") {
		return store.ErrInvalid
	}
	if destination == "" || filepath.Base(destination) == "." || filepath.Base(destination) == string(filepath.Separator) {
		return store.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	partial := destination + ".part"
	var offset int64
	if info, statErr := os.Stat(partial); statErr == nil {
		offset = info.Size()
		if offset >= bytes {
			_ = os.Remove(partial)
			offset = 0
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		request.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if offset > 0 && response.StatusCode == http.StatusOK {
		offset = 0
		if err := os.Truncate(partial, 0); err != nil {
			return err
		}
	}
	if (offset == 0 && response.StatusCode != http.StatusOK) || (offset > 0 && response.StatusCode != http.StatusPartialContent) {
		return fmt.Errorf("artifact download returned HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		file.Close()
		return err
	}
	written, copyErr := io.CopyN(file, response.Body, bytes-offset+1)
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		file.Close()
		return copyErr
	}
	if written > bytes-offset {
		file.Close()
		return store.ErrBackpressure
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	info, err := os.Stat(partial)
	if err != nil || info.Size() != bytes {
		return fmt.Errorf("artifact size mismatch: %w", ErrTampered)
	}
	hash, err := hashFile(partial)
	if err != nil {
		return err
	}
	if hash != digest {
		_ = os.Remove(partial)
		return ErrTampered
	}
	if err := os.Rename(partial, destination); err != nil {
		return err
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, MaxArtifactBytes+1)); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
