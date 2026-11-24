// store.go - the durable local corpus behind `mailotp serve`.
//
// A single SQLite database on the data volume holds every message the poller
// has ingested, plus the allocation and lease bookkeeping that gives one agent
// an address no other agent reads. SQLite (pure Go, modernc.org/sqlite, no cgo)
// keeps the store decoupled from the artex Postgres and lets it survive a
// container recreate. All agent-facing reads are local queries against this
// store; only the poller ever talks to Gmail.
package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed message corpus and allocation bookkeeping.
type Store struct {
	db *sql.DB
}

// StoredMsg is one ingested email as it lives in the store. Body holds the full
// decoded text (plain + tag-stripped HTML + links); callers cap it at read time.
type StoredMsg struct {
	ID          int64
	Mailbox     string
	Folder      string
	UID         uint32
	MessageID   string
	From        string
	To          string
	DeliveredTo string
	Subject     string
	Date        time.Time
	Body        string
	IngestedAt  time.Time
}

// Allocation is a reserved address handed to one agent so it can read only its
// own message. With a +tag the mailbox stays shared; with NoPlus the whole
// mailbox is leased for the sign-up window.
type Allocation struct {
	Handle    string
	Mailbox   string
	Tag       string
	Address   string
	NoPlus    bool
	State     string // "open" | "released" | "expired"
	CreatedAt time.Time
	TTL       time.Duration
}

// Filter is a local search over the corpus. Every field is optional; the zero
// Filter returns the newest messages across all mailboxes up to Limit.
type Filter struct {
	Mailbox   string
	From      string
	To        string
	Subject   string
	Body      string
	Tag       string // substring that must appear in To or Delivered-To (from an allocation)
	SinceTime time.Time
	Limit     int
}

const schema = `
CREATE TABLE IF NOT EXISTS messages (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  mailbox      TEXT NOT NULL,
  folder       TEXT NOT NULL,
  uid          INTEGER NOT NULL,
  message_id   TEXT,
  from_addr    TEXT,
  to_addr      TEXT,
  delivered_to TEXT,
  subject      TEXT,
  date         INTEGER,
  body         TEXT,
  ingested_at  INTEGER NOT NULL,
  UNIQUE(mailbox, folder, uid)
);
CREATE INDEX IF NOT EXISTS idx_messages_mailbox_date ON messages(mailbox, date DESC);
CREATE INDEX IF NOT EXISTS idx_messages_date ON messages(date DESC);

CREATE TABLE IF NOT EXISTS allocations (
  handle             TEXT PRIMARY KEY,
  mailbox            TEXT NOT NULL,
  tag                TEXT,
  address            TEXT NOT NULL,
  no_plus            INTEGER NOT NULL DEFAULT 0,
  state              TEXT NOT NULL,
  created_at         INTEGER NOT NULL,
  ttl_seconds        INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS leases (
  mailbox      TEXT PRIMARY KEY,
  handle       TEXT NOT NULL,
  state        TEXT NOT NULL,
  acquired_at  INTEGER NOT NULL,
  ttl_seconds  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_state (
  mailbox     TEXT NOT NULL,
  folder      TEXT NOT NULL,
  uidvalidity INTEGER NOT NULL,
  last_uid    INTEGER NOT NULL,
  PRIMARY KEY (mailbox, folder)
);
`

// OpenStore opens (creating if needed) the SQLite store at path and applies the
// schema. WAL plus a busy timeout let the poller write while agent queries read
// concurrently without "database is locked".
func OpenStore(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Insert stores one message, idempotently. A message already present for the
// same (mailbox, folder, uid) is left untouched and inserted reports false.
func (s *Store) Insert(m StoredMsg) (inserted bool, err error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO messages
		   (mailbox, folder, uid, message_id, from_addr, to_addr, delivered_to, subject, date, body, ingested_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.Mailbox, m.Folder, m.UID, m.MessageID, m.From, m.To, m.DeliveredTo, m.Subject,
		m.Date.Unix(), m.Body, time.Now().Unix(),
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Query returns messages matching the filter, newest first. All substring
// filters are case-insensitive; Tag matches the To or Delivered-To header, which
// is where Gmail preserves a +tag.
func (s *Store) Query(f Filter) ([]StoredMsg, error) {
	var where []string
	var args []any
	like := func(col, v string) {
		where = append(where, "lower("+col+") LIKE '%'||lower(?)||'%'")
		args = append(args, v)
	}
	if f.Mailbox != "" {
		where = append(where, "mailbox = ?")
		args = append(args, f.Mailbox)
	}
	if f.From != "" {
		like("from_addr", f.From)
	}
	if f.To != "" {
		where = append(where, "(lower(to_addr) LIKE '%'||lower(?)||'%' OR lower(delivered_to) LIKE '%'||lower(?)||'%')")
		args = append(args, f.To, f.To)
	}
	if f.Tag != "" {
		where = append(where, "(lower(to_addr) LIKE '%'||lower(?)||'%' OR lower(delivered_to) LIKE '%'||lower(?)||'%')")
		args = append(args, f.Tag, f.Tag)
	}
	if f.Subject != "" {
		like("subject", f.Subject)
	}
	if f.Body != "" {
		like("body", f.Body)
	}
	if !f.SinceTime.IsZero() {
		where = append(where, "date >= ?")
		args = append(args, f.SinceTime.Unix())
	}
	q := `SELECT id, mailbox, folder, uid, message_id, from_addr, to_addr, delivered_to, subject, date, body, ingested_at FROM messages`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY date DESC, id DESC"
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredMsg
	for rows.Next() {
		var m StoredMsg
		var date, ingested int64
		if err := rows.Scan(&m.ID, &m.Mailbox, &m.Folder, &m.UID, &m.MessageID, &m.From, &m.To, &m.DeliveredTo, &m.Subject, &date, &m.Body, &ingested); err != nil {
			return nil, err
		}
		m.Date = time.Unix(date, 0)
		m.IngestedAt = time.Unix(ingested, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// MailboxStat is a per-mailbox summary for `mailotp mailboxes`.
type MailboxStat struct {
	Mailbox  string
	Count    int
	LastDate time.Time
}

// Stats returns the message count and newest date per mailbox.
func (s *Store) Stats() (map[string]MailboxStat, error) {
	rows, err := s.db.Query(`SELECT mailbox, COUNT(*), COALESCE(MAX(date),0) FROM messages GROUP BY mailbox`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MailboxStat{}
	for rows.Next() {
		var st MailboxStat
		var last int64
		if err := rows.Scan(&st.Mailbox, &st.Count, &last); err != nil {
			return nil, err
		}
		if last > 0 {
			st.LastDate = time.Unix(last, 0)
		}
		out[st.Mailbox] = st
	}
	return out, rows.Err()
}

// CountOpenAllocations returns, per mailbox, how many allocations are still open
// and unexpired (sign-ups awaiting their code).
func (s *Store) CountOpenAllocations() (map[string]int, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT mailbox, COUNT(*) FROM allocations WHERE state='open' AND created_at + ttl_seconds >= ? GROUP BY mailbox`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var mb string
		var n int
		if err := rows.Scan(&mb, &n); err != nil {
			return nil, err
		}
		out[mb] = n
	}
	return out, rows.Err()
}

// CountHeldLeases returns, per mailbox, whether a whole-mailbox lease is held and
// unexpired (0 or 1 per mailbox).
func (s *Store) CountHeldLeases() (map[string]int, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT mailbox, COUNT(*) FROM leases WHERE state='held' AND acquired_at + ttl_seconds >= ? GROUP BY mailbox`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var mb string
		var n int
		if err := rows.Scan(&mb, &n); err != nil {
			return nil, err
		}
		out[mb] = n
	}
	return out, rows.Err()
}

// CreateAllocation records a new open allocation.
func (s *Store) CreateAllocation(a Allocation) error {
	_, err := s.db.Exec(
		`INSERT INTO allocations (handle, mailbox, tag, address, no_plus, state, created_at, ttl_seconds)
		 VALUES (?,?,?,?,?,?,?,?)`,
		a.Handle, a.Mailbox, a.Tag, a.Address, boolToInt(a.NoPlus), "open", a.CreatedAt.Unix(), int64(a.TTL.Seconds()),
	)
	return err
}

// GetAllocation returns the allocation for a handle, if present.
func (s *Store) GetAllocation(handle string) (Allocation, bool, error) {
	var a Allocation
	var noPlus, created, ttl int64
	err := s.db.QueryRow(
		`SELECT handle, mailbox, tag, address, no_plus, state, created_at, ttl_seconds FROM allocations WHERE handle = ?`,
		handle,
	).Scan(&a.Handle, &a.Mailbox, &a.Tag, &a.Address, &noPlus, &a.State, &created, &ttl)
	if err == sql.ErrNoRows {
		return Allocation{}, false, nil
	}
	if err != nil {
		return Allocation{}, false, err
	}
	a.NoPlus = noPlus != 0
	a.CreatedAt = time.Unix(created, 0)
	a.TTL = time.Duration(ttl) * time.Second
	return a, true, nil
}

// ReleaseAllocation marks an allocation released and frees any mailbox lease it
// held.
func (s *Store) ReleaseAllocation(handle string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE allocations SET state='released' WHERE handle=?`, handle); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE leases SET state='released' WHERE handle=?`, handle); err != nil {
		return err
	}
	return tx.Commit()
}

// AcquireLease takes an exclusive lease on a mailbox for a handle. It fails
// (ok=false) when a non-expired lease is already held by another handle.
func (s *Store) AcquireLease(mailbox, handle string, ttl time.Duration) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var curHandle, state string
	var acquired, ttlSec int64
	err = tx.QueryRow(`SELECT handle, state, acquired_at, ttl_seconds FROM leases WHERE mailbox=?`, mailbox).
		Scan(&curHandle, &state, &acquired, &ttlSec)
	now := time.Now()
	held := err == nil && state == "held" && now.Before(time.Unix(acquired, 0).Add(time.Duration(ttlSec)*time.Second))
	if held && curHandle != handle {
		return false, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if _, err := tx.Exec(
		`INSERT INTO leases (mailbox, handle, state, acquired_at, ttl_seconds)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(mailbox) DO UPDATE SET handle=excluded.handle, state=excluded.state, acquired_at=excluded.acquired_at, ttl_seconds=excluded.ttl_seconds`,
		mailbox, handle, "held", now.Unix(), int64(ttl.Seconds()),
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// Prune deletes messages older than the retention window and marks expired
// allocations and leases, keeping the store bounded on a long-running daemon.
func (s *Store) Prune(messageTTL, bookkeepingTTL time.Duration) error {
	now := time.Now()
	if _, err := s.db.Exec(`DELETE FROM messages WHERE ingested_at < ?`, now.Add(-messageTTL).Unix()); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE allocations SET state='expired' WHERE state='open' AND created_at + ttl_seconds < ?`, now.Unix()); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE leases SET state='expired' WHERE state='held' AND acquired_at + ttl_seconds < ?`, now.Unix()); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM allocations WHERE state!='open' AND created_at < ?`, now.Add(-bookkeepingTTL).Unix()); err != nil {
		return err
	}
	return nil
}

// GetSyncState returns the stored UIDVALIDITY and last ingested UID for a
// mailbox/folder.
func (s *Store) GetSyncState(mailbox, folder string) (uidvalidity, lastUID uint32, ok bool, err error) {
	var uv, lu int64
	err = s.db.QueryRow(`SELECT uidvalidity, last_uid FROM sync_state WHERE mailbox=? AND folder=?`, mailbox, folder).Scan(&uv, &lu)
	if err == sql.ErrNoRows {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return uint32(uv), uint32(lu), true, nil
}

// SetSyncState records the UIDVALIDITY and last ingested UID for a
// mailbox/folder.
func (s *Store) SetSyncState(mailbox, folder string, uidvalidity, lastUID uint32) error {
	_, err := s.db.Exec(
		`INSERT INTO sync_state (mailbox, folder, uidvalidity, last_uid)
		 VALUES (?,?,?,?)
		 ON CONFLICT(mailbox, folder) DO UPDATE SET uidvalidity=excluded.uidvalidity, last_uid=excluded.last_uid`,
		mailbox, folder, uidvalidity, lastUID,
	)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
