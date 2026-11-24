package main

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "mailotp.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustInsert(t *testing.T, s *Store, m StoredMsg) {
	t.Helper()
	if _, err := s.Insert(m); err != nil {
		t.Fatalf("Insert: %v", err)
	}
}

func TestInsertIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	m := StoredMsg{Mailbox: "01", Folder: "INBOX", UID: 42, Subject: "hi", Date: time.Now()}
	ins, err := s.Insert(m)
	if err != nil || !ins {
		t.Fatalf("first insert: ins=%v err=%v", ins, err)
	}
	ins, err = s.Insert(m)
	if err != nil {
		t.Fatalf("second insert err: %v", err)
	}
	if ins {
		t.Fatal("second insert of same (mailbox,folder,uid) should be ignored")
	}
	got, err := s.Query(Filter{Mailbox: "01"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row after idempotent insert, got %d", len(got))
	}
}

func TestQueryFiltersAndOrder(t *testing.T) {
	s := newTestStore(t)
	base := time.Now().Add(-time.Hour)
	mustInsert(t, s, StoredMsg{Mailbox: "01", Folder: "INBOX", UID: 1, From: "noreply@amazon.com", Subject: "Amazon code 111", Body: "your code is 111", Date: base})
	mustInsert(t, s, StoredMsg{Mailbox: "01", Folder: "INBOX", UID: 2, From: "verify@ibm.com", Subject: "IBM verification", Body: "code 222", Date: base.Add(10 * time.Minute)})
	mustInsert(t, s, StoredMsg{Mailbox: "02", Folder: "Spam", UID: 1, From: "verify@ibm.com", Subject: "IBM verification", Body: "code 333", Date: base.Add(20 * time.Minute)})

	// From filter.
	got, _ := s.Query(Filter{From: "ibm.com"})
	if len(got) != 2 {
		t.Fatalf("from=ibm.com want 2, got %d", len(got))
	}
	// Newest first.
	if got[0].Body != "code 333" {
		t.Fatalf("want newest (code 333) first, got %q", got[0].Body)
	}
	// Mailbox + subject.
	got, _ = s.Query(Filter{Mailbox: "01", Subject: "verification"})
	if len(got) != 1 || got[0].UID != 2 {
		t.Fatalf("mailbox=01 subject=verification want uid 2, got %+v", got)
	}
	// Body substring, case-insensitive.
	got, _ = s.Query(Filter{Body: "CODE IS 111"})
	if len(got) != 1 || got[0].UID != 1 {
		t.Fatalf("body=CODE IS 111 want uid 1, got %+v", got)
	}
	// Since floor.
	got, _ = s.Query(Filter{SinceTime: base.Add(15 * time.Minute)})
	if len(got) != 1 || got[0].Body != "code 333" {
		t.Fatalf("since floor want only code 333, got %+v", got)
	}
}

// TestConcurrentSignupIsolation is the TICKET-006 acceptance case at the store
// level: two agents sign up on the same site using the same shared mailbox, each
// with its own +tag; each must read only its own code even though both messages
// come from the same sender with the same subject.
func TestConcurrentSignupIsolation(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	mustInsert(t, s, StoredMsg{
		Mailbox: "01", Folder: "INBOX", UID: 10,
		From: "no-reply@site.example", Subject: "Confirm your email",
		To: "psylinux.hackerone.test01+a1b2c3@gmail.com", DeliveredTo: "psylinux.hackerone.test01+a1b2c3@gmail.com",
		Body: "Your code is 100001", Date: now,
	})
	mustInsert(t, s, StoredMsg{
		Mailbox: "01", Folder: "INBOX", UID: 11,
		From: "no-reply@site.example", Subject: "Confirm your email",
		To: "psylinux.hackerone.test01+d4e5f6@gmail.com", DeliveredTo: "psylinux.hackerone.test01+d4e5f6@gmail.com",
		Body: "Your code is 200002", Date: now.Add(time.Second),
	})

	// Agent A reads by its tag, agent B by its tag.
	a, _ := s.Query(Filter{Tag: "+a1b2c3"})
	if len(a) != 1 || a[0].Body != "Your code is 100001" {
		t.Fatalf("agent A should read only its own code, got %+v", a)
	}
	b, _ := s.Query(Filter{Tag: "+d4e5f6"})
	if len(b) != 1 || b[0].Body != "Your code is 200002" {
		t.Fatalf("agent B should read only its own code, got %+v", b)
	}
	// A bare mailbox query under this race is ambiguous (returns both), which is
	// exactly why alloc/+tag exists.
	both, _ := s.Query(Filter{Mailbox: "01", Subject: "Confirm your email"})
	if len(both) != 2 {
		t.Fatalf("shared-mailbox query should see both messages, got %d", len(both))
	}
}

func TestAllocationLifecycle(t *testing.T) {
	s := newTestStore(t)
	a := Allocation{Handle: "h1", Mailbox: "01", Tag: "a1b2c3", Address: "x+a1b2c3@gmail.com", State: "open", CreatedAt: time.Now(), TTL: 15 * time.Minute}
	if err := s.CreateAllocation(a); err != nil {
		t.Fatalf("CreateAllocation: %v", err)
	}
	got, ok, err := s.GetAllocation("h1")
	if err != nil || !ok {
		t.Fatalf("GetAllocation: ok=%v err=%v", ok, err)
	}
	if got.Tag != "a1b2c3" || got.Address != "x+a1b2c3@gmail.com" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if err := s.ReleaseAllocation("h1"); err != nil {
		t.Fatalf("ReleaseAllocation: %v", err)
	}
	got, _, _ = s.GetAllocation("h1")
	if got.State != "released" {
		t.Fatalf("want released, got %q", got.State)
	}
}

func TestLeaseExclusivity(t *testing.T) {
	s := newTestStore(t)
	ok, err := s.AcquireLease("01", "h1", 15*time.Minute)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	// Another handle cannot take the same mailbox while held.
	ok, err = s.AcquireLease("01", "h2", 15*time.Minute)
	if err != nil {
		t.Fatalf("second acquire err: %v", err)
	}
	if ok {
		t.Fatal("second handle should not acquire a held mailbox")
	}
	// Re-acquiring with the same handle is fine (idempotent extend).
	ok, _ = s.AcquireLease("01", "h1", 15*time.Minute)
	if !ok {
		t.Fatal("same handle should re-acquire its own lease")
	}
	// After release, another handle can take it.
	if err := s.ReleaseAllocation("h1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	ok, _ = s.AcquireLease("01", "h2", 15*time.Minute)
	if !ok {
		t.Fatal("after release the mailbox should be free")
	}
}

func TestSyncState(t *testing.T) {
	s := newTestStore(t)
	if _, _, ok, _ := s.GetSyncState("01", "INBOX"); ok {
		t.Fatal("no sync state expected initially")
	}
	if err := s.SetSyncState("01", "INBOX", 123, 456); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}
	uv, lu, ok, err := s.GetSyncState("01", "INBOX")
	if err != nil || !ok || uv != 123 || lu != 456 {
		t.Fatalf("GetSyncState: uv=%d lu=%d ok=%v err=%v", uv, lu, ok, err)
	}
	if err := s.SetSyncState("01", "INBOX", 123, 789); err != nil {
		t.Fatalf("update SetSyncState: %v", err)
	}
	_, lu, _, _ = s.GetSyncState("01", "INBOX")
	if lu != 789 {
		t.Fatalf("want last_uid 789 after update, got %d", lu)
	}
}
