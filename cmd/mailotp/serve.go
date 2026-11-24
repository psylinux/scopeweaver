// serve.go - the `mailotp serve` daemon. It runs one IMAP poller per configured
// mailbox (persistent connection, read-only, INBOX + Spam) that syncs arriving
// mail into the SQLite corpus, and a unix-socket server that answers agent
// queries from that corpus. It is the only component that talks to Gmail.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/emersion/go-imap/client"
)

type daemon struct {
	store      *Store
	socketPath string
	poll       time.Duration
	folders    []string
	backfill   uint32
	maxChars   int
	mboxes     []mbConfig
	metrics    *metrics
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	socketPath := fs.String("socket", defaultSocketPath(), "unix socket to listen on")
	dbPath := fs.String("db", defaultDBPath(), "SQLite corpus path")
	poll := fs.Duration("poll", 15*time.Second, "interval between IMAP polls per mailbox")
	foldersCSV := fs.String("folders", "INBOX,Spam", "comma-separated folders to sync")
	backfill := fs.Int("backfill", 50, "messages to ingest on first sync of a folder")
	maxChars := fs.Int("max-chars", defaultMaxChars, "cap stored body length")
	pruneDays := fs.Int("retain-days", 7, "prune messages older than this many days")
	metricsAddr := fs.String("metrics-addr", ":9469", "address for the Prometheus /metrics endpoint (empty disables)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	mboxes := configuredMailboxes()
	if len(mboxes) == 0 {
		fmt.Fprintln(os.Stderr, "mailotp serve: no mailboxes configured (ARTEX_MAIL_<NN>_EMAIL in secrets.yaml)")
		return 1
	}
	for _, mb := range mboxes {
		if mb.Pass == "" {
			fmt.Fprintf(os.Stderr, "mailotp serve: mailbox %s has no app password (ARTEX_MAIL_%s_API_TOKEN)\n", mb.Slot, mb.Slot)
			return 1
		}
	}

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "mailotp serve: data dir: %v\n", err)
		return 1
	}
	store, err := OpenStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailotp serve: open store: %v\n", err)
		return 1
	}
	defer store.Close()

	initCharset()
	d := &daemon{
		store:      store,
		socketPath: *socketPath,
		poll:       *poll,
		folders:    splitCSV(*foldersCSV),
		backfill:   uint32(*backfill),
		maxChars:   *maxChars,
		mboxes:     mboxes,
		metrics:    newMetrics(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.SetPrefix("mailotp serve: ")
	log.SetFlags(log.LstdFlags | log.LUTC)
	slots := make([]string, len(mboxes))
	for i, mb := range mboxes {
		slots[i] = mb.Slot
	}
	log.Printf("starting: mailboxes=%s folders=%s poll=%s socket=%s db=%s", strings.Join(slots, ","), strings.Join(d.folders, ","), d.poll, d.socketPath, *dbPath)

	for _, mb := range mboxes {
		go d.runPoller(ctx, mb)
	}
	go d.runPruner(ctx, time.Duration(*pruneDays)*24*time.Hour)
	go d.runGaugeRefresh(ctx)
	if *metricsAddr != "" {
		d.startMetricsServer(ctx, *metricsAddr)
	}

	if err := d.serveSocket(ctx); err != nil {
		log.Printf("socket server stopped: %v", err)
		return 1
	}
	log.Printf("shutdown")
	return 0
}

// runPoller keeps one mailbox synced, reconnecting with capped backoff whenever
// the connection or a poll fails.
func (d *daemon) runPoller(ctx context.Context, mb mbConfig) {
	backoff := time.Second
	for ctx.Err() == nil {
		d.metrics.reconnects.WithLabelValues(mb.Slot).Inc()
		c, err := imapDialLogin(mb.Email, mb.Pass)
		if err != nil {
			d.metrics.pollerUp.WithLabelValues(mb.Slot).Set(0)
			d.metrics.pollErrors.WithLabelValues(mb.Slot).Inc()
			log.Printf("poller %s connect: %v", mb.Slot, err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		d.metrics.pollerUp.WithLabelValues(mb.Slot).Set(1)
		backoff = time.Second
		err = d.pollLoop(ctx, c, mb)
		c.Logout()
		d.metrics.pollerUp.WithLabelValues(mb.Slot).Set(0)
		if err != nil && ctx.Err() == nil {
			d.metrics.pollErrors.WithLabelValues(mb.Slot).Inc()
			log.Printf("poller %s: %v (reconnecting)", mb.Slot, err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
		}
	}
}

func (d *daemon) pollLoop(ctx context.Context, c *client.Client, mb mbConfig) error {
	for ctx.Err() == nil {
		for _, folder := range d.folders {
			n, err := d.syncFolder(c, mb, folder)
			if err != nil {
				return err
			}
			if n > 0 {
				log.Printf("poller %s/%s: ingested %d", mb.Slot, folder, n)
			}
		}
		if !sleepCtx(ctx, d.poll) {
			return nil
		}
	}
	return nil
}

func (d *daemon) syncFolder(c *client.Client, mb mbConfig, folder string) (int, error) {
	uvStored, lastUID, _, err := d.store.GetSyncState(mb.Slot, folder)
	if err != nil {
		return 0, fmt.Errorf("sync state: %w", err)
	}
	msgs, uv, newLast, err := fetchFolder(c, mb.Slot, folder, lastUID, uvStored, d.backfill, d.maxChars)
	if err != nil {
		return 0, err
	}
	ingested := 0
	for _, m := range msgs {
		ins, err := d.store.Insert(m)
		if err != nil {
			return ingested, fmt.Errorf("insert: %w", err)
		}
		if ins {
			ingested++
		}
	}
	if err := d.store.SetSyncState(mb.Slot, folder, uv, newLast); err != nil {
		return ingested, fmt.Errorf("set sync state: %w", err)
	}
	if ingested > 0 {
		d.metrics.ingested.WithLabelValues(mb.Slot, folder).Add(float64(ingested))
	}
	d.metrics.lastSync.WithLabelValues(mb.Slot, folder).Set(float64(time.Now().Unix()))
	return ingested, nil
}

// runGaugeRefresh periodically refreshes the gauges that are cheaper to read
// from the store than to track inline: corpus size, open allocations and held
// leases per mailbox.
func (d *daemon) runGaugeRefresh(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		if stats, err := d.store.Stats(); err == nil {
			for _, mb := range d.mboxes {
				d.metrics.stored.WithLabelValues(mb.Slot).Set(float64(stats[mb.Slot].Count))
			}
		}
		if open, err := d.store.CountOpenAllocations(); err == nil {
			for _, mb := range d.mboxes {
				d.metrics.allocOpen.WithLabelValues(mb.Slot).Set(float64(open[mb.Slot]))
			}
		}
		if held, err := d.store.CountHeldLeases(); err == nil {
			for _, mb := range d.mboxes {
				d.metrics.leasesHeld.WithLabelValues(mb.Slot).Set(float64(held[mb.Slot]))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// startMetricsServer serves the Prometheus endpoint on addr until ctx is done.
// It is the operator monitoring plane; agents never use it.
func (d *daemon) startMetricsServer(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", d.metrics.handler())
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	go func() {
		log.Printf("metrics on %s/metrics", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server: %v", err)
		}
	}()
}

func (d *daemon) runPruner(ctx context.Context, ttl time.Duration) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := d.store.Prune(ttl, 7*24*time.Hour); err != nil {
			log.Printf("prune: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// serveSocket listens on the unix socket and dispatches each connection. It
// returns when ctx is cancelled.
func (d *daemon) serveSocket(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(d.socketPath), 0o700); err != nil {
		return fmt.Errorf("socket dir: %w", err)
	}
	_ = os.Remove(d.socketPath) // clear a stale socket from a prior run
	ln, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", d.socketPath, err)
	}
	_ = os.Chmod(d.socketPath, 0o660)
	go func() {
		<-ctx.Done()
		ln.Close()
		_ = os.Remove(d.socketPath)
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("accept: %v", err)
			continue
		}
		go d.handleConn(ctx, conn)
	}
}

func (d *daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	// The request line must arrive promptly; the response (for fetch) may take
	// up to the requested wait, so the deadline is extended per op below.
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeResponse(conn, Response{Error: "bad request: " + err.Error()})
		return
	}
	conn.SetDeadline(time.Now().Add(time.Duration(req.WaitSec+40) * time.Second))
	writeResponse(conn, d.dispatch(ctx, req))
}

func (d *daemon) dispatch(ctx context.Context, req Request) Response {
	switch req.Op {
	case "ping":
		return Response{OK: true}
	case "mailboxes":
		return d.opMailboxes()
	case "query":
		return d.opQuery(req)
	case "fetch":
		return d.opFetch(ctx, req)
	case "alloc":
		return d.opAlloc(req)
	case "release":
		return d.opRelease(req)
	default:
		return Response{Error: "unknown op " + req.Op}
	}
}

// filterFor builds a store Filter from a request, resolving --handle to the
// allocation's tag (or, for a whole-mailbox lease, to its mailbox).
func (d *daemon) filterFor(req Request) (Filter, error) {
	f := Filter{
		Mailbox: normalizeSlotOrEmpty(req.Mailbox),
		From:    req.From,
		To:      req.To,
		Subject: req.Subject,
		Body:    req.Body,
		Limit:   req.Limit,
	}
	if req.SinceSec > 0 {
		f.SinceTime = time.Now().Add(-time.Duration(req.SinceSec) * time.Second)
	}
	if req.Handle != "" {
		a, ok, err := d.store.GetAllocation(req.Handle)
		if err != nil {
			return f, err
		}
		if !ok {
			return f, fmt.Errorf("unknown handle %q", req.Handle)
		}
		f.Mailbox = a.Mailbox
		if a.Tag != "" {
			f.Tag = "+" + a.Tag
		}
	}
	return f, nil
}

func (d *daemon) opQuery(req Request) Response {
	f, err := d.filterFor(req)
	if err != nil {
		return Response{Error: err.Error()}
	}
	msgs, err := d.store.Query(f)
	if err != nil {
		return Response{Error: err.Error()}
	}
	d.recordRead("query", req.Agent, len(msgs))
	return Response{OK: true, Messages: d.toViews(msgs, req.MaxChars)}
}

func (d *daemon) opFetch(ctx context.Context, req Request) Response {
	f, err := d.filterFor(req)
	if err != nil {
		return Response{Error: err.Error()}
	}
	wait := time.Duration(req.WaitSec) * time.Second
	start := time.Now()
	deadline := start.Add(wait)
	d.metrics.fetchInflight.Inc()
	defer d.metrics.fetchInflight.Dec()
	for {
		msgs, err := d.store.Query(f)
		if err != nil {
			return Response{Error: err.Error()}
		}
		if len(msgs) > 0 || wait <= 0 || time.Now().After(deadline) {
			d.metrics.fetchWait.Observe(time.Since(start).Seconds())
			d.recordRead("fetch", req.Agent, len(msgs))
			return Response{OK: true, Messages: d.toViews(msgs, req.MaxChars)}
		}
		if !sleepCtx(ctx, 2*time.Second) {
			d.metrics.fetchWait.Observe(time.Since(start).Seconds())
			d.recordRead("fetch", req.Agent, len(msgs))
			return Response{OK: true, Messages: d.toViews(msgs, req.MaxChars)}
		}
	}
}

// recordRead attributes a query/fetch to its agent: a hit or miss, and the count
// of messages handed over.
func (d *daemon) recordRead(op, agent string, n int) {
	a := agentLabel(agent)
	result := "miss"
	if n > 0 {
		result = "hit"
	}
	d.metrics.reads.WithLabelValues(op, a, result).Inc()
	if n > 0 {
		d.metrics.returned.WithLabelValues(op, a).Add(float64(n))
	}
}

func (d *daemon) opAlloc(req Request) Response {
	slot := normalizeSlotOrEmpty(req.Mailbox)
	if slot == "" {
		return Response{Error: "alloc requires --mailbox NN"}
	}
	addr := mailboxAddress(slot)
	if addr == "" {
		return Response{Error: "mailbox " + slot + " is not configured"}
	}
	handle := randToken(8)
	ttl := time.Duration(req.TTLSec) * time.Second
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	a := Allocation{Handle: handle, Mailbox: slot, State: "open", CreatedAt: time.Now(), TTL: ttl, NoPlus: req.NoPlus}
	if req.NoPlus {
		ok, err := d.store.AcquireLease(slot, handle, ttl)
		if err != nil {
			return Response{Error: "lease: " + err.Error()}
		}
		if !ok {
			return Response{Error: "mailbox " + slot + " is already leased to another agent"}
		}
		a.Address = addr
	} else {
		a.Tag = randToken(4)
		a.Address = plusTag(addr, a.Tag)
	}
	if err := d.store.CreateAllocation(a); err != nil {
		return Response{Error: "create allocation: " + err.Error()}
	}
	return Response{OK: true, Handle: handle, Address: a.Address, Tag: a.Tag}
}

func (d *daemon) opRelease(req Request) Response {
	if req.Handle == "" {
		return Response{Error: "release requires --handle H"}
	}
	if err := d.store.ReleaseAllocation(req.Handle); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{OK: true}
}

func (d *daemon) opMailboxes() Response {
	stats, err := d.store.Stats()
	if err != nil {
		return Response{Error: err.Error()}
	}
	var views []MailboxView
	for _, mb := range d.mboxes {
		v := MailboxView{Mailbox: mb.Slot, Address: mb.Email, State: "syncing"}
		if st, ok := stats[mb.Slot]; ok {
			v.Count = st.Count
			if !st.LastDate.IsZero() {
				v.LastDate = st.LastDate.Format(time.RFC3339)
			}
		}
		views = append(views, v)
	}
	return Response{OK: true, Mailboxes: views}
}

func (d *daemon) toViews(msgs []StoredMsg, maxChars int) []MsgView {
	if maxChars <= 0 {
		maxChars = d.maxChars
	}
	out := make([]MsgView, 0, len(msgs))
	for _, m := range msgs {
		body, trunc := capStr(m.Body, maxChars)
		out = append(out, MsgView{
			Mailbox: m.Mailbox, Folder: m.Folder, From: m.From, To: m.To,
			Subject: m.Subject, Date: m.Date.Format(time.RFC3339), Body: body, Truncated: trunc,
		})
	}
	return out
}

// --- small helpers ---

func plusTag(addr, tag string) string {
	at := strings.IndexByte(addr, '@')
	if at < 0 {
		return addr
	}
	return addr[:at] + "+" + tag + addr[at:]
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func normalizeSlotOrEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return normalizeSlot(s)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// sleepCtx sleeps for d or until ctx is cancelled. It returns false if the
// context was cancelled (caller should stop).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
