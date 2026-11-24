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
- [x] handles forwarded / nested MIME (message/rfc822), HTML-only mail, and multipart/alternative where the code lives only in the HTML part - keeps BOTH the plain and the tag-stripped HTML text (drops `<style>`/`<script>` noise, preserves href links), so no code is lost
- [x] `go build ./...` green; `go vet ./cmd/mailotp` and `go test ./cmd/mailotp` green (full `go test ./...` needs the DB harness and was not run here)
- [x] `mailotp` on PATH in the live container - interim volume deploy (`docker cp` to `/app/data/bin/mailotp`, `/usr/local/bin/mailotp` symlink); skill copied to `/app/skills/email-otp/`, visible to worker/auto/pentest
- [x] reproducible deploy wired: `mailotp` is built and shipped on PATH (`/usr/local/bin/mailotp`) in `Dockerfile.hardened` (the image Ansible builds) and the stock `Dockerfile`; recon-server Ansible `scopeweaver_image` bumped to `0.1.0-authfix.6` so the next image build bakes it in (replaces the interim `/app/data/bin` volume copy)
- [ ] the 10 task descriptions updated (post-deploy): drop the alias clause, add the "Accounts and email" block below (note: task descriptions are read-only via API/UI; on task 14 this was injected as a new open objective instead - see Log)
- [x] concurrency interim win: `--to` filter (matches the `To` header) + a `+tag` sub-address convention documented in the skill, so an agent reads only its own sign-up's email on a shared mailbox; the full fix (sync service + API) is TICKET-006
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
  fixed extractor would miss. It keeps BOTH the text/plain and the tag-stripped
  text/html parts (href links listed, `<style>`/`<script>` content dropped), so a code
  or URL present only in the HTML alternative of a multipart/alternative message is not
  lost. The HTML part is omitted only when it is a pure substring of the plain text.
- `fetch` is read-only (never marks read / deletes) and considers mail from about run
  time minus `--grace`, newest first - not a full-inbox search. The skill documents
  raising `--grace` and using `--from`/`--subject` to reach an email that already landed.

## Log

- 2026-11-24: filed; helper + skill built and tested on branch `ticket-005-email-otp` (New -> WIP). Deploy and live task edits pending operator OK.
- 2026-11-24: reworked `fetch` to print subject + full decoded body instead of a regex-extracted Code/Link, per operator (ARTEX agents extract themselves; OTPs can be in the subject).
- 2026-11-24: interim deploy - built static `mailotp` (`CGO_ENABLED=0`), `docker cp` to `scopeweaver-local-scopeweaver-1:/app/data/bin/mailotp` (atomic swap) + `/usr/local/bin/mailotp` symlink; skill copied to `/app/skills/email-otp/`, visible to agents worker/auto/pentest. Pipeline proven with a simulated OTP (test01 -> test02). Task 14 (IBM) reactivated by injecting a new open objective (descriptions are read-only), pointing the agent at mailboxes 01/02 and away from the `psylinux@wearehackerone.com` alias.
- 2026-11-24: live run on task 14 (IBM). Agent invoked `mailotp` for real (`mailboxes`, `fetch` 01+02) from 03:17; email-otp skill auto-loaded 5x and also picked up on tasks 16 and 23. Two issues surfaced: (1) **a code that lives only in the HTML part was dropped** - `fetch` returned the text/plain alternative alone; (2) IBM's own signup email-send (`www.ibm.com /account/apis/v2.0/emails/sendemail`) was blocked 403/502 by Akamai from the datacenter IP, so no fresh IBM code landed via www.ibm.com (agent pivoted to cloud.ibm.com/registration).
- 2026-11-24: fixed (1) - `extractBody` now keeps both the plain and the tag-stripped HTML parts and drops `<style>`/`<script>` noise (so CSS cannot bury or truncate the code); added regression test `TestExtractBodyCodeOnlyInHTML`. Rebuilt and redeployed the interim binary; verified live against a real multipart email in mailbox 01 (both parts printed, CSS gone). (2) is an environment limitation (datacenter-IP WAF reputation), not a helper bug.
- 2026-11-24: baked `mailotp` into the image (Dockerfile.hardened + stock Dockerfile, `/usr/local/bin/mailotp`); recon-server Ansible bumped to authfix.6; merged ticket-005 into `v0.1.0-authfix` (0c187ac) and deployed by image rebuild + container recreate (pause active tasks, `compose up -d`, resume). The 9 signal-batch tasks that still carried the alias in their read-only descriptions (13, 15-22) were each given an injected objective steering them to mailboxes 01/02.
- 2026-11-24: concurrency interim win shipped - `--to` filter (matches `To`, where Gmail keeps the `+tag`) + `+tag` convention in the skill; shows `To:` in output and JSON. Verified live. Full sync-service solution opened as TICKET-006.
