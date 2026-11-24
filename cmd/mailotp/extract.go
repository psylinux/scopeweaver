// extract.go - turn a raw email into the decoded, human-readable text the agent
// reads the code or link from. Shared by the direct fetch path and the daemon's
// poller.
package main

import (
	"html"
	"io"
	"regexp"
	"strings"

	"github.com/emersion/go-message"
)

var (
	// styleScriptRe drops the contents of <style>/<script> blocks so their CSS
	// and JavaScript do not pollute the HTML-derived text or, worse, push a code
	// that lives only in the HTML past the --max-chars cap.
	styleScriptRe = regexp.MustCompile(`(?is)<(style|script)\b[^>]*>.*?</(style|script)>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	hrefRe        = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)
	urlRe         = regexp.MustCompile(`https?://[^\s"'<>)]+`)
)

const defaultMaxChars = 6000

// extractBody returns the decoded body text of the message in r, capped at
// maxChars. It is a convenience wrapper over extractBodyEntity for callers that
// hold a reader (the direct fetch path and the tests).
func extractBody(r io.Reader, maxChars int) (string, bool) {
	if r == nil {
		return "", false
	}
	ent, err := message.Read(r)
	if ent == nil {
		if err != nil {
			b, _ := io.ReadAll(r)
			return capStr(string(b), maxChars)
		}
		return "", false
	}
	return extractBodyEntity(ent, maxChars)
}

// extractBodyEntity keeps both the text/plain and the tag-stripped text/html
// parts, plus the links found in href attributes, so the agent sees the full
// body and a one-time code or confirmation URL is never dropped - in particular
// when it appears only in the HTML alternative of a multipart/alternative
// message. The HTML part is omitted only when it carries nothing the plain text
// already has. The result is capped at maxChars.
func extractBodyEntity(ent *message.Entity, maxChars int) (string, bool) {
	if ent == nil {
		return "", false
	}
	var plain, htmlBuf strings.Builder
	walkEntity(ent, &plain, &htmlBuf, 0)

	var sections []string
	plainTxt := strings.TrimSpace(plain.String())
	if plainTxt != "" {
		sections = append(sections, plainTxt)
	}
	if htmlTxt := strings.TrimSpace(collapse(htmlBuf.String())); htmlTxt != "" && !redundant(htmlTxt, plainTxt) {
		if len(sections) > 0 {
			sections = append(sections, "--- HTML part (tags stripped) ---\n"+htmlTxt)
		} else {
			sections = append(sections, htmlTxt)
		}
	}
	body := strings.Join(sections, "\n\n")
	if links := extractLinks(htmlBuf.String()); len(links) > 0 {
		body += "\n\nLinks in this email:\n" + strings.Join(links, "\n")
	}
	return capStr(strings.TrimSpace(body), maxChars)
}

// redundant reports whether the HTML-derived text carries nothing - no code,
// no link text - that the plain text does not already contain. It compares the
// two with case and whitespace removed, so the HTML part is dropped only when
// it is a genuine substring of the plain part; anything the plain part lacks
// (such as a code shown only in the HTML) keeps the HTML part in the output.
func redundant(htmlTxt, plainTxt string) bool {
	h := squash(htmlTxt)
	return h != "" && strings.Contains(squash(plainTxt), h)
}

// squash lowercases a string and removes all whitespace, for substring compares
// that ignore how plain and HTML renderings space their text.
func squash(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// walkEntity collects the decoded text of a message into plain and html, by
// content type, descending into multipart containers and forwarded
// (message/rfc822) parts.
func walkEntity(e *message.Entity, plain, htmlBuf *strings.Builder, depth int) {
	if e == nil || depth > 12 {
		return
	}
	if mr := e.MultipartReader(); mr != nil {
		for {
			p, err := mr.NextPart()
			if err != nil {
				return
			}
			walkEntity(p, plain, htmlBuf, depth+1)
		}
	}
	ct, _, _ := e.Header.ContentType()
	ct = strings.ToLower(ct)
	switch {
	case strings.HasPrefix(ct, "message/"):
		if nested, _ := message.Read(e.Body); nested != nil {
			walkEntity(nested, plain, htmlBuf, depth+1)
		}
	case ct == "text/html":
		b, _ := io.ReadAll(e.Body)
		htmlBuf.Write(b)
		htmlBuf.WriteString("\n")
	case ct == "" || strings.HasPrefix(ct, "text/"):
		b, _ := io.ReadAll(e.Body)
		plain.Write(b)
		plain.WriteString("\n")
	}
}

// extractLinks pulls URLs out of HTML: href attributes first, then any bare
// http(s) URL, deduplicated and entity-unescaped.
func extractLinks(h string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		u = strings.TrimSpace(html.UnescapeString(u))
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, m := range hrefRe.FindAllStringSubmatch(h, -1) {
		add(m[1])
	}
	for _, u := range urlRe.FindAllString(h, -1) {
		add(u)
	}
	return out
}

// collapse drops <style>/<script> blocks, strips the remaining HTML tags,
// unescapes entities and squeezes whitespace down to the visible text.
func collapse(s string) string {
	s = styleScriptRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func capStr(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	return s[:max] + "\n...[truncated]", true
}
