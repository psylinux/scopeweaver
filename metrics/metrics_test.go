package metrics

import (
	"net/http"
	"strings"
	"testing"
)

func TestIsBlockStatus(t *testing.T) {
	block := []int{0, 403, 429, 503, 520, 521, 523, 527}
	for _, s := range block {
		if !IsBlockStatus(s) {
			t.Errorf("IsBlockStatus(%d) = false, want true", s)
		}
	}
	notBlock := []int{200, 204, 301, 400, 401, 404, 418, 500, 502, 519, 528, 599}
	for _, s := range notBlock {
		if IsBlockStatus(s) {
			t.Errorf("IsBlockStatus(%d) = true, want false", s)
		}
	}
}

func header(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestClassifyVendor(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header http.Header
		body   string
		block  bool
		vendor string
	}{
		{"not a block", 200, header("Cf-Ray", "abc"), "", false, ""},
		{"akamai server header", 403, header("Server", "AkamaiGHost"), "", true, VendorAkamai},
		{"akamai reference body", 403, header("Server", "nginx"), "Reference #18.abcd", true, VendorAkamai},
		{"cloudflare cf-ray", 403, header("Cf-Ray", "8f2b"), "", true, VendorCloudflare},
		{"cloudflare mitigated", 403, header("Cf-Mitigated", "challenge"), "", true, VendorCloudflare},
		{"cloudflare just a moment", 503, header("Server", "cloudflare"), "<title>Just a moment...</title>", true, VendorCloudflare},
		{"cloudfront via", 403, header("Via", "1.1 abc.cloudfront.net (CloudFront)"), "", true, VendorCloudFront},
		{"cloudfront amz id", 403, header("X-Amz-Cf-Id", "xyz"), "", true, VendorCloudFront},
		{"imperva x-iinfo", 403, header("X-Iinfo", "9-12345"), "", true, VendorImperva},
		{"imperva incident body", 403, header("Server", "nginx"), "Incident ID: 0123-456", true, VendorImperva},
		{"imperva cookie", 429, header("Set-Cookie", "visid_incap_1234=abcd; path=/"), "", true, VendorImperva},
		{"f5 bigip server", 403, header("Server", "BigIP"), "", true, VendorF5},
		{"f5 x-cnection", 403, header("X-Cnection", "close"), "", true, VendorF5},
		{"f5 cookie", 403, header("Set-Cookie", "BIGipServerpool=123.45; path=/"), "", true, VendorF5},
		{"kpsdk header", 429, header("X-Kpsdk-Ct", "token"), "", true, VendorKPSDK},
		{"kpsdk body", 429, header("Server", "nginx"), "window.KPSDK.configure", true, VendorKPSDK},
		{"unknown vendor", 403, header("Server", "nginx"), "forbidden", true, VendorOther},
		{"edge 520 no signature", 520, header(), "", true, VendorOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block, vendor := Classify(c.status, c.header, []byte(c.body))
			if block != c.block {
				t.Fatalf("Classify block = %v, want %v", block, c.block)
			}
			if vendor != c.vendor {
				t.Fatalf("Classify vendor = %q, want %q", vendor, c.vendor)
			}
		})
	}
}

func TestClassifyHeaderBeatsBody(t *testing.T) {
	// A header marker must win over a conflicting body marker: CloudFront header set
	// but the body carries an Akamai string.
	_, vendor := Classify(403, header("X-Amz-Cf-Id", "id"), []byte("Reference #1"))
	if vendor != VendorCloudFront {
		t.Fatalf("header should win: got %q, want %q", vendor, VendorCloudFront)
	}
}

func TestObserveCountsRequestsAndBlocks(t *testing.T) {
	r := New()
	r.Observe(200, header(), nil)                              // request, not a block
	r.Observe(403, header("Cf-Ray", "1"), nil)                 // block cloudflare 403
	r.Observe(403, header("Cf-Ray", "2"), nil)                 // block cloudflare 403 again
	r.Observe(429, header("Server", "AkamaiGHost"), nil)       // block akamai 429
	r.Observe(503, header("Server", "nginx"), []byte("x"))     // block other 503

	if got := r.Requests(); got != 5 {
		t.Fatalf("Requests() = %d, want 5", got)
	}
	if got := r.BlockCount(403, VendorCloudflare); got != 2 {
		t.Fatalf("blocks{403,cloudflare} = %d, want 2", got)
	}
	if got := r.BlockCount(429, VendorAkamai); got != 1 {
		t.Fatalf("blocks{429,akamai} = %d, want 1", got)
	}
	if got := r.BlockCount(503, VendorOther); got != 1 {
		t.Fatalf("blocks{503,other} = %d, want 1", got)
	}
	// A non-block status is never counted as a block under any vendor.
	if got := r.BlockCount(200, VendorOther); got != 0 {
		t.Fatalf("blocks{200,other} = %d, want 0", got)
	}
}

func TestWritePrometheus(t *testing.T) {
	r := New()
	r.Observe(200, header(), nil)
	r.Observe(403, header("Cf-Ray", "1"), nil)
	r.Observe(429, header("Server", "AkamaiGHost"), nil)

	var sb strings.Builder
	r.WritePrometheus(&sb)
	out := sb.String()

	for _, want := range []string{
		"# TYPE scopeweaver_requests_total counter",
		"scopeweaver_requests_total 3",
		"# TYPE scopeweaver_blocks_total counter",
		`scopeweaver_blocks_total{status="403",vendor="cloudflare"} 1`,
		`scopeweaver_blocks_total{status="429",vendor="akamai"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q in:\n%s", want, out)
		}
	}
	// Series are emitted in stable sorted order (403 before 429).
	if strings.Index(out, `status="403"`) > strings.Index(out, `status="429"`) {
		t.Fatalf("series not sorted by status:\n%s", out)
	}
}

func TestWritePrometheusEmpty(t *testing.T) {
	var sb strings.Builder
	New().WritePrometheus(&sb)
	out := sb.String()
	if !strings.Contains(out, "scopeweaver_requests_total 0") {
		t.Fatalf("empty registry must still expose requests_total 0:\n%s", out)
	}
	// No block series when nothing was blocked, but the TYPE line is still present.
	if !strings.Contains(out, "# TYPE scopeweaver_blocks_total counter") {
		t.Fatalf("blocks TYPE header missing:\n%s", out)
	}
	if strings.Contains(out, "scopeweaver_blocks_total{") {
		t.Fatalf("empty registry should emit no block series:\n%s", out)
	}
}
