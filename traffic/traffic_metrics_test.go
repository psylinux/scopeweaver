package traffic

import (
	"testing"

	"github.com/Autumn-27/artex/metrics"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

// respStatus sets the synthetic flow's response status code.
func respStatus(code int) flowOpt {
	return func(f *mproxy.Flow) { f.Response.StatusCode = code }
}

// respHeader sets a response header on the synthetic flow.
func respHeader(k, v string) flowOpt {
	return func(f *mproxy.Flow) { f.Response.Header.Set(k, v) }
}

// TestSinkResponseCountsAndPersistGate drives the addon response path directly to
// show that counting is independent of persistence: with persistence off the proxy
// still classifies and counts WAF blocks but writes nothing to the index, and with
// persistence on it both counts and records.
func TestSinkResponseCountsAndPersistGate(t *testing.T) {
	tr, _ := openTraffic(t)

	reg := metrics.New()
	s := &sink{t: tr, reg: reg}

	// Metrics-only: persistence off. A 403 behind Cloudflare and a 200 pass through.
	tr.SetPersist(false)
	s.Response(newFlow("blocked.example.com", "GET", "/", nil, []byte("Just a moment..."),
		respStatus(403), respHeader("Cf-Ray", "8f2b1234")))
	s.Response(newFlow("ok.example.com", "GET", "/", nil, []byte(`{"ok":true}`),
		respStatus(200)))

	if got := reg.Requests(); got != 2 {
		t.Fatalf("Requests() = %d, want 2", got)
	}
	if got := reg.BlockCount(403, metrics.VendorCloudflare); got != 1 {
		t.Fatalf("blocks{403,cloudflare} = %d, want 1", got)
	}
	if c, err := tr.Count(); err != nil || c != 0 {
		t.Fatalf("metrics-only must not persist: count = %d err = %v, want 0", c, err)
	}

	// Full capture: persistence on. The block is both counted and recorded.
	tr.SetPersist(true)
	s.Response(newFlow("blocked.example.com", "GET", "/login", nil, []byte("Incident ID: 1-2"),
		respStatus(429), respHeader("X-Iinfo", "9-12345")))

	if got := reg.Requests(); got != 3 {
		t.Fatalf("Requests() = %d, want 3", got)
	}
	if got := reg.BlockCount(429, metrics.VendorImperva); got != 1 {
		t.Fatalf("blocks{429,imperva} = %d, want 1", got)
	}
	if c, err := tr.Count(); err != nil || c != 1 {
		t.Fatalf("capture on must persist: count = %d err = %v, want 1", c, err)
	}
}

// TestSinkResponseNilRegistryFallsBackToDefault confirms a sink with no registry
// does not panic and counts into the process-wide Default.
func TestSinkResponseNilRegistryFallsBackToDefault(t *testing.T) {
	tr, _ := openTraffic(t)
	tr.SetPersist(false)
	s := &sink{t: tr} // reg nil
	before := metrics.Default.Requests()
	s.Response(newFlow("x.example.com", "GET", "/", nil, nil, respStatus(200)))
	if after := metrics.Default.Requests(); after != before+1 {
		t.Fatalf("Default.Requests() = %d, want %d", after, before+1)
	}
}
