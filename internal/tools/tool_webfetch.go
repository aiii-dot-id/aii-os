package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type WebFetchTool struct {
	maxBytes func() int
	timeout  time.Duration

	local  []firewall.LocalScope
	refuse func(net.IP, int) bool

	onFetch func(url string)
}

const ReasonHTTPStatus = "HTTP_STATUS"

const ReasonOffsetPastEnd = "OFFSET_PAST_END"

func (t *WebFetchTool) Name() string { return "web_fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch a page and return its text from the top, with markup, scripts and styles removed. A long page ends with a line saying where to continue: pass that number as offset to read on."
}

func (t *WebFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":    map[string]any{"type": "string", "description": "URL to fetch"},
			"offset": map[string]any{"type": "integer", "description": "Byte of the page's text to start at, from a long page's last line; the page is fetched again"},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) ceiling() int {
	if t.maxBytes != nil {
		if n := t.maxBytes(); n > 0 {
			return n
		}
	}
	return limits.DefaultWebFetchMaxBytes
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]any) (Result, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return Refusal(ReasonArgumentsRequired, "url is required"), nil
	}
	offset, err := intArg(args, "offset", 0)
	if err == nil && offset < 0 {
		err = fmt.Errorf("must not be negative, got %d", offset)
	}
	if err != nil {
		return Refusal(ReasonArgumentsRequired, "offset "+err.Error()), nil
	}
	lg := firewall.GuardAdmitting(nil, t.local, t.refuse)
	if err := lg.Guard(ctx, strings.TrimSpace(url)); err != nil {
		return Result{Error: fmt.Sprintf("web_fetch blocked: %v", err)}, nil
	}

	client := firewall.GuardedClient(t.timeout, lg.Guard, nil)
	ctx = firewall.WithPins(ctx, lg)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	req.Header.Set("User-Agent", "AII-OS/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	ceiling := t.ceiling()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(ceiling)+1))
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	truncated := len(body) > ceiling
	if truncated {
		body = body[:ceiling]
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		source := strings.TrimSpace(fmt.Sprintf("%s — HTTP %d %s", url, resp.StatusCode, http.StatusText(resp.StatusCode)))
		return Result{Error: untrusted.Wrap(source, string(body)), Truncated: truncated, ReasonCode: ReasonHTTPStatus}, nil
	}

	if t.onFetch != nil {
		t.onFetch(url)
	}

	text, scripted := string(body), false
	if isHTML(resp.Header.Get("Content-Type"), body) {
		text = pageText(body, resp.Request.URL)
		scripted = text == "" && len(body) > 0
	}

	notUTF8 := !utf8.ValidString(text)
	if notUTF8 {
		text = strings.ToValidUTF8(text, "\uFFFD")
	}
	if offset > 0 && offset >= len(text) {
		return Result{Error: fmt.Sprintf("offset %d is past the end of this page's text, which is %d bytes as fetched now", offset, len(text)), ReasonCode: ReasonOffsetPastEnd}, nil
	}
	for offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset++
	}

	var tail string
	if scripted {
		tail += "\n[This page's HTML holds no text: what it shows may be written by its scripts, which web_fetch does not run.]"
	}
	if truncated {
		tail += "\n" + fmt.Sprintf("[This page is longer than the %d bytes web_fetch reads, so its text past them cannot be read; the operator's setting tools.webfetch_max_bytes sets that size.]", ceiling)
	}
	if notUTF8 {
		tail += "\n[Bytes of this page that are not UTF-8 text are shown as \uFFFD.]"
	}
	rest := text[offset:]
	room := limits.ToolResultChars(ctx) - utf8.RuneCountInString(untrusted.Wrap(url, "")+tail)
	if truncated {
		room -= utf8.RuneCountInString("\n[truncated]")
	}
	cut := utf8.RuneCountInString(rest) > room
	if cut {

		room -= 1 + utf8.RuneCountInString(continueAt(len(text), len(text)))
		rest = runePrefix(rest, max(room, 1))
	}
	page := untrusted.Wrap(url, rest) + tail
	if cut {
		page += "\n" + continueAt(offset+len(rest), len(text))
	}
	return Result{Output: page, Truncated: truncated}, nil
}

func continueAt(end, total int) string {
	return fmt.Sprintf("[shown to byte %d of %d. Continue with offset=%d.]", end, total, end)
}

func runePrefix(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

func isHTML(contentType string, body []byte) bool {
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mt == "text/html" || mt == "application/xhtml+xml")
}

var (
	notProse = map[string]bool{"script": true, "style": true, "noscript": true, "iframe": true, "noembed": true, "noframes": true}

	blocks = map[string]bool{
		"address": true, "article": true, "aside": true, "blockquote": true, "body": true, "br": true, "caption": true,
		"dd": true, "details": true, "dialog": true, "div": true, "dl": true, "dt": true, "fieldset": true, "figcaption": true,
		"figure": true, "footer": true, "form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"header": true, "hgroup": true, "hr": true, "html": true, "legend": true, "li": true, "main": true, "nav": true,
		"ol": true, "option": true, "p": true, "pre": true, "section": true, "summary": true, "table": true, "tbody": true,
		"tfoot": true, "thead": true, "title": true, "tr": true, "ul": true,
	}
)

func pageText(body []byte, base *url.URL) string {
	z := html.NewTokenizer(bytes.NewReader(body))
	w := pageWriter{linkAt: -1}
	skip, template, pre := false, 0, 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			w.closeLink()
			return strings.TrimSpace(string(w.buf))
		case html.TextToken:
			if !skip && template == 0 {
				w.text(string(z.Text()), pre > 0)
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			name, hasAttr := z.TagName()
			tag, end := string(name), tt == html.EndTagToken
			switch {
			case tag == "template" && !end:
				template++
			case tag == "template":
				template = max(template-1, 0)
			case template > 0:
			case tag == "a":
				w.closeLink()
				if !end && hasAttr {
					if href := linkHref(z, base); href != "" {
						w.openLink(href)
					}
				}
			case blocks[tag]:
				w.line = true
				if tag == "pre" && end {
					pre = max(pre-1, 0)
				} else if tag == "pre" {
					pre++
				}
			case (tag == "td" || tag == "th") && !end:
				w.cell = true
			}

			skip = notProse[tag] && !end
			continue
		}
		skip = false
	}
}

func linkHref(z *html.Tokenizer, base *url.URL) string {
	for {
		key, val, more := z.TagAttr()
		if string(key) == "href" {
			ref := strings.TrimSpace(string(val))
			u, err := url.Parse(ref)
			if ref == "" || strings.HasPrefix(ref, "#") || err != nil {
				return ""
			}
			if base != nil {
				u = base.ResolveReference(u)
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				return ""
			}
			return u.String()
		}
		if !more {
			return ""
		}
	}
}

type pageWriter struct {
	buf               []byte
	space, cell, line bool

	linkAt int
	link   string
}

func (w *pageWriter) flush() {
	if n := len(w.buf); n > 0 && n-1 != w.linkAt {
		switch last := w.buf[n-1]; {
		case w.line:
			if last != '\n' {
				w.buf = append(w.buf, '\n')
			}
		case w.cell:
			w.buf = append(w.buf, '\t')
		case w.space && last != '\n' && last != '\t':
			w.buf = append(w.buf, ' ')
		}
	}
	w.space, w.cell, w.line = false, false, false
}

func (w *pageWriter) text(s string, pre bool) {
	if pre {
		if s = strings.ReplaceAll(s, "\r\n", "\n"); s != "" {
			w.flush()
			w.buf = append(w.buf, s...)
		}
		return
	}
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	for i, word := range strings.Fields(s) {
		w.space = w.space || i > 0 || unicode.IsSpace(first)
		w.flush()
		w.buf = append(w.buf, word...)
	}
	w.space = w.space || (s != "" && unicode.IsSpace(last))
}

func (w *pageWriter) openLink(href string) {
	w.flush()
	w.linkAt, w.link = len(w.buf), href
	w.buf = append(w.buf, '[')
}

func (w *pageWriter) closeLink() {
	if w.linkAt < 0 {
		return
	}
	if len(w.buf) == w.linkAt+1 {
		w.buf = w.buf[:w.linkAt]
	} else {
		w.buf = append(w.buf, "]("+w.link+")"...)
	}
	w.linkAt = -1
}
