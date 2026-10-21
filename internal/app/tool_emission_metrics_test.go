package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestRealTurnStoresEmissionsAndMarksUnretainedCoverage(t *testing.T) {
	var requests atomic.Int32
	a := meteredApp(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			io.WriteString(w, `{"content":[{"type":"tool_use","id":"a","name":"read","input":{}},{"type":"tool_use","id":"b","name":"read","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":10}}`)
		} else {
			io.WriteString(w, `{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`)
		}
	})
	if _, err := a.handleMessage(t.Context(), "synthetic emission count"); err != nil {
		t.Fatal(err)
	}
	var count, complete int
	if err := a.store.DB().QueryRow(`SELECT emitted_calls, emission_coverage_complete FROM turn_metrics`).Scan(&count, &complete); err != nil {
		t.Fatal(err)
	}
	// This existing metering fixture has no transcript. A counted response
	// must not be called durably covered merely because the turn completed.
	if count != 2 || complete != 0 {
		t.Fatalf("got %d/%d, want 2/incomplete", count, complete)
	}
}

func TestChildMetricIncludesBudgetRefusedRequests(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			io.WriteString(w, `{"model":"fake-child","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"ls","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"ls","arguments":"{}"}},{"id":"c","type":"function","function":{"name":"ls","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`)
		} else {
			io.WriteString(w, `{"model":"fake-child","choices":[{"message":{"role":"assistant","content":"partial: budget ended"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`)
		}
	}))
	t.Cleanup(srv.Close)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: limits.Of(3), SubagentMaxToolRounds: limits.Of(3), SubagentMaxToolCalls: limits.Of(1), SubagentMaxLegs: limits.Of(1), SubagentWallSeconds: 5})
	id := spawnChild(t, a, st, "synthetic bounded child")
	waitDelivered(t, st, id)
	var calls, emitted, complete int
	if err := st.DB().QueryRow(`SELECT calls, emitted_calls, emission_coverage_complete FROM subagent_metrics WHERE session_id=?`, id).Scan(&calls, &emitted, &complete); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || emitted != 3 || complete != 1 {
		t.Fatalf("child measured executed=%d emitted=%d complete=%d", calls, emitted, complete)
	}
}

func TestChildEmissionAccumulatorKeepsCoverageAcrossQueuedLegs(t *testing.T) {
	first := accumulateEmissions(nil, 1, conversation.Result{ToolCallsEmitted: 5, ToolCallCoverageComplete: true})
	raw, err := json.Marshal(identity.SubagentRequest{AccEmissions: first, Leg: 2})
	if err != nil {
		t.Fatal(err)
	}
	var queued identity.SubagentRequest
	if err := json.Unmarshal(raw, &queued); err != nil {
		t.Fatal(err)
	}
	second := accumulateEmissions(queued.AccEmissions, queued.Leg, conversation.Result{ToolCallsEmitted: 3, ToolCallCoverageComplete: true})
	if *second != (store.ToolCallEmissions{Count: 8, Complete: true}) || first.Count != 5 {
		t.Fatalf("chain changed: %+v %+v", first, second)
	}
	for _, tc := range []struct {
		prior  *store.ToolCallEmissions
		result conversation.Result
		want   int
	}{
		{nil, conversation.Result{ToolCallsEmitted: 3, ToolCallCoverageComplete: true}, 3},
		{second, conversation.Result{}, 8},
		{&store.ToolCallEmissions{Count: 2}, conversation.Result{ToolCallsEmitted: 3, ToolCallCoverageComplete: true}, 5},
	} {
		got := accumulateEmissions(tc.prior, 2, tc.result)
		if got.Complete || got.Count != tc.want {
			t.Fatalf("unknown or interrupted leg became complete: %+v", got)
		}
	}
}

func TestDeliveredChildRecoveryMarksAnUnmeasuredFinalLegIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name    string
		started int
		metric  *store.ToolCallEmissions
		want    *int
	}{
		{"final_leg_interrupted", 2, &store.ToolCallEmissions{Count: 3, Complete: true}, new(0)},
		{"final_leg_measured", 1, &store.ToolCallEmissions{Count: 3, Complete: true}, new(1)},
		{"historical_unknown", 2, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := deliveringChild(t)
			a, st, _ := legFixture(t, srv.URL, AgencyConfig{SubagentMaxLegs: limits.Of(4)})
			const id = "ws_delivered_crash"
			if err := st.StartWorkSession(t.Context(), id, store.SubagentDescription("synthetic crash")); err != nil {
				t.Fatal(err)
			}
			if err := st.InsertSubagentMetric(store.SubagentMetric{SessionID: id, Calls: 2, Legs: 1, Emissions: tc.metric}); err != nil {
				t.Fatal(err)
			}
			if err := st.StartWorkSessionLeg(id, tc.started); err != nil {
				t.Fatal(err)
			}
			if err := st.DeliverWorkSession(t.Context(), id, "served: original result", store.EvidenceNotRun, ""); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(identity.SubagentRequest{Goal: "synthetic crash", Depth: 1, SessionID: id, Context: "folded", Leg: 2})
			if err != nil {
				t.Fatal(err)
			}
			if err := (&subagentHandler{a: a}).RunWork(t.Context(), &store.WorkItem{Payload: string(payload)}); err != nil {
				t.Fatal(err)
			}
			var executed int
			var emitted, complete *int
			if err := st.DB().QueryRow(`SELECT calls, emitted_calls, emission_coverage_complete FROM subagent_metrics WHERE session_id=?`, id).Scan(&executed, &emitted, &complete); err != nil {
				t.Fatal(err)
			}
			if executed != 2 || (tc.metric == nil && emitted != nil) || (tc.metric != nil && (emitted == nil || *emitted != 3)) {
				t.Fatalf("recovery changed retained counts: %d %v", executed, emitted)
			}
			if (complete == nil) != (tc.want == nil) || (complete != nil && *complete != *tc.want) {
				t.Fatalf("coverage=%v want=%v (started leg %d, measured leg 1)", complete, tc.want, tc.started)
			}
			ws, err := st.WorkSessionByID(id)
			if err != nil || ws == nil || ws.Result != "served: original result" || calls.Load() != 0 {
				t.Fatalf("recovery replayed or replaced delivery: session=%+v err=%v model_calls=%d", ws, err, calls.Load())
			}
		})
	}
}
