// proto.go - the local wire protocol between the thin `mailotp` client and the
// `mailotp serve` daemon. One newline-delimited JSON request, one reply, over a
// unix socket on the data volume. This is deliberately not HTTP and not exposed
// on any network: agents on the same host read the synced corpus through it, and
// only the daemon ever opens IMAP.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Request is a single client call. Op selects the operation; the rest are the
// optional arguments each op reads.
type Request struct {
	Op       string `json:"op"` // query | fetch | alloc | release | mailboxes | ping
	Mailbox  string `json:"mailbox,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Body     string `json:"body,omitempty"`
	Handle   string `json:"handle,omitempty"`
	Agent    string `json:"agent,omitempty"` // caller identity for read attribution (e.g. task id)
	SinceSec int    `json:"since_sec,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	WaitSec  int    `json:"wait_sec,omitempty"`
	TTLSec   int    `json:"ttl_sec,omitempty"`
	NoPlus   bool   `json:"no_plus,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

// MsgView is a message as handed to the client (body capped at MaxChars).
type MsgView struct {
	Mailbox   string `json:"mailbox"`
	Folder    string `json:"folder"`
	From      string `json:"from"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Date      string `json:"date"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated,omitempty"`
}

// MailboxView is one mailbox's state in a `mailboxes` reply.
type MailboxView struct {
	Mailbox  string `json:"mailbox"`
	Address  string `json:"address"`
	State    string `json:"state"`
	Count    int    `json:"count"`
	LastDate string `json:"last_date,omitempty"`
}

// Response is the daemon's single reply to a Request.
type Response struct {
	OK        bool          `json:"ok"`
	Error     string        `json:"error,omitempty"`
	Messages  []MsgView     `json:"messages,omitempty"`
	Mailboxes []MailboxView `json:"mailboxes,omitempty"`
	Handle    string        `json:"handle,omitempty"`
	Address   string        `json:"address,omitempty"`
	Tag       string        `json:"tag,omitempty"`
}

// defaultSocketPath is where the client looks for the daemon and where the
// daemon listens, overridable with MAILOTP_SOCKET.
func defaultSocketPath() string {
	if p := os.Getenv("MAILOTP_SOCKET"); p != "" {
		return p
	}
	return filepath.Join(defaultDataDir(), "mailotp.sock")
}

// defaultDBPath is the SQLite corpus location, overridable with MAILOTP_DB.
func defaultDBPath() string {
	if p := os.Getenv("MAILOTP_DB"); p != "" {
		return p
	}
	return filepath.Join(defaultDataDir(), "store.db")
}

// defaultDataDir is the daemon's working directory, overridable with
// MAILOTP_DATA_DIR. In the scopeweaver container this resolves under the data
// volume so the corpus survives a recreate.
func defaultDataDir() string {
	if d := os.Getenv("MAILOTP_DATA_DIR"); d != "" {
		return d
	}
	return "/app/data/mailotp"
}

// roundtrip dials the daemon, sends one request, and returns its reply. The read
// deadline is sized to outlast a fetch --wait so a long block is not cut short.
func roundtrip(socketPath string, req Request) (Response, error) {
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return Response{}, fmt.Errorf("mailotp serve not reachable at %s: %w", socketPath, err)
	}
	defer conn.Close()

	readWindow := 30 * time.Second
	if req.WaitSec > 0 {
		readWindow = time.Duration(req.WaitSec+30) * time.Second
	}
	conn.SetDeadline(time.Now().Add(readWindow))

	enc, _ := json.Marshal(req)
	if _, err := conn.Write(append(enc, '\n')); err != nil {
		return Response{}, fmt.Errorf("write request: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Response{}, fmt.Errorf("read reply: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("decode reply: %w", err)
	}
	return resp, nil
}

// writeResponse marshals one reply and terminates it with a newline.
func writeResponse(conn net.Conn, resp Response) error {
	enc, err := json.Marshal(resp)
	if err != nil {
		enc, _ = json.Marshal(Response{Error: "encode reply: " + err.Error()})
	}
	_, err = conn.Write(append(enc, '\n'))
	return err
}
