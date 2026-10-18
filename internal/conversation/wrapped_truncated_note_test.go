package conversation

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func TestUpstreamTruncatedForeignResultKeepsBoundaryAndHostNotice(t *testing.T) {
	const source = "plugin org.example.docs"
	full := untrusted.Wrap(source, strings.Repeat("foreign content ", 200)+"surviving tail") + "\n[truncated]"
	got := truncateToolResult(full, 180, 0)
	open, tail, close := strings.Index(got, untrusted.Open), strings.Index(got, "surviving tail"), strings.LastIndex(got, untrusted.Close)
	if open < 0 || tail < open || close < tail ||
		strings.Count(got, untrusted.Open) != 1 || strings.Count(got, untrusted.Close) != 1 ||
		!strings.Contains(got[open:close], "source: "+source) ||
		!strings.HasSuffix(got, untrusted.Close+"\n[truncated]") {
		t.Fatalf("upstream truncation left foreign content outside its boundary: %q", got)
	}
}

func TestLongPluginErrorKeepsUntrustedBoundaryAfterLoopTruncation(t *testing.T) {
	const source = "plugin org.example.docs"
	full := "Error: " + untrusted.Wrap(source, strings.Repeat("foreign failure ", 200)+"failure tail")
	got := truncateToolResult(full, 180, 0)
	open, tail, close := strings.Index(got, untrusted.Open), strings.Index(got, "failure tail"), strings.LastIndex(got, untrusted.Close)
	if !strings.HasPrefix(got, "Error: [output truncated") || open < 0 || tail < open || close < tail ||
		strings.Count(got, untrusted.Open) != 1 || strings.Count(got, untrusted.Close) != 1 ||
		!strings.Contains(got[open:close], "source: "+source) {
		t.Fatalf("model saw an unlabeled plugin error tail: %q", got)
	}
}

func TestCutStatusExcerptKeepsItsStatusAndBoundary(t *testing.T) {
	const source = "https://example.invalid/gone — HTTP 404 Not Found"
	full := "Error: " + untrusted.Wrap(source, strings.Repeat("not found ", 200)+"page tail") + "\n[truncated]"
	got := truncateToolResult(full, 180, 0)
	open, tail, close := strings.Index(got, untrusted.Open), strings.Index(got, "page tail"), strings.LastIndex(got, untrusted.Close)
	if !strings.HasPrefix(got, "Error: [output truncated") || open < 0 || tail < open || close < tail ||
		strings.Count(got, untrusted.Open) != 1 || strings.Count(got, untrusted.Close) != 1 ||
		!strings.Contains(got[open:close], "source: "+source) ||
		!strings.HasSuffix(got, untrusted.Close+"\n[truncated]") {
		t.Fatalf("a cut status excerpt lost its status or its boundary: %q", got)
	}
}
