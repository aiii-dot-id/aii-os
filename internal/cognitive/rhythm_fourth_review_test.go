package cognitive

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// duringCall is a model during whose call something else happens: here the
// settings door or a reload putting new numbers in force (Reconfigure),
// which holds no gate a pass holds.
type duringCall struct {
	*mockLLM
	during func()
}

func (m *duringCall) ChatStructured(ctx context.Context, system, user string, tool wire.ToolDefinition) (string, string, bool, error) {
	text, id, via, err := m.mockLLM.ChatStructured(ctx, system, user, tool)
	if m.during != nil {
		m.during()
	}
	return text, id, via, err
}

func TestASettingChangedWhileAPassIsWithTheModelActsAtTheNextPass(t *testing.T) {
	pass := func(t *testing.T, experiences int, before, after ConsolidateConfig) (*mockStore, *mockLedger, *duringCall, *mockRingWriter, []Condition) {
		t.Helper()
		st := &mockStore{unprocessedCnt: experiences}
		for i := 1; i <= experiences; i++ {
			st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("the operator shipped at midnight, time %d", i), Raw: 1})
		}
		lg := &mockLedger{st: st}
		model := &duringCall{mockLLM: &mockLLM{override: overBudget(5, "e1")}}
		rw := &mockRingWriter{}
		c := NewConsolidate(st, model, lg, rw, before)
		model.during = func() { c.Reconfigure(after) }
		var said []Condition
		c.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }, Door: rememberingLedger{lg}, Exists: st.EntityExists})
		if err := c.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := c.cfg().MaxOps; after.MaxOps != 0 && got != after.MaxOps {
			t.Fatalf("rig: the change is in force once the pass is over: max_ops %d, want %d", got, after.MaxOps)
		}
		return st, lg, model, rw, said
	}

	t.Run("the limit raised over a pass of one", func(t *testing.T) {
		st, lg, model, _, said := pass(t, 1, ConsolidateConfig{Threshold: 1, MaxOps: 2}, ConsolidateConfig{Threshold: 1, MaxOps: 8})
		if !strings.Contains(model.lastSystem, "At most 2 operations") {
			t.Fatalf("rig: the prompt states the number the pass began under:\n%s", model.lastSystem)
		}
		last := said[len(said)-1]
		want := "the reply to a pass of one experience (e1) held 5 operations, and a pass mints at most 2 (cognition.consolidate_max_ops) — the 3 past that were not minted, and the experience is consumed"
		if upsertsIn(lg) != 2 || st.unprocessedCnt != 0 || last.Kind != ConditionRefused || last.Said != want {
			t.Fatalf("THE CARD SAYS THE OPERATIONS PAST 2 WERE NOT MINTED, AND THE PROMPT SAID SO: %d upsert(s) minted, %d still waiting, the card was last told %+v", upsertsIn(lg), st.unprocessedCnt, last)
		}
	})

	t.Run("the limit lowered over a pass of several", func(t *testing.T) {
		// What the same pass mints with nothing changing under it: not
		// every operation of a reply need mint (salience decides), so
		// the number is taken from the pass itself, not assumed.
		_, alone, _, _, _ := pass(t, 3, ConsolidateConfig{Threshold: 1, MaxOps: 8}, ConsolidateConfig{Threshold: 1, MaxOps: 8})
		if upsertsIn(alone) <= 2 {
			t.Fatalf("rig: a pass under a limit of 8 mints more than the 2 the limit is lowered to: %d", upsertsIn(alone))
		}
		st, lg, model, _, said := pass(t, 3, ConsolidateConfig{Threshold: 1, MaxOps: 8}, ConsolidateConfig{Threshold: 1, MaxOps: 2})
		if !strings.Contains(model.lastSystem, "At most 8 operations") {
			t.Fatalf("rig: the prompt states the number the pass began under:\n%s", model.lastSystem)
		}
		if upsertsIn(lg) != upsertsIn(alone) || st.unprocessedCnt != 0 {
			t.Fatalf("A REPLY OF 5 OPERATIONS UNDER A STATED LIMIT OF 8 MINTS WHAT IT MINTS WITH NO CHANGE UNDER IT (%d): %d upsert(s) minted, %d of 3 still waiting, the card was told %+v", upsertsIn(alone), upsertsIn(lg), st.unprocessedCnt, said)
		}
		for _, c := range said {
			if c.Kind == ConditionRefused || c.Kind == ConditionHalved {
				t.Fatalf("a pass that kept its own limit said a refusal: %+v", c)
			}
		}
	})

	// The view's bound is the pass's too: the prompt asked for a view
	// inside the bound the pass began under, and the reply is held to
	// that one, not to a bound that arrived while it was being written.
	t.Run("the view's bound lowered over a pass", func(t *testing.T) {
		_, lg, _, rw, _ := pass(t, 1, ConsolidateConfig{Threshold: 1, Ring3MaxChars: 4000}, ConsolidateConfig{Threshold: 1, Ring3MaxChars: 10})
		if got := rw.section(ring.Ring3, "working_truth"); upsertsIn(lg) == 0 || got != "You believe several things." {
			t.Fatalf("A VIEW INSIDE THE BOUND ITS PROMPT STATED WAS REFUSED BY A BOUND THAT CAME LATER: %d upsert(s), Ring 3 holds %q", upsertsIn(lg), got)
		}
	})
}

func TestNothingInsideAPassReadsTheConfigurationAgain(t *testing.T) {
	begins := map[string]string{
		"ConsolidateFacility.cfg":                  "the reader itself",
		"ConsolidateFacility.Reconfigure":          "the writer: the operator's numbers put in force",
		"ConsolidateFacility.Predicate":            "RHYTHM's probe",
		"ConsolidateFacility.Execute":              "a consolidation pass, and the render where none is due",
		"ConsolidateFacility.BeliefRetired":        "a retirement's render of Ring 3, outside any pass",
		"ConsolidateFacility.OutcomesPending":      "RHYTHM's probe of the outcome intake",
		"ConsolidateFacility.ObserveOutcomes":      "one outcome intake",
		"ConsolidateFacility.HeldForRetry":         "DREAM's question of what consolidation holds, outside any consolidation pass",
		"DreamFacility.cfg":                        "the reader itself",
		"DreamFacility.Reconfigure":                "the writer",
		"DreamFacility.Predicate":                  "RHYTHM's probe",
		"DreamFacility.ConversationPending":        "RHYTHM's probe of the conversation",
		"DreamFacility.Execute":                    "a DREAM pass",
		"DreamFacility.OnAlarm":                    "an alarm: its spacing, what is due, and the pass it runs",
		"MorningBriefFacility.Reconfigure":         "the writer, which also takes the brief away when what it writes is off",
		"MorningBriefFacility.Off":                 "the app's question when it arms the alarm; no pass asks it",
		"MorningBriefFacility.pass":                "one brief",
		"MorningBriefFacility.computeNextDeadline": "the next alarm, armed under the hour now set",
		"SelfModelFacility.Reconfigure":            "the writer",
		"SelfModelFacility.Execute":                "one portrait pass",
	}
	// calls: a facility's method that calls one of its own listed readers.
	calls := map[string]string{
		"ConsolidateFacility.OnAlarm -> Execute":                   "the alarm is the pass, and reads nothing itself",
		"ConsolidateFacility.ReconcileRing3 -> BeliefRetired":      "boot's reconciliation is a retirement's render, and reads nothing itself",
		"MorningBriefFacility.Execute -> pass":                     "the brief for a caller that is not the alarm; it reads nothing itself",
		"MorningBriefFacility.OnAlarm -> pass":                     "the brief itself; a failed call's wait comes back with it, from the pass's own reading",
		"MorningBriefFacility.OnAlarm -> computeNextDeadline":      "the next alarm is armed under the hour in force when the brief is done, not the hour it began under: the app re-arms on a change, and this must not put the old hour back",
		"MorningBriefFacility.NextDeadline -> computeNextDeadline": "the app's arming, which reads nothing itself",
		"SelfModelFacility.OnAlarm -> Execute":                     "the alarm is the pass, and reads nothing itself",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	reads, called := map[string]int{}, map[string]bool{}
	type method struct {
		owner, recv string
		fn          *ast.FuncDecl
	}
	var methods []method
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			where := "package level of " + name
			var body ast.Node = decl
			if fn, ok := decl.(*ast.FuncDecl); ok {
				where = "func " + fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) == 1 {
					recv := fn.Recv.List[0].Type
					if star, ok := recv.(*ast.StarExpr); ok {
						recv = star.X
					}
					if ident, ok := recv.(*ast.Ident); ok {
						where = ident.Name + "." + fn.Name.Name
						if names := fn.Recv.List[0].Names; len(names) == 1 {
							methods = append(methods, method{owner: ident.Name, recv: names[0].Name, fn: fn})
						}
					}
				}
			}
			// A reference is a reading only where it is called on the spot
			// (x.cfg(), x.config.Load(), x.config.Store(v), x.config.Swap(v)). Taken as a
			// value, or made inside a function literal, it can be called any
			// number of times from one place in the source, which a count of
			// places cannot hold: both are refused outright.
			direct := map[*ast.SelectorExpr]bool{}
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
					if fun.Sel.Name == "cfg" {
						direct[fun] = true
					} else if inner, ok := fun.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "config" && (fun.Sel.Name == "Load" || fun.Sel.Name == "Store" || fun.Sel.Name == "Swap") {
						direct[inner] = true
					}
				}
				return true
			})
			var literals []*ast.FuncLit
			ast.Inspect(body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FuncLit:
					literals = append(literals, x)
				case *ast.SelectorExpr:
					if x.Sel.Name != "cfg" && x.Sel.Name != "config" {
						return true
					}
					reads[where]++
					if !direct[x] {
						t.Errorf("%s takes a facility's configuration reader as a value (line %d): a value can be called again anywhere; call it where the pass begins and hand the reading on", where, fset.Position(x.Pos()).Line)
					}
					for _, lit := range literals {
						if lit.Pos() <= x.Pos() && x.End() <= lit.End() {
							t.Errorf("%s reads a facility's configuration inside a function literal (line %d): it runs as often as it is called; read where the pass begins and let the literal use that", where, fset.Position(x.Pos()).Line)
						}
					}
				}
				return true
			})
		}
	}
	for _, m := range methods {
		ast.Inspect(m.fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name == "cfg" {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == m.recv {
				if _, reader := begins[m.owner+"."+sel.Sel.Name]; reader {
					called[m.owner+"."+m.fn.Name.Name+" -> "+sel.Sel.Name] = true
				}
			}
			return true
		})
	}
	var names []string
	for name := range reads {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch what, listed := begins[name]; {
		case !listed:
			t.Errorf("%s refers to a facility's configuration and is not where a pass, a probe or a render begins: take the number from the caller, or list it here with what it is", name)
		case reads[name] != 1:
			t.Errorf("%s (%s) refers to the configuration %d times: once, and hand the copy on", name, what, reads[name])
		}
	}
	for name := range begins {
		if reads[name] == 0 {
			t.Errorf("%s is listed and refers to no configuration: the list names a reader that is gone", name)
		}
	}
	for pair := range called {
		if _, listed := calls[pair]; !listed {
			t.Errorf("%s: a facility's method calls one of its own readers of the configuration, so one pass reads it more than once: hand the reading on, or list the call here with what makes it a second act", pair)
		}
	}
	for pair := range calls {
		if !called[pair] {
			t.Errorf("%s is listed and is not in the source: the list names a call that is gone", pair)
		}
	}
	if len(reads) < 15 {
		t.Fatalf("rig: only %d function(s) referring to a configuration found in this package: %v", len(reads), names)
	}
}
