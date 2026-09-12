# Collector fixtures

These payloads are deterministic, secret-free responses used by the Docker,
Proxmox, SMART, ZFS, and fake third-provider tests. They are not recordings of
a live provider and do not establish compatibility with a specific Docker
Engine, Proxmox VE, smartmontools, or OpenZFS release. ZFS counter fixtures
represent cumulative Linux kstat values; the adapter treats a decrease as an
explicit reset gap.
