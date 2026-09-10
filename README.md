# Scout

Scout is a self-hosted server, network, and device monitor. Distributed agents collect metrics and contribute observations to a shared network map. Scout discovers potential monitoring targets, explains what access is missing, and enrolls approved machines using credentials supplied by the network owner.

## Product direction

- See infrastructure health and network relationships in one interface.
- Grow monitoring coverage through policy-controlled discovery and enrollment.
- Report inaccessible machines with an actionable access request.
- Run on infrastructure you own, starting with Docker Compose and a Linux VM suitable for Proxmox VE.
- Treat credentials, agent identity, and installation permissions as core product concerns.
- Keep agents current through automatic or owner-triggered, signed updates delivered by the control server, including on networks without internet access.

Discovery is not permission to install. Owners define allowed networks and enrollment policies. Automatic enrollment is possible inside those explicit boundaries; targets outside them require approval.

## First usable milestone

One control server and one manually enrolled Linux agent, with live host metrics, health history, an inventory, and a network map that distinguishes observed relationships from inferred ones. This milestone does not collect SSH credentials or install agents remotely.

The Linux-first MVP is the selected direction. It includes server-delivered agent updates before expanding into scoped discovery, access requests, and audited SSH enrollment. The sequence is in [the roadmap](docs/roadmap.md).

## Design documents

- [Architecture](docs/architecture.md)
- [Security model](docs/security.md)
- [UI direction](docs/interface.md)
- [Agent updates](docs/agent-updates.md)
- [Roadmap and acceptance criteria](docs/roadmap.md)

## Status

Product and architecture foundation. No application, installer, or deployment manifests exist yet. Technology choices and implementation scope remain open.
