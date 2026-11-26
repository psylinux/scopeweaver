---
id: TICKET-011
title: "Harden the email-otp skill against shared-mailbox noise"
status: Done          # New | To-Do | WIP | Feedback | Done
area: skill, docs
priority: medium
blocked_by: none
owner: psylinux
branch: scopeweaver v0.1.0-authfix (skills/email-otp)
pr: none
source: operator observation (task 13 surfaced a stray code; 2026-11-25)
---

## Goal

An agent never acts on a one-time code that is not its own. The `email-otp` skill
must steer agents to the per-sign-up `alloc` + `fetch --handle` flow and make them
verify provenance before using any code read with a mailbox-wide command.

## Why / source

Task 13 read a confirmation code (`953408`, sender `verificationemail.com`) that it
could not tie to any flow it drove, and correctly held. Investigation:

- The mailboxes `psylinux.hackerone.test01/02@gmail.com` are real, shared Gmail
  accounts. They already carry unrelated inbound mail (for example an Amazon Games
  "Data Request Confirmation" to `test01`), and they are shared by every running
  agent at once. Unrelated codes arrive on their own.
- Read patterns in `activity` (2026-11-25): `mailotp alloc` 24, `fetch --handle` 52,
  but `query --mailbox NN` 77. The mailbox-wide read is the most common, and it
  returns every message in the shared mailbox inside the window - other agents'
  sign-ups and ambient mail included. That is how task 13 saw a code that was not its.
- An agent only sees its own worker traces, so it cannot tell a stray code from its
  own; it has to judge from the message's own `To:` / `From:` / `Subject:`.

## Scope (files changed)

- `skills/email-otp/SKILL.md`:
  - "Searching the mailboxes": a bold warning that `query` / `fetch --mailbox NN`
    (no `--handle`) are not scoped to the caller.
  - New "Before you use a code or link" section: a code is yours only if read by your
    own `--handle`, or if a mailbox-wide read's `To:` carries your `+tag` AND the
    `From:` / `Subject:` match the service you just signed up for; otherwise log it as
    ambient noise and move on.
- The task **goal directive** (`exploration_nodes`, `kind=goal`, `origin=human`), which
  is what actually drives agent behaviour (the skill is only loaded on demand). The old
  seed told agents to `mailotp fetch --mailbox 01`; 10 live goals (tasks 13-22) carried
  it and none mentioned `alloc`/`--handle`. Rewritten to the scoped alloc+handle flow
  plus the provenance guard.
- `ops/email-otp/task-seed-directive.md`: the canonical goal text to seed into every
  NEW task (there is no DB template; the operator pastes it at `create`).

## How it goes live

The skill body is read once at server start (`skill.LoadDir` -> `os.ReadFile`, then
cached as `s.Instructions`), so editing the file in a running container has no effect.
It needs a rebuilt image and a container recreate (the standard pause-running ->
`docker compose up -d` -> resume-running cutover). Fold it into the next operator
rebuild. Build command (note the version ARG added in 51f072b):

    docker build -f Dockerfile.hardened \
      --build-arg ARTEX_VERSION=0.1.0-authfix.10 \
      -t scopeweaver-local:0.1.0-authfix.10 .

Then bump `ARTEX_VERSION` default (Dockerfile.hardened), the compose image tag, and
the recon-server Ansible `scopeweaver_image` / `scopeweaver_version` defaults.

## Acceptance

- [x] Skill reinforces `alloc` + `fetch --handle` and warns on mailbox-wide reads.
- [x] Skill carries an explicit provenance guard (To-tag + sender match).
- [x] The 10 live task goals (13-22) rewritten to the alloc+handle+guard directive
      (transactional, verified 10/0; before-state backed up, restore script saved).
- [x] Canonical seed text captured in `ops/email-otp/task-seed-directive.md` so FUTURE
      tasks can be born with it (operator seeds it at `create`).
- [x] Shipped in a rebuilt image (authfix.10, d4040fd) and live via a cutover
      (2026-11-26; pause 13 / recreate / resume 13; /api/health=authfix.10, serve up).
- [x] This ticket set to Done (operator closed, 2026-11-26).

## Notes / dependencies

Parent: TICKET-010 (the skill's serve-model rewrite). No code change, skill text only.
The deploy is intentionally held: the operator is actively on the build path
(51f072b, host dev instance), so this should ride their next rebuild rather than a
separate cutover.

## Log

- 2026-11-25: filed. Root-caused the stray-code read (mailbox-wide `query` dominates
  over `alloc`+`--handle`). Edited `skills/email-otp/SKILL.md`: mailbox-wide warning +
  "Before you use a code or link" provenance guard. Committed (no push). Deploy held
  for the operator's next rebuild. Status Feedback.
- 2026-11-25: deeper root cause - the human goal directive itself ordered the broad
  read (`mailotp fetch --mailbox 01`). Confirmed fleet-wide: 10 goals (tasks 13-22)
  carried it, 0 used alloc/handle; goals are per-task (not global), seeded by the
  operator at creation. Rewrote the 10 live goals to the alloc+handle+guard directive
  in one transaction (UPDATE 10, guard new=10/old=0, COMMIT); before-state backed up
  and a restore script generated. Added `ops/email-otp/task-seed-directive.md` for
  future-task seeding. The DB change is live now and needs no rebuild; the SKILL.md
  change still rides the operator's next image build.
- 2026-11-26: deployed on operator request. Built `scopeweaver-local:0.1.0-authfix.10`
  (`--build-arg ARTEX_VERSION=0.1.0-authfix.10`; Dockerfile default bumped to match,
  commit d4040fd). Cutover: paused the running set (task 13 only), `docker compose up -d`
  recreate, resumed 13; postgres + sw_data untouched. Verified: /api/health reports
  authfix.10 (the 51f072b ARG fix held), serve sidecar up (pollers 01/02 live, last sync
  ~2s), skill baked with the provenance guard, store.db persisted on sw_data. Ansible
  bumped (recon-server 9ecaa02). Ready for the operator to close.
- 2026-11-26: operator closed. Status Done.
