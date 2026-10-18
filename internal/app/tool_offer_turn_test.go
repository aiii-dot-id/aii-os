package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// offerTurnScript answers each request that may call tools with the next
// response in its queue — a nil response is the turn's answer, "done",
// as is any request once the queue is empty. It keeps
// the tool names every request carried and the observations handed back.
type offerTurnScript struct {
	mu        sync.Mutex
	responses [][]llm.ToolCall
	sent      [][]string        // the tool names of each request, in order
	observed  map[string]string // tool call id → the observation handed back
}

func (s *offerTurnScript) Chat(_ context.Context, msgs []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		if m.Role == "tool" {
			s.observed[m.ToolCallID] = m.Content
		}
	}
	if opts.DisableTools || len(s.responses) == 0 {
		return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}}, nil
	}
	var names []string
	for _, d := range opts.Tools {
		names = append(names, d.Function.Name)
	}
	s.sent = append(s.sent, names)
	next := s.responses[0]
	s.responses = s.responses[1:]
	if next == nil { // the turn's answer
		return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}}, nil
	}
	return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: next}}}}, nil
}

func scriptCall(id, name string, args map[string]any) llm.ToolCall {
	raw, _ := json.Marshal(args)
	var tc llm.ToolCall
	tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = id, "function", name, string(raw)
	return tc
}

// toolRecords is every recorded result of tool, oldest first: its outcome
// and its reason code.
func toolRecords(t *testing.T, a *App, tool string) [][2]string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT outcome, COALESCE(json_extract(details, '$.reason_code'), '') FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')=? ORDER BY rowid`, tool)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var r [2]string
		if err := rows.Scan(&r[0], &r[1]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAnOperationOfferedMidTurnIsCallableFromTheNextTurn(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a.engine = identity.NewEngine(a.store, nil, ring.NewManager(), toolDiscovererAdapter{a.toolReg}, identity.Ports{})

	const (
		plugin   = "org.example.speaker"
		midTurn  = "pl_org_example_speaker_associate" // offered during the turn
		before   = "pl_org_example_speaker_list"      // offered before the turn
		released = "pl_org_example_speaker_forget"    // offered before, released during
		builtin  = "fixture_builtin"
	)
	ran := map[string]*atomic.Int32{}
	for _, name := range []string{midTurn, before, released, builtin} {
		n := &atomic.Int32{}
		ran[name] = n
		op := &fixtureTool{name: name, run: func(context.Context) (tools.Result, error) {
			n.Add(1)
			return tools.Result{Output: "ran"}, nil
		}}
		if name == builtin {
			a.toolReg.Register(op)
			continue
		}
		if err := a.toolReg.RegisterDynamic(op, plugin); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{before, released} {
		if _, err := a.toolReg.Offer(name); err != nil {
			t.Fatal(err)
		}
	}

	model := &offerTurnScript{observed: map[string]string{}, responses: [][]llm.ToolCall{
		// Turn 1, first response: offer one operation, release another.
		{scriptCall("offer", "tools", map[string]any{"action": "offer", "name": midTurn}),
			scriptCall("release", "tools", map[string]any{"action": "release", "name": released})},
		// Turn 1, second response: call all four.
		{scriptCall("same-turn", midTurn, nil), scriptCall("before", before, nil),
			scriptCall("released", released, nil), scriptCall("builtin", builtin, nil)},
		nil, // turn 1 answers
		// Turn 2: the offered operation, now shown.
		{scriptCall("next-turn", midTurn, nil)},
	}}
	loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, conversation.Config{})

	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "link this voice to me"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(model.observed["offer"], "callable from your next turn") {
		t.Fatalf("the offer did not land: %q", model.observed["offer"])
	}
	for _, sent := range model.sent {
		if slices.Contains(sent, midTurn) {
			t.Fatalf("turn 1 showed %s, which it offered while it ran: %v", midTurn, sent)
		}
	}
	if got := model.observed["same-turn"]; !strings.Contains(got, "offered; callable from your next turn") {
		t.Fatalf("a call to an operation offered this turn must be refused with the promise: %q", got)
	}
	if n := ran[midTurn].Load(); n != 0 {
		t.Fatalf("an operation the turn was never shown ran %d time(s)", n)
	}
	if got := toolRecords(t, a, midTurn); len(got) != 1 || got[0] != [2]string{"refused", tools.ReasonNotOfferedThisTurn} {
		t.Fatalf("the record must say refused, %s: %v", tools.ReasonNotOfferedThisTurn, got)
	}
	if ran[before].Load() != 1 || ran[builtin].Load() != 1 {
		t.Fatalf("an operation offered before the turn and a builtin must run: %d, %d (%q, %q)",
			ran[before].Load(), ran[builtin].Load(), model.observed["before"], model.observed["builtin"])
	}
	if got := model.observed["released"]; ran[released].Load() != 0 || !strings.Contains(got, "not in your active offer") {
		t.Fatalf("an operation released mid-turn must be refused as before: ran %d, %q", ran[released].Load(), got)
	}
	if got := toolRecords(t, a, released); len(got) != 1 || got[0] != [2]string{"refused", tools.ReasonInspectOnly} {
		t.Fatalf("the released operation's record: %v", got)
	}

	sentBefore := len(model.sent)
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "now link it"}}); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) <= sentBefore || !slices.Contains(model.sent[sentBefore], midTurn) {
		t.Fatalf("the next turn must show %s: %v", midTurn, model.sent[sentBefore:])
	}
	if got := model.observed["next-turn"]; ran[midTurn].Load() != 1 || strings.HasPrefix(got, "Error") || !strings.Contains(got, "\nran\n") {
		t.Fatalf("the next turn's call must run: ran %d, %q", ran[midTurn].Load(), got)
	}
	if got := toolRecords(t, a, midTurn); len(got) != 2 || got[1][0] != "succeeded" {
		t.Fatalf("the next turn's call must be recorded succeeded: %v", got)
	}
}
