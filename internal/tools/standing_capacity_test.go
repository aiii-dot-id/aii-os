package tools

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestDormantChoicesDoNotSpendActiveOfferCapacity(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var saved []StandingSeat
	r.SetStandingOffer(nil, func(s []StandingSeat) error { saved = s; return nil })
	var dormant []Tool
	for i := 0; i < 7; i++ {
		op := opWith("org.example.voice", fmt.Sprintf("op%d", i), nil)
		dormant = append(dormant, op)
		if err := r.RegisterDynamic(op, "org.example.voice"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Offer(op.Name()); err != nil {
			t.Fatal(err)
		}
		r.Deregister(op.Name())
	}
	before := slices.Clone(saved)
	for _, id := range []string{"org.example.memory", "org.example.planning"} {
		primaryPlugin(t, r, id)
		if _, err := r.OfferPrimary(id); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.Offered()) != 4 || len(saved) != 11 || !slices.Equal(saved[:7], before) {
		t.Fatalf("dormant choices changed or blocked active tools: offered=%v saved=%v", r.Offered(), saved)
	}
	active := r.Offered()
	for _, op := range dormant {
		if err := r.RegisterDynamic(op, "org.example.voice"); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.Offered()) != MaxOffered || !slices.Equal(r.Offered()[:4], active) || !slices.Equal(saved[:7], before) {
		t.Fatal("reactivation evicted a choice or exceeded active capacity")
	}
	for _, op := range dormant[4:] {
		state, _, why, ok := r.State(op.Name())
		if !ok || state != StateInspectOnly || !strings.Contains(why, "waiting for free offer space") {
			t.Fatalf("waiting choice was not explained: %s %s %v", state, why, ok)
		}
	}
	restarted := NewRegistry(t.TempDir(), nil, Timeouts{})
	restarted.SetStandingOffer(saved, nil)
	if !slices.Equal(restarted.standing, saved) {
		t.Fatal("restart discarded remembered choices")
	}
}

func TestRestoringChoicesDeduplicatesWithoutTruncation(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var seats []StandingSeat
	for i := 0; i < 12; i++ {
		seats = append(seats, StandingSeat{Name: fmt.Sprintf("pl_absent_%d", i), Print: fmt.Sprintf("fingerprint-%d", i)})
	}
	input := append(slices.Clone(seats), seats[0], StandingSeat{})
	r.SetStandingOffer(input, nil)
	if !slices.Equal(r.standing, seats) || len(r.Offered()) != 0 {
		t.Fatalf("restore changed choices: %v", r.standing)
	}
}

func TestWaitingChoiceTakesFreedActiveSpaceInStandingOrder(t *testing.T) {
	for _, free := range []string{"release", "plugin_release", "deregister", "changed_contract"} {
		t.Run(free, func(t *testing.T) {
			r := NewRegistry(t.TempDir(), nil, Timeouts{})
			var seats []StandingSeat
			var ops []Tool
			for i := 0; i < MaxOffered+2; i++ {
				op := opWith(fmt.Sprintf("org.example.p%d", i), "search", nil)
				ops = append(ops, op)
				seats = append(seats, StandingSeat{Name: op.Name(), Print: Fingerprint(op)})
			}
			r.SetStandingOffer(seats, nil)
			for i, op := range ops {
				if err := r.RegisterDynamic(op, fmt.Sprintf("org.example.p%d", i)); err != nil {
					t.Fatal(err)
				}
			}
			switch free {
			case "release":
				if _, err := r.Release(ops[0].Name()); err != nil {
					t.Fatal(err)
				}
			case "plugin_release":
				if err := r.ReleasePlugin("org.example.p0"); err != nil {
					t.Fatal(err)
				}
			case "deregister":
				r.Deregister(ops[0].Name())
			case "changed_contract":
				changed := opWith("org.example.p0", "search", func(d *Discovery) { d.Effects = "write.local" })
				if err := r.SupersedeOrigin("org.example.p0", []Tool{changed}, nil); err != nil {
					t.Fatal(err)
				}
			}
			active := r.Offered()
			if len(active) != MaxOffered || active[MaxOffered-1] != ops[MaxOffered].Name() || slices.Contains(active, ops[MaxOffered+1].Name()) {
				t.Fatalf("waiting choice did not take freed seat in standing order: %v", active)
			}
		})
	}
}

func TestRestoringChoicesRemovesOldActiveSeats(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := primaryPlugin(t, r, "org.example.notes")
	if _, err := r.OfferPrimary("org.example.notes"); err != nil {
		t.Fatal(err)
	}
	r.SetStandingOffer([]StandingSeat{{Name: names[1], Print: Fingerprint(r.tools[names[1]])}}, nil)
	if !slices.Equal(r.Offered(), names[1:2]) {
		t.Fatalf("restore retained removed active choice: %v", r.Offered())
	}
}

func TestMixedRetainedChoicesDoNotBlockFourNewOperations(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var prior []StandingSeat
	var current []Tool
	for i := 0; i < 7; i++ {
		op := opWith("org.example.voice", fmt.Sprintf("operation%d", i), nil)
		print := Fingerprint(op)
		if i >= 3 && i < 6 {
			print = Fingerprint(opWith("org.example.voice", fmt.Sprintf("operation%d", i), func(d *Discovery) { d.Effects = "write.local" }))
		}
		prior = append(prior, StandingSeat{Name: op.Name(), Print: print})
		if i < 6 {
			current = append(current, op)
			if err := r.RegisterDynamic(op, "org.example.voice"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var saved []StandingSeat
	r.SetStandingOffer(prior, func(s []StandingSeat) error { saved = s; return nil })
	if len(r.Offered()) != 3 || len(r.WithheldOffers()) != 3 {
		t.Fatal("mixed fixture must have three active, three stale and one absent choice")
	}
	for _, op := range current {
		if !r.ToolEnabled(op.Name()) {
			t.Fatal("mixed fixture must have no operator-disabled operations")
		}
	}
	for _, id := range []string{"org.example.memory", "org.example.planning"} {
		primaryPlugin(t, r, id)
		if _, err := r.OfferPrimary(id); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.Offered()) != 7 || len(saved) != 11 || !slices.Equal(saved[:7], prior) {
		t.Fatalf("retained choices blocked or changed: active=%v saved=%v", r.Offered(), saved)
	}
	if len(r.WithheldOffers()) != 3 {
		t.Fatal("new selection cleared stale-contract withholding")
	}
	restarted := NewRegistry(t.TempDir(), nil, Timeouts{})
	restarted.SetStandingOffer(saved, nil)
	for _, op := range current {
		if err := restarted.RegisterDynamic(op, "org.example.voice"); err != nil {
			t.Fatal(err)
		}
	}
	primaryPlugin(t, restarted, "org.example.memory")
	primaryPlugin(t, restarted, "org.example.planning")
	if len(restarted.Offered()) != 7 || len(restarted.WithheldOffers()) != 3 || !slices.Equal(restarted.standing, saved) {
		t.Fatal("restart changed mixed retained choices")
	}
}
