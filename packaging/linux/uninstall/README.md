# Scout Linux uninstall boundary

The optional uninstaller is intentionally separate from decommissioning. The
control plane revokes the device identity and persists its exclusion first;
only an owner-authorized runner may call `internal/enrollment.Uninstall` over
verified SSH.

The fixed operation disables `scout-agent.service`, removes the installed
agent unit and executable, reloads systemd, and requires an explicit
`scout-uninstall-confirmed` marker before reporting removal. It leaves
`/var/lib/scout/agent` intact for local recovery and forensics. An offline or
partially completed operation is reported as unconfirmed, never as removed.
