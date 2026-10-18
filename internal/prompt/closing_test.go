package prompt

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// The closing — the loop's contract, set once by the app — ends every
// composed system text, is priced with the rest, and is protected: under a
// budget that folds and omits everything elastic, it is still there whole.
func TestTheClosingEndsEveryComposeAndSurvivesPressure(t *testing.T) {
	rm := accordionRings(t)
	rm.SetSection(ring.Ring3, "working_truth", strings.Repeat("Working truth accumulates in long sentences about the day. ", 300))
	c := newTestComposer(rm, 100000)
	c.SetName("Closing")
	const closing = "Your newest message may end with a block.\nYou are running on a test platform."
	c.SetClosing(closing)
	for name, compose := range map[string]func() (*Prompt, error){
		"turn":   func() (*Prompt, error) { return c.ComposeTurn("live work state", "### Rhythm — 1 turn", "", 0) },
		"plain":  func() (*Prompt, error) { return c.Compose("live work state", 0) },
		"folded": func() (*Prompt, error) { return c.ComposeFolded("live work state", 0) },
	} {
		p, err := compose()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasSuffix(p.Text, closing) {
			t.Fatalf("%s: the system text does not end with the closing: %.80q", name, p.Text[len(p.Text)-80:])
		}
		if strings.Contains(p.Turn, closing) {
			t.Fatalf("%s: the closing leaked into the block", name)
		}
	}
	c.SetMaxTokens(1100)
	p, err := c.Compose(strings.Repeat("live work state line. ", 100), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p.Text, closing) {
		t.Fatal("under pressure the closing was folded or dropped; it is protected")
	}
	if p.TokenEstimate < estimateTokens(closing) {
		t.Fatal("the closing is not priced")
	}
}

// BYTE FOR BYTE where the loop used to append: the closing follows the
// last byte of the text directly — no joiner, it carries its own separator.
// Unfolded, the text with the closing is the text without it plus the
// closing; folded, the ladder prices the closing too and may fold more, so
// the bytes before it are compared for the joiner alone.
func TestTheClosingIsAppendedWithoutAJoiner(t *testing.T) {
	rm := accordionRings(t)
	rm.SetSection(ring.Ring3, "working_truth", strings.Repeat("Working truth accumulates in long sentences about the day. ", 300))
	const closing = "\n\n## Your turn\nAnswer as asked.\n\nYou are running on a test platform."
	c := newTestComposer(rm, 100000)
	c.SetName("Bytes")
	without, err := c.Compose("live work state", 0)
	if err != nil {
		t.Fatal(err)
	}
	c.SetClosing(closing)
	with, err := c.Compose("live work state", 0)
	if err != nil {
		t.Fatal(err)
	}
	if with.Text != without.Text+closing {
		t.Fatalf("unfolded: the closing changed more than the tail: %q", with.Text[len(with.Text)-120:])
	}
	c.SetMaxTokens(1100)
	folded, err := c.Compose(strings.Repeat("live work state line. ", 100), 0)
	if err != nil {
		t.Fatal(err)
	}
	before, ok := strings.CutSuffix(folded.Text, closing)
	if !ok {
		t.Fatalf("folded: the text does not end with the closing: %q", folded.Text[len(folded.Text)-120:])
	}
	if strings.HasSuffix(before, "\n\n") {
		t.Fatalf("folded: a joiner was inserted before the closing: %q", before[len(before)-60:])
	}
}
