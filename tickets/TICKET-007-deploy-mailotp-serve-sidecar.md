---
id: TICKET-007
title: "Deploy mailotp serve as a sidecar in the scopeweaver stack"
status: New          # New | To-Do | WIP | Feedback | Done
area: ops, agent
priority: high
blocked_by: operator go-ahead for the live deploy (and the build-source decision)
owner: psylinux
branch: none
pr: none
source: operator request (split the pending TICKET-006 Fase 1b deploy items into their own tickets, 2026-11-24)
---

## Goal

`mailotp serve` runs as a supervised sidecar inside the live scopeweaver
container, started by `start.sh` and built into the image, deployed reproducibly
through the recon-server Ansible role. Agents reach it over the unix socket; only
the daemon talks to Gmail. Done when the running container hosts the daemon,
`query`/`fetch` work against the synced corpus, and the store survives a recreate.

## Why / source

TICKET-006 Fase 1 built and tested the daemon + clients in `psylinux/mailotp`
(commit `de88d32`), but nothing is deployed: the live image still ships only the
one-shot `cmd/mailotp` helper (TICKET-005, image authfix.7). This ticket is the
cutover.

## Scope (files likely to change)

- `start.sh` - launch `mailotp serve` in the background, restart it on exit.
- `Dockerfile.hardened` / `Dockerfile` - build and install the serve binary.
- recon-server `ansible/roles/scopeweaver` - image tag bump and any wiring
  (cross-repo: `psylinux/recon-server`, live branch master).

## Acceptance

- [ ] `mailotp serve` runs as a supervised process in the container (restarts on
      exit), reads creds from the mounted `secrets.yaml`, listens on
      `/app/data/mailotp/mailotp.sock`, exposes `:9469/metrics`.
- [ ] Store at `/app/data/mailotp/store.db` on the `sw_data` named volume;
      survives a container recreate (verified).
- [ ] The build source is decided and documented: either sync the standalone
      `psylinux/mailotp` code into `cmd/mailotp`, or have the Docker build pull the
      private module with build-time auth. Pick one and record why.
- [ ] Image built reproducibly; recon-server Ansible bumped; deploy via
      pause/recreate/resume with postgres untouched and exactly the tasks that were
      running resumed (per the deploy procedure in memory).
- [ ] `go build ./...` and `go test ./...` green, stated in the PR.
- [ ] affected docs updated (README, this ticket, TICKET-006 remaining).
- [ ] PR opened; ticket set to `Feedback`.

## Notes / dependencies

Parent: TICKET-006. Gates TICKET-008 (Prometheus), TICKET-009 (Grafana) and
TICKET-010 (skill), which need the daemon live (and `:9469` reachable) first. The
serve code and the staged monitoring files live in `psylinux/mailotp` (cross-repo;
a scopeweaver PR cannot carry them). Open decision to resolve here: the build
source (cmd/mailotp sync vs private module).

## Log

- 2026-11-24: filed (New), split from TICKET-006; awaits operator go-ahead for the live cutover.
