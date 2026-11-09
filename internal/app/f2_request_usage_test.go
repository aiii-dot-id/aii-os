package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/workrecord"
)

// USAGE PER PROVIDER REQUEST (F2, the future-learning design's F2). Each
// request the loop sends is keyed by its turn and its place among the
// turn's requests; an annotation beside the start row of every call its
// response emitted keeps what it reported, whole and identical on each,
// with a count that was not reported null; a request that emitted no call keeps nothing anywhere and
// is in its turn's aggregate alone. The export lists a session's distinct
// requests once, marks one a call of which the record counts to another
// session, and sums them; a union sums a shared request once.

// requestScript answers the loop's requests in order: a response, or, for
// a nil one, a refusal of the definition of the request's last tool. Past
// its script it closes with a text reply.
type requestScript struct {
	mu      sync.Mutex
	replies []*llm.Response
	sent    int
}

func (s *requestScript) Chat(_ context.Context, _ []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent++
	if s.sent > len(s.replies) {
		return answerText("m-closing", usageOf(30, 3), "done"), nil
	}
	if r := s.replies[s.sent-1]; r != nil {
		return r, nil
	}
	for i := len(opts.Tools) - 1; i >= 0; i-- {
		if name := opts.Tools[i].Function.Name; !strings.HasPrefix(name, "f2_") {
			return nil, &llm.ToolSchemaRefusedError{Tool: name, Err: errors.New("unreadable definition")}
		}
	}
	return nil, errors.New("the request offers no tool to refuse")
}

func usageOf(in, out int) llm.Usage {
	return llm.Usage{PromptTokens: in, CompletionTokens: out, TotalTokens: in + out, Reported: true}
}

func answerCalls(model string, u llm.Usage, calls ...llm.ToolCall) *llm.Response {
	return &llm.Response{ModelID: model, Usage: u, Choices: []llm.Choice{{FinishReason: "tool_calls", Message: llm.Message{Role: "assistant", ToolCalls: calls}}}}
}

func answerText(model string, u llm.Usage, text string) *llm.Response {
	return &llm.Response{ModelID: model, Usage: u, Choices: []llm.Choice{{FinishReason: "stop", Message: llm.Message{Role: "assistant", Content: text}}}}
}

func askedCall(id, tool, args string) llm.ToolCall {
	var tc llm.ToolCall
	tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = id, "function", tool, args
	return tc
}

// lookFixture is a fixture tool that declares itself read-only, so a
// response of nothing but its calls runs as the loop's parallel batch.
type lookFixture struct{ *fixtureTool }

func (lookFixture) ReadOnly() bool { return true }

// requestApp is the seam app with the two fixture tools the requests call.
func requestApp(t *testing.T) *App {
	t.Helper()
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	ran := func(context.Context) (tools.Result, error) { return tools.Result{Output: "ran"}, nil }
	a.toolReg.Register(&fixtureTool{name: "f2_probe", run: ran})
	a.toolReg.Register(lookFixture{&fixtureTool{name: "f2_look", run: ran}})
	return a
}

// runRequests runs one turn of the real loop under turn's id against
// model, recording into the app's store as the resident's loop does.
func runRequests(t *testing.T, a *App, model conversation.LLMClient, turn string, cfg conversation.Config) (conversation.Result, error) {
	t.Helper()
	loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store, safe: a.safeTools}, nil, testLoop(cfg))
	return loop.Run(interaction.WithTurn(t.Context(), turn), "system", []llm.Message{{Role: "user", Content: "go"}})
}

// startedRequests is each call row of turn, by its ordinal: the request
// facts kept beside its start (store.AnnotationRequest), nil where none
// are, each under its request's key.
func startedRequests(t *testing.T, a *App, turn string) map[int]*interaction.RequestFacts {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT json_extract(c.details,'$.emission_ordinal'), q.key, q.payload FROM conversations c
		LEFT JOIN turn_annotations q ON q.turn_seq=c.turn_seq AND q.kind=? WHERE c.turn_id=? AND c.kind='tool_call'`, store.AnnotationRequest, turn)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int]*interaction.RequestFacts{}
	for rows.Next() {
		var ordinal int
		var key, payload sql.NullString
		if err := rows.Scan(&ordinal, &key, &payload); err != nil {
			t.Fatal(err)
		}
		out[ordinal] = nil
		if !payload.Valid {
			continue
		}
		var f interaction.RequestFacts
		if err := json.Unmarshal([]byte(payload.String), &f); err != nil {
			t.Fatal(err)
		}
		if key.String != store.RequestKey(turn, f.Ordinal) {
			t.Errorf("call %d's request is keyed %q, want %q", ordinal, key.String, store.RequestKey(turn, f.Ordinal))
		}
		out[ordinal] = &f
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// factsOf is what a request that answered resp at ordinal reported, as
// every start of its calls must keep it: the whole response's usage.
func factsOf(ordinal int, resp *llm.Response) interaction.RequestFacts {
	u := resp.Usage
	f := interaction.RequestFacts{Ordinal: ordinal, Model: resp.ModelID, UnknownAttempts: u.UnknownAttempts}
	if u.Reported {
		f.PromptTokens, f.CompletionTokens, f.TotalTokens = &u.PromptTokens, &u.CompletionTokens, &u.TotalTokens
		if u.CacheReadReported {
			f.CachedPromptTokens = &u.CachedPromptTokens
		}
		if u.CacheWriteReported {
			f.CacheWriteTokens = &u.CacheWriteTokens
		}
	}
	return f
}

func sameFacts(a, b *interaction.RequestFacts) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return a != nil && b != nil && bytes.Equal(x, y)
}

// exportLines is the export of the filter, by session id.
func exportLines(t *testing.T, a *App, f workrecord.Filter) map[string]workrecord.Line {
	t.Helper()
	var buf bytes.Buffer
	if _, err := workrecord.Write(t.Context(), &buf, a.store, nil, f, time.Now()); err != nil {
		t.Fatal(err)
	}
	out := map[string]workrecord.Line{}
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var l workrecord.Line
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatal(err)
		}
		if l.Kind == "session" {
			out[l.Session.ID] = l
		}
	}
	return out
}

// sessionNamed is the id of the session the record holds by description.
func sessionNamed(t *testing.T, a *App, description string) string {
	t.Helper()
	all, err := a.store.WorkRecordSessions(time.Time{}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range all {
		if ws.Description == description {
			return ws.ID
		}
	}
	t.Fatalf("no session %q", description)
	return ""
}

// SEVERAL CALLS FROM ONE RESPONSE CARRY ONE KEY AND IDENTICAL FACTS, ON
// EVERY START PATH. A response of two read-only calls (the parallel batch),
// one of two ordinary calls (the serial walk), and the closing response
// whose call is refused before execution (finalCall's disabled tools):
// each call's start keeps its own request's ordinal and that request's
// whole usage, never a share of it. Controls: usage divided among a
// request's calls; the request dropped from the start row.
func TestSeveralCallsFromOneResponseCarryOneRequest(t *testing.T) {
	a := requestApp(t)
	parallel := answerCalls("m-one", usageOf(1000, 40), askedCall("c1", "f2_look", "{}"), askedCall("c2", "f2_look", "{}"))
	serial := answerCalls("m-two", llm.Usage{PromptTokens: 1200, CompletionTokens: 60, TotalTokens: 1260, Reported: true, CachedPromptTokens: 900, CacheReadReported: true},
		askedCall("c3", "f2_probe", "{}"), askedCall("c4", "f2_probe", "{}"))
	closing := answerCalls("m-three", usageOf(1300, 20), askedCall("c5", "f2_probe", "{}"))
	_, err := runRequests(t, a, &requestScript{replies: []*llm.Response{parallel, serial, closing}}, "t_f2_several", conversation.Config{MaxIterations: limits.Of(2)})
	if err == nil || !strings.Contains(err.Error(), "requested tools that were disabled") {
		t.Fatalf("the closing response's call should end the turn refused: %v", err)
	}
	got := startedRequests(t, a, "t_f2_several")
	if len(got) != 5 {
		t.Fatalf("five calls started, got %d", len(got))
	}
	for ordinal, want := range map[int]interaction.RequestFacts{1: factsOf(1, parallel), 2: factsOf(1, parallel), 3: factsOf(2, serial), 4: factsOf(2, serial), 5: factsOf(3, closing)} {
		if !sameFacts(got[ordinal], &want) {
			g, _ := json.Marshal(got[ordinal])
			w, _ := json.Marshal(want)
			t.Errorf("call %d keeps the request %s, want its request's whole facts %s", ordinal, g, w)
		}
	}
	for ordinal, want := range map[int]interaction.Outcome{1: interaction.Succeeded, 2: interaction.Succeeded, 3: interaction.Succeeded, 4: interaction.Succeeded, 5: interaction.Refused} {
		var outcome string
		if err := a.store.DB().QueryRow(`SELECT outcome FROM conversations WHERE turn_id='t_f2_several' AND kind='tool_result' AND json_extract(details,'$.emission_ordinal')=?`, ordinal).Scan(&outcome); err != nil || outcome != string(want) {
			t.Errorf("call %d ended %q (%v), want %s", ordinal, outcome, err, want)
		}
	}
}

// A RESEND AFTER A REFUSED DEFINITION IS A REQUEST OF ITS OWN. The first
// request is refused for a tool's definition and sent again without it;
// the answered request is the turn's second, and its calls carry that key.
// The refused request emitted no call and keeps nothing; the turn's
// aggregate still counts it. Control: the key minted per round.
func TestAResendAfterARefusedDefinitionIsARequestOfItsOwn(t *testing.T) {
	a := requestApp(t)
	answered := answerCalls("m-resent", usageOf(800, 25), askedCall("c1", "f2_probe", "{}"))
	res, err := runRequests(t, a, &requestScript{replies: []*llm.Response{nil, answered}}, "t_f2_resend", conversation.Config{})
	if err != nil {
		t.Fatal(err)
	}
	got := startedRequests(t, a, "t_f2_resend")
	want := factsOf(2, answered)
	if len(got) != 1 || !sameFacts(got[1], &want) {
		g, _ := json.Marshal(got[1])
		t.Fatalf("the answered request's call keeps %s (of %d calls), want the turn's second request %+v", g, len(got), want)
	}
	if res.Usage.Calls != 3 || res.Usage.Silent != 1 || res.Usage.TotalTokens != 825+33 {
		t.Errorf("the turn's aggregate: %+v, want the refused request, the answered one and the closing reply", res.Usage)
	}
}

// A CLIENT'S RETRY IS THE SAME REQUEST. The provider fails the first
// attempt with a server error and answers the second: one request, one
// key, its attempt of unknown cost counted in its facts.
func TestAClientRetryIsTheSameRequest(t *testing.T) {
	a := requestApp(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch n.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"overloaded"}}`)
		case 2:
			_, _ = io.WriteString(w, `{"id":"r2","model":"m-retried","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"f2_probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":500,"completion_tokens":10,"total_tokens":510}}`)
		default:
			_, _ = io.WriteString(w, `{"id":"r3","model":"m-retried","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":520,"completion_tokens":2,"total_tokens":522}}`)
		}
	}))
	t.Cleanup(srv.Close)
	client := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "m-retried", MaxOutputTokens: 64, Retries: 1, RetryBackoffMS: 1})
	if _, err := runRequests(t, a, client, "t_f2_retry", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 {
		t.Fatalf("the provider was asked %d times, want 3 (an attempt, its retry, the closing reply)", n.Load())
	}
	got := startedRequests(t, a, "t_f2_retry")
	if len(got) != 1 || got[1] == nil || got[1].Ordinal != 1 || got[1].UnknownAttempts != 1 || got[1].TotalTokens == nil || *got[1].TotalTokens != 510 {
		g, _ := json.Marshal(got[1])
		t.Fatalf("the retried request's call keeps %s, want ordinal 1 with one attempt of unknown cost and the answer's usage", g)
	}
}

// A COMPLETION WITH NO CALLS KEEPS NOTHING ANYWHERE, AND IS IN ITS TURN'S
// AGGREGATE. A turn answered by text alone writes no request facts on any
// row; its usage is in the turn's row as before.
func TestACompletionWithNoCallsKeepsNoRequestFacts(t *testing.T) {
	a := requestApp(t)
	res, err := runRequests(t, a, &requestScript{replies: []*llm.Response{answerText("m-text", usageOf(400, 12), "only words")}}, "t_f2_text", conversation.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var kept int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM turn_annotations WHERE kind=?`, store.AnnotationRequest).Scan(&kept); err != nil || kept != 0 {
		t.Fatalf("%d rows keep request facts after a turn with no call (%v)", kept, err)
	}
	a.logTurnSummary(turnLoop{source: "chat", turn: "t_f2_text", usage: &res.Usage})
	var total int64
	if err := a.store.DB().QueryRow(`SELECT total_tokens FROM turn_metrics WHERE turn_id='t_f2_text'`).Scan(&total); err != nil || total != 412 {
		t.Fatalf("the turn's aggregate holds %d tokens (%v), want the completion's 412", total, err)
	}
}

// TWO SESSIONS SHARE A REQUEST. One response starts new work and calls a
// tool: the start is counted to the work already active, the call to the
// new session (F1). Exported alone, the new session's request is marked
// shared; a union of both sessions, keyed as the export's doc says, sums it
// once; the turn's aggregate also holds the closing reply, which no
// session's requests do. Control: shared computed from the exported
// sessions only.
func TestTwoSessionsShareARequest(t *testing.T) {
	a := requestApp(t)
	if err := a.store.StartWorkSession(t.Context(), "ws_f2_old", "the work already active"); err != nil {
		t.Fatal(err)
	}
	shared := answerCalls("m-shared", usageOf(2000, 80), askedCall("c1", "work", `{"action":"start","description":"the new work"}`), askedCall("c2", "f2_probe", "{}"))
	res, err := runRequests(t, a, &requestScript{replies: []*llm.Response{shared}}, "t_f2_share", conversation.Config{})
	if err != nil {
		t.Fatal(err)
	}
	a.logTurnSummary(turnLoop{source: "chat", turn: "t_f2_share", usage: &res.Usage})
	fresh := sessionNamed(t, a, "the new work")
	for _, ordinal := range []int{1, 2} {
		if got := startedRequests(t, a, "t_f2_share")[ordinal]; got == nil || got.Ordinal != 1 {
			t.Fatalf("call %d does not keep the shared request: %+v", ordinal, got)
		}
	}

	alone := exportLines(t, a, workrecord.Filter{Session: fresh})[fresh]
	if len(alone.Calls) != 1 || len(alone.Requests) != 1 {
		t.Fatalf("the new session alone: %d calls, %d requests, want one of each", len(alone.Calls), len(alone.Requests))
	}
	if r := alone.Requests[0]; r.TurnID != "t_f2_share" || r.Ordinal != 1 || !r.Shared {
		t.Errorf("the new session's request %+v is not marked shared", r)
	}
	if s := alone.RequestSum; s.Requests != 1 || s.Shared != 1 || s.TotalTokens.Sum == nil || *s.TotalTokens.Sum != 2080 || s.CallsWithoutRequest != 0 {
		t.Errorf("the new session's request sum: %+v", s)
	}

	both := exportLines(t, a, workrecord.Filter{})
	union, naive := map[[2]any]int64{}, int64(0)
	for _, id := range []string{"ws_f2_old", fresh} {
		l := both[id]
		if len(l.Requests) != 1 || !l.Requests[0].Shared {
			t.Fatalf("%s's requests: %+v", id, l.Requests)
		}
		for _, r := range l.Requests {
			union[[2]any{r.TurnID, r.Ordinal}] = int64(*r.TotalTokens)
		}
		naive += *l.RequestSum.TotalTokens.Sum
	}
	var sum int64
	for _, n := range union {
		sum += n
	}
	if sum != 2080 || naive != 2*2080 {
		t.Errorf("the union sums %d (the request sums add to %d), want the shared request once: 2080", sum, naive)
	}
	turns := both[fresh].Turns
	if len(turns) != 1 || turns[0].TotalTokens == nil || *turns[0].TotalTokens != 2080+33 {
		t.Errorf("the turn's aggregate: %+v, want the shared request and the closing reply", turns)
	}
}

// WHAT WAS NOT REPORTED STAYS NOT REPORTED. Through the real client: a
// reply whose cache read is reported as 0 and whose cache write is not, a
// reply with no usage, and one whose counts do not add up. Each call's
// request keeps a reported 0 as 0 and every count not reported as null;
// the session's sum adds what was reported and counts the rest beside it.
func TestWhatWasNotReportedStaysNotReported(t *testing.T) {
	a := requestApp(t)
	if err := a.store.StartWorkSession(t.Context(), "ws_f2_bits", "the counted work"); err != nil {
		t.Fatal(err)
	}
	call := func(model, usage string) string {
		return `{"id":"r","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"c","type":"function","function":{"name":"f2_probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}]` + usage + `}`
	}
	replies := []string{
		call("m-a", `,"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":0}}`),
		call("m-b", ``),
		call("m-c", `,"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":999}`),
		`{"id":"r","model":"m-d","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
	}
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replies[min(int(n.Add(1)), len(replies))-1])
	}))
	t.Cleanup(srv.Close)
	client := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "m-a", MaxOutputTokens: 64, Retries: -1})
	if _, err := runRequests(t, a, client, "t_f2_bits", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.RecordToolStart("t_f2_host", 1, "main", "", "", "f2_probe", "{}"); err != nil {
		t.Fatal(err)
	}
	l := exportLines(t, a, workrecord.Filter{Session: "ws_f2_bits"})["ws_f2_bits"]
	if len(l.Calls) != 4 || len(l.Requests) != 3 {
		t.Fatalf("%d calls, %d requests, want 4 and 3", len(l.Calls), len(l.Requests))
	}
	raw, _ := json.Marshal(l.Requests)
	for i, want := range []string{
		`"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"cached_prompt_tokens":0,"cache_write_tokens":null`,
		`"prompt_tokens":null,"completion_tokens":null,"total_tokens":null,"cached_prompt_tokens":null,"cache_write_tokens":null`,
		`"prompt_tokens":null,"completion_tokens":null,"total_tokens":null,"cached_prompt_tokens":null,"cache_write_tokens":null`,
	} {
		one, _ := json.Marshal(l.Requests[i])
		if !strings.Contains(string(one), want) {
			t.Errorf("request %d is %s, want %s", i+1, one, want)
		}
	}
	s := l.RequestSum
	if s.TotalTokens.Sum == nil || *s.TotalTokens.Sum != 110 || s.TotalTokens.NotReported != 2 ||
		s.CachedPromptTokens.Sum == nil || *s.CachedPromptTokens.Sum != 0 || s.CachedPromptTokens.NotReported != 2 ||
		s.CacheWriteTokens.Sum != nil || s.CacheWriteTokens.NotReported != 3 || s.CallsWithoutRequest != 1 {
		sum, _ := json.Marshal(s)
		t.Errorf("the session's sum is %s (requests %s)", sum, raw)
	}
	if c := l.Calls[3]; c.Request != nil || c.TurnID != "t_f2_host" {
		t.Errorf("a call no request emitted shows one: %+v", c.Request)
	}
	if !strings.Contains(string(mustJSON(t, l.Calls[0])), `"request":{"ordinal":1,"model":"m-a"`) {
		t.Errorf("a call does not show its request: %s", mustJSON(t, l.Calls[0]))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// THE FACTS ARE BESIDE THE CALL ROW, NEVER IN ITS DETAILS. 0.1.23 and
// 0.2.0 decode a row's details strictly (interaction_query.go at both
// tags) and would refuse every page holding a member they do not know;
// they read a row's annotations as an open map. So a call row's details
// hold no "request" member and decode strictly into the details every
// release knows, its facts are an annotation keyed by its request, and a
// plugin's read of the row carries neither. Control: the facts written
// back into the details.
func TestACallRowKeepsItsRequestBesideItsDetails(t *testing.T) {
	a := requestApp(t)
	both := answerCalls("m-beside", usageOf(300, 9), askedCall("c1", "f2_probe", "{}"), askedCall("c2", "f2_probe", "{}"))
	if _, err := runRequests(t, a, &requestScript{replies: []*llm.Response{both}}, "t_f2_beside", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	rows, err := a.store.DB().Query(`SELECT details FROM conversations WHERE turn_id='t_f2_beside' AND kind='tool_call'`)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		n++
		var members map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &members); err != nil {
			t.Fatal(err)
		}
		if _, has := members["request"]; has {
			t.Errorf("a call row's details carry a request member: %s", raw)
		}
		var d interaction.Details
		if err := canonicaljson.DecodeStrict([]byte(raw), &d); err != nil {
			t.Errorf("a call row's details do not decode strictly: %v (%s)", err, raw)
		}
	}
	rows.Close()
	if n != 2 {
		t.Fatalf("%d call rows, want 2", n)
	}
	got := startedRequests(t, a, "t_f2_beside")
	if want := factsOf(1, both); !sameFacts(got[1], &want) || !sameFacts(got[2], &want) {
		t.Fatalf("the requests beside the rows: %+v %+v", got[1], got[2])
	}
	// Through the reads a plugin calls (the broker's interaction.query and
	// interaction.read, served by the App): a page of the calls, and the
	// detail read of each row the page advertised.
	page, err := a.QueryInteractions(t.Context(), interaction.Query{Version: 1, Source: "recorded", Filter: interaction.Filter{Kinds: []interaction.Kind{interaction.ToolCall}}})
	if err != nil {
		t.Fatal(err)
	}
	served := 0
	for _, r := range page.Rows {
		if r.TurnID != "t_f2_beside" {
			continue
		}
		served++
		if _, has := r.Annotations[store.AnnotationRequest]; has {
			t.Errorf("a plugin's read of a call carries its request: %v", r.Annotations)
		}
		row, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		read, err := a.ReadInteraction(t.Context(), interaction.ReadRequest{Version: 1, Source: "recorded", ID: r.ID, Incarnation: page.Incarnation, SHA256: interaction.Hash(row)})
		if err != nil {
			t.Fatalf("a plugin's detail read of a call: %v", err)
		}
		detail, err := base64.StdEncoding.DecodeString(read.Data)
		if err != nil {
			t.Fatal(err)
		}
		var record interaction.Record
		if err := json.Unmarshal(detail, &record); err != nil {
			t.Fatal(err)
		}
		if _, has := record.Annotations[store.AnnotationRequest]; has || record.ID != r.ID {
			t.Errorf("a plugin's detail read of a call carries its request: %s", detail)
		}
	}
	if served != 2 {
		t.Fatalf("a plugin's read served %d of the turn's calls, want 2", served)
	}
}

// A START SAFE DIVERTS KEEPS NO REQUEST: its record is not durable, and the
// durable record holds no row for it.
func TestASAFEDivertedStartKeepsNoRequest(t *testing.T) {
	a := requestApp(t)
	tr := appTranscript{st: a.store, safe: a.safeTools}
	total := 5
	a.store.SetFrozen(true, "a test hold")
	err := tr.RecordToolStartOfRequest("t_f2_safe", 1, "c", "f2_probe", "{}", "m", interaction.RequestFacts{Ordinal: 1, Model: "m", PromptTokens: &total, CompletionTokens: &total, TotalTokens: &total})
	a.store.SetFrozen(false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := startedRequests(t, a, "t_f2_safe"); len(got) != 0 {
		t.Fatalf("a SAFE-diverted start wrote a durable row: %v", got)
	}
	if _, ok := a.safeTools.seat("t_f2_safe", 1); !ok {
		t.Fatal("the SAFE record does not hold the diverted start")
	}
}
