package conversation

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func TestTruncatedWebFetchKeepsItsUntrustedSourceAroundTheModelTail(t *testing.T) {
	const source = "https://example.invalid/document"
	full := untrusted.Wrap(source, strings.Repeat("foreign content ", 400)+"surviving tail")
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("web-1", "web_fetch", `{"url":"`+source+`"}`)),
		textResp("done"),
	}}
	tr := &fakeTranscript{limit: 4000}
	loop := New(client, &fakeTools{results: map[string]string{"web_fetch": full}}, &fakeDefs{}, tr, nil,
		testBounds(Config{MaxToolResultChars: 180}))
	if _, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "read the page"}}); err != nil {
		t.Fatal(err)
	}
	if len(tr.events) != 2 || tr.events[1].result != full {
		t.Fatal("the transcript lost or rewrote the full source result")
	}
	var seen string
	for _, m := range client.requests[1] {
		if strings.Contains(m.Content, "[output truncated") {
			seen = m.Content
		}
	}
	open, tail, close := strings.Index(seen, untrusted.Open), strings.Index(seen, "surviving tail"), strings.LastIndex(seen, untrusted.Close)
	if open < 0 || tail < open || close < tail ||
		strings.Count(seen, untrusted.Open) != 1 || strings.Count(seen, untrusted.Close) != 1 ||
		!strings.Contains(seen[open:close], "source: "+source) {
		t.Fatalf("model got a tail outside its source boundary: %q", seen)
	}
}

func TestTinyToolWindowStillClosesWrappedForeignText(t *testing.T) {
	const source = "https://example.invalid/page"
	full := untrusted.Wrap(source, strings.Repeat("foreign ", 20))
	got := truncateToolResult(full, 3, 0)
	if strings.Count(got, untrusted.Open) != 1 || strings.Count(got, untrusted.Close) != 1 ||
		!strings.Contains(got, "source: "+source) || !strings.Contains(got, "no transcript excerpt is available") ||
		strings.Contains(got, "foreign") {
		t.Fatalf("tiny window broke the boundary or invented retained content: %q", got)
	}
	plain := truncateToolResult("abcdefghijklmnopqrstuvwxyz", 5, 10)
	if strings.Contains(plain, untrusted.Open) || !strings.HasSuffix(plain, "vwxyz") ||
		!strings.Contains(plain, "first 21 characters omitted") {
		t.Fatalf("ordinary tool truncation changed: %q", plain)
	}
}

// The cap counts characters, not bytes: a result of exactly the cap in
// characters arrives whole however many bytes they take, and one more
// character truncates it.
func TestToolResultCapCountsCharacters(t *testing.T) {
	const word = "résumé" // 6 characters, 8 bytes
	if got := truncateToolResult(word, 6, 0); got != word {
		t.Fatalf("a result at the cap in characters was cut by its bytes: %q", got)
	}
	if got := truncateToolResult(word, 5, 0); !strings.Contains(got, "first 1 characters omitted") || !strings.HasSuffix(got, "ésumé") {
		t.Fatalf("one character over the cap: %q", got)
	}
}
