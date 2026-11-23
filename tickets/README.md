# ScopeWeaver tickets

One ticket is one unit of remaining implementation work. One agent, one ticket, one branch.
This folder is the work queue; tickets are tracked in git.

The format is `TEMPLATE.md`, copied from `src/ai-recon/tickets/TEMPLATE.md` with one change: the gate
line names this repo's gate (`go build ./...` and `go test ./...`) instead of `deploy/test.sh`, which
does not exist here.

## Statuses

| Status | Meaning | Who moves it |
|---|---|---|
| `New` | filed; not yet released, or blocked by an operator decision | whoever files it |
| `To-Do` | released, ready to be picked up | the operator (triage, or when a blocking decision is answered) |
| `WIP` | an agent has claimed it and is implementing | the agent |
| `Feedback` | implemented, gate green, PR open; awaiting the operator's merge | the agent |
| `Done` | merged and verified | the operator/automation |

## How an agent works a ticket

1. Pick a ticket with `status: To-Do` and `blocked_by: none`.
   List candidates: `grep -l 'status: To-Do' tickets/*.md`, then check each one's `blocked_by`.
2. Claim it atomically: in one commit, set `status: WIP`, `owner: <name>`, `branch: <branch>`.
   If a second agent sees `WIP`, it picks another ticket.
3. Implement on your branch. Touch only the files the ticket scopes.
4. Run the gate: `go build ./...` then `go test ./...`. Fix until green. Update the docs the change
   affects.
5. Commit, push the branch, open a PR. Record the PR link and the gate result in the ticket, then set
   `status: Feedback`. STOP here: never push to main.
6. The operator reviews and MERGES the PR.

## Rules that bind every ticket

- The gate is `go build ./...` and `go test ./...`. There is no `deploy/test.sh` in this repo.
- New files in English; never the em dash character (U+2014), never an en dash used as punctuation,
  never emoji. Hyphens stay inside identifiers (`--once`, `docker-compose.yml`).
- A ticket blocked by an operator decision stays `New` with `blocked_by:` naming it; do not pick it up.
- Never commit a credential, a token, an API key or a response body that carries one.

## The watcher: code here, data there

TICKET-001 to TICKET-003 concern the Level 2 ARTEX watcher. On 2026-11-23 it was moved into this
repository:

| What | Where | Why |
|---|---|---|
| `watch.sh` and its `README.md` | `ops/triage-level2/` in this repository | it is code |
| `inbox.log`, `LEVEL2-INDEX.tsv`, `seen.tsv` | `/home/cowboy/bugbounty/programs/` | it is program data, and program data never lives in a product repository |
| `ROLE.md` | `/home/cowboy/bugbounty/programs/_ops/triage-level2/` | role profile for the bug bounty pipeline |

The split matters when working a ticket. `bugbounty` is a separate git repository that ignores
`src/scopeweaver`, so a PR here cannot carry a change to `ROLE.md` or to the queue files. Apply those on
the server and name them in the PR body, exactly as the tickets say in their Scope section.

Every path in the script is overridable from the environment (`WATCH_LOG`, `WATCH_INDEX`, `WATCH_PG`,
`WATCH_INTERVAL`, `WATCH_BASE`, `WATCH_DIR`), and the defaults are the paths in use before the move. To
exercise a change without touching the live queue:

```bash
cd ops/triage-level2
WATCH_LOG=/tmp/watch-test.log WATCH_INTERVAL=5 timeout 12 ./watch.sh
wc -l /tmp/watch-test.log
rm -f /tmp/watch-test.log
```
