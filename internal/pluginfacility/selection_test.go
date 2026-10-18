package pluginfacility

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// choosingRuntime prepares the small component set, passing over the full.
type choosingRuntime struct{ *sharedRuntime }

func (c choosingRuntime) Prepare(ctx context.Context, ev Evidence) (Prepared, error) {
	p, err := c.sharedRuntime.Prepare(ctx, ev)
	p.Selection = &Selection{Variant: "small", Excluded: []Exclusion{{Variant: "full", Reasons: []string{"accelerator:cuda availability is unknown"}}}}
	return p, err
}

// WHY A SET WAS PASSED OVER IS THE FACILITY'S RECORD, not only a line the
// runtime logs: each activation carries the set it prepared and the
// runtime's reasons, so the card reads data, and every reader gets its own
// copy.
func TestTheChosenSetAndItsReasonsAreOnTheActivation(t *testing.T) {
	var mu sync.Mutex
	var log []string
	f := New(Config{Runtime: choosingRuntime{newSharedRuntime()}, Capacity: ampleCapacity{},
		Log: func(format string, args ...any) {
			mu.Lock()
			log = append(log, fmt.Sprintf(format, args...))
			mu.Unlock()
		}})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })

	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the package to activate", func() bool { return f.stateOf("id.example.one") == StateActive })
	var sel *Selection
	for _, v := range f.Snapshot().Instances {
		for _, a := range v.Activations {
			if a.Role == RoleActive {
				sel = a.Selection
			}
		}
	}
	if sel == nil || sel.Variant != "small" || len(sel.Excluded) != 1 || sel.Excluded[0].Variant != "full" || sel.Excluded[0].Reasons[0] != "accelerator:cuda availability is unknown" {
		t.Fatalf("the serving activation's selection is %+v", sel)
	}
	sel.Excluded[0].Reasons[0] = "scribbled"
	for _, v := range f.Snapshot().Instances {
		for _, a := range v.Activations {
			if a.Selection != nil && a.Selection.Excluded[0].Reasons[0] != "accelerator:cuda availability is unknown" {
				t.Fatal("a reader reached the facility's own record through its view")
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(log, "\n"), "prepared component set small") {
		t.Fatalf("the facility's sequence does not name the set:\n%s", strings.Join(log, "\n"))
	}
}

// judgedRefusal is a runtime's refusal that judged every component set and
// found none that fits.
type judgedRefusal struct{ sets []SetVerdict }

func (j judgedRefusal) Error() string        { return "no component set fits this host" }
func (j judgedRefusal) Judgment() *Selection { return &Selection{Sets: j.sets} }

// refusingRuntime judges the sets and refuses: none fits.
type refusingRuntime struct{ *sharedRuntime }

func (r refusingRuntime) Prepare(context.Context, Evidence) (Prepared, error) {
	return Prepared{}, judgedRefusal{sets: []SetVerdict{{Variant: "large", Storage: "needs 4.09 GB more on disk",
		Reasons: []Reason{{Words: "3.00 GB is free", Remedy: "free 3.09 GB on that volume"}}}}}
}

func TestAJudgmentWithNoSetThatFitsIsOnTheRefusedAttempt(t *testing.T) {
	f := New(Config{Runtime: refusingRuntime{newSharedRuntime()}, Capacity: ampleCapacity{}, Log: func(string, ...any) {}})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })

	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the attempt to refuse", func() bool { return f.stateOf("id.example.one") == StateRefused })
	var sel *Selection
	for _, v := range f.Snapshot().Instances {
		for _, a := range v.Activations {
			if a.Selection != nil {
				sel = a.Selection
			}
		}
	}
	if sel == nil || sel.Variant != "" || len(sel.Sets) != 1 || sel.Sets[0].Reasons[0].Remedy != "free 3.09 GB on that volume" {
		t.Fatalf("the refused attempt's judgment is %+v", sel)
	}
}
