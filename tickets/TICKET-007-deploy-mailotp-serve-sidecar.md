---
id: TICKET-007
title: "Deploy mailotp serve as a sidecar in the scopeweaver stack"
status: Feedback     # New | To-Do | WIP | Feedback | Done
area: ops, agent
priority: high
blocked_by: none
owner: psylinux
branch: v0.1.0-authfix
pr: none (direct to v0.1.0-authfix, the live-build branch; deployed and verified)
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

## Approach (Fase 1B plan)

Grounded in the current repo: `start.sh` is the ENTRYPOINT and supervises the
`scopeweaver` binary in a restart loop; the Dockerfile builds mailotp with
`CGO_ENABLED=0 go build -o /mailotp ./cmd/mailotp` and installs it at
`/usr/local/bin/mailotp`; `cmd/mailotp` imports `github.com/Autumn-27/artex/sharedsecrets`.

### Build source (operator confirmed Option A, 2026-11-24)

- **Option A (CHOSEN) - sync into `cmd/mailotp`.** Copy the Fase 1 files
  (store.go, proto.go, serve.go, client_cmds.go, metrics.go, imap.go, extract.go,
  main.go) from `psylinux/mailotp` into `cmd/mailotp`, rewriting the one import
  `github.com/psylinux/mailotp/internal/sharedsecrets` ->
  `github.com/Autumn-27/artex/sharedsecrets` (same API; the standalone copy derives
  from it), and add `modernc.org/sqlite` + `prometheus/client_golang` to the
  scopeweaver go.mod. The existing Dockerfile line is unchanged and needs no
  build-time auth; modernc is pure Go so `CGO_ENABLED=0` keeps working. Source of
  truth stays `psylinux/mailotp`; `cmd/mailotp` is a generated mirror refreshed by
  a small `scripts/sync-mailotp.sh` at each release.
- **Option B - pull the private module** (`go install github.com/psylinux/mailotp@<rev>`),
  needing GOPRIVATE + a deploy key/token as a BuildKit secret. Single source of
  truth, but adds build-time auth and risk to the hardened build. Not recommended.

### Sidecar (start.sh)

Launch `mailotp serve` as a supervised background process before the app loop
(restart on exit, kill on INT/TERM alongside the app). It reads creds from the
already-mounted `/root/.config/bugbounty/secrets.yaml`, creates `/app/data/mailotp/`,
listens on the socket there and exposes `:9469`. No ordering dependency:
`fetch --mailbox NN` falls back to a direct read until the daemon is up.

### Image + Ansible

The `cmd/mailotp` change rebuilds the baked binary with no Dockerfile change beyond
the new go.mod deps. Bump `scopeweaver_image` (authfix.8) and `scopeweaver_version`
in recon-server `ansible/roles/scopeweaver`; the image builds-when-missing, so the
deploy produces it reproducibly. Keep authfix.7 for rollback.

### Metrics exposure

`:9469` is reached by Prometheus over the `scopeweaver_default` docker network by
container name; no host port publish needed (TICKET-008 adds the scrape).

### Cutover (the proven procedure)

1. Note the running tasks (paused=false; today 13/15/16).
2. Pause them via the API.
3. `docker compose up -d` to recreate scopeweaver with the new image (postgres and
   `sw_data` untouched; the mailotp store starts empty and backfills in seconds).
4. Verify: `mailotp mailboxes` shows synced counts, `/metrics` responds, and the
   `email-otp` skill is the serve-model version (TICKET-010).
5. Resume exactly the tasks from step 1.
6. Confirm an agent can `fetch`/`query`.

Ships together with TICKET-010 (skill) in the same image. Rollback: recreate with
authfix.7 (direct-fetch helper) and the previous skill.

### Risks

- The daemon holds 2 persistent IMAP connections (vs the per-agent churn it
  replaces) - well under Gmail's ~15 cap.
- A serve crash: start.sh restarts it and agents fall back to a direct fetch
  meanwhile; no hard dependency.
- Store on `sw_data` survives a recreate; first start backfills ~50/folder
  (~2s observed in the Fase 1 smoke).

## Notes / dependencies

Parent: TICKET-006. Gates TICKET-008 (Prometheus), TICKET-009 (Grafana) and
TICKET-010 (skill), which need the daemon live (and `:9469` reachable) first. The
serve code and the staged monitoring files live in `psylinux/mailotp` (cross-repo;
a scopeweaver PR cannot carry them). Open decision to resolve here: the build
source (cmd/mailotp sync vs private module).

## Log

- 2026-11-24: filed (New), split from TICKET-006; awaits operator go-ahead for the live cutover.
- 2026-11-24: operator confirmed build source Option A (sync into cmd/mailotp). Only the go-ahead for the live cutover remains.
- 2026-11-24: implemented (Option A) and DEPLOYED. Synced sources into cmd/mailotp
  via scripts/sync-mailotp.sh; go build ./... and go test ./cmd/mailotp green;
  start.sh runs the serve sidecar; image built as authfix.8; scopeweaver
  v0.1.0-authfix at 6b232d2; recon-server defaults bumped (4a2a248). Cutover:
  paused 13/15/16, `docker compose up -d` recreated the container (postgres +
  sw_data untouched), resumed exactly 13/15/16. Verified live: container healthy,
  serve sidecar up (pid 8), store+socket on /app/data/mailotp (sw_data), both
  pollers synced (01=20, 02=15), /metrics serves 32 series on :9469, and an
  agent-style alloc -> query --handle -> release worked in the container. Ships
  with TICKET-010 (skill). Status Feedback: awaiting operator sign-off (no PR;
  direct-to-branch deploy mode). TICKET-008/009 (Prometheus/Grafana wiring) are now
  actionable (the daemon is live on :9469).
