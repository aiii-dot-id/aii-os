package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// recallThroughTheHost runs one recall call as a model emits it, through
// the host's tool executor: the path every resident's call takes.
func recallThroughTheHost(t *testing.T, a *App, args string) string {
	t.Helper()
	return recallObservation(t, a, args).Text
}

func recallObservation(t *testing.T, a *App, args string) conversation.Observation {
	t.Helper()
	var call llm.ToolCall
	call.ID, call.Type, call.Function.Name, call.Function.Arguments = "recall-limit", "function", "recall", args
	obs := appToolExecutor{a}.Execute(context.Background(), call)
	if obs.Failed {
		t.Fatalf("recall %s failed: %s", args, obs.Text)
	}
	return obs
}

// THE REPORTED CALL. On 0.1.17 a resident asked for two conversation hits
// and was given ten, with a footer offering the next page; limit 6 gave
// ten too. Through the host's own executor, a named source now returns the
// limit it is given and names where its next page starts.
func TestTheReportedConversationCallReturnsItsLimitThroughTheHost(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	for i := 0; i < 14; i++ {
		if _, err := a.store.AddConversationTurnSeq("operator", fmt.Sprintf("the reply is in lanternfish/ file %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	rows := regexp.MustCompile(`(?m)^  \[seq (\d+),`)
	for _, limit := range []int{2, 6} {
		out := recallThroughTheHost(t, a, fmt.Sprintf(`{"source":"conversation","query":"lanternfish/","limit":%d}`, limit))
		got := rows.FindAllStringSubmatch(out, -1)
		if len(got) != limit {
			t.Fatalf("limit %d returned %d conversation hits:\n%s", limit, len(got), out)
		}
		if want := "after_seq=" + got[len(got)-1][1]; !strings.Contains(out, "PAGED, more remain: conversation (its next page: "+want+")") {
			t.Fatalf("limit %d: the footer does not name the next page as %s:\n%s", limit, want, out)
		}
	}
}

// The heard history, through the host: given, the limit is the page; over
// 50 it is read as 50 and said; the pages its cursor names walk the history
// with nothing skipped and nothing repeated.
func TestHeardHistoryHonoursTheLimitThroughTheHost(t *testing.T) {
	a := newVoiceApp(t)
	applyDefaults(a.cfg)
	a.cfg.Prompt.RecentTurns = 20
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	withHeardHistory(a)
	const n = 60
	for i := 1; i <= n; i++ {
		a.retainHeard("heard-limit", int64(i), fmt.Sprintf("final number %d", i), dashboard.VoiceEvent{}, "withheld", "pending", nil)
	}
	type page struct {
		Rows []heardHistoryRow `json:"rows"`
		Next uint64            `json:"next_after_seq"`
	}
	read := func(args string) (page, string) {
		t.Helper()
		obs := recallObservation(t, a, args)
		var p page
		if err := json.Unmarshal([]byte(obs.Text), &p); err != nil {
			t.Fatalf("%s: the heard answer is not JSON: %v\n%s", args, err, obs.Text)
		}
		return p, obs.HostNote
	}
	for _, limit := range []int{1, 2, 6, 10, 50} {
		seen := map[uint64]bool{}
		var last uint64
		after := uint64(0)
		for {
			p, out := read(fmt.Sprintf(`{"source":"heard","query":"","limit":%d,"after_seq":%d}`, limit, after))
			if len(p.Rows) == 0 || len(p.Rows) > limit || (p.Next != 0 && len(p.Rows) != limit) {
				t.Fatalf("limit %d: a page of %d rows (next %d):\n%s", limit, len(p.Rows), p.Next, out)
			}
			for _, r := range p.Rows {
				if seen[r.Seq] || (last != 0 && r.Seq >= last) {
					t.Fatalf("limit %d: seq %d repeated or out of order", limit, r.Seq)
				}
				seen[r.Seq], last = true, r.Seq
			}
			if p.Next == 0 {
				break
			}
			after = p.Next
		}
		if len(seen) != n {
			t.Fatalf("limit %d: the pages walked %d of the %d finals", limit, len(seen), n)
		}
	}
	p, note := read(`{"source":"heard","query":"","limit":80}`)
	if len(p.Rows) != 50 || !strings.Contains(note, "limit=80 is over the most one recall returns, and was read as 50.") {
		t.Fatalf("limit 80 gave %d rows, or the host did not say it was read as 50: %q", len(p.Rows), note)
	}
	// The schema names the host setting, not a fixed page size. A change
	// through the settings door reaches the next model call without restart.
	if !strings.Contains(recallLimitDescription(t), "heard: the host's speech.heard_history.page_rows") {
		t.Fatal("the recall schema does not name the host's heard-page setting")
	}
	for _, tc := range []struct{ configured, want int }{{0, 20}, {3, 3}, {7, 7}, {50, 50}, {0, 20}} {
		state, err := a.applyConfigChange(map[string]any{"speech.heard_history.page_rows": tc.configured})
		if err != nil || len(state.RestartRequired) != 0 {
			t.Fatalf("set page_rows %d: restart %v, error %v", tc.configured, state.RestartRequired, err)
		}
		if p, note := read(`{"source":"heard","query":""}`); len(p.Rows) != tc.want || p.Next == 0 || note != "" {
			t.Fatalf("page_rows %d: no-limit read gives %d rows, next %d, note %q; want %d", tc.configured, len(p.Rows), p.Next, note, tc.want)
		}
		if p, note := read(`{"source":"heard","query":"","limit":1}`); len(p.Rows) != 1 || note != "" {
			t.Fatalf("page_rows %d overrode explicit limit 1: %d rows, note %q", tc.configured, len(p.Rows), note)
		}
	}
}

// recallLimitDescription is the limit parameter's words as recall
// advertises them.
func recallLimitDescription(t *testing.T) string {
	t.Helper()
	for _, v := range identity.Verbs() {
		if v.Name != "recall" {
			continue
		}
		raw, err := json.Marshal(v.Params)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		return schema.Properties["limit"].Description
	}
	t.Fatal("recall is not advertised")
	return ""
}

// The sentence that says a limit was read as 50 is the host's, and stands
// outside the untrusted boundary the heard context is handed in, which
// still holds the history's answer whole and valid JSON.
func TestTheHeardLimitNoteStandsOutsideTheUntrustedBoundary(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	withHeardHistory(a)
	for i := 1; i <= 60; i++ {
		a.retainHeard("heard-note", int64(i), fmt.Sprintf("final number %d", i), dashboard.VoiceEvent{}, "withheld", "pending", nil)
	}
	model := &heardReadScript{args: `{"source":"heard","query":"","limit":80}`}
	loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, conversation.Config{})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "what was said?"}}); err != nil {
		t.Fatal(err)
	}
	var handed string
	for _, m := range model.handed {
		if strings.Contains(m.Content, "final number") {
			handed = m.Content
		}
	}
	open, closing := strings.Index(handed, untrusted.Open), strings.LastIndex(handed, untrusted.Close)
	if handed == "" || open < 0 || closing < open {
		t.Fatalf("the heard read was not handed in its boundary: %q", handed)
	}
	note := strings.Index(handed, "was read as 50")
	if note < 0 || note > open {
		t.Fatalf("the host's sentence is not before and outside the untrusted boundary: %q", handed)
	}
	inside := handed[open:closing]
	inside = inside[strings.Index(inside, "\n")+1:]
	var p struct {
		Rows []heardHistoryRow `json:"rows"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(inside)), &p); err != nil || len(p.Rows) != 50 {
		t.Fatalf("inside the boundary is not the history's answer whole (%d rows, %v): %q", len(p.Rows), err, inside)
	}
}

// The host's sentence about a recall's limit belongs to a recall alone: a
// stray source and limit on another verb's call is not a recall, and no
// host sentence may claim one ran.
func TestOnlyARecallCarriesTheRecallHostNote(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	withHeardHistory(a)
	var call llm.ToolCall
	call.ID, call.Type, call.Function.Name, call.Function.Arguments = "not-recall", "function", "work", `{"action":"status","source":"heard","limit":80}`
	if obs := (appToolExecutor{a}).Execute(context.Background(), call); obs.HostNote != "" {
		t.Fatalf("a work call carries the recall's host note: %q", obs.HostNote)
	}
	if obs := recallObservation(t, a, `{"source":"heard","query":"","limit":80}`); obs.HostNote == "" {
		t.Fatal("the heard recall itself lost its host note")
	}
}
