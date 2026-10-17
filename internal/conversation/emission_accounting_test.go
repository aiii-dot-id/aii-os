package conversation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// Keep the actual recorder inputs, including typed nonexecution, separately
// from the executor's calls. Provider IDs deliberately repeat in these tests.
type emissionRecorder struct {
	fakeTranscript
	observations        map[int]Observation
	startFail, doneFail int
}

func (r *emissionRecorder) RecordToolStart(turn string, n int, id, tool, args, model string) error {
	if n == r.startFail {
		return errors.New("start unavailable")
	}
	return r.fakeTranscript.RecordToolStart(turn, n, id, tool, args, model)
}

func (r *emissionRecorder) RecordToolDone(turn string, n int, tool, args string, obs Observation) error {
	if n == r.doneFail {
		return errors.New("result unavailable")
	}
	if r.observations == nil {
		r.observations = map[int]Observation{}
	}
	r.observations[n] = obs
	return r.fakeTranscript.RecordToolDone(turn, n, tool, args, obs)
}

func assertEmissionRecords(t *testing.T, r *emissionRecorder, reasons ...string) {
	t.Helper()
	starts, dones := []int{}, []int{}
	for _, e := range r.events {
		if e.phase == "started" {
			starts = append(starts, e.ordinal)
		} else {
			dones = append(dones, e.ordinal)
		}
	}
	want := make([]int, len(reasons))
	for i, reason := range reasons {
		want[i] = i + 1
		obs, ok := r.observations[i+1]
		if !ok || obs.ReasonCode != reason || (reason != "" && (!obs.Refused || !obs.Failed || obs.Cancelled)) {
			t.Fatalf("ordinal %d = %+v, want refused reason %q", i+1, obs, reason)
		}
	}
	if !reflect.DeepEqual(starts, want) || !reflect.DeepEqual(dones, want) {
		t.Fatalf("starts %v, done %v, want exactly %v", starts, dones, want)
	}
}

func TestDiscardedEmissionsRemainCountedAndRecorded(t *testing.T) {
	call := toolCall("same-id", "bash", `{}`)
	for _, tc := range []struct {
		name    string
		script  []llm.Response
		reasons []string
	}{
		{"degenerate twice", []llm.Response{resp(collapseText(), call), resp(collapseText(), call)}, []string{ReasonToolDegenerate, ReasonToolDegenerate}},
		{"degenerate then recovery", []llm.Response{resp(collapseText(), call), resp("", call), textResp("done")}, []string{ReasonToolDegenerate, ""}},
		{"provider refusal", []llm.Response{{Choices: []llm.Choice{{FinishReason: "refusal", Message: llm.Message{ToolCalls: []llm.ToolCall{call}}}}}}, []string{ReasonToolStopRefused}},
		{"unknown stop", []llm.Response{{Choices: []llm.Choice{{FinishReason: "new_stop", Message: llm.Message{ToolCalls: []llm.ToolCall{call}}}}}}, []string{ReasonToolStopRefused}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &emissionRecorder{}
			ex := &fakeTools{results: map[string]string{"bash": "ok"}}
			res, err := New(&scriptLLM{script: tc.script}, ex, &fakeDefs{}, r, nil, Config{}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
			if err != nil {
				t.Fatal(err)
			}
			wantUsed := 0
			for _, reason := range tc.reasons {
				if reason == "" {
					wantUsed++
				}
			}
			if res.ToolCallsEmitted != len(tc.reasons) || res.ToolCallsUsed != wantUsed || !res.ToolCallCoverageComplete || len(ex.calls) != wantUsed {
				t.Fatalf("result %+v, executed %v", res, ex.calls)
			}
			assertEmissionRecords(t, r, tc.reasons...)
		})
	}
}

func TestChildCallBudgetCountsSurplusWithoutExecutingIt(t *testing.T) {
	r := &emissionRecorder{}
	ex := &fakeTools{results: map[string]string{"bash": "ok"}}
	res, err := New(&scriptLLM{script: []llm.Response{wideBatch(5), wideBatch(5)}}, ex, &fakeDefs{}, r, nil, Config{MaxToolCalls: 8}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ExhaustedCallBudget || res.ToolCallsUsed != 8 || res.ToolCallsEmitted != 10 || !res.ToolCallCoverageComplete || len(ex.calls) != 8 {
		t.Fatalf("result %+v, executed %d", res, len(ex.calls))
	}
	assertEmissionRecords(t, r, "", "", "", "", "", "", "", "", ReasonToolCallBudget, ReasonToolCallBudget)
}

func TestClosingEmissionsAreRecordedAtEveryFinalCallSite(t *testing.T) {
	for _, site := range closingSites() {
		t.Run(site.name, func(t *testing.T) {
			r := &emissionRecorder{}
			ex := &fakeTools{results: map[string]string{"bash": "ok"}}
			closing := resp("", toolCall("same", "bash", `{}`), toolCall("same", "bash", `{}`))
			res, err := New(&scriptLLM{script: []llm.Response{site.first, closing}}, ex, site.defs, r, nil, site.cfg).Run(t.Context(), site.system, []llm.Message{{Role: "user", Content: "go"}})
			if err == nil || !strings.Contains(err.Error(), "requested tools that were disabled") {
				t.Fatalf("err %v", err)
			}
			if res.ToolCallsEmitted != 3 || res.ToolCallsUsed != 1 || !res.ToolCallCoverageComplete || len(ex.calls) != 1 {
				t.Fatalf("result %+v, executed %v", res, ex.calls)
			}
			assertEmissionRecords(t, r, "", ReasonToolDisabled, ReasonToolDisabled)
		})
	}
}

func TestFailedClosingEmissionRecordPreservesCountAndModel(t *testing.T) {
	r := &emissionRecorder{doneFail: 2}
	closing := resp("", toolCall("same", "bash", `{}`))
	closing.ModelID = "closing-model"
	ex := &fakeTools{results: map[string]string{"bash": "ok"}}
	res, err := New(&scriptLLM{script: []llm.Response{wideBatch(1), closing}}, ex, &fakeDefs{}, r, nil, Config{MaxIterations: 1}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil || err.Error() != "record tool result: result unavailable" || res.ModelID != "closing-model" || res.ToolCallsEmitted != 2 || res.ToolCallCoverageComplete || len(ex.calls) != 1 {
		t.Fatalf("result %+v, err %v, ran %v", res, err, ex.calls)
	}
}

func TestEmissionPersistenceFailureKeepsTotalAndDeclaresIncompleteCoverage(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		for _, done := range []bool{false, true} {
			t.Run(fmt.Sprintf("parallel=%v/done=%v", parallel, done), func(t *testing.T) {
				r := &emissionRecorder{}
				if done {
					r.doneFail = 2
				} else {
					r.startFail = 2
				}
				ex := &serialTools{safe: map[string]bool{"read": parallel}}
				calls := []llm.ToolCall{toolCall("x", "read", `{}`), toolCall("x", "read", `{}`), toolCall("x", "read", `{}`)}
				res, err := New(&scriptLLM{script: []llm.Response{resp("", calls...)}}, ex, &fakeDefs{}, r, nil, Config{}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
				if err == nil || res.ToolCallsEmitted != 3 || res.ToolCallCoverageComplete {
					t.Fatalf("result %+v, err %v", res, err)
				}
				wantRun := 1
				if done {
					wantRun = 2
				}
				if parallel {
					wantRun = 0
					if done {
						wantRun = 3
					}
				}
				if len(ex.order) != wantRun {
					t.Fatalf("ran %v, want %d", ex.order, wantRun)
				}
			})
		}
	}
	for _, done := range []bool{false, true} {
		t.Run(fmt.Sprintf("discarded/done=%v", done), func(t *testing.T) {
			r := &emissionRecorder{}
			if done {
				r.doneFail = 1
			} else {
				r.startFail = 1
			}
			ex := &fakeTools{}
			res, err := New(&scriptLLM{script: []llm.Response{resp(collapseText(), toolCall("x", "read", `{}`), toolCall("x", "read", `{}`))}}, ex, &fakeDefs{}, r, nil, Config{}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
			if err == nil || res.ToolCallsEmitted != 2 || res.ToolCallsUsed != 0 || res.ToolCallCoverageComplete || len(ex.calls) != 0 {
				t.Fatalf("result %+v, err %v, executed %v", res, err, ex.calls)
			}
		})
	}
}

func TestParallelEmissionOrdinalsSurviveEarlierDiscard(t *testing.T) {
	r := &emissionRecorder{}
	ex := newBarrierTools(2, map[string]bool{"read_a": true, "read_b": true})
	client := &scriptLLM{script: []llm.Response{resp(collapseText(), toolCall("same", "read_a", `{}`)), resp("", toolCall("same", "read_a", `{}`), toolCall("same", "read_b", `{}`)), textResp("done")}}
	res, err := New(client, ex, &fakeDefs{}, r, nil, Config{}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil || !res.ToolCallCoverageComplete || res.ToolCallsEmitted != 3 || res.ToolCallsUsed != 2 || ex.arrived != 2 {
		t.Fatalf("result %+v, err %v", res, err)
	}
	assertEmissionRecords(t, r, ReasonToolDegenerate, "", "")
	for _, obs := range r.observations {
		if strings.Contains(obs.Text, "TIMEOUT") {
			t.Fatal(obs.Text)
		}
	}
}

// Starts still sample the active execution context after preceding mutations.
type changingSession struct {
	active   string
	starts   []string
	ordinals []int
}

func (s *changingSession) Execute(_ context.Context, tc llm.ToolCall) Observation {
	s.ordinals = append(s.ordinals, tc.EmissionOrdinal)
	s.active = tc.Function.Name
	return Observation{Text: "ok"}
}
func (s *changingSession) RecordToolStart(string, int, string, string, string, string) error {
	s.starts = append(s.starts, s.active)
	return nil
}
func (*changingSession) RecordToolDone(string, int, string, string, Observation) error { return nil }
func (*changingSession) TranscriptResultExcerptLimit() int                             { return 0 }

func TestEmissionNumberingPreservesSequentialStartAttribution(t *testing.T) {
	s := &changingSession{active: "old"}
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("x", "new", `{}`), toolCall("x", "other", `{}`)), textResp("done")}}
	res, err := New(client, s, &fakeDefs{}, s, nil, Config{}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil || !res.ToolCallCoverageComplete || !reflect.DeepEqual(s.starts, []string{"old", "new"}) || !reflect.DeepEqual(s.ordinals, []int{1, 2}) {
		t.Fatalf("starts %v ordinals %v result %+v err %v", s.starts, s.ordinals, res, err)
	}
	if client.script[0].Choices[0].Message.ToolCalls[0].EmissionOrdinal != 0 {
		t.Fatal("numbering mutated the provider-owned response")
	}
}

func TestUnobservedProviderItemsNeverClaimCompleteCoverage(t *testing.T) {
	limited := textResp("cut")
	limited.Choices[0].FinishReason = "length"
	contextCut := textResp("cut")
	contextCut.Choices[0].FinishReason = "model_context_window_exceeded"
	retried := textResp("done")
	retried.Usage.UnknownAttempts = 1
	for _, tc := range []struct {
		name   string
		script []llm.Response
	}{
		{"failed exchange", nil}, {"choiceless", []llm.Response{{}}}, {"output limit", []llm.Response{limited}}, {"context limit", []llm.Response{contextCut}}, {"unobserved retry", []llm.Response{retried}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := New(&scriptLLM{script: tc.script}, &fakeTools{}, &fakeDefs{}, &emissionRecorder{}, nil, Config{}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
			if res.ToolCallCoverageComplete || res.ToolCallsEmitted != 0 {
				t.Fatalf("result %+v", res)
			}
		})
	}
}

func TestMissingExecutorRecordsEveryRefusedEmission(t *testing.T) {
	r := &emissionRecorder{}
	res, err := New(&scriptLLM{script: []llm.Response{wideBatch(2)}}, nil, &fakeDefs{}, r, nil, Config{}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil || res.ToolCallsEmitted != 2 || res.ToolCallsUsed != 2 || !res.ToolCallCoverageComplete {
		t.Fatalf("result %+v, err %v", res, err)
	}
	assertEmissionRecords(t, r, ReasonToolNoExecutor, ReasonToolNoExecutor)
}

type transientEmissionRecorder struct{ emissionRecorder }

func (r *transientEmissionRecorder) RecordToolDoneDurable(turn string, ordinal int, tool, args string, obs Observation) (bool, error) {
	return false, r.RecordToolDone(turn, ordinal, tool, args, obs)
}

func TestTransientOrAbsentTranscriptCannotClaimDurableEmissionCoverage(t *testing.T) {
	for _, tr := range []Transcript{nil, &transientEmissionRecorder{}} {
		res, err := New(&scriptLLM{script: []llm.Response{wideBatch(1), textResp("done")}}, &fakeTools{}, &fakeDefs{}, tr, nil, Config{}).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
		if err != nil || res.ToolCallsEmitted != 1 || res.ToolCallCoverageComplete {
			t.Fatalf("result %+v, err %v", res, err)
		}
	}
}

func TestCancelledBatchStillAccountsForUnexecutedItems(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := &emissionRecorder{}
	ex := &stopFirst{stop: cancel}
	res, err := New(&scriptLLM{script: []llm.Response{wideBatch(3)}}, ex, &fakeDefs{}, r, nil, Config{}).Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
	if !errors.Is(err, context.Canceled) || res.ToolCallsEmitted != 3 || res.ToolCallsUsed != 3 || !res.ToolCallCoverageComplete || len(ex.ran) != 1 {
		t.Fatalf("result %+v, err %v, ran %v", res, err, ex.ran)
	}
	for _, n := range []int{2, 3} {
		if !r.observations[n].Cancelled || r.observations[n].Refused {
			t.Fatalf("unexecuted ordinal %d = %+v", n, r.observations[n])
		}
	}
}

func TestOutsideRequestRefusalCountsExactlyOneEmission(t *testing.T) {
	allowed := []string{"read"}
	ctx := requestContext(t, RequestOptions{AllowedTools: &allowed})
	r := &emissionRecorder{}
	ex := &fakeTools{}
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("same", "unknown", `{}`)), textResp("done")}}
	res, err := New(client, ex, &fakeDefs{defs: []llm.ToolDefinition{requestDef("read")}}, r, nil, Config{}).Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil || res.ToolCallsEmitted != 1 || res.ToolCallsUsed != 1 || !res.ToolCallCoverageComplete || len(ex.calls) != 0 {
		t.Fatalf("result %+v, err %v, ran %v", res, err, ex.calls)
	}
	assertEmissionRecords(t, r, ReasonToolOutsideRequest)
}
