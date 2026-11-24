// client_cmds.go - the agent-facing subcommands. Each builds one Request and
// round-trips it to the `mailotp serve` daemon over the unix socket, then prints
// the reply. None of them opens IMAP, with one deliberate exception: `fetch`
// falls back to a direct single-shot read (scanOnce) when the daemon is not
// running, so an agent is never left without a way to read a code during a
// rollout.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/emersion/go-imap"
)

// filterFlags are the search flags shared by query and fetch.
type filterFlags struct {
	mailbox, from, to, subject, body, handle, agent *string
	since                                           *time.Duration
	limit, maxChars                                 *int
}

func addFilterFlags(fs *flag.FlagSet) *filterFlags {
	return &filterFlags{
		mailbox:  fs.String("mailbox", "", "mailbox slot, e.g. 01"),
		from:     fs.String("from", "", "only messages whose sender contains this substring"),
		to:       fs.String("to", "", "only messages whose To/Delivered-To contains this substring (e.g. +tag)"),
		subject:  fs.String("subject", "", "only messages whose subject contains this substring"),
		body:     fs.String("body", "", "only messages whose body contains this substring"),
		handle:   fs.String("handle", "", "read only the message for this allocation handle"),
		agent:    fs.String("agent", "", "caller identity for metrics attribution (defaults to $MAILOTP_AGENT / $ARTEX_TASK_ID)"),
		since:    fs.Duration("since", 0, "only messages received within this window, e.g. 15m"),
		limit:    fs.Int("limit", 10, "maximum messages to return"),
		maxChars: fs.Int("max-chars", defaultMaxChars, "truncate each body to this many characters"),
	}
}

func (ff *filterFlags) toRequest(op string) Request {
	req := Request{
		Op:       op,
		Mailbox:  *ff.mailbox,
		From:     *ff.from,
		To:       *ff.to,
		Subject:  *ff.subject,
		Body:     *ff.body,
		Handle:   *ff.handle,
		Agent:    resolveAgent(*ff.agent),
		Limit:    *ff.limit,
		MaxChars: *ff.maxChars,
	}
	if *ff.since > 0 {
		req.SinceSec = int(ff.since.Seconds())
	}
	return req
}

// resolveAgent determines the caller identity for metrics attribution without
// the agent having to pass --agent: an explicit flag wins, then the environment,
// then the task id in the working directory. ARTEX runs an agent's shell
// commands for a sign-up under /app/data/tasks/<id>/..., so a read made from
// there (the common case for fetch/query) is attributed to that task on its own.
// When none of these apply the label is empty and the daemon records "unknown".
func resolveAgent(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("MAILOTP_AGENT"); v != "" {
		return v
	}
	if v := os.Getenv("ARTEX_TASK_ID"); v != "" {
		return v
	}
	if id := taskIDFromCwd(); id != "" {
		return "task" + id
	}
	return ""
}

// taskDirRe pulls the numeric task id out of a working directory like
// /app/data/tasks/16/i8464.
var taskDirRe = regexp.MustCompile(`(?:^|/)tasks/([0-9]+)(?:/|$)`)

func taskIDFromCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if m := taskDirRe.FindStringSubmatch(wd); m != nil {
		return m[1]
	}
	return ""
}

func cmdQuery(args []string) int {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	ff := addFilterFlags(fs)
	socket := fs.String("socket", defaultSocketPath(), "daemon socket path")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resp, err := roundtrip(*socket, ff.toRequest("query"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailotp query: %v\n", err)
		return 1
	}
	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "mailotp query: %s\n", resp.Error)
		return 1
	}
	printMessages(resp.Messages, *asJSON)
	return 0
}

func cmdFetch(args []string) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	ff := addFilterFlags(fs)
	socket := fs.String("socket", defaultSocketPath(), "daemon socket path")
	wait := fs.Int("wait", 90, "seconds to wait for a matching message to arrive")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	direct := fs.Bool("direct", false, "bypass the daemon and read IMAP directly (single shot)")
	grace := fs.Int("grace", 30, "direct mode: also accept a message received this many seconds before now")
	poll := fs.Int("poll", 5, "direct mode: seconds between IMAP polls")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *direct {
		return cmdFetchDirect(*ff.mailbox, *ff.from, *ff.subject, *ff.to, *wait, *grace, *poll, *ff.maxChars, *asJSON)
	}

	req := ff.toRequest("fetch")
	req.WaitSec = *wait
	resp, err := roundtrip(*socket, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailotp fetch: daemon not reachable (%v); falling back to a direct IMAP read\n", err)
		return cmdFetchDirect(*ff.mailbox, *ff.from, *ff.subject, *ff.to, *wait, *grace, *poll, *ff.maxChars, *asJSON)
	}
	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "mailotp fetch: %s\n", resp.Error)
		return 1
	}
	if len(resp.Messages) == 0 {
		fmt.Fprintf(os.Stderr, "mailotp fetch: no message matched within %ds\n", *wait)
		return 2
	}
	printMessages(resp.Messages, *asJSON)
	return 0
}

func cmdAlloc(args []string) int {
	fs := flag.NewFlagSet("alloc", flag.ContinueOnError)
	mailbox := fs.String("mailbox", "", "mailbox slot to allocate from, e.g. 01 (required)")
	ttl := fs.Duration("ttl", 15*time.Minute, "how long the allocation (and any lease) stays valid")
	noPlus := fs.Bool("no-plus", false, "lease the whole mailbox instead of a +tag (for sites that reject +)")
	socket := fs.String("socket", defaultSocketPath(), "daemon socket path")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	slot := normalizeSlot(*mailbox)
	if slot == "" {
		fmt.Fprintln(os.Stderr, "mailotp alloc: --mailbox NN is required")
		return 2
	}
	req := Request{Op: "alloc", Mailbox: slot, NoPlus: *noPlus}
	if *ttl > 0 {
		req.TTLSec = int(ttl.Seconds())
	}
	resp, err := roundtrip(*socket, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailotp alloc: %v\n", err)
		return 1
	}
	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "mailotp alloc: %s\n", resp.Error)
		return 1
	}
	if *asJSON {
		b, _ := json.Marshal(map[string]string{"handle": resp.Handle, "address": resp.Address, "tag": resp.Tag})
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("handle:  %s\naddress: %s\n", resp.Handle, resp.Address)
	fmt.Fprintf(os.Stderr, "sign up with the address above, then: mailotp fetch --handle %s --wait 90\n", resp.Handle)
	return 0
}

func cmdRelease(args []string) int {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	handle := fs.String("handle", "", "allocation handle to release (required)")
	socket := fs.String("socket", defaultSocketPath(), "daemon socket path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *handle == "" {
		fmt.Fprintln(os.Stderr, "mailotp release: --handle H is required")
		return 2
	}
	resp, err := roundtrip(*socket, Request{Op: "release", Handle: *handle})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailotp release: %v\n", err)
		return 1
	}
	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "mailotp release: %s\n", resp.Error)
		return 1
	}
	fmt.Println("released")
	return 0
}

func cmdMailboxes(args []string) int {
	fs := flag.NewFlagSet("mailboxes", flag.ContinueOnError)
	socket := fs.String("socket", defaultSocketPath(), "daemon socket path")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resp, err := roundtrip(*socket, Request{Op: "mailboxes"})
	if err != nil {
		// Daemon down: fall back to listing what is configured.
		mbs := configuredMailboxes()
		if len(mbs) == 0 {
			fmt.Fprintln(os.Stderr, "mailotp mailboxes: none configured (ARTEX_MAIL_<NN>_EMAIL in secrets.yaml)")
			return 2
		}
		for _, mb := range mbs {
			state := "ready"
			if mb.Pass == "" {
				state = "no app password"
			}
			fmt.Printf("%s  %s  (%s, daemon down)\n", mb.Slot, mb.Email, state)
		}
		return 0
	}
	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "mailotp mailboxes: %s\n", resp.Error)
		return 1
	}
	if *asJSON {
		b, _ := json.Marshal(resp.Mailboxes)
		fmt.Println(string(b))
		return 0
	}
	for _, v := range resp.Mailboxes {
		last := ""
		if v.LastDate != "" {
			last = ", last " + v.LastDate
		}
		fmt.Printf("%s  %s  (%d msgs synced%s)\n", v.Mailbox, v.Address, v.Count, last)
	}
	return 0
}

// cmdFetchDirect is the single-shot direct IMAP read used by `fetch --direct`
// and as the daemon-down fallback. It is the only client path that opens IMAP.
func cmdFetchDirect(mailbox, fromF, subjF, toF string, wait, grace, poll, maxChars int, asJSON bool) int {
	slot := normalizeSlot(mailbox)
	if slot == "" {
		fmt.Fprintln(os.Stderr, "mailotp fetch --direct: --mailbox NN is required")
		return 2
	}
	email, pass := mailboxCreds(slot)
	if email == "" || pass == "" {
		fmt.Fprintf(os.Stderr, "mailotp fetch: mailbox %s is not configured\n", slot)
		return 1
	}
	initCharset()
	c, err := imapDialLogin(email, pass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailotp fetch: %v\n", err)
		return 1
	}
	defer c.Logout()

	section := &imap.BodySectionName{}
	start := time.Now()
	deadline := start.Add(time.Duration(wait) * time.Second)
	floor := start.Add(-time.Duration(grace) * time.Second)
	seen := map[uint32]bool{}
	for {
		m, err := scanOnce(c, section, seen, floor, fromF, subjF, toF, maxChars)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mailotp fetch: %v\n", err)
			return 1
		}
		if m != nil {
			printDirect(slot, m, asJSON)
			return 0
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(time.Duration(poll) * time.Second)
	}
	fmt.Fprintf(os.Stderr, "mailotp fetch: no message for mailbox %s within %ds\n", slot, wait)
	return 2
}

func printMessages(msgs []MsgView, asJSON bool) {
	if asJSON {
		b, _ := json.Marshal(msgs)
		fmt.Println(string(b))
		return
	}
	for i, m := range msgs {
		if i > 0 {
			fmt.Println("\n----------------------------------------")
		}
		fmt.Printf("Mailbox: %s\nFolder:  %s\nFrom:    %s\nTo:      %s\nDate:    %s\nSubject: %s\n\n%s\n",
			m.Mailbox, m.Folder, m.From, m.To, m.Date, m.Subject, m.Body)
		if m.Truncated {
			fmt.Fprintln(os.Stderr, "[body truncated; raise --max-chars to see more]")
		}
	}
}

func printDirect(slot string, m *mailMsg, asJSON bool) {
	if asJSON {
		b, _ := json.Marshal(map[string]any{
			"mailbox": slot, "from": m.from, "to": m.to, "date": m.date.Format(time.RFC3339),
			"subject": m.subject, "body": m.body, "truncated": m.truncated,
		})
		fmt.Println(string(b))
		return
	}
	fmt.Printf("Mailbox: %s\nFrom:    %s\nTo:      %s\nDate:    %s\nSubject: %s\n\n%s\n",
		slot, m.from, m.to, m.date.Format(time.RFC3339), m.subject, m.body)
	if m.truncated {
		fmt.Fprintln(os.Stderr, "[body truncated; raise --max-chars to see more]")
	}
}
