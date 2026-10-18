package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// recordReply seeds a reply and its reasoning the way the app's one seam
// (App.recordReply) records them: the reply, then its reasoning under the
// reply's turn.
func recordReply(t *testing.T, st *store.Store, text string, calls []store.ReasoningCall) uint64 {
	t.Helper()
	seq, err := st.AddConversationTurnSeq("resident", text)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddReasoning(seq, calls); err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestReasoningIsRecalledOnlyWhenNamed(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	seq := recordReply(t, st, "I will look at the heron photographs.",
		[]store.ReasoningCall{{Model: "glm", Text: "The heron was mentioned first; the photographs are in the archive under wading birds."}})
	recall := func(args map[string]any) string {
		t.Helper()
		out, err := engine.ExecuteAction(ctx, "verb", "recall", args)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := recall(map[string]any{"query": "heron"}); strings.Contains(out, "wading birds") {
		t.Fatalf("a recall across the record surfaced reasoning:\n%s", out)
	}
	if out := recall(map[string]any{}); strings.Contains(out, "wading birds") || strings.Contains(out, "Reasoning") {
		t.Fatalf("the unnamed listing included reasoning:\n%s", out)
	}
	named := recall(map[string]any{"source": "reasoning", "query": "heron"})
	if !strings.Contains(named, "heron") || !strings.Contains(named, fmt.Sprintf("turn %d", seq)) {
		t.Fatalf("naming source=reasoning did not find it by its words:\n%s", named)
	}
	whole := recall(map[string]any{"source": "reasoning", "query": fmt.Sprintf("turn:%d", seq)})
	if !strings.Contains(whole, "The heron was mentioned first; the photographs are in the archive under wading birds.") {
		t.Fatalf("turn:N did not read the reply's reasoning whole:\n%s", whole)
	}
	for _, out := range []string{named, whole} {
		if !strings.Contains(out, "not something you said") {
			t.Fatalf("reasoning is not labelled as thinking:\n%s", out)
		}
	}
	if out := recall(map[string]any{"source": "reasoning", "query": "turn:99999"}); !strings.Contains(out, "no reasoning") {
		t.Fatalf("a turn without reasoning must say so:\n%s", out)
	}
	if out := recall(map[string]any{"source": "reasoning", "query": fmt.Sprintf("Turn #%d", seq)}); !strings.Contains(out, "wading birds") {
		t.Fatalf("\"Turn #N\" is a turn reference too:\n%s", out)
	}
	miss := recall(map[string]any{"source": "reasoning", "query": "kingfisher"})
	if !strings.Contains(miss, "not something you said") || strings.Contains(miss, "omit source") || !strings.Contains(miss, "turn:N") {
		t.Fatalf("a miss must stay labelled and must not send the identity to a recall that never searches reasoning:\n%s", miss)
	}
}

// A long reply's reasoning reads in labelled parts, each under any tool
// result cap, each naming the next: a reader never loses the label to a
// truncated head, and never loses the rest.
func TestLongReasoningReadsInLabelledParts(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	long := strings.Repeat("a careful line of thought about the archive. ", 700) // ~31,500 runes
	seq := recordReply(t, st, "done", []store.ReasoningCall{{Model: "glm", Text: long}})
	read := func(q string) string {
		out, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "reasoning", "query": q})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	var whole strings.Builder
	for p := 1; ; p++ {
		q := fmt.Sprintf("turn:%d", seq)
		if p > 1 {
			q = fmt.Sprintf("turn:%d part:%d", seq, p)
		}
		out := read(q)
		if !strings.HasPrefix(out, "Reasoning — your own deliberation") || !strings.Contains(out, fmt.Sprintf("part %d of 3", p)) {
			t.Fatalf("part %d is not labelled as part of three: %.200q", p, out)
		}
		if len([]rune(out)) > 13000 {
			t.Fatalf("part %d is %d runes: over the part size", p, len([]rune(out)))
		}
		body := out[strings.Index(out, ":\n\n")+3:]
		end := strings.Index(body, "\n\n[end of turn ")
		if end < 0 || !strings.HasSuffix(out, "]") || !strings.Contains(out[strings.LastIndex(out, "[end of turn "):], "not something you said") {
			t.Fatalf("part %d does not end with its label: %.200q", p, out[len(out)-min(200, len(out)):])
		}
		if p < 3 && !strings.Contains(body[end:], fmt.Sprintf("continues: query=\"turn:%d part:%d\"", seq, p+1)) {
			t.Fatalf("part %d does not name the next", p)
		}
		body = body[:end]
		whole.WriteString(body)
		if p == 3 {
			break
		}
	}
	if whole.String() != long {
		t.Fatalf("the parts do not rebuild the reasoning exactly: %d of %d runes", len([]rune(whole.String())), len([]rune(long)))
	}
}

func TestTheRecallDescriptionMakesReasoningObvious(t *testing.T) {
	v := lookupVerb("recall")
	if v == nil {
		t.Fatal("no recall verb")
	}
	if !strings.Contains(v.Description, "source=reasoning") || !strings.Contains(v.Description, "turn:") {
		t.Fatalf("the recall description does not say how to reach reasoning: %s", v.Description)
	}
	props, _ := v.Params["properties"].(map[string]any)
	src, _ := props["source"].(map[string]any)
	enum, _ := src["enum"].([]string)
	found := false
	for _, e := range enum {
		if e == "reasoning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("source=reasoning is not an allowed value: %v", src["enum"])
	}
}

// A word hit in a long reply points to the part that holds it, with the
// excerpt around the match; an exact phrase that merely reads like a turn
// reference is searched as a phrase; a phrase miss says so.
func TestAHitPointsToItsPartAndPhrasesStayPhrases(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	long := strings.Repeat("an ordinary line of thought. ", 600) + "the kingfisher waited at the weir " + strings.Repeat("more thought. ", 50)
	seq := recordReply(t, st, "done", []store.ReasoningCall{{Model: "glm", Text: long}})
	recordReply(t, st, "noted", []store.ReasoningCall{{Model: "glm", Text: "I will call it turn 3 of the plan."}})
	recall := func(args map[string]any) string {
		t.Helper()
		out, err := engine.ExecuteAction(context.Background(), "verb", "recall", args)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	hit := recall(map[string]any{"source": "reasoning", "query": "kingfisher"})
	if !strings.Contains(hit, "the kingfisher waited") || !strings.Contains(hit, fmt.Sprintf("query=\"turn:%d part:2\"", seq)) {
		t.Fatalf("the hit must show the match and name its part:\n%s", hit)
	}
	phrase := recall(map[string]any{"source": "reasoning", "query": "turn 3", "exact": true})
	if !strings.Contains(phrase, "call it turn 3 of the plan") {
		t.Fatalf("an exact phrase that reads like a turn reference must be searched as a phrase:\n%s", phrase)
	}
	miss := recall(map[string]any{"source": "reasoning", "query": "heron at dusk", "exact": true})
	if !strings.Contains(miss, "exact phrase") || strings.Contains(miss, "every word must appear") {
		t.Fatalf("a phrase miss must speak of the phrase:\n%s", miss)
	}
}
