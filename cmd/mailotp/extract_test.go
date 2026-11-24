package main

import (
	"strings"
	"testing"
)

func TestExtractBodyForwardedMIME(t *testing.T) {
	raw := strings.ReplaceAll(`From: fwd@example.com
To: test01@example.com
Subject: Fwd: Verify
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="OUTER"

--OUTER
Content-Type: text/plain; charset=utf-8

---------- Forwarded message ----------
--OUTER
Content-Type: message/rfc822

From: no-reply@service.com
Subject: Verify your email
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="INNER"

--INNER
Content-Type: text/plain; charset=utf-8

Your verification code is 778899. Confirm at https://service.com/verify?token=zzz
--INNER
Content-Type: text/html; charset=utf-8

<p>Your code is <b>778899</b></p>
--INNER--
--OUTER--
`, "\n", "\r\n")

	body, _ := extractBody(strings.NewReader(raw), defaultMaxChars)
	if !strings.Contains(body, "778899") {
		t.Errorf("body missing code 778899: %q", body)
	}
	if !strings.Contains(body, "service.com/verify?token=zzz") {
		t.Errorf("body missing confirm link: %q", body)
	}
}

func TestExtractBodyHTMLOnly(t *testing.T) {
	raw := strings.ReplaceAll(`From: a@b.example
Subject: Verify
MIME-Version: 1.0
Content-Type: text/html; charset=utf-8

<html><body><p>Your code is <b>4242</b>.</p>
<a href="https://x.test/verify?k=1&amp;u=2">Click here to confirm</a></body></html>
`, "\n", "\r\n")

	body, _ := extractBody(strings.NewReader(raw), defaultMaxChars)
	if !strings.Contains(body, "4242") {
		t.Errorf("body missing code 4242: %q", body)
	}
	if !strings.Contains(body, "Click here to confirm") {
		t.Errorf("body missing visible text: %q", body)
	}
	// The confirm URL lives only in the href; it must be preserved and unescaped.
	if !strings.Contains(body, "https://x.test/verify?k=1&u=2") {
		t.Errorf("body missing href link: %q", body)
	}
}

func TestCapStr(t *testing.T) {
	if s, trunc := capStr("hello", 100); s != "hello" || trunc {
		t.Errorf("short capStr=%q,%v", s, trunc)
	}
	s, trunc := capStr("abcdefghij", 4)
	if !trunc || !strings.HasPrefix(s, "abcd") {
		t.Errorf("long capStr=%q,%v", s, trunc)
	}
}

func TestNormalizeSlot(t *testing.T) {
	for in, want := range map[string]string{"1": "01", "01": "01", "2": "02", "02": "02", " 3 ": "03", "": "", "x": "", "0": ""} {
		if got := normalizeSlot(in); got != want {
			t.Errorf("normalizeSlot(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSplitEndpoint(t *testing.T) {
	cases := []struct{ in, host, port string }{
		{"", "imap.gmail.com", "993"},
		{"imaps://imap.gmail.com:993", "imap.gmail.com", "993"},
		{"imap.gmail.com:993", "imap.gmail.com", "993"},
		{"mail.example.com", "mail.example.com", "993"},
		{"imaps://mail.example.com:1993", "mail.example.com", "1993"},
	}
	for _, tc := range cases {
		h, p := splitEndpoint(tc.in)
		if h != tc.host || p != tc.port {
			t.Errorf("splitEndpoint(%q)=%q:%q want %q:%q", tc.in, h, p, tc.host, tc.port)
		}
	}
}
