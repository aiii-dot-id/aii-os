//go:build !android && !ios

package app

// THE KIT AND THE HOST, HELD TOGETHER. The plugin SDK is a separate
// module: its own tests run a guest against a stand-in host, the host's
// run its own fixtures, and until this suite no plugin the SDK built had
// ever run in an App. examples/compat, built from the pinned SDK commit
// (test/sdk-compat.rev, test/run_sdk_compat.sh), is installed in a real
// identity on the supervised lane and walked through what an author's
// plugin meets: the operator's grant, the record's lifetime, a
// correction, the history it may read, SAFE, a call stopped, a restart
// and an update.
//
// This file asserts on three things only: what the plugin returned, the
// reason code its call carried, and what the store holds. Everything that
// drives the App is in sdk_compat_harness_test.go, which a change to the
// runtime may edit; a change that needs THIS file edited has changed
// what a plugin author can rely on.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// The texts are far apart, so the record's own judgement of similarity
// never folds one into another: every remember below creates, except the
// one that names what it corrects. The correction shares nothing with the
// memory it corrects, so only its supersedes can make it an update.
const (
	compatFirst    = "The ledger is the only truth about who an identity is."
	compatOld      = "Plugins reach the record through four rings of authority."
	compatNew      = "Correction: each call crosses rings zero to five before it lands."
	compatTurnText = "A scripted turn asked for this memory to be kept."
	compatSpin     = "Spinning until the host stops this call."
	compatSpinTurn = "Inside a turn, spinning until the operator presses stop."
	compatSaid     = "An earlier message the plugin may read back."
	compatPrivate  = "A note to a peer that must never reach a plugin."
	compatTemp     = "An unsigned plugin's memory lives as long as its activation."
)

type compatRemembered struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Of      string `json:"of"`
	Scope   string `json:"scope"`
}

type compatRecall struct {
	Status string `json:"status"`
	Hits   []struct {
		ID           string `json:"id"`
		Text         string `json:"text"`
		SupersededBy string `json:"superseded_by"`
	} `json:"hits"`
}

type compatPage struct {
	Rows []struct {
		ID         string `json:"id"`
		Kind       string `json:"kind"`
		Role       string `json:"role"`
		Content    string `json:"content"`
		Outcome    string `json:"outcome"`
		Tool       string `json:"tool"`
		Model      string `json:"model"`
		DurationMS *int64 `json:"duration_ms"`
	} `json:"rows"`
	NextCursor   string `json:"next_cursor"`
	Lost         uint64 `json:"lost"`
	Availability string `json:"availability"`
}

// compatResult decodes what the plugin returned, failing on a refusal.
func compatResult(t *testing.T, op, out, code string, v any) {
	t.Helper()
	if code != "" {
		t.Fatalf("%s was refused (%s): %s", op, code, out)
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("%s did not return the plugin's result: %v: %s", op, err, out)
	}
}

func compatRemember(t *testing.T, h *compatHost, args map[string]any) compatRemembered {
	t.Helper()
	var r compatRemembered
	out, code := h.call(t, "remember", args)
	compatResult(t, "remember", out, code, &r)
	return r
}

func compatRecallID(t *testing.T, h *compatHost, id string) compatRecall {
	t.Helper()
	var r compatRecall
	out, code := h.call(t, "recall", map[string]any{"id": id})
	compatResult(t, "recall", out, code, &r)
	return r
}

func TestSDKCompat(t *testing.T) {
	b := sdkCompatBuild(t)
	t.Run("T1", func(t *testing.T) { compatPublisherProven(t, b) })
	t.Run("T0", func(t *testing.T) { compatUnsigned(t, b) })
}

// compatPublisherProven is the dev-signed plugin's life on one identity.
func compatPublisherProven(t *testing.T, b compatBuild) {
	h := startCompatHost(t, b, "T1")
	var first, old, current string

	t.Run("grants", func(t *testing.T) {
		for _, c := range []struct {
			op   string
			args map[string]any
		}{
			{"remember", map[string]any{"text": compatFirst}},
			{"recall", map[string]any{"query": "ledger"}},
			{"interactions", nil},
			{"overreach", nil},
			{"spin", map[string]any{"text": compatSpin}},
		} {
			if out, code := h.call(t, c.op, c.args); code != "POLICY_DENY" {
				t.Errorf("ungranted, %s must be denied by the host, got %q: %s", c.op, code, out)
			}
		}
		if n := len(h.memories(t)); n != 0 {
			t.Fatalf("denied calls left %d memories", n)
		}
		// Each grant is its own: history read does not admit a memory.
		h.grant(t, "interactions")
		var p compatPage
		out, code := h.call(t, "interactions", nil)
		compatResult(t, "interactions", out, code, &p)
		if out, code := h.call(t, "remember", map[string]any{"text": compatFirst}); code != "POLICY_DENY" {
			t.Fatalf("the interactions grant admitted a memory, got %q: %s", code, out)
		}
		h.grant(t, "memory")
		r := compatRemember(t, h, map[string]any{"text": compatFirst})
		if r.Outcome != "created" || r.ID == "" {
			t.Fatalf("the grant admits the next call: %+v", r)
		}
		first = r.ID
		if out, code := h.call(t, "overreach", nil); code != "POLICY_DENY" {
			t.Errorf("an operation that declared a read may not write, granted or not, got %q: %s", code, out)
		}
		if n := len(h.memories(t)); n != 1 {
			t.Fatalf("the record holds %d memories, want the one admitted", n)
		}
	})

	t.Run("memory", func(t *testing.T) {
		r := compatRemember(t, h, map[string]any{"text": compatOld})
		if r.Outcome != "created" || r.Scope != "persistent" {
			t.Fatalf("a publisher-proven plugin's memory is persistent: %+v", r)
		}
		old = r.ID
		u := compatRemember(t, h, map[string]any{"text": compatNew, "supersedes": old})
		if u.Outcome != "updated" || u.Of != old || u.Scope != "persistent" || u.ID == old {
			t.Fatalf("a supersede is an update of the memory it names: %+v", u)
		}
		current = u.ID
		got := compatRecallID(t, h, old)
		if got.Status != "found" || len(got.Hits) != 1 || got.Hits[0].SupersededBy != current || got.Hits[0].Text != compatOld {
			t.Fatalf("the corrected memory reads by id, naming its successor: %+v", got)
		}
		if m, ok := h.memory(t, old); !ok || m.supersededBy != current || m.temp {
			t.Fatalf("the store holds the corrected memory, superseded and persistent: %+v %v", m, ok)
		}
	})

	t.Run("interactions", func(t *testing.T) {
		h.seedHistory(t, compatSaid, 55, compatPrivate)
		if reply := h.turn(t, "SDKCOMPATTURN", "remember", map[string]any{"text": compatTurnText}, "Kept."); !strings.Contains(reply, "Kept.") {
			t.Fatalf("the scripted turn ends with its answer: %q", reply)
		}
		if h.memoryByText(t, compatTurnText) == "" {
			t.Fatal("the turn's call reached the record")
		}
		seen := map[string]bool{}
		said := 0
		var call, result bool
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > 10 {
				t.Fatal("the history never ended")
			}
			args := map[string]any{}
			if cursor != "" {
				args["cursor"] = cursor
			}
			var p compatPage
			out, code := h.call(t, "interactions", args)
			compatResult(t, "interactions", out, code, &p)
			if len(p.Rows) > 50 {
				t.Fatalf("a page holds %d rows, over 50", len(p.Rows))
			}
			if p.Lost != 0 || p.Availability != "complete" {
				t.Errorf("the recorded history is whole: lost %d, availability %q", p.Lost, p.Availability)
			}
			for _, r := range p.Rows {
				if seen[r.ID] {
					t.Fatalf("%s appeared twice", r.ID)
				}
				seen[r.ID] = true
				if strings.Contains(r.Content, compatPrivate) {
					t.Fatal("a message to a peer reached the plugin")
				}
				if r.Content == compatSaid {
					said++
				}
				if r.Tool == compatTool("remember") || r.Tool == "tools" && strings.Contains(r.Content, compatTool("remember")) {
					switch r.Kind {
					case "tool_call":
						call = r.Model == "m"
					case "tool_result":
						result = r.Model == "m" && r.DurationMS != nil && r.Outcome == "succeeded"
					}
				}
			}
			if cursor = p.NextCursor; cursor == "" {
				break
			}
		}
		if said != 55 {
			t.Errorf("the 55 seeded messages each appear once, found %d", said)
		}
		if !call || !result {
			t.Errorf("the turn's call and result carry the model, the duration and the outcome: call %v, result %v", call, result)
		}
		turn := h.lastTurn(t)
		u := turn.usage
		if turn.provider.String != "test" || turn.model.String != "m" || !turn.known || u.Calls != 2 ||
			u.PromptTokens != 200 || u.CompletionTokens != 15 || u.TotalTokens != 215 || u.UnknownAttempts != 0 || u.Silent != 0 {
			t.Errorf("the turn's route and usage are what the provider reported: %+v %+v", turn, u)
		}
	})

	t.Run("safe", func(t *testing.T) {
		before := h.memories(t)
		h.enterSAFE()
		for op, args := range map[string]map[string]any{
			"remember": {"text": "written in SAFE"},
			"recall":   {"query": "rings"},
		} {
			if out, code := h.call(t, op, args); code != tools.ReasonSafeSuspended {
				t.Errorf("under SAFE %s is suspended, got %q: %s", op, code, out)
			}
		}
		if after := h.memories(t); !reflect.DeepEqual(after, before) {
			t.Fatalf("the record changed under SAFE:\nbefore %+v\nafter  %+v", before, after)
		}
		h.leaveSAFE()
		if got := compatRecallID(t, h, current); got.Status != "found" || len(got.Hits) != 1 {
			t.Fatalf("after SAFE the call works: %+v", got)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		err := h.callCancelled(t, "spin", map[string]any{"text": compatSpin}, compatSpin)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled spin ends cancelled: %v", err)
		}
		if got := compatRecallID(t, h, first); got.Status != "found" {
			t.Fatalf("the next call answers: %+v", got)
		}
		if n := h.restarts(t); n != 0 {
			t.Fatalf("a cancel is not a crash: %d restarts", n)
		}

		found, err := h.turnCancelled(t, "SDKCOMPATSPIN", "spin", map[string]any{"text": compatSpinTurn}, compatSpinTurn)
		if !found || !errors.Is(err, context.Canceled) {
			t.Fatalf("the operator's stop ends the turn cancelled: found %v, err %v", found, err)
		}

		rows := h.toolRows(t, "spin")
		if len(rows) != 2 || rows[0].kind != "tool_call" || rows[1].kind != "tool_result" || rows[1].outcome != "cancelled" || rows[1].reasonCode != "" {
			t.Errorf("the stopped call's record: %+v", rows)
		}
		turn := h.lastTurn(t)
		if u := turn.usage; !turn.known || u.Calls != 1 || u.Silent != 0 || u.PromptTokens != 100 || u.CompletionTokens != 10 || u.TotalTokens != 110 {
			t.Errorf("the stopped turn's row counts the calls it made: %+v", turn)
		}
		if n := h.restarts(t); n != 0 {
			t.Fatalf("a stopped turn is not a crash: %d restarts", n)
		}
	})

	holds := func(t *testing.T, text string) {
		t.Helper()
		if got := compatRecallID(t, h, current); got.Status != "found" || len(got.Hits) != 1 || got.Hits[0].Text != compatNew || got.Hits[0].SupersededBy != "" {
			t.Errorf("the correction holds: %+v", got)
		}
		if got := compatRecallID(t, h, old); got.Status != "found" || len(got.Hits) != 1 || got.Hits[0].SupersededBy != current {
			t.Errorf("the corrected memory still names its successor: %+v", got)
		}
		if got := compatRecallID(t, h, first); got.Status != "found" || len(got.Hits) != 1 || got.Hits[0].Text != compatFirst {
			t.Errorf("the first memory holds: %+v", got)
		}
		if r := compatRemember(t, h, map[string]any{"text": text}); r.Outcome != "created" || r.Scope != "persistent" {
			t.Errorf("the grant holds: %+v", r)
		}
		if out, code := h.call(t, "overreach", nil); code != "POLICY_DENY" {
			t.Errorf("and the wall still stands: %q %s", code, out)
		}
		if !h.callable("remember") || !h.callable("spin") {
			t.Errorf("the operations stay callable: remember %v, spin %v", h.callable("remember"), h.callable("spin"))
		}
	}

	t.Run("restart", func(t *testing.T) {
		h.restart(t)
		holds(t, "After a restart the host came back with this record intact.")
	})

	t.Run("update", func(t *testing.T) {
		h.update(t)
		if v := h.release(); v != "0.2.0" {
			t.Fatalf("the update serves 0.2.0, got %q", v)
		}
		holds(t, "Version two writes into the same persistent plugin store.")
	})
}

// compatUnsigned is the unsigned plugin's: its memory is temporary, a
// crash of its child keeps it, and the end of its activation does not.
func compatUnsigned(t *testing.T, b compatBuild) {
	h := startCompatHost(t, b, "T0")
	var id string

	t.Run("temp", func(t *testing.T) {
		h.grant(t, "memory")
		r := compatRemember(t, h, map[string]any{"text": compatTemp})
		if r.Outcome != "created" || r.Scope != "temp" {
			t.Fatalf("an unsigned plugin's memory is temporary: %+v", r)
		}
		id = r.ID
		if m, ok := h.memory(t, id); !ok || !m.temp {
			t.Fatalf("the store holds it as temporary: %+v %v", m, ok)
		}
	})

	t.Run("kill", func(t *testing.T) {
		h.killChild(t)
		if n := h.restarts(t); n < 1 {
			t.Fatalf("a killed child is a crash, and counted: %d restarts", n)
		}
		if got := compatRecallID(t, h, id); got.Status != "found" || len(got.Hits) != 1 || got.Hits[0].Text != compatTemp {
			t.Fatalf("the activation outlives its child, and its memory with it: %+v", got)
		}
	})

	t.Run("restart", func(t *testing.T) {
		h.restart(t)
		if got := compatRecallID(t, h, id); got.Status != "found_nothing" || len(got.Hits) != 0 {
			t.Fatalf("a new activation does not inherit temporary memory: %+v", got)
		}
		if m, ok := h.memory(t, id); ok {
			t.Fatalf("the store still holds the dead activation's memory: %+v", m)
		}
	})
}
