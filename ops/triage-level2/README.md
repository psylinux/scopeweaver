# Level 2 ARTEX watcher

`watch.sh` polls the ARTEX database every 10 minutes and appends one line to a queue file for every
finding that has no Level 2 verdict yet. It is read only against ARTEX: it never writes to the database
and it never decides anything. A finding is queued once and never repeated.

Moved here on 2026-11-23 from
`/home/cowboy/bugbounty/programs/_ops/triage-level2/watch.sh`. The move separates code from data: the
script is product code and lives in this repository, while the queue it writes stays in the bug bounty
tree, because program data is not product data.

## Modes

| Invocation | Behavior |
|---|---|
| `./watch.sh` | poll, sleep `INTERVAL`, repeat forever |
| `./watch.sh --once` | poll exactly once and exit 0; this is what the timer runs |
| `./watch.sh --help` or any other argument | print usage and exit 2 |

`--once` exits 0 even when the query produced nothing, so a oneshot unit is not marked failed while the
database is away. The failure is recorded as an `ERROR` line in the queue instead. Telling an outage
apart from an empty table is TICKET-002.

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

It runs as a `systemd` timer on recon-server, so it comes back by itself after a reboot. The units are
templates in `systemd/`; nothing in this repository installs them.

Install, once:

```bash
D=/home/cowboy/bugbounty/src/scopeweaver/ops/triage-level2
sudo install -m 644 "$D/systemd/artex-level2-watch.service" /etc/systemd/system/
sudo install -m 644 "$D/systemd/artex-level2-watch.timer"   /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now artex-level2-watch.timer
```

Check it is scheduled and read the queue:

```bash
systemctl list-timers artex-level2-watch.timer
systemctl status artex-level2-watch.service --no-pager
tail -30 /home/cowboy/bugbounty/programs/_ops/triage-level2/inbox.log
```

Run one poll by hand, without waiting for the timer:

```bash
sudo systemctl start artex-level2-watch.service
```

Stop it, and remove it:

```bash
sudo systemctl disable --now artex-level2-watch.timer
sudo rm -f /etc/systemd/system/artex-level2-watch.service /etc/systemd/system/artex-level2-watch.timer
sudo systemctl daemon-reload
```

Do not also run `./watch.sh` detached while the timer is enabled: two pollers do not produce duplicate
lines, because the queue dedupes by id, but they do waste a database query every five minutes on average
and they make the log's cadence meaningless.

Identify a running poller by its working directory, never by its command line. Every watcher in this
tree has the same relative command line, so a `pkill -f` on a path silently misses them:

```bash
for p in $(pgrep -f watch.sh); do printf '%s %s\n' "$p" "$(readlink /proc/$p/cwd)"; done
```

## Test it without touching the real queue

Point the queue at a throwaway file. Every finding absent from the index is then enqueued there, which
proves the poll works end to end and leaves `inbox.log` untouched:

```bash
WATCH_LOG=/tmp/watch-test.log ./watch.sh --once
wc -l /tmp/watch-test.log
rm -f /tmp/watch-test.log
```

## Known limits

1. It depends on the database container. When the container is down the query returns nothing, the
   script cannot tell that apart from an empty table, and it writes one `ERROR` line per cycle for the
   whole outage, with no start, end or duration. TICKET-002 covers the fix.
2. It queues a finding once and never again, including when a parked finding meets the reopening
   condition recorded for it in `LEVEL-2-REVIEW.md`. TICKET-003 covers the fix.

The role this serves is
`/home/cowboy/bugbounty/programs/_ops/triage-level2/ROLE.md`, section 6.
