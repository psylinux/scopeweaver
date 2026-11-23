# Level 2 ARTEX watcher

`watch.sh` polls the ARTEX database every 10 minutes and appends one line to a queue file for every
finding that has no Level 2 verdict yet. It is read only against ARTEX: it never writes to the database
and it never decides anything. A finding is queued once and never repeated.

Moved here on 2026-11-23 from
`/home/cowboy/bugbounty/programs/_ops/triage-level2/watch.sh`. The move separates code from data: the
script is product code and lives in this repository, while the queue it writes stays in the bug bounty
tree, because program data is not product data.

## What it reads and writes

| Item | Default path | Override |
|---|---|---|
| Finding source | container `scopeweaver-local-postgres-1`, database `artex`, table `findings` | `WATCH_PG` |
| Verdict index (read) | `/home/cowboy/bugbounty/programs/_triage/LEVEL2-INDEX.tsv` | `WATCH_INDEX` |
| Queue (append) | `/home/cowboy/bugbounty/programs/_ops/triage-level2/inbox.log` | `WATCH_LOG` |
| Poll interval | 600 seconds | `WATCH_INTERVAL` |
| Tree root | `/home/cowboy/bugbounty` | `WATCH_BASE`, `WATCH_DIR` |

The defaults are exactly the paths that were in use before the move, so nothing downstream changes.

## Run it

```bash
cd /home/cowboy/bugbounty/src/scopeweaver/ops/triage-level2
setsid --fork ./watch.sh >/dev/null 2>&1 </dev/null
```

Check it is alive and read the queue:

```bash
ps -eo pid,etime,cmd | grep '[.]/watch.sh'
tail -30 /home/cowboy/bugbounty/programs/_ops/triage-level2/inbox.log
```

Stop it:

```bash
pkill -f 'ops/triage-level2/watch.sh'
```

## Test it without touching the real queue

Point the queue at a throwaway file. Every finding absent from the index is then enqueued there, which
proves the poll works end to end and leaves `inbox.log` untouched:

```bash
WATCH_LOG=/tmp/watch-test.log WATCH_INTERVAL=5 timeout 12 ./watch.sh
wc -l /tmp/watch-test.log
rm -f /tmp/watch-test.log
```

## Known limits

1. It does not survive a reboot. Persisting it needs a `systemd` timer, which is an infrastructure
   change on recon-server and needs the operator's approval. TICKET-001 covers the `--once` mode and the
   inert unit templates that change requires.
2. It depends on the database container. When the container is down the query returns nothing, the
   script cannot tell that apart from an empty table, and it writes one `ERROR` line per cycle for the
   whole outage, with no start, end or duration. TICKET-002 covers the fix.
3. It queues a finding once and never again, including when a parked finding meets the reopening
   condition recorded for it in `LEVEL-2-REVIEW.md`. TICKET-003 covers the fix.

The role this serves is
`/home/cowboy/bugbounty/programs/_ops/triage-level2/ROLE.md`, section 6.
