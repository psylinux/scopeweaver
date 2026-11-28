# email-otp: the task goal directive (seed this into every new task)

Agents read their sign-up mail the way the task's **goal** tells them to, not the way
the skill does. The `email-otp` skill is only loaded on demand; the goal (an
`exploration_nodes` row, `kind=goal`, `origin=human`) is injected into the task and is
what actually drives behaviour. So the mailbox guidance has to live in the goal too.

There is **no template** for this: the goal is seeded by the operator at task creation
(`create`), per task. Each task carries its own copy - it is not global, and updating
one task's goal does not touch the others or any future task. When you create a new
task, seed the goal below so the new task is born with the scoped, provenance-checked
flow. (Existing tasks 13-22 were updated in place on 2026-11-25; see TICKET-011.)

## Canonical goal text

```
For any signup, login, or verification flow that needs an email, use the controlled
mailboxes instead of the psylinux.hackerone.test01@gmail.com or psylinux.hackerone.test02@gmail.com alias (that alias is not readable
from this environment). Reserve a unique address with `mailotp alloc --mailbox 01`
(use `--mailbox 02` for a second, independent account), enter the printed address in
the target, then read ONLY your own reply with `mailotp fetch --handle <h> --wait 90`;
run `mailotp release --handle <h>` when done. Read the whole printed email, including
the "--- HTML part ---" section (the code is sometimes only in the HTML, or in the
subject). Use a code or link only if the email is addressed to your own +tag (or you
read it by your handle) AND the sender matches the service you just signed up for; a
code from any other sender or tag is not yours, so log it as ambient mailbox noise and
ignore it. Mailbox 01 and mailbox 02 are two separate accounts for authenticated, A/B,
and IDOR tests. Only create and access accounts you own. See the email-otp skill for
details.
```

## Why the old wording was a problem

The previous seed said "run `mailotp fetch --mailbox 01` (or `--mailbox 02`)". A
mailbox-wide read returns every message in that shared mailbox inside the window -
other agents' sign-ups and pre-existing mail included - so an agent can read a code
that is not its own. On 2026-11-25 task 13 read a stray `verificationemail.com` code
(953408) it never triggered and correctly held; across the fleet the mailbox-wide read
was the dominant pattern (activity: `query --mailbox` 77, `alloc` 24, `fetch --handle`
52) and no goal mentioned `alloc`/`--handle` at all.
