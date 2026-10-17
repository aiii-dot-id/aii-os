package cognitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
)

// The production transcript is the store, and the store says what each
// turn did: DREAM finds the reader on the source it is handed, never
// through a setter a boot could forget.
var _ toolCallReader = (*store.Store)(nil)

// callsTalk is a transcript whose turns carry ids, and the tool calls
// recorded under them.
type callsTalk struct {
	oneTurn
	parts []cursor.TurnPart
	calls map[string][]store.ToolCall
	err   error
	asked [][]string
}

func (c *callsTalk) NextConversation(budget int, exclude string) (cursor.ConversationBatch, error) {
	if c.read {
		return readTo(c.at), nil
	}
	last := c.parts[len(c.parts)-1]
	return cursor.ConversationBatch{Through: cursor.TurnCursor{Turn: last.ID, Position: len(last.Text)}, Parts: c.parts}, nil
}

func (c *callsTalk) TurnToolCalls(ids []string) (map[string][]store.ToolCall, error) {
	c.asked = append(c.asked, ids)
	return c.calls, c.err
}

// DREAM SEES WHAT EACH TURN DID. A turn that claims "Recorded permanently"
// and wrote nothing read exactly like one that wrote: DREAM saw only the
// words, and a false claim could become memory. Beside the identity's
// words now stands the list of calls recorded under its turn — one read
// per pass, of the identity's own turns — and the prompt names the law the
// turn contract already states.
func TestADreamPassSeesWhatEachTurnDid(t *testing.T) {
	model := &mockLLM{override: "You notice you said a lesson was recorded."}
	d := NewDream(noRaw{}, model, &captureDoor{}, nil, DreamConfig{})
	talk := &callsTalk{
		parts: []cursor.TurnPart{
			{ID: "q1", Role: "operator", Text: "Remember this lesson.", Whole: true, TurnID: "t1"},
			{ID: "a1", Role: "resident", Text: "Recorded permanently. The lesson now lives in my record.", Whole: true, TurnID: "t1"},
			{ID: "q2", Role: "operator", Text: "And the plan?", Whole: true, TurnID: "t2"},
			{ID: "a2", Role: "resident", Text: "Noted.", Whole: true, TurnID: "t2"},
			{ID: "a0", Role: "resident", Text: "An answer from before turns had ids.", Whole: true},
		},
		calls: map[string][]store.ToolCall{
			"t1": {},
			"t2": {{Tool: "recall", Outcome: "succeeded"}, {Tool: "recall", Outcome: "succeeded"}, {Tool: "note", Outcome: "succeeded"}, {Tool: "commit", Outcome: "refused"}},
		},
	}
	d.SetConversation(talk)
	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if model.calls != 1 {
		t.Fatalf("the pass made %d model calls, want 1", model.calls)
	}
	req := model.lastUser
	if !strings.Contains(req, "[you] Recorded permanently. The lesson now lives in my record.\n  [tool calls this turn: none]") {
		t.Fatalf("the claimed recording does not stand beside its empty list:\n%s", req)
	}
	if !strings.Contains(req, "[you] Noted.\n  [tool calls this turn: recall ×2, note, commit (refused)]") {
		t.Fatalf("the turn that worked does not show its calls as recorded:\n%s", req)
	}
	if n := strings.Count(req, "[tool calls this turn:"); n != 2 {
		t.Fatalf("%d lists in the request, want 2 — a turn without an id shows none, and the operator's words carry none:\n%s", n, req)
	}
	if len(talk.asked) != 1 || strings.Join(talk.asked[0], ",") != "t1,t2" {
		t.Fatalf("the calls were read %v — want one read per pass, of the identity's own turns", talk.asked)
	}
	for _, law := range []string{"A tool call is", "only record that anything", "unknown, not"} {
		if !strings.Contains(model.lastSystem, law) {
			t.Fatalf("DREAM's prompt does not carry the law (%q missing)", law)
		}
	}
}

// A list stands once per turn, after the last of its words in the pass,
// and only when those words reach their end: a turn whose rest comes in a
// later pass shows its list then. A failed read shows no list anywhere.
func TestAToolCallListStandsOncePerTurnAfterItsWords(t *testing.T) {
	batch := cursor.ConversationBatch{Parts: []cursor.TurnPart{
		{ID: "m1", Role: "resident", Text: "Working on it.", Whole: true, TurnID: "t1"},
		{ID: "m2", Role: "resident", Text: "Done: the plan is written.", Whole: true, TurnID: "t1"},
		{ID: "m3", Role: "resident", Text: "The beginning of a long", TurnID: "t3"},
	}}
	calls := map[string][]store.ToolCall{
		"t1": {{Tool: "work", Outcome: "succeeded"}, {Tool: "shell"}, {Tool: "read", Outcome: "unknown"}},
		"t3": {{Tool: "recall", Outcome: "succeeded"}},
	}
	out := renderConversation(batch, calls)
	if n := strings.Count(out, "[tool calls this turn:"); n != 1 {
		t.Fatalf("%d lists, want exactly one — after the turn's last whole words:\n%s", n, out)
	}
	if !strings.Contains(out, "Done: the plan is written.\n  [tool calls this turn: work, shell (unfinished), read (outcome unknown)]") {
		t.Fatalf("the list is not after the turn's last words, or says the calls wrongly:\n%s", out)
	}
	if plain := renderConversation(batch, nil); strings.Contains(plain, "tool calls") {
		t.Fatalf("a pass with no calls read showed a list:\n%s", plain)
	}
	var many []store.ToolCall
	for i := 0; i < maxCallGroups+2; i++ {
		many = append(many, store.ToolCall{Tool: fmt.Sprintf("tool%02d", i), Outcome: "succeeded"})
	}
	if got := toolCallList(many); !strings.HasSuffix(got, ", and 2 more") || strings.Contains(got, "tool12") {
		t.Fatalf("a long list is not bounded: %s", got)
	}
}

// refusingLLM admits a request unless it holds the words the fit must
// leave out, and remembers every request it was asked about.
type refusingLLM struct {
	*mockLLM
	checked map[string]bool
}

func (r *refusingLLM) CheckSimple(ctx context.Context, systemPrompt, userMessage string) error {
	r.checked[userMessage] = true
	if strings.Contains(userMessage, "THIRD") {
		return &wire.ContextLimitError{Required: 2, Limit: 1}
	}
	return nil
}

// THE LISTS RIDE THE FIT AS THEY RIDE THE SEND: the request that is sent
// is one the input check admitted in exactly that form, lists included —
// the fit never measures a request without them and then sends one with.
func TestTheListsRideTheFitAsTheyRideTheSend(t *testing.T) {
	model := &refusingLLM{mockLLM: &mockLLM{override: "You notice a first answer."}, checked: map[string]bool{}}
	d := NewDream(noRaw{}, model, &captureDoor{}, nil, DreamConfig{})
	talk := &callsTalk{
		parts: []cursor.TurnPart{
			{ID: "a1", Role: "resident", Text: "First answer.", Whole: true, TurnID: "t1"},
			{ID: "q2", Role: "operator", Text: "Go on.", Whole: true, TurnID: "t3"},
			{ID: "a3", Role: "resident", Text: "THIRD answer.", Whole: true, TurnID: "t3"},
		},
		calls: map[string][]store.ToolCall{"t1": {{Tool: "note", Outcome: "succeeded"}}, "t3": {{Tool: "recall", Outcome: "succeeded"}}},
	}
	d.SetConversation(talk)
	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	sent := model.lastUser
	if model.calls != 1 || strings.Contains(sent, "THIRD") {
		t.Fatalf("the fit did not leave out what the check refused (%d calls):\n%s", model.calls, sent)
	}
	if !strings.Contains(sent, "First answer.\n  [tool calls this turn: note]") {
		t.Fatalf("the kept turn lost its list:\n%s", sent)
	}
	if !model.checked[sent] {
		t.Fatalf("the request sent was never admitted in that form:\n%s", sent)
	}
}

// A read that fails is said, and the pass goes on with no list anywhere —
// a turn without one is unknown, never "none".
func TestAFailedCallsReadShowsNoList(t *testing.T) {
	model := &mockLLM{override: "You notice a quiet turn."}
	d := NewDream(noRaw{}, model, &captureDoor{}, nil, DreamConfig{})
	talk := &callsTalk{
		parts: []cursor.TurnPart{{ID: "a1", Role: "resident", Text: "Recorded.", Whole: true, TurnID: "t1"}},
		err:   errors.New("store closed"),
	}
	d.SetConversation(talk)
	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if model.calls != 1 || strings.Contains(model.lastUser, "tool calls") {
		t.Fatalf("a failed read showed a list, or stopped the pass (%d calls):\n%s", model.calls, model.lastUser)
	}
}
