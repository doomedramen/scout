package enrollment

import (
	"context"
	"errors"
	"strings"
	"time"
)

type UninstallRequest struct {
	Target   string
	Username string
}

type UninstallResult struct {
	Target    string    `json:"target"`
	Removed   bool      `json:"removed"`
	Confirmed bool      `json:"confirmed"`
	Completed time.Time `json:"completedAt"`
}

// Uninstall removes only the Scout service and executable. Agent data is left
// in place so server-side history and local forensic material are not erased.
// The caller must request this separately from decommissioning.
func Uninstall(ctx context.Context, transport SSHTransport, request UninstallRequest) (UninstallResult, error) {
	if transport == nil {
		return UninstallResult{}, errors.New("SSH transport is required")
	}
	if err := ValidateTarget(request.Target); err != nil {
		return UninstallResult{}, err
	}
	if strings.TrimSpace(request.Username) == "" {
		return UninstallResult{}, errors.New("uninstall user is required")
	}
	defer transport.Close()
	output, err := transport.Run(ctx, fixedUninstallCommand())
	if err != nil {
		return UninstallResult{Target: request.Target}, err
	}
	if !strings.Contains(string(output), "scout-uninstall-confirmed") {
		return UninstallResult{Target: request.Target}, errors.New("uninstall confirmation was not returned")
	}
	return UninstallResult{Target: request.Target, Removed: true, Confirmed: true, Completed: time.Now().UTC()}, nil
}

func fixedUninstallCommand() string {
	return "set -eu; if command -v systemctl >/dev/null 2>&1; then if systemctl is-enabled --quiet scout-agent.service || systemctl is-active --quiet scout-agent.service; then systemctl disable --now scout-agent.service; fi; rm -f /etc/systemd/system/scout-agent.service; systemctl daemon-reload; fi; rm -f /usr/local/libexec/scout-agent; test ! -e /usr/local/libexec/scout-agent; printf 'scout-uninstall-confirmed\\n'"
}
