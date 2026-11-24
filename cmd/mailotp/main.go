// Command mailotp gives an ARTEX agent the sign-up emails of a controlled
// mailbox pool without ever touching Gmail itself.
//
// A long-running daemon (`mailotp serve`) holds one IMAP connection per mailbox,
// syncs INBOX and Spam into a local SQLite corpus as mail arrives, and serves it
// over a unix socket. Every other subcommand is a thin client of that daemon:
//
//	mailotp serve                 run the sync daemon (one per host)
//	mailotp mailboxes             list the mailboxes and their sync state
//	mailotp query   [filters]     search the local corpus, return at once
//	mailotp fetch   [filters]     search and wait for a match to arrive
//	mailotp alloc   --mailbox NN  reserve a unique +tag address for one sign-up
//	mailotp release --handle H    free an allocation (and any mailbox lease)
//
// Filters are --mailbox, --from, --to, --subject, --body, --handle, --since and
// --limit. The agent reads the one-time code or confirmation link out of the
// returned body itself; mailotp does no extraction of its own and a code is
// sometimes in the subject.
//
// Credentials (ARTEX_MAIL_<NN>_EMAIL / _API_TOKEN and the shared
// ARTEX_MAIL_IMAP_URL) are read by the daemon from the shared secrets file, so a
// mailbox password never appears on a command line or in an agent's context.
//
// Exit codes: 0 success; 2 nothing matched within --wait or bad usage; 1 a
// configuration, connection or daemon error.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(cmdServe(os.Args[2:]))
	case "mailboxes":
		os.Exit(cmdMailboxes(os.Args[2:]))
	case "query":
		os.Exit(cmdQuery(os.Args[2:]))
	case "fetch":
		os.Exit(cmdFetch(os.Args[2:]))
	case "alloc":
		os.Exit(cmdAlloc(os.Args[2:]))
	case "release":
		os.Exit(cmdRelease(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "mailotp: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `mailotp - serve an ARTEX agent its sign-up emails from a controlled mailbox pool

Usage:
  mailotp serve   [--socket PATH] [--db PATH] [--poll 15s] [--folders INBOX,Spam]
  mailotp mailboxes
  mailotp query   [--mailbox NN] [--from s] [--to s] [--subject s] [--body s] [--handle H] [--since 15m] [--limit N] [--json]
  mailotp fetch   [same filters] [--wait 90] [--json]
  mailotp alloc   --mailbox NN [--ttl 15m] [--no-plus] [--json]
  mailotp release --handle H

query/fetch/alloc/release are thin clients of the running 'mailotp serve' daemon;
they read a local synced corpus and never open IMAP. For a shared mailbox under
concurrency, alloc a +tag address and read your own message with fetch --handle H
(or --to +<tag>). fetch --mailbox NN falls back to a direct IMAP read when the
daemon is not running.
`)
}
