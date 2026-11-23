package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServeMetricsExposition checks the /metrics handler renders the Prometheus
// text exposition format with the expected metric names and a text content type.
// It needs no database: serveMetrics reads only the process-wide metrics registry.
func TestServeMetricsExposition(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)

	(&Server{}).serveMetrics(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain...", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE scopeweaver_requests_total counter",
		"scopeweaver_requests_total ",
		"# TYPE scopeweaver_blocks_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q in:\n%s", want, body)
		}
	}
}
