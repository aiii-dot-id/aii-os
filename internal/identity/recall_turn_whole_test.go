package identity

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestAConversationTurnIsReadWholeInParts(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	var b strings.Builder
	b.WriteString("The kittiwake colony is on the north cliff. ")
	for i := 1; b.Len() < 30000; i++ {
		fmt.Fprintf(&b, "Line %05d of what was said about the cliff. ", i)
	}
	long := b.String()
	seq, err := st.AddConversationTurnSeq("resident", long)
	if err != nil {
		t.Fatal(err)
	}
	short, err := st.AddConversationTurnSeq("operator", "A guillemot nests there too.")
	if err != nil {
		t.Fatal(err)
	}
	recall := func(args map[string]any) string {
		t.Helper()
		out, err := engine.ExecuteAction(context.Background(), "verb", "recall", args)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	route := fmt.Sprintf(`[shortened: recall source=conversation query="turn:%d" reads it whole]`, seq)
	if out := recall(map[string]any{"source": "conversation", "query": "kittiwake"}); !strings.Contains(out, route) {
		t.Fatalf("a shortened listing hit does not name the whole read:\n%s", out)
	}
	if out := recall(map[string]any{"query": "kittiwake"}); !strings.Contains(out, route) {
		t.Fatalf("a shortened ranked hit does not name the whole read:\n%s", out)
	}
	if out := recall(map[string]any{"source": "conversation", "query": "guillemot"}); strings.Contains(out, "shortened") {
		t.Fatalf("a hit that shows its whole turn names a read it does not need:\n%s", out)
	}

	var walked strings.Builder
	query := fmt.Sprintf("turn:%d", seq)
	for part := 1; ; part++ {
		out := recall(map[string]any{"source": "conversation", "query": query})
		head := fmt.Sprintf("Conversation — turn %d, resident, ", seq)
		if !strings.HasPrefix(out, head) || !strings.Contains(out, fmt.Sprintf(", part %d of 3:\n\n", part)) {
			t.Fatalf("part %d is not labelled:\n%.300s", part, out)
		}
		body := out[strings.Index(out, ":\n\n")+3 : strings.LastIndex(out, "\n\n[end of turn")]
		walked.WriteString(body)
		tail := out[strings.LastIndex(out, "\n\n[end of turn"):]
		if part == 3 {
			if tail != fmt.Sprintf("\n\n[end of turn %d, part 3 of 3]", seq) {
				t.Fatalf("the last part's label: %q", tail)
			}
			break
		}
		next := fmt.Sprintf("turn:%d part:%d", seq, part+1)
		if tail != fmt.Sprintf("\n\n[end of turn %d, part %d of 3; continues: query=%q]", seq, part, next) {
			t.Fatalf("part %d does not name the next: %q", part, tail)
		}
		query = next
	}
	if walked.String() != long {
		t.Fatalf("the parts walked are not the turn: %d runes walked of %d", len([]rune(walked.String())), len([]rune(long)))
	}

	whole := recall(map[string]any{"source": "conversation", "query": fmt.Sprintf("Turn #%d", short)})
	if !strings.HasPrefix(whole, fmt.Sprintf("Conversation — turn %d, operator, ", short)) ||
		!strings.HasSuffix(whole, fmt.Sprintf(":\n\nA guillemot nests there too.\n\n[end of turn %d, part 1 of 1]", short)) {
		t.Fatalf("a short turn reads whole in one part:\n%s", whole)
	}
	if out := recall(map[string]any{"source": "conversation", "query": "turn:99999"}); !strings.Contains(out, "Turn 99999 holds no words recall reads") {
		t.Fatalf("a turn recall cannot read says so:\n%s", out)
	}
	if out := recall(map[string]any{"source": "conversation", "query": query + "0"}); !strings.Contains(out, "is not one of them") {
		t.Fatalf("a part the turn does not have says so:\n%s", out)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "conversation", "query": fmt.Sprintf("turn:%d", seq), "after_seq": 5}); err == nil {
		t.Fatal("a whole read with a page cursor must be refused, as for reasoning")
	}
}

// A WALK THROUGH THE PARTS LOSES NOTHING UNDER A SMALL BOUND. Parts were
// 12,000 runes whatever the operator's bound on a tool result
// (prompt.max_tool_result_chars); over its bound the loop keeps a result's
// tail, so under a bound of 3,000 each part lost its first 9,000 runes and
// the walk lost three quarters of the text. A part leaves room for its
// labels under the bound, for a turn's words and for its reasoning alike.
func TestAWalkThroughThePartsFitsASmallBound(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	engine.toolResultChars = 3000
	var b strings.Builder
	for i := 1; b.Len() < 20000; i++ {
		fmt.Fprintf(&b, "Line %05d of a long reply. ", i)
	}
	long := b.String()
	seq := recordReply(t, st, long, []store.ReasoningCall{{Model: "glm", Text: long}})
	for _, source := range []string{"conversation", "reasoning"} {
		var walked strings.Builder
		query := fmt.Sprintf("turn:%d", seq)
		for n := 1; n < 100; n++ {
			out, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": source, "query": query})
			if err != nil {
				t.Fatal(err)
			}
			if size := len([]rune(out)); size > 3000 {
				t.Fatalf("%s part %d is %d runes, over the bound of 3000: the loop would cut its head", source, n, size)
			}
			body := out[strings.Index(out, "\n\n")+2 : strings.LastIndex(out, "\n\n[end of turn")]
			walked.WriteString(body)
			cont := regexp.MustCompile(`continues: query="([^"]+)"`).FindStringSubmatch(out)
			if cont == nil {
				break
			}
			query = cont[1]
		}
		if walked.String() != long {
			t.Fatalf("%s: the parts walked are not the text: %d runes of %d", source, len([]rune(walked.String())), len([]rune(long)))
		}
	}
}
