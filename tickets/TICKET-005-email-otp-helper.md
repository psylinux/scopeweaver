---
id: TICKET-005
title: "Email OTP helper: read sign-up codes/links from ARTEX mailboxes"
status: WIP          # New | To-Do | WIP | Feedback | Done
area: agent, skills, ops
priority: medium
blocked_by: none
owner: psylinux
branch: ticket-005-email-otp
pr: none
source: operator request (self-service email verification for autonomous sign-ups)
---

## Goal

The agent can create and operate its own accounts on in-scope targets end to end:
when a sign-up or verification flow emails a one-time code or confirmation link, it
reads that code/link itself from a controlled mailbox and continues, with no human
in the loop. The mailbox password never enters the agent's context.

## Why / source

Operator request. Autonomous tasks (the Signal batch, tasks 13-22) need to sign up
for accounts to reach authenticated surface, run A/B and IDOR/multi-account tests,
and any flow gated by an emailed code or link. Until now the task conduct pointed at
the HackerOne alias `psylinux@wearehackerone.com`, which forwards to the operator's
own inbox and is not readable by ARTEX. Replaced by two controlled Gmail mailboxes
read over IMAP.

## Scope (files likely to change)

- `cmd/mailotp/` - the helper binary (`fetch`, `mailboxes`)  [this branch]
- `skills/email-otp/SKILL.md` - how the agent uses it  [this branch]
- `go.mod` / `go.sum` - adds emersion/go-imap, go-message  [this branch]
- `Dockerfile` / `Dockerfile.hardened` - build and ship the `mailotp` binary on PATH  [deploy step]
- the 10 running task descriptions - via `PATCH /api/tasks/{id}`  [post-deploy, operator OK]

## Secrets (already in place, outside the repo)

`~/.config/bugbounty/secrets.yaml` + `secrets-registry.yaml` carry, per SECRETS-STANDARD.md:

- `ARTEX_MAIL_IMAP_URL` (shared endpoint, `imaps://imap.gmail.com:993`)
- `ARTEX_MAIL_01_EMAIL` / `ARTEX_MAIL_01_API_TOKEN`
- `ARTEX_MAIL_02_EMAIL` / `ARTEX_MAIL_02_API_TOKEN`

The App Tokens are Gmail IMAP App Passwords (2-Step Verification on each account).
The container already mounts `~/.config/bugbounty` read-only, and `sharedsecrets`
resolves these names.

## Acceptance

- [x] `mailotp mailboxes` lists configured slots and addresses (never passwords)
- [x] `mailotp fetch --mailbox 01 --wait N` prints the newest email's From/Date/Subject and full decoded body (the agent reads the code or link from it), reading the credential itself from secrets.yaml
- [x] handles forwarded / nested MIME (message/rfc822) and HTML-only mail (strips tags, preserves href links) - walks all text parts
- [x] `go build ./...` green; `go vet ./cmd/mailotp` and `go test ./cmd/mailotp` green (full `go test ./...` needs the DB harness and was not run here)
- [ ] `mailotp` shipped on PATH in the ARTEX runtime image (deploy step, operator OK)
- [ ] the 10 task descriptions updated (post-deploy): drop the alias clause, add the "Accounts and email" block below
- [ ] PR opened; ticket set to `Feedback`

## Task-description message (applied post-deploy, to tasks 13-22)

Remove `(create them with psylinux@wearehackerone.com when a program asks for the HackerOne alias)`
and add to the conduct:

> **Accounts and email.** Two controlled mailboxes are available for sign-ups, so you
> can create and operate your own accounts on in-scope targets:
> psylinux.hackerone.test01@gmail.com and psylinux.hackerone.test02@gmail.com. Use
> them for authenticated testing, A/B comparison, IDOR and other tests that need two
> distinct accounts, and any flow that requires an email address or authentication. To
> read a confirmation code or verification link sent to one of them, use the mailotp
> helper (email-otp skill): `mailotp fetch --mailbox 01 --wait 60` (or `02`) prints the
> newest email's subject and body for you to read the code or link from (a code is
> sometimes in the subject); the helper reads the credential itself, so you never
> handle the mailbox password. Only interact with accounts you own.

## Notes / dependencies

- Sequencing is a hard constraint: the task-description edit points agents at `mailotp`,
  so it must land only AFTER the binary is on PATH in the running image. Editing the
  live tasks first would make running agents call a missing command.
- Deploy options for the binary: (a) proper image build (Dockerfile + Ansible, reproducible),
  or (b) interim placement in the `sw_data` volume at `/app/data/bin/mailotp` with the skill
  pointed there. Prefer (a).
- Pool model for now (two fixed mailboxes). A catch-all domain would scale to unlimited
  readable addresses and could also receive forwards of the HackerOne alias; revisit if needed.

## Design notes

- The helper delivers the whole email (From/Date/Subject + decoded body); the ARTEX AI
  agent extracts the code or link itself. No server-side regex extraction: that only
  existed in the throwaway test harness, and some OTPs arrive in the subject, which a
  fixed extractor would miss. For HTML-only mail the tags are stripped but href links
  are preserved so a confirmation URL in the markup is not lost.

## Log

- 2026-11-24: filed; helper + skill built and tested on branch `ticket-005-email-otp` (New -> WIP). Deploy and live task edits pending operator OK.
- 2026-11-24: reworked `fetch` to print subject + full decoded body instead of a regex-extracted Code/Link, per operator (ARTEX agents extract themselves; OTPs can be in the subject).
