// Package metrics exposes a tiny, dependency-free Prometheus counter surface for
// ScopeWeaver's recording proxy: how many target HTTP responses were observed and
// how many of those were WAF blocks, labelled by HTTP status and a small fixed WAF
// vendor set.
//
// It keeps only aggregate counts. Response bodies, URLs, headers and any other
// request detail are read for classification and then discarded, never stored. That
// is what lets a deployment chart its WAF block rate with full traffic persistence
// turned off, so metrics do not fill the traffic index.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The WAF vendor label is a closed set so the metric's cardinality stays bounded.
// Anything unrecognized falls back to VendorOther; a target host or URL is never a
// label value.
const (
	VendorAkamai     = "akamai"
	VendorCloudflare = "cloudflare"
	VendorCloudFront = "cloudfront"
	VendorImperva    = "imperva"
	VendorF5         = "f5"
	VendorKPSDK      = "kpsdk"
	VendorOther      = "other"
)

// maxBodyScan bounds how far into a response body signature matching reads. Only
// this leading slice is scanned, in memory; the body itself is never retained.
const maxBodyScan = 16 << 10

// blockKey is the label tuple for scopeweaver_blocks_total. Both components come
// from fixed small sets (block statuses and the vendor constants above).
type blockKey struct {
	status string
	vendor string
}

// Registry accumulates request and block counts. The zero value is not usable;
// construct one with New. Default is the process-wide registry the recording proxy
// and the /metrics handler share in production.
type Registry struct {
	mu       sync.Mutex
	requests uint64
	blocks   map[blockKey]uint64
}

// New returns an empty registry. Tests use their own instance to stay isolated from
// the process-wide Default.
func New() *Registry { return &Registry{blocks: map[blockKey]uint64{}} }

// Default is the registry the recording proxy writes and the /metrics endpoint
// reads. It exists for the lifetime of the process.
var Default = New()

// IsBlockStatus reports whether an HTTP status code is one this system treats as a
// WAF block: 0 (no response / reset), 403, 429, 503, or the 520-527 range emitted
// by edge networks when the origin or the edge itself refuses the request.
func IsBlockStatus(status int) bool {
	switch status {
	case 0, 403, 429, 503:
		return true
	}
	return status >= 520 && status <= 527
}

// Classify reports whether a response is a WAF block and, when it is, which vendor
// its signatures point to. It is pure and reads only the arguments, so it is the
// unit-testable core of the classifier. header and body are inspected for vendor
// markers and never stored.
func Classify(status int, header http.Header, body []byte) (blocked bool, vendor string) {
	if !IsBlockStatus(status) {
		return false, ""
	}
	return true, classifyVendor(header, body)
}

// classifyVendor matches a fixed, ordered set of response signatures to one vendor
// label. Header markers are checked first (cheap and reliable), then block-page
// cookies, then a bounded body scan for challenge-page text. First match wins.
func classifyVendor(header http.Header, body []byte) string {
	get := header.Get
	server := strings.ToLower(get("Server"))
	switch {
	case strings.Contains(server, "akamaighost"):
		return VendorAkamai
	case get("Cf-Ray") != "" || get("Cf-Mitigated") != "":
		return VendorCloudflare
	case get("X-Amz-Cf-Id") != "" || strings.Contains(strings.ToLower(get("Via")), "cloudfront"):
		return VendorCloudFront
	case get("X-Iinfo") != "":
		return VendorImperva
	case strings.Contains(server, "bigip") || get("X-Cnection") != "":
		return VendorF5
	case get("X-Kpsdk-Ct") != "" || get("X-Kpsdk-A") != "":
		return VendorKPSDK
	}
	// Block-page cookies some WAFs set on the challenge/deny response.
	for _, c := range header.Values("Set-Cookie") {
		switch {
		case strings.HasPrefix(c, "visid_incap_"), strings.HasPrefix(c, "incap_ses_"):
			return VendorImperva
		case strings.HasPrefix(c, "BIGipServer"):
			return VendorF5
		}
	}
	// Body signatures, lower-cased and bounded. The body is scanned here only; no
	// slice of it leaves this function.
	b := body
	if len(b) > maxBodyScan {
		b = b[:maxBodyScan]
	}
	lb := strings.ToLower(string(b))
	switch {
	case strings.Contains(lb, "just a moment"),
		strings.Contains(lb, "attention required"),
		strings.Contains(lb, "cf-ray"):
		return VendorCloudflare
	case strings.Contains(lb, "reference #"),
		strings.Contains(lb, "akamaighost"):
		return VendorAkamai
	case strings.Contains(lb, "incident id"):
		return VendorImperva
	case strings.Contains(lb, "kpsdk"):
		return VendorKPSDK
	}
	return VendorOther
}

// Observe records one completed target response. It always increments the request
// counter and, when the response classifies as a WAF block, increments the block
// counter for that status and vendor. The body is classified before the lock is
// taken so signature scanning never serializes concurrent callers.
func (r *Registry) Observe(status int, header http.Header, body []byte) {
	blocked := IsBlockStatus(status)
	var vendor string
	if blocked {
		vendor = classifyVendor(header, body)
	}
	r.mu.Lock()
	r.requests++
	if blocked {
		r.blocks[blockKey{status: strconv.Itoa(status), vendor: vendor}]++
	}
	r.mu.Unlock()
}

// Requests returns the total number of observed responses. For tests and callers
// that want the raw count without parsing the exposition text.
func (r *Registry) Requests() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests
}

// BlockCount returns the block count for one status and vendor pair.
func (r *Registry) BlockCount(status int, vendor string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.blocks[blockKey{status: strconv.Itoa(status), vendor: vendor}]
}

// WritePrometheus renders the counters in the Prometheus text exposition format.
// scopeweaver_requests_total is always emitted (starting at 0) so the scraper sees
// the metric from the first scrape; scopeweaver_blocks_total emits one series per
// observed status/vendor pair, in a stable sorted order.
func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.Lock()
	requests := r.requests
	keys := make([]blockKey, 0, len(r.blocks))
	snap := make(map[blockKey]uint64, len(r.blocks))
	for k, v := range r.blocks {
		keys = append(keys, k)
		snap[k] = v
	}
	r.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].status != keys[j].status {
			return keys[i].status < keys[j].status
		}
		return keys[i].vendor < keys[j].vendor
	})

	fmt.Fprintln(w, "# HELP scopeweaver_requests_total Target HTTP responses observed by the recording proxy.")
	fmt.Fprintln(w, "# TYPE scopeweaver_requests_total counter")
	fmt.Fprintf(w, "scopeweaver_requests_total %d\n", requests)
	fmt.Fprintln(w, "# HELP scopeweaver_blocks_total Target responses classified as WAF blocks, by HTTP status and vendor.")
	fmt.Fprintln(w, "# TYPE scopeweaver_blocks_total counter")
	for _, k := range keys {
		fmt.Fprintf(w, "scopeweaver_blocks_total{status=%q,vendor=%q} %d\n", k.status, k.vendor, snap[k])
	}
}
