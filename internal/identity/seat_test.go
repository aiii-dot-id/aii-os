package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAWorkerSeatMayNotArchiveOrSupersedeARing2Belief(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	primary := context.Background()
	worker := context.WithValue(primary, SubagentWorkSession{}, "ws_worker")
	commit := func(ctx context.Context, args map[string]any) error {
		t.Helper()
		_, err := engine.ExecuteAction(ctx, "verb", "commit", args)
		return err
	}
	for _, b := range []map[string]any{
		{"variant": "belief.upsert", "id": "b_who", "statement": "I keep my word", "evidence_refs": "exp_test_origin"},
		{"variant": "belief.upsert", "id": "b_passing", "statement": "The build is slow on Mondays", "evidence_refs": "exp_test_origin"},
		{"variant": "belief.upsert", "id": "b_next", "statement": "I keep my word, and say when I cannot", "evidence_refs": "exp_test_origin"},
	} {
		if err := commit(primary, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.RecordConversationTurn("operator", "yes, that held up"); err != nil {
		t.Fatal(err)
	}
	opTurn, err := st.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("operator turn unavailable: %+v %v", opTurn, err)
	}
	for _, note := range []map[string]any{
		{"content": "I watched it hold", "supports": "b_who"},
		{"content": "the operator confirmed it", "source_turn": opTurn.TurnSeq, "supports": "b_who"},
		{"content": "a second experience showed it", "supports": "b_who"},
	} {
		if _, err := engine.ExecuteAction(primary, "verb", "note", note); err != nil {
			t.Fatal(err)
		}
	}
	if err := commit(primary, map[string]any{"variant": "belief.promote", "id": "b_who", "ring": 2}); err != nil {
		t.Fatal(err)
	}

	before := lg.LastSeq()
	for _, args := range []map[string]any{
		{"variant": "belief.archive", "id": "b_who"},
		{"variant": "belief.supersede", "old_id": "b_who", "new_id": "b_next", "reason": "tidying duplicates"},
	} {
		err := commit(worker, args)
		var seat *SeatError
		if !errors.As(err, &seat) || !strings.Contains(err.Error(), "a worker seat cannot archive or supersede a Ring 2 belief") {
			t.Fatalf("a worker's %s of a Ring 2 belief: %v", args["variant"], err)
		}
	}
	if lg.LastSeq() != before {
		t.Fatalf("a refused removal appended a record: %d -> %d", before, lg.LastSeq())
	}
	if b, err := st.GetBelief("b_who"); err != nil || b.Ring != 2 {
		t.Fatalf("the Ring 2 belief changed: %+v %v", b, err)
	}
	if err := commit(worker, map[string]any{"variant": "belief.archive", "id": "b_passing"}); err != nil {
		t.Fatalf("a worker could not archive a Ring 3 belief: %v", err)
	}
	// A belief that is not on record is the verb's to name, not the seat's
	// (store.ErrBeliefNotFound, read without database/sql).
	err = commit(worker, map[string]any{"variant": "belief.archive", "id": "b_absent"})
	var seat *SeatError
	if err == nil || errors.As(err, &seat) || strings.Contains(err.Error(), "cannot be read to tell") {
		t.Fatalf("a worker's archive of a belief not on record must be the verb's refusal, not the seat's: %v", err)
	}
	if err := commit(primary, map[string]any{"variant": "belief.supersede", "old_id": "b_who", "new_id": "b_next", "reason": "said better"}); err != nil {
		t.Fatalf("the primary seat could not supersede its Ring 2 belief: %v", err)
	}
}

func TestWhatAWorkerSeatMayCallIsOneClosedTable(t *testing.T) {
	advertised := map[string]bool{}
	for _, v := range Verbs() {
		props, _ := v.Params["properties"].(map[string]any)
		key := map[string]string{"work": "action", "commit": "variant"}[v.Name]
		if key == "" {
			advertised[v.Name] = true
			continue
		}
		param, _ := props[key].(map[string]any)
		values, _ := param["enum"].([]string)
		if len(values) == 0 {
			t.Fatalf("%s advertises no %s values to read", v.Name, key)
		}
		for _, value := range values {
			advertised[v.Name+" "+value] = true
		}
	}
	for op := range advertised {
		if _, ok := workerSeatTable[op]; !ok {
			t.Errorf("%s is advertised and has no row in the worker seat's table", op)
		}
	}
	for op := range workerSeatTable {
		// belief.attest, removed and no longer offered: the verb refuses
		// it in its own words.
		if !advertised[op] && op != "commit belief.attest" {
			t.Errorf("the worker seat's table has a row for %s, which no facade advertises", op)
		}
	}

	engine, _, _, _, _ := setupEngine(t)
	worker := context.WithValue(context.Background(), SubagentWorkSession{}, "ws_worker")
	for _, c := range []struct {
		verb string
		args map[string]any
		op   string
	}{
		{"timer", map[string]any{"action": "set"}, "timer"},
		{"work", map[string]any{"action": "alarm.list"}, "work alarm.list"},
		{"commit", map[string]any{}, "commit "},
	} {
		_, err := engine.ExecuteAction(worker, "verb", c.verb, c.args)
		var seat *SeatError
		if !errors.As(err, &seat) || seat.Operation != c.op || !strings.Contains(err.Error(), "is not among the operations a worker seat may call") {
			t.Errorf("a worker calling %s: %v", c.op, err)
		}
	}
	// The same calls from the primary seat reach their verbs.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]any{"action": "alarm.list"}); err == nil || strings.Contains(err.Error(), "worker seat") {
		t.Errorf("the primary seat's call met the worker's table: %v", err)
	}
}
