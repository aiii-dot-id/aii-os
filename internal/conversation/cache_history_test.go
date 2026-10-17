package conversation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
)

// growingConversation is n exchanges of structured prose that
// SummarizeUnits can halve; every row opens with its own word, so a row
// is recognisable abridged or whole.
func growingConversation(n int) []llm.Message {
	rows := make([]llm.Message, 0, 2*n)
	for i := 0; i < n; i++ {
		rows = append(rows,
			llm.Message{Role: "user", Content: paragraphs(fmt.Sprintf("asked%02d", i), 4, 30)},
			llm.Message{Role: "assistant", Content: paragraphs(fmt.Sprintf("answered%02d", i), 4, 30)})
	}
	return rows
}

// fitTurn composes one request — system, the window's history, the
// current message — and fits it from scratch, as every turn does
// (folding never persists). It returns the history as sent and the fit.
func fitTurn(t *testing.T, history []llm.Message, current string, budget int) ([]llm.Message, []llm.Message, fitState) {
	t.Helper()
	msgs := append([]llm.Message{{Role: "system", Content: "stable"}}, history...)
	msgs = append(msgs, llm.Message{Role: "user", Content: current})
	st := fitState{current: len(msgs) - 1}
	if err := fitRequest(&msgs, &st, "stable", nil, budget, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	return append([]llm.Message(nil), msgs[1:st.current]...), msgs, st
}

func rowKey(m llm.Message) string {
	if f := strings.Fields(m.Content); len(f) > 0 {
		return f[0]
	}
	return ""
}

func sameMessages(a, b []llm.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return false
		}
	}
	return true
}

// Consecutive requests share their history until the fit crosses a
// chunk boundary: the start changes about once per half-chunk of growth
// (each yield drops the chunk abridged earlier and abridges the next),
// not once per request. And the shape still survives before the turn
// goes: every row that leaves the request was shown abridged first.
func TestHistoryPrefixHoldsBetweenChunkBoundaries(t *testing.T) {
	const chunk = 10 // the default: half of recent_turns' default 20
	rows := growingConversation(40)
	room := append([]llm.Message{{Role: "system", Content: "stable"}}, rows[:12]...)
	budget := estimate(t, append(room, llm.Message{Role: "user", Content: "current question 00"}))

	var prev []llm.Message
	shownAbridged := map[string]bool{}
	breaks, pressured := 0, 0
	for s := 1; s <= len(rows)/2; s++ {
		hist, _, st := fitTurn(t, rows[:2*s], fmt.Sprintf("current question %02d", s), budget)
		for _, m := range hist {
			if strings.Contains(m.Content, prompt.SummaryMarker) {
				shownAbridged[rowKey(m)] = true
			}
		}
		if prev != nil {
			kept := map[string]bool{}
			for _, m := range hist {
				kept[rowKey(m)] = true
			}
			for _, m := range prev {
				if k := rowKey(m); !kept[k] && !shownAbridged[k] {
					t.Fatalf("step %d: %s left the request without ever being shown abridged", s, k)
				}
			}
		}
		if st.omitted > 0 || st.abridged > 0 {
			pressured++
			if prev != nil && (len(hist) < len(prev) || !sameMessages(hist[:len(prev)], prev)) {
				breaks++
			}
		}
		prev = hist
	}
	t.Logf("%d pressured requests, %d changed the history under the cached prefix", pressured, breaks)
	if pressured < 20 {
		t.Fatalf("the conversation was under pressure for only %d requests; the test needs a long pressured run", pressured)
	}
	// About one change per half-chunk of growth, plus the first steps
	// (the first yield only abridges). The one-message fit changed the
	// history on every pressured request: 34 of 34 here.
	if limit := pressured/(chunk/2) + 3; breaks > limit {
		t.Fatalf("history changed under the cached prefix on %d of %d pressured requests; at most %d expected (one per half-chunk of growth)",
			breaks, pressured, limit)
	}
}

// The block declares how many older turns are SHOWN abridged. A turn
// abridged and then dropped is shown nowhere, so it leaves that count:
// the declaration describes the request as sent.
func TestAbridgedCountDeclaresOnlyWhatIsShown(t *testing.T) {
	rows := growingConversation(8)
	room := append([]llm.Message{{Role: "system", Content: "stable"}}, rows[:4]...)
	budget := estimate(t, append(room, llm.Message{Role: "user", Content: "q"}))
	hist, msgs, st := fitTurn(t, rows, "q", budget)
	shown := 0
	for _, m := range hist {
		if strings.Contains(m.Content, prompt.SummaryMarker) {
			shown++
		}
	}
	if st.omitted == 0 {
		t.Fatal("nothing was dropped; the test needs pressure beyond abridgement")
	}
	if st.abridged != shown {
		t.Fatalf("the fit counts %d abridged turns but the request shows %d", st.abridged, shown)
	}
	block := msgs[st.current].Content
	if want := fmt.Sprintf("%d older turn(s) are shown abridged", shown); shown > 0 && !strings.Contains(block, want) {
		t.Fatalf("the block does not declare %q: %.300q", want, block)
	}
	if shown == 0 && strings.Contains(block, "shown abridged") {
		t.Fatalf("the block declares abridged turns the request does not show: %.300q", block)
	}
}

// A row that merely QUOTES the summary marker was not shortened by this
// fit, so it cannot count as the chunk "already shown abridged": the
// chunk is abridged, not dropped whole. (Now likely, since the identity's
// own replies are abridged too and it can quote the marker back.)
func TestAQuotedMarkerDoesNotDropItsChunkWhole(t *testing.T) {
	rows := growingConversation(10)
	rows[3].Content = "As the earlier note put it, " + prompt.SummaryMarker + " — quoted, not abridged.\n\n" + rows[3].Content
	full := append([]llm.Message{{Role: "system", Content: "stable"}}, rows...)
	budget := estimate(t, append(full, llm.Message{Role: "user", Content: "q"})) - 200
	hist, _, st := fitTurn(t, rows, "q", budget)
	if st.omitted != 0 {
		t.Fatalf("%d rows were dropped whole although abridging made room; a quoted marker was read as an abridgement", st.omitted)
	}
	if st.abridged == 0 {
		t.Fatal("nothing was abridged under pressure")
	}
	if !strings.Contains(hist[3].Content, "quoted, not abridged") {
		t.Fatalf("the quoting row was rewritten: %.120q", hist[3].Content)
	}
}

// When at most a chunk is left, the newest exchange is what the
// operator's words most likely lean on: the oldest exchange is abridged,
// then dropped, and the newest stays whole while anything older remains.
func TestTheLastChunkYieldsTheOldestExchangeFirst(t *testing.T) {
	rows := growingConversation(3) // 6 rows: less than a chunk
	full := append([]llm.Message{{Role: "system", Content: "stable"}}, rows...)
	whole := estimate(t, append(full, llm.Message{Role: "user", Content: "q"}))
	for _, cut := range []int{200, whole / 3} {
		hist, _, st := fitTurn(t, rows, "q", whole-cut)
		n := len(hist)
		if n < 2 || hist[n-2].Content != rows[4].Content || hist[n-1].Content != rows[5].Content {
			t.Fatalf("budget -%d: the newest exchange was abridged or dropped while older history remained (omitted=%d abridged=%d)", cut, st.omitted, st.abridged)
		}
		if st.omitted == 0 && st.abridged == 0 {
			t.Fatalf("budget -%d: nothing yielded", cut)
		}
	}
}
