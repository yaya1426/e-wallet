# TDR-015 — Dockerfile and docker-compose.yml at the Repository Root

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-04) |
| Date       | 2026-11-04 |
| Deciders   | Yahya Elaraby |
| Related    | Supersedes the `deployments/` location in TDR-012 §1 |

## Context
TDR-012 put the Dockerfile and docker-compose.yml under `deployments/`. DigitalOcean App
Platform only detects a Dockerfile at the root (or at an explicit path). It did not find
ours, so it fell back to a Buildpack build and picked the wrong binary (`bin/banksim`).

## Decision
- `Dockerfile` and `docker-compose.yml` live at the **repository root**. The `deployments/` folder is removed.
- The Dockerfile's `ARG APP` defaults to **`wallet`**, so any platform building the root
  Dockerfile without build arguments produces the wallet service. Compose still passes
  `APP: banksim` for the simulator.
- `make` targets, CI and the verification guide use the root files (`docker compose` default file name).

## Consequences
- + Zero-config detection by App Platform and similar PaaS builders.
- − Two more files at the root.
