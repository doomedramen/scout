package discovery

import (
	"fmt"
	"regexp"
	"strings"

	"scout.local/scout/internal/store"
)

var scanEntryPointIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// DefaultScanCatalog returns the only access-capable entry point enabled for
// new scan policies. Other TCP ports can be added as observation-only points.
func DefaultScanCatalog() []store.ScanEntryPoint {
	return []store.ScanEntryPoint{{
		ID:           "ssh-default",
		Name:         "SSH",
		Transport:    store.ScanTransportTCP,
		Port:         22,
		AccessMethod: store.ScanAccessSSH,
		Enabled:      true,
	}}
}

// ValidateScanCatalog checks that entry points stay within Scout's bounded,
// TCP-only discovery contract. It does not infer access from an open port.
func ValidateScanCatalog(entries []store.ScanEntryPoint) error {
	if len(entries) == 0 || len(entries) > 64 {
		return fmt.Errorf("%w: entry point count", store.ErrInvalid)
	}
	seenIDs := map[string]bool{}
	seenEndpoints := map[string]bool{}
	enabled := 0
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		name := strings.TrimSpace(entry.Name)
		if id == "" || len(id) > 64 || !scanEntryPointIDPattern.MatchString(id) || seenIDs[id] {
			return fmt.Errorf("%w: entry point id", store.ErrInvalid)
		}
		if name == "" || len(name) > 64 {
			return fmt.Errorf("%w: entry point name", store.ErrInvalid)
		}
		if entry.Transport != store.ScanTransportTCP || entry.Port < 1 || entry.Port > 65535 {
			return fmt.Errorf("%w: entry point transport or port", store.ErrInvalid)
		}
		if entry.AccessMethod != "" && entry.AccessMethod != store.ScanAccessSSH {
			return fmt.Errorf("%w: entry point access method", store.ErrInvalid)
		}
		endpoint := fmt.Sprintf("%s/%d", entry.Transport, entry.Port)
		if seenEndpoints[endpoint] {
			return fmt.Errorf("%w: duplicate endpoint", store.ErrInvalid)
		}
		seenIDs[id] = true
		seenEndpoints[endpoint] = true
		if entry.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return fmt.Errorf("%w: no enabled entry point", store.ErrInvalid)
	}
	return nil
}

// ScanCatalogEntry resolves one policy entry point by its stable identifier.
func ScanCatalogEntry(entries []store.ScanEntryPoint, id string) (store.ScanEntryPoint, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return store.ScanEntryPoint{}, false
}
