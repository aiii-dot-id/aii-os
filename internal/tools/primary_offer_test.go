package tools

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func primaryPlugin(t *testing.T, r *Registry, id string) []string {
	t.Helper()
	var names []string
	for _, op := range []string{"search", "store", "legacy"} {
		tool := opWith(id, op, func(d *Discovery) { d.Primary = op != "legacy" })
		if err := r.RegisterDynamic(tool, id); err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name())
	}
	return names
}

func TestPrimarySelectionIsExplicitAtomicAndPersistent(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var saved []StandingSeat
	r.SetStandingOffer(nil, func(s []StandingSeat) error { saved = s; return nil })
	names := primaryPlugin(t, r, "org.example.notes")
	if len(r.Offered()) != 0 {
		t.Fatal("descriptor self-promoted into prompt")
	}
	if card, err := r.Show(names[0]); err != nil || !card.Primary {
		t.Fatalf("primary hint missing: %+v %v", card, err)
	}
	if hits := r.Search("search", 0); len(hits) != 1 || !hits[0].Primary {
		t.Fatalf("search lost primary hint: %+v", hits)
	}
	got, err := r.OfferPrimary("org.example.notes")
	if err != nil || !slices.Equal(got, names[:2]) || !slices.Equal(r.Offered(), names[:2]) || len(saved) != 2 {
		t.Fatalf("selection: %v %v saved=%v", got, err, saved)
	}
	if _, err := r.OfferPrimary("org.example.notes"); err != nil || len(r.Offered()) != 2 {
		t.Fatal("selection is not idempotent", err)
	}
	if _, err := r.Release(names[0]); err != nil {
		t.Fatal(err)
	}
	r.Deregister(names[0])
	tool := opWith("org.example.notes", "search", func(d *Discovery) { d.Primary = true })
	if err := r.RegisterDynamic(tool, "org.example.notes"); err != nil {
		t.Fatal(err)
	}
	if contains(r.Offered(), names[0]) {
		t.Fatal("registration overrode explicit release")
	}
	restored := NewRegistry(t.TempDir(), nil, Timeouts{})
	restored.SetStandingOffer(saved, nil)
	primaryPlugin(t, restored, "org.example.notes")
	if !slices.Equal(restored.Offered(), names[1:2]) {
		t.Fatalf("restart changed choice: %v", restored.Offered())
	}
	// Hints change discoverability, not the contract a standing choice promised.
	unhinted := opWith("org.example.notes", "store", nil)
	if Fingerprint(unhinted) != saved[0].Print {
		t.Fatal("primary hint changed operation authority fingerprint")
	}
}

func TestPrimarySelectionDoesNotEvictOrPartiallyOffer(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(fmt.Sprint("absent_", absent), func(t *testing.T) {
			r := NewRegistry(t.TempDir(), nil, Timeouts{})
			var saved []StandingSeat
			r.SetStandingOffer(nil, func(s []StandingSeat) error { saved = s; return nil })
			for i := 0; i < MaxOffered-1; i++ {
				n := registerPlugin(t, r, fmt.Sprintf("org.example.p%d", i), "one")[0]
				if _, err := r.Offer(n); err != nil {
					t.Fatal(err)
				}
				if absent {
					r.Deregister(n)
				}
			}
			before := slices.Clone(saved)
			active := r.Offered()
			primaryPlugin(t, r, "org.example.notes")
			_, err := r.OfferPrimary("org.example.notes")
			if absent {
				if err != nil || len(saved) != len(before)+2 || !slices.Equal(saved[:len(before)], before) || len(r.Offered()) != 2 {
					t.Fatalf("dormant choices blocked or changed by selection: %v", err)
				}
			} else if err == nil || !reflect.DeepEqual(saved, before) || !slices.Equal(active, r.Offered()) {
				t.Fatal("full active offer did not refuse the batch unchanged")
			}
		})
	}
}

func TestPrimarySelectionKeepsHiddenHostAndAmbiguousOperationsOut(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := primaryPlugin(t, r, "org.example.notes")
	other := primaryPlugin(t, r, "org.example.rival")
	if _, err := r.Offer("search"); err == nil {
		t.Fatal("ambiguous single-op offer guessed a plugin")
	}
	host := opWith("org.example.notes", "host", func(d *Discovery) { d.Primary = true })
	if err := r.RegisterHostOp(host, "org.example.notes"); err != nil {
		t.Fatal(err)
	}
	r.SetToolEnabled(names[1], false)
	if _, err := r.OfferPrimary("org.example.notes"); err == nil || len(r.Offered()) != 0 {
		t.Fatal("disabled member did not refuse whole batch")
	}
	r.SetToolEnabled(names[1], true)
	safe := true
	r.SetSafeSource(func() (string, bool) { return "fixture", safe })
	if _, err := r.OfferPrimary("org.example.notes"); err == nil {
		t.Fatal("SAFE allowed primary selection")
	}
	safe = false
	if _, err := r.OfferPrimary("org.example.notes"); err != nil {
		t.Fatal(err)
	}
	if contains(r.PromptNames(), host.Name()) || contains(r.PromptNames(), other[0]) {
		t.Fatal("selection escaped exact plugin non-host boundary")
	}
	r.SetToolEnabled(names[1], false)
	if contains(r.PromptNames(), names[1]) {
		t.Fatal("disabled primary remained in provider schema")
	}
	safe = true
	if contains(r.PromptNames(), names[0]) || len(r.Brief().OfferedNames) != 0 {
		t.Fatal("SAFE primary remained advertised as callable")
	}
	safe = false
	r.SetToolEnabled(names[1], true)
	if !contains(r.PromptNames(), names[1]) {
		t.Fatal("availability change erased standing choice")
	}
}

func TestConcurrentPrimarySelectionsCannotOverfillTheStandingOffer(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	for _, id := range []string{"org.example.a", "org.example.b"} {
		for i := 0; i < 5; i++ {
			op := opWith(id, fmt.Sprintf("op%d", i), func(d *Discovery) { d.Primary = true })
			if err := r.RegisterDynamic(op, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"org.example.a", "org.example.b"} {
		go func(id string) { <-start; _, err := r.OfferPrimary(id); results <- err }(id)
	}
	close(start)
	failures := 0
	for range 2 {
		if <-results != nil {
			failures++
		}
	}
	if failures != 1 || len(r.Offered()) != 5 || len(r.standing) != 5 {
		t.Fatalf("partial or overflowing batches: failed=%d offered=%v standing=%v", failures, r.Offered(), r.standing)
	}
}
