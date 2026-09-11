# Confirmed product decisions

- One agent per device. After initial bootstrap, Scout automatically installs agents on eligible devices within owner-configured scopes and continues discovery from those new vantage points. Missing access returns to the owner; routine per-device approval is not required.
- Linux first, macOS second, Windows third.
- A single owner for the initial release. Multi-user roles and customer isolation are deferred.
- Go control server and agent, PostgreSQL, React with TypeScript, shadcn/ui (including Recharts-backed shadcn chart components), and Turborepo. npm workspaces host the web application and Go task wrappers; a shared Go module holds backend code.
- Agent updates support automatic and manual rollout through the server, including machines without internet access.
- An extensible collector framework supports hypervisors, container runtimes, and other services. Docker and Proxmox are examples of initial adapters, not the limits of the architecture.
- The supplied Beszel screenshots guide visual density: restrained dark surfaces, compact tables, inline resource bars, and host-specific charts. Topology remains a separate view alongside inventory.

Fleet size has not been specified. One agent per device defines placement, not a capacity target. Do not claim a supported fleet size before measurement.
