---
name: email-otp
description: Read a sign-up email (one-time code or confirmation link) from a controlled ARTEX mailbox over IMAP, using the mailotp helper. Use during account creation and any flow that emails a code or verification link.
allowed-tools: Bash(mailotp:*)
---

# Email OTP / confirmation link

ARTEX owns a small pool of real, readable mailboxes for creating and operating its
own accounts on in-scope targets. Use them for authenticated testing, A/B
comparisons, IDOR and other tests that need two distinct accounts, and any flow
that requires an email address or authentication.

The `mailotp` helper connects to a mailbox over IMAP and prints the newest email's
`From`, `Date`, `Subject` and full decoded body. It does not try to find the code
for you: read the printed email and take the one-time code or verification link
yourself. The code is sometimes in the **subject**. The helper loads the mailbox
password itself from the shared secrets file, so you never see or handle it.

Only ever read mailboxes you own through this helper, and only create accounts you
own on the target.

## Mailboxes

```
mailotp mailboxes
```

prints the configured slots and their addresses, for example:

```
01  psylinux.hackerone.test01@gmail.com  (ready)
02  psylinux.hackerone.test02@gmail.com  (ready)
```

Use mailbox `01` for one account and `02` for a second, independent account (the
two sides of an A/B or IDOR test).

## Flow

1. Pick a mailbox (`01` or `02`) and read its address from `mailotp mailboxes`.
2. Enter that address in the target's sign-up / email field and submit, so the
   target sends the code or verification email.
3. Immediately read the email back:

   ```
   mailotp fetch --mailbox 01 --wait 60
   ```

   It waits up to `--wait` seconds for a new message and prints it:

   ```
   Mailbox: 01
   From:    no-reply@target.example
   Date:    2026-11-24T02:37:46Z
   Subject: Verify your email

   Your verification code is 123456. Or confirm at
   https://target.example/verify?token=...
   ```

4. Read the code or link out of that email and use it: type the code into the
   page, or open the link in the browser, to finish verification.

## Options and exit codes

- `--mailbox <NN>` (required): the slot, e.g. `01` or `02`.
- `--wait <sec>` (default 90): how long to wait for a new message to arrive.
- `--grace <sec>` (default 30): also accept a message received up to this many
  seconds before you ran fetch (covers an email that landed just before).
- `--from <substr>` / `--subject <substr>`: only consider messages whose sender
  or subject contains the substring, when more than one email may arrive.
- `--max-chars <n>` (default 6000): truncate the printed body.
- `--json`: print `{mailbox, from, date, subject, body, truncated}` instead.

Exit code `0` means an email was printed; `2` means nothing arrived within
`--wait` (make sure you actually triggered the send, then retry with a longer
`--wait`, or a `--from`/`--subject` filter); `1` is a configuration or connection
error.
