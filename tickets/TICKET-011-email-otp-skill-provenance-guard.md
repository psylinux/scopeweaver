---
id: TICKET-011
title: "Harden the email-otp skill against shared-mailbox noise"
status: Feedback      # New | To-Do | WIP | Feedback | Done
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
- [ ] Shipped in a rebuilt image and live via a cutover (operator-gated).
- [ ] This ticket set to Done after the cutover.

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
