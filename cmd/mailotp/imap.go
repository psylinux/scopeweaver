// imap.go - all Gmail/IMAP access. Only the daemon's poller calls fetchFolder;
// the direct fetch path (scanOnce) is a fallback used when the daemon is not
// running. The CLI client never imports any of this at runtime.
package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
	"github.com/Autumn-27/artex/sharedsecrets"
)

// initCharset wires the charset decoders into go-imap and go-message once, so a
// non-UTF-8 subject or body is decoded rather than mangled.
func initCharset() {
	imap.CharsetReader = charset.Reader
	message.CharsetReader = charset.Reader
}

// imapDialLogin opens an authenticated IMAPS connection for one mailbox.
func imapDialLogin(email, pass string) (*client.Client, error) {
	host, port := imapEndpoint()
	c, err := client.DialTLS(host+":"+port, &tls.Config{ServerName: host})
	if err != nil {
		return nil, fmt.Errorf("connect %s:%s: %w", host, port, err)
	}
	if err := c.Login(email, pass); err != nil {
		c.Logout()
		return nil, fmt.Errorf("login %s: %w", email, err)
	}
	return c, nil
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

// imapMailboxName maps a logical folder ("INBOX", "Spam") to the IMAP mailbox to
// SELECT. Gmail exposes spam as "[Gmail]/Spam".
func imapMailboxName(folder string) string {
	switch strings.ToLower(folder) {
	case "spam", "junk":
		return "[Gmail]/Spam"
	case "inbox":
		return "INBOX"
	default:
		return folder
	}
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

// recipients joins a message's To addresses, for the --to substring filter and
// for display. Gmail keeps the +tag in the To header, so matching the address
// the agent signed up with (e.g. test01+t15i7700@gmail.com) pins the message to
// that one request even when the mailbox is shared by other agents.
func recipients(m *imap.Message) string {
	if m.Envelope == nil {
		return ""
	}
	addrs := make([]string, 0, len(m.Envelope.To))
	for _, a := range m.Envelope.To {
		addrs = append(addrs, a.Address())
	}
	return strings.Join(addrs, ", ")
}

// mbConfig is one configured mailbox slot.
type mbConfig struct {
	Slot  string
	Email string
	Pass  string
}

// configuredMailboxes lists the mailbox slots that have an address in the shared
// secrets, in slot order. Passwords are read but never printed.
func configuredMailboxes() []mbConfig {
	var out []mbConfig
	for n := 1; n <= 20; n++ {
		slot := fmt.Sprintf("%02d", n)
		email := sharedsecrets.Get("ARTEX_MAIL_" + slot + "_EMAIL")
		if email == "" {
			continue
		}
		out = append(out, mbConfig{Slot: slot, Email: email, Pass: sharedsecrets.Get("ARTEX_MAIL_" + slot + "_API_TOKEN")})
	}
	return out
}

func mailboxAddress(slot string) string {
	return sharedsecrets.Get("ARTEX_MAIL_" + slot + "_EMAIL")
}

// mailboxCreds returns the address and app password for a slot. The password is
// used only to open an IMAP connection and is never printed.
func mailboxCreds(slot string) (email, pass string) {
	return sharedsecrets.Get("ARTEX_MAIL_" + slot + "_EMAIL"), sharedsecrets.Get("ARTEX_MAIL_" + slot + "_API_TOKEN")
}

// fetchFolder ingests the messages in one folder with UID greater than lastUID,
// parsing each into a StoredMsg. On the first sync for a folder (lastUID 0) it
// backfills at most backfill recent messages rather than the whole mailbox. If
// the folder's UIDVALIDITY no longer matches storedValidity, the UID space has
// been renumbered, so lastUID is dropped and the recent window is re-backfilled.
// It returns the folder's current UIDVALIDITY and the new high-water UID. The
// SELECT is read-only and fetches use Peek, so nothing is marked read.
func fetchFolder(c *client.Client, mailbox, folder string, lastUID, storedValidity, backfill uint32, maxChars int) (msgs []StoredMsg, uidValidity, newLastUID uint32, err error) {
	name := imapMailboxName(folder)
	mbox, err := c.Select(name, true)
	if err != nil {
		return nil, 0, lastUID, fmt.Errorf("select %s: %w", name, err)
	}
	uidValidity = mbox.UidValidity
	if storedValidity != 0 && uidValidity != storedValidity {
		lastUID = 0 // UID space renumbered; re-scan the recent window
	}
	if mbox.Messages == 0 || mbox.UidNext == 0 {
		return nil, uidValidity, lastUID, nil
	}
	highest := mbox.UidNext - 1
	from := lastUID + 1
	if lastUID == 0 && backfill > 0 && highest > backfill {
		from = highest - backfill + 1
	}
	if from > highest {
		return nil, uidValidity, lastUID, nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddRange(from, highest)
	bodySection := &imap.BodySectionName{Peek: true}
	dtSection := &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier, Fields: []string{"Delivered-To"}},
		Peek:         true,
	}
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchInternalDate, bodySection.FetchItem(), dtSection.FetchItem()}

	ch := make(chan *imap.Message, 32)
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(seqset, items, ch) }()

	newLastUID = lastUID
	for m := range ch {
		if m.Uid > newLastUID {
			newLastUID = m.Uid
		}
		subj, sender := envelopeMeta(m)
		sm := StoredMsg{
			Mailbox: mailbox,
			Folder:  folder,
			UID:     m.Uid,
			From:    sender,
			To:      recipients(m),
			Subject: subj,
			Date:    m.InternalDate,
		}
		if m.Envelope != nil {
			sm.MessageID = m.Envelope.MessageId
		}
		if dt := m.GetBody(dtSection); dt != nil {
			raw, _ := io.ReadAll(dt)
			sm.DeliveredTo = parseHeaderValue(string(raw), "Delivered-To")
		}
		sm.Body, _ = extractBody(m.GetBody(bodySection), maxChars)
		msgs = append(msgs, sm)
	}
	if ferr := <-done; ferr != nil {
		return msgs, uidValidity, newLastUID, fmt.Errorf("uid fetch %s: %w", name, ferr)
	}
	return msgs, uidValidity, newLastUID, nil
}

// parseHeaderValue returns the (possibly folded) value of the named header from
// a raw header block, joining multiple occurrences with ", ".
func parseHeaderValue(raw, field string) string {
	var vals []string
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	prefix := strings.ToLower(field) + ":"
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.ToLower(lines[i]), prefix) {
			continue
		}
		v := strings.TrimSpace(lines[i][len(prefix):])
		for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t")) {
			v += " " + strings.TrimSpace(lines[i+1])
			i++
		}
		if v != "" {
			vals = append(vals, v)
		}
	}
	return strings.Join(vals, ", ")
}

// --- direct fetch fallback (used only when the daemon is not running) ---

type mailMsg struct {
	subject   string
	from      string
	to        string
	date      time.Time
	body      string
	truncated bool
}

// scanOnce returns the newest INBOX message received at or after floor that
// matches the optional filters and has not been returned before. It is the
// single-shot, direct-IMAP read behind `fetch --direct`.
func scanOnce(c *client.Client, section *imap.BodySectionName, seen map[uint32]bool, floor time.Time, fromF, subjF, toF string, maxChars int) (*mailMsg, error) {
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
		rcpt := recipients(m)
		if toF != "" && !strings.Contains(strings.ToLower(rcpt), strings.ToLower(toF)) {
			continue
		}
		seen[m.Uid] = true
		body, trunc := extractBody(m.GetBody(section), maxChars)
		return &mailMsg{subject: subj, from: sender, to: rcpt, date: m.InternalDate, body: body, truncated: trunc}, nil
	}
	return nil, nil
}
