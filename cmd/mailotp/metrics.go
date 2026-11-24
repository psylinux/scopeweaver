// metrics.go - Prometheus instrumentation for the daemon. This HTTP endpoint is
// the operator's monitoring plane only (Prometheus scrapes it); it is separate
// from and additional to the agent-facing unix socket, which stays the only
// interface agents use. It gives Grafana visibility into mail arriving, mail
// read and by which agent, the pending-verification queue, and poller health.
package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metrics struct {
	reg *prometheus.Registry

	// Mail flow.
	ingested *prometheus.CounterVec // mailbox, folder: messages synced in from Gmail
	stored   *prometheus.GaugeVec   // mailbox: messages currently in the corpus
	lastSync *prometheus.GaugeVec   // mailbox, folder: unix time of last successful sync

	// Reads (who consumed what).
	reads    *prometheus.CounterVec // op, agent, result: query/fetch calls by outcome
	returned *prometheus.CounterVec // op, agent: messages handed to an agent

	// The pending-verification queue.
	fetchInflight prometheus.Gauge     // agents currently blocked in fetch waiting for a code
	fetchWait     prometheus.Histogram // seconds a fetch waited before a match appeared
	allocOpen     *prometheus.GaugeVec // mailbox: open allocations (sign-ups awaiting a code)
	leasesHeld    *prometheus.GaugeVec // mailbox: whole-mailbox leases held

	// Poller health.
	pollerUp   *prometheus.GaugeVec   // mailbox: 1 connected, 0 down
	pollErrors *prometheus.CounterVec // mailbox
	reconnects *prometheus.CounterVec // mailbox
}

func newMetrics() *metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	f := promauto.With(reg)
	return &metrics{
		reg: reg,
		ingested: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mailotp_messages_ingested_total",
			Help: "Emails synced into the corpus from Gmail.",
		}, []string{"mailbox", "folder"}),
		stored: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mailotp_messages_stored",
			Help: "Messages currently stored in the corpus.",
		}, []string{"mailbox"}),
		lastSync: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mailotp_last_sync_timestamp_seconds",
			Help: "Unix time of the last successful sync of a folder.",
		}, []string{"mailbox", "folder"}),
		reads: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mailotp_reads_total",
			Help: "query/fetch calls, labelled by op, agent and result (hit or miss).",
		}, []string{"op", "agent", "result"}),
		returned: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mailotp_messages_returned_total",
			Help: "Messages handed to an agent by a query or fetch.",
		}, []string{"op", "agent"}),
		fetchInflight: f.NewGauge(prometheus.GaugeOpts{
			Name: "mailotp_fetch_inflight",
			Help: "Agents currently blocked in fetch waiting for a message.",
		}),
		fetchWait: f.NewHistogram(prometheus.HistogramOpts{
			Name:    "mailotp_fetch_wait_seconds",
			Help:    "Seconds a fetch waited before a matching message appeared.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 45, 60, 90, 120},
		}),
		allocOpen: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mailotp_allocations_open",
			Help: "Open allocations: sign-ups awaiting their code.",
		}, []string{"mailbox"}),
		leasesHeld: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mailotp_leases_held",
			Help: "Whole-mailbox leases currently held.",
		}, []string{"mailbox"}),
		pollerUp: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mailotp_poller_up",
			Help: "1 when the mailbox poller has a live IMAP connection, 0 otherwise.",
		}, []string{"mailbox"}),
		pollErrors: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mailotp_poll_errors_total",
			Help: "Poll or connection errors per mailbox.",
		}, []string{"mailbox"}),
		reconnects: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mailotp_imap_reconnects_total",
			Help: "IMAP reconnect attempts per mailbox.",
		}, []string{"mailbox"}),
	}
}

func (m *metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// agentLabel keeps the agent label bounded: an empty agent becomes "unknown".
func agentLabel(a string) string {
	if a == "" {
		return "unknown"
	}
	return a
}
