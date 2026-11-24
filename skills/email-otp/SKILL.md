---
name: email-otp
description: Read a sign-up email (one-time code or confirmation link) from a controlled ARTEX mailbox, using the mailotp helper. Use during account creation and any flow that emails a code or verification link.
allowed-tools: Bash(mailotp:*)
---

# Email OTP / confirmation link

ARTEX owns a small pool of real, readable mailboxes for creating and operating its
own accounts on in-scope targets. Use them for authenticated testing, A/B
comparisons, IDOR and other tests that need two distinct accounts, and any flow
that requires an email address or verification.

A background service (`mailotp serve`) continuously syncs those mailboxes into a
local store. You never connect to a mailbox yourself: every `mailotp` command below
is a fast local read against that store. The helper prints each email's `From`,
`Date`, `To`, `Subject` and full decoded body, and does not try to find the code
for you: read the printed email and take the one-time code or verification link
yourself. The code is sometimes in the **subject**, and some emails carry it only
in their HTML part, so the helper prints both the plain text and, under a
`--- HTML part (tags stripped) ---` marker, the readable HTML text (with any `href`
links collected under `Links in this email:`). **Read the whole output.** The
service loads the mailbox password itself, so you never see or handle it.

Only ever read mailboxes you own through this helper, and only create accounts you
own on the target.

## Sign up and read your own code (the normal flow)

These mailboxes are shared by many agents at once, so you must read **your** email,
not someone else's. `mailotp alloc` gives you a unique address for one sign-up and
a handle that reads exactly its reply:

1. Reserve an address on a mailbox:

   ```
   mailotp alloc --mailbox 01
   ```
   ```
   handle:  8142abc9cbe2dda1
   address: psylinux.hackerone.test01+95bb6638@gmail.com
   ```

2. Enter that `address` in the target's sign-up / email field and submit.

3. Wait for and read only your message, by its handle:

   ```
   mailotp fetch --handle 8142abc9cbe2dda1 --wait 90
   ```
   It blocks up to `--wait` seconds for your email to arrive, then prints it:
   ```
   Mailbox: 01
   From:    no-reply@target.example
   To:      psylinux.hackerone.test01+95bb6638@gmail.com
   Date:    2026-11-24T02:37:46Z
   Subject: Verify your email

   Your verification code is 123456. Or confirm at
   https://target.example/verify?token=...
   ```

4. Read the code or link out of it and use it (type the code, or open the link).

5. When done, free the reservation: `mailotp release --handle 8142abc9cbe2dda1`.

Gmail delivers `...test01+<tag>@gmail.com` to the same inbox and keeps the `+tag`
in the `To` header, so the handle reads your message even while other agents use
the same mailbox. If a site **rejects `+`** in an address, lease the whole mailbox
instead: `mailotp alloc --mailbox 01 --no-plus` returns the bare address and holds
the mailbox for you until you `release`.

Use mailbox `01` for one account and `02` for a second, independent account (the
two sides of an A/B or IDOR test). `mailotp mailboxes` lists the slots and how many
messages are synced.

## Searching the mailboxes

To look through mail already received (not tied to one alloc), search the local
store directly:

```
mailotp query --mailbox 01 --from target.example --since 15m
mailotp query --subject verify --limit 5
```

`query` returns matches at once (newest first); `fetch` is the same search but
waits for a match to appear.

## Options

- `--handle H`: read only the message for an `alloc` handle (the normal way).
- `--mailbox NN`: a slot, e.g. `01` or `02`.
- `--from` / `--to` / `--subject` / `--body <substr>`: case-insensitive substring
  filters (`--to` matches the `To`/`Delivered-To`, e.g. a `+tag`).
- `--since <dur>`: only mail received within the window, e.g. `30m`.
- `--wait <sec>` (fetch, default 90): how long to wait for a match to arrive.
- `--limit <n>` / `--max-chars <n>`: cap the result count / body length.
- `--agent <id>`: label your reads for metrics (optional; otherwise taken from the
  environment). Pass your task/intent id so the operator can see which agent read
  which mail.
- `--json`: machine-readable output.

Exit code `0` means a message was printed; `2` means nothing matched within
`--wait` (make sure you actually triggered the send, then retry with a longer
`--wait`); `1` is a configuration or service error.

## Notes

- Reads are **read-only** and re-readable: a message is never marked read or
  deleted, so you can fetch the same email again as many times as you need.
- `mailotp fetch --mailbox 01 --wait 60` still works for a quick "newest on this
  mailbox" read, and falls back to a direct IMAP read if the service is down. But
  for a sign-up, prefer `alloc` + `fetch --handle`, so you never read another
  agent's code.
