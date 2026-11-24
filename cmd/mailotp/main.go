// Command mailotp hands one of ARTEX's controlled sign-up mailboxes to the
// agent: it reads the newest matching email over IMAP and prints its sender,
// date, subject and full decoded body, for the agent to read the one-time code
// or confirmation link from (a code is sometimes in the subject). It does no
// extraction of its own.
//
// It is meant to be run by the agent through its shell tool during a sign-up
// flow: trigger the email on the target, then run
// `mailotp fetch --mailbox 01 --wait 60`.
//
// Credentials are read from the shared bug bounty secrets file through the
// sharedsecrets package (ARTEX_MAIL_<NN>_EMAIL / ARTEX_MAIL_<NN>_API_TOKEN, and
// the shared ARTEX_MAIL_IMAP_URL endpoint), so a mailbox password never appears
// on the command line, in the environment, or in the caller's context.
//
// Subcommands:
//
//	mailotp mailboxes            list the configured mailbox slots and addresses
//	mailotp fetch --mailbox 01   wait for and print the newest email
//
// Exit codes: 0 an email was printed; 2 nothing matched within --wait or bad
// usage; 1 a configuration or connection error.
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/sharedsecrets"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
)

var (
	// styleScriptRe drops the contents of <style>/<script> blocks so their CSS
	// and JavaScript do not pollute the HTML-derived text or, worse, push a code
	// that lives only in the HTML past the --max-chars cap.
	styleScriptRe = regexp.MustCompile(`(?is)<(style|script)\b[^>]*>.*?</(style|script)>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	hrefRe        = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)
	urlRe         = regexp.MustCompile(`https?://[^\s"'<>)]+`)
)

const defaultMaxChars = 6000

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "fetch":
		os.Exit(cmdFetch(os.Args[2:]))
	case "mailboxes":
		os.Exit(cmdMailboxes())
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
	fmt.Fprint(os.Stderr, `mailotp - read a sign-up email from an ARTEX mailbox and print it for the agent

Usage:
  mailotp mailboxes
  mailotp fetch --mailbox <NN> [--wait 90] [--grace 30] [--from s] [--subject s] [--max-chars 6000] [--json]

Prints the newest matching email's From/Date/Subject and decoded body. The agent
reads the code or link from it. Credentials come from secrets.yaml
(ARTEX_MAIL_<NN>_EMAIL / _API_TOKEN).
`)
}

// cmdMailboxes lists the configured slots and their addresses (never passwords).
func cmdMailboxes() int {
	found := 0
	for n := 1; n <= 20; n++ {
		slot := fmt.Sprintf("%02d", n)
		email := sharedsecrets.Get("ARTEX_MAIL_" + slot + "_EMAIL")
		if email == "" {
			continue
		}
		state := "ready"
		if sharedsecrets.Get("ARTEX_MAIL_"+slot+"_API_TOKEN") == "" {
			state = "no app password"
		}
		fmt.Printf("%s  %s  (%s)\n", slot, email, state)
		found++
	}
	if found == 0 {
		fmt.Fprintln(os.Stderr, "mailotp: no mailboxes configured (ARTEX_MAIL_<NN>_EMAIL in secrets.yaml)")
		return 2
	}
	return 0
}

func cmdFetch(args []string) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	mailbox := fs.String("mailbox", "", "mailbox slot to read, e.g. 01 or 02 (required)")
	wait := fs.Int("wait", 90, "seconds to wait for a new message to arrive")
	grace := fs.Int("grace", 30, "also accept a message received up to this many seconds before now")
	from := fs.String("from", "", "only consider messages whose sender contains this substring")
	subject := fs.String("subject", "", "only consider messages whose subject contains this substring")
	poll := fs.Int("poll", 5, "seconds between IMAP polls")
	maxChars := fs.Int("max-chars", defaultMaxChars, "truncate the printed body to this many characters")
	asJSON := fs.Bool("json", false, "print the result as a JSON object")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	slot := normalizeSlot(*mailbox)
	if slot == "" {
		fmt.Fprintln(os.Stderr, "mailotp fetch: --mailbox is required, e.g. --mailbox 01")
		return 2
	}
	email := sharedsecrets.Get("ARTEX_MAIL_" + slot + "_EMAIL")
	pass := sharedsecrets.Get("ARTEX_MAIL_" + slot + "_API_TOKEN")
	if email == "" || pass == "" {
		fmt.Fprintf(os.Stderr, "mailotp fetch: mailbox %s is not configured (ARTEX_MAIL_%s_EMAIL / _API_TOKEN in secrets.yaml)\n", slot, slot)
		return 1
	}

	host, port := imapEndpoint()
	imap.CharsetReader = charset.Reader
	message.CharsetReader = charset.Reader
	c, err := client.DialTLS(host+":"+port, &tls.Config{ServerName: host})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailotp fetch: connect %s:%s: %v\n", host, port, err)
		return 1
	}
	defer c.Logout()
	if err := c.Login(email, pass); err != nil {
		fmt.Fprintf(os.Stderr, "mailotp fetch: login %s: %v\n", email, err)
		return 1
	}

	section := &imap.BodySectionName{}
	start := time.Now()
	deadline := start.Add(time.Duration(*wait) * time.Second)
	floor := start.Add(-time.Duration(*grace) * time.Second)
	seen := map[uint32]bool{}

	for {
		m, err := scanOnce(c, section, seen, floor, *from, *subject, *maxChars)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mailotp fetch: %v\n", err)
			return 1
		}
		if m != nil {
			printMsg(slot, m, *asJSON)
			return 0
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(time.Duration(*poll) * time.Second)
	}
	fmt.Fprintf(os.Stderr, "mailotp fetch: no message for mailbox %s within %ds\n", slot, *wait)
	return 2
}

type mailMsg struct {
	subject   string
	from      string
	date      time.Time
	body      string
	truncated bool
}

// scanOnce returns the newest message in INBOX received at or after floor that
// matches the optional from/subject filters and has not been returned before.
// Relevance is left to the caller (the AI agent): no content filtering is done.
func scanOnce(c *client.Client, section *imap.BodySectionName, seen map[uint32]bool, floor time.Time, fromF, subjF string, maxChars int) (*mailMsg, error) {
	mbox, err := c.Select("INBOX", true)
	if err != nil {
		return nil, fmt.Errorf("select INBOX: %w", err)
	}
	if mbox.Messages == 0 {
		return nil, nil
	}
	const scan = uint32(15)
	fromSeq := uint32(1)
	if mbox.Messages > scan {
		fromSeq = mbox.Messages - scan + 1
	}
	seqset := new(imap.SeqSet)
	seqset.AddRange(fromSeq, mbox.Messages)
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchInternalDate, section.FetchItem()}

	ch := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- c.Fetch(seqset, items, ch) }()
	var msgs []*imap.Message
	for m := range ch {
		msgs = append(msgs, m)
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].InternalDate.After(msgs[j].InternalDate) })

	for _, m := range msgs {
		if seen[m.Uid] || m.InternalDate.Before(floor) {
			continue
		}
		subj, sender := envelopeMeta(m)
		if fromF != "" && !strings.Contains(strings.ToLower(sender), strings.ToLower(fromF)) {
			continue
		}
		if subjF != "" && !strings.Contains(strings.ToLower(subj), strings.ToLower(subjF)) {
			continue
		}
		seen[m.Uid] = true
		body, trunc := extractBody(m.GetBody(section), maxChars)
		return &mailMsg{subject: subj, from: sender, date: m.InternalDate, body: body, truncated: trunc}, nil
	}
	return nil, nil
}

func printMsg(slot string, m *mailMsg, asJSON bool) {
	if asJSON {
		b, _ := json.Marshal(map[string]any{
			"mailbox": slot, "from": m.from, "date": m.date.Format(time.RFC3339),
			"subject": m.subject, "body": m.body, "truncated": m.truncated,
		})
		fmt.Println(string(b))
		return
	}
	fmt.Printf("Mailbox: %s\nFrom:    %s\nDate:    %s\nSubject: %s\n\n%s\n", slot, m.from, m.date.Format(time.RFC3339), m.subject, m.body)
	if m.truncated {
		fmt.Fprintln(os.Stderr, "[body truncated; raise --max-chars to see more]")
	}
}

// imapEndpoint resolves ARTEX_MAIL_IMAP_URL into host and port.
func imapEndpoint() (host, port string) {
	return splitEndpoint(strings.TrimSpace(sharedsecrets.Get("ARTEX_MAIL_IMAP_URL")))
}

// splitEndpoint parses an IMAP endpoint, accepting "imaps://host:port",
// "host:port" or "host". An empty value defaults to Gmail's IMAPS.
func splitEndpoint(raw string) (host, port string) {
	if raw == "" {
		return "imap.gmail.com", "993"
	}
	s := raw
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		s = u.Host
	}
	host, port = s, "993"
	if h, p, ok := strings.Cut(s, ":"); ok {
		host, port = h, p
	}
	return host, port
}

func normalizeSlot(s string) string {
	s = strings.TrimSpace(s)
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("%02d", n)
}

func envelopeMeta(m *imap.Message) (subject, sender string) {
	if m.Envelope != nil {
		subject = m.Envelope.Subject
		if len(m.Envelope.From) > 0 {
			sender = m.Envelope.From[0].Address()
		}
	}
	return subject, sender
}

// extractBody returns the decoded, human-readable text of a message. It keeps
// both the text/plain and the tag-stripped text/html parts, plus the links
// found in href attributes, so the agent sees the full body and a one-time code
// or confirmation URL is never dropped - in particular when it appears only in
// the HTML alternative of a multipart/alternative message. The HTML part is
// omitted only when it carries nothing the plain text already has. The result
// is capped at maxChars.
func extractBody(r io.Reader, maxChars int) (string, bool) {
	if r == nil {
		return "", false
	}
	ent, err := message.Read(r)
	if ent == nil {
		if err != nil {
			b, _ := io.ReadAll(r)
			return capStr(string(b), maxChars)
		}
		return "", false
	}
	var plain, htmlBuf strings.Builder
	walkEntity(ent, &plain, &htmlBuf, 0)

	var sections []string
	plainTxt := strings.TrimSpace(plain.String())
	if plainTxt != "" {
		sections = append(sections, plainTxt)
	}
	if htmlTxt := strings.TrimSpace(collapse(htmlBuf.String())); htmlTxt != "" && !redundant(htmlTxt, plainTxt) {
		if len(sections) > 0 {
			sections = append(sections, "--- HTML part (tags stripped) ---\n"+htmlTxt)
		} else {
			sections = append(sections, htmlTxt)
		}
	}
	body := strings.Join(sections, "\n\n")
	if links := extractLinks(htmlBuf.String()); len(links) > 0 {
		body += "\n\nLinks in this email:\n" + strings.Join(links, "\n")
	}
	return capStr(strings.TrimSpace(body), maxChars)
}

// redundant reports whether the HTML-derived text carries nothing - no code,
// no link text - that the plain text does not already contain. It compares the
// two with case and whitespace removed, so the HTML part is dropped only when
// it is a genuine substring of the plain part; anything the plain part lacks
// (such as a code shown only in the HTML) keeps the HTML part in the output.
func redundant(htmlTxt, plainTxt string) bool {
	h := squash(htmlTxt)
	return h != "" && strings.Contains(squash(plainTxt), h)
}

// squash lowercases a string and removes all whitespace, for substring compares
// that ignore how plain and HTML renderings space their text.
func squash(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// walkEntity collects the decoded text of a message into plain and html, by
// content type, descending into multipart containers and forwarded
// (message/rfc822) parts.
func walkEntity(e *message.Entity, plain, htmlBuf *strings.Builder, depth int) {
	if e == nil || depth > 12 {
		return
	}
	if mr := e.MultipartReader(); mr != nil {
		for {
			p, err := mr.NextPart()
			if err != nil {
				return
			}
			walkEntity(p, plain, htmlBuf, depth+1)
		}
	}
	ct, _, _ := e.Header.ContentType()
	ct = strings.ToLower(ct)
	switch {
	case strings.HasPrefix(ct, "message/"):
		if nested, _ := message.Read(e.Body); nested != nil {
			walkEntity(nested, plain, htmlBuf, depth+1)
		}
	case ct == "text/html":
		b, _ := io.ReadAll(e.Body)
		htmlBuf.Write(b)
		htmlBuf.WriteString("\n")
	case ct == "" || strings.HasPrefix(ct, "text/"):
		b, _ := io.ReadAll(e.Body)
		plain.Write(b)
		plain.WriteString("\n")
	}
}

// extractLinks pulls URLs out of HTML: href attributes first, then any bare
// http(s) URL, deduplicated and entity-unescaped.
func extractLinks(h string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		u = strings.TrimSpace(html.UnescapeString(u))
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, m := range hrefRe.FindAllStringSubmatch(h, -1) {
		add(m[1])
	}
	for _, u := range urlRe.FindAllString(h, -1) {
		add(u)
	}
	return out
}

// collapse drops <style>/<script> blocks, strips the remaining HTML tags,
// unescapes entities and squeezes whitespace down to the visible text.
func collapse(s string) string {
	s = styleScriptRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func capStr(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	return s[:max] + "\n...[truncated]", true
}
