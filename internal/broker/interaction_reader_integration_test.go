//go:build !windows

package broker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"os"
	"strings"
	"testing"
	"time"
)

// A separately built SDK fixture crosses stdio and calls the real broker. The
// test imports no SDK and derives expected occurrences from its own inputs.
func TestInteractionIndependentSDKReader(t *testing.T) {
	testInteractionSDKReader(t, os.Getenv("AII_INTERACTION_READER"), false)
}
func TestInteractionIndependentSDKWasmReader(t *testing.T) {
	testInteractionSDKReader(t, os.Getenv("AII_INTERACTION_READER_WASM"), true)
}

type interactionFixtureChild struct {
	invoke func(context.Context, []byte) ([]byte, error)
	close  func()
}

func testInteractionSDKReader(t *testing.T, bin string, wasm bool) {
	if bin == "" {
		t.Skip("integration: build SDK examples/interaction-reader and set AII_INTERACTION_READER")
	}
	st := newStore(t)
	for i := 0; i < 55; i++ {
		if err := st.AddConversationTurn("operator", "same text"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StartWorkSession(t.Context(), "work", "known task"); err != nil {
		t.Fatal(err)
	}
	plan := "before result"
	if err := st.UpdateWorkPlan(t.Context(), "work", nil, nil, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.DeliverWorkSession(t.Context(), "work", "served: done", "locally_verified", "readback"); err != nil {
		t.Fatal(err)
	}
	plan = "after result"
	if err := st.UpdateWorkPlan(t.Context(), "work", nil, nil, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordInteraction(t.Context(), interaction.Input{ID: "project-criteria", Kind: interaction.Notice, Role: interaction.System, ProjectID: "project", Details: interaction.Details{Project: &interaction.ProjectChange{After: interaction.ProjectContract{Acceptance: []string{"observable success"}}}}}); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("😀", 20000)
	if err := st.AddConversationTurn("resident", large); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOutboxMessage("private", "peer", "other", "must not leak", nil); err != nil {
		t.Fatal(err)
	}
	h := newHost(t, st, Config{Interactions: st, Grants: map[string]Grant{"org.example.interaction-reader": {Interactions: true, ReadOnly: true}}})
	tier := packagefmt.TierT3
	if wasm {
		tier = packagefmt.TierT1
	}
	b := h.Bind("org.example.interaction-reader", tier, []string{capInteractionRead})
	b.BeginOperation(OperationScope{Operation: "reader.query", Declared: true, Effects: EffectsReadInternal, Capabilities: []string{capInteractionRead}})
	launch := func() *interactionFixtureChild {
		if wasm {
			raw, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			module, err := pluginworker.Load(t.Context(), raw, pluginworker.Config{Dispatcher: b})
			if err != nil {
				t.Fatal(err)
			}
			child := &interactionFixtureChild{invoke: module.Invoke, close: func() { module.Close(context.Background()) }}
			t.Cleanup(child.close)
			return child
		}

		s, err := supervisor.Start(supervisor.Spec{PluginID: "org.example.interaction-reader", Argv: []string{bin}, ReadyMark: "child-ready"}, b)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return &interactionFixtureChild{invoke: s.Invoke, close: func() { s.Close() }}
	}
	child := launch()
	invoke := func(op string, args any) map[string]json.RawMessage {
		raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "fixture", "method": "invoke.call", "params": map[string]any{"operation": op, "arguments": args}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		reply, err := child.invoke(ctx, raw)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Result struct {
				Status string                     `json:"status"`
				Data   map[string]json.RawMessage `json:"operation_result"`
			} `json:"result"`
		}
		if err = json.Unmarshal(reply, &envelope); err != nil || envelope.Result.Status != "succeeded" {
			t.Fatalf("SDK reply: %s (%v)", reply, err)
		}
		return envelope.Result.Data
	}
	seen := map[string]bool{}
	cursor := ""
	var full *interaction.ContentRef
	delivered := false
	plans := map[string]bool{}
	criteria := false
	for {
		m := invoke("reader.query", map[string]any{"cursor": cursor})
		var rows []struct {
			ID, Kind, Content, Result, Evidence, Plan string
			Criteria                                  []string                `json:"criteria"`
			Readback                                  string                  `json:"evidence_readback"`
			Ref                                       *interaction.ContentRef `json:"content_ref"`
		}
		if err := json.Unmarshal(m["rows"], &rows); err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Fatal("duplicate occurrence", r.ID)
			}
			seen[r.ID] = true
			if r.Plan != "" {
				plans[r.Plan] = true
			}
			if len(r.Criteria) == 1 && r.Criteria[0] == "observable success" {
				criteria = true
			}
			if r.Content == "must not leak" {
				t.Fatal("audience leak")
			}
			if r.Ref != nil {
				full = r.Ref
			}
			if r.Result == "served: done" {
				delivered = r.Evidence == "locally_verified" && r.Readback == "readback"
			}
		}
		if err := json.Unmarshal(m["next_cursor"], &cursor); err != nil {
			t.Fatal(err)
		}
		if cursor == "" {
			break
		}
	}
	if len(seen) != 61 || !delivered || full == nil || !plans["before result"] || !plans["after result"] || !criteria {
		t.Fatalf("coverage rows=%d delivery=%v full=%v", len(seen), delivered, full)
	}
	// Restart loses no source facts and does not reset the host source incarnation.
	child.close()
	child = launch()
	var all []byte
	for {
		m := invoke("reader.read", interaction.ReadRequest{Version: 1, ID: full.ID, Source: full.Source, Incarnation: full.Incarnation, SHA256: full.SHA256, Offset: int64(len(all)), Length: 16384})
		var encoded string
		var eof bool
		json.Unmarshal(m["data_b64"], &encoded)
		json.Unmarshal(m["eof"], &eof)
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(data) == 0 {
			t.Fatal("bad range", err)
		}
		all = append(all, data...)
		if eof {
			break
		}
	}
	var r interaction.Record
	if err := json.Unmarshal(all, &r); err != nil {
		t.Fatal(err)
	}
	if r.Content != large || !bytes.Equal([]byte(interaction.Hash(all)), []byte(full.SHA256)) {
		t.Fatal("full detail changed across plugin restart")
	}
}
