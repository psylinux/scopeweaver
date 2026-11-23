---
id: TICKET-001
title: "Watcher: survive a reboot with a systemd timer and a single-pass mode"
status: New
area: watcher
priority: high
blocked_by: operator authorization (ROLE.md section 6: installing a systemd timer on recon-server is an infrastructure change)
owner: none
branch: none
pr: none
source: ROLE.md section 6; measurement of 2026-11-22 on recon-server (pid 449912, started with setsid --fork, no timer); watcher moved into this repository on 2026-11-23
---

## Goal

The Level 2 ARTEX watcher resumes by itself after recon-server reboots, without a human starting it and
without enqueueing anything twice. Today it dies with the boot and the queue silently stops growing.

## Why / source

`programs/_ops/triage-level2/ROLE.md`, section 6, last paragraph:

> The watcher does not survive a reboot. If it must, install a systemd timer, and that is an
> infrastructure change that needs the operator's OK.

Measured on recon-server on 2026-11-22 at 23:23 UTC: one process, pid 449912, `/bin/bash ./watch.sh`,
parent pid 1, session 449912, started Sun Nov 22 14:16:06 2026, with a `sleep 600` child between polls.
It was started with `setsid --fork ./watch.sh`, which detaches it from the terminal but not from the
boot. No `systemd` unit or timer references it.

## Scope (files likely to change)

- `ops/triage-level2/watch.sh` (add a single-pass mode; the loop stays the default)
- `ops/triage-level2/systemd/artex-level2-watch.service` (new, inert template)
- `ops/triage-level2/systemd/artex-level2-watch.timer` (new, inert template)
- `ops/triage-level2/README.md` (install, verify, rollback)
- `/home/cowboy/bugbounty/programs/_ops/triage-level2/ROLE.md`, section 6 (replace the reboot caveat
  with the installed state)

The first four are in this repository. The last one is in the `bugbounty` repository, which is separate
and ignores `src/scopeweaver`; it is a documentation edit to the role profile and is applied on the
server, not in this PR. State that split in the PR body.

## Acceptance

- [ ] `watch.sh --once` runs exactly one poll, appends at most one line per unseen finding, and exits 0
- [ ] running `--once` twice in a row enqueues nothing the second time (idempotent, proven by line count)
- [ ] `watch.sh` with no argument still loops every 600 s, unchanged behavior
- [ ] the timer unit uses `OnBootSec=` plus `OnUnitActiveSec=600`, `Persistent=true`, and a `WantedBy=timers.target` install section
- [ ] the service unit is `Type=oneshot` and runs the script with `--once`, so a slow poll can never overlap the next one
- [ ] both unit files are inert: no script in the repo installs them, and the README documents the three commands to enable them and the one to roll back
- [ ] after `systemctl enable --now`, a reboot leaves the watcher running and `inbox.log` gains no duplicate id
- [ ] `go build ./...` and `go test ./...` green, stated in the PR (they are unaffected by an ops change, so state that explicitly)
- [ ] affected docs updated, including `ROLE.md` section 6
- [ ] PR opened; ticket set to `Feedback`

## Notes / dependencies

Blocked by the operator's authorization, because enabling a unit on recon-server is an infrastructure
change. The precedent to copy is `src/ai-recon/deploy/systemd/`: that repo ships inert templates and
the server-side install is a separate, approved step.

Depends on TICKET-002 and TICKET-003 only in one direction: both of those must keep working in
`--once` mode, so land this ticket's mode first if the three are worked together.

## Log

- 2026-11-23: filed (New), blocked by the operator's authorization for the systemd install
- 2026-11-23: the watcher was moved into this repository at `ops/triage-level2/watch.sh`; the scope above
  reflects the new location, and the only remaining file outside this repository is the ROLE.md edit
