package tools

import (
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func seatNames(seats []StandingSeat) []string {
	var out []string
	for _, s := range seats {
		out = append(out, s.Name)
	}
	return out
}

// opWith is registerPlugin's operation, declared the same way, with one
// change applied: the second release of a plugin.
func opWith(plugin, op string, change func(*Discovery)) opTool {
	name := "pl_" + strings.NewReplacer(".", "_", "-", "_").Replace(plugin) + "_" + strings.ReplaceAll(op, ".", "_")
	d := Discovery{Plugin: plugin, Version: "0.1.0", Tier: "T1", Operation: op, Summary: "Operation " + op + " of " + plugin,
		Effects: "read.internal", Capabilities: []string{"ring4.kv"}, MaxResultBytes: 4096, Keywords: []string{"remember"}}
	if change != nil {
		change(&d)
	}
	return opTool{name: name, disc: d}
}

// paramTool is an operation whose argument schema changed.
type paramTool struct {
	opTool
	params map[string]any
}

func (p paramTool) Parameters() map[string]any { return p.params }

func TestAStandingOfferComesBackWithItsPlugin(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var saved [][]StandingSeat
	r.SetStandingOffer(nil, func(seats []StandingSeat) error { saved = append(saved, seats); return nil })
	names := registerPlugin(t, r, "com.example.memory", "store", "search", "recall")
	if _, err := r.Offer("store"); err != nil {
		t.Fatal(err)
	}
	if got := saved[len(saved)-1]; !reflect.DeepEqual(seatNames(got), []string{names[0]}) || got[0].Print == "" {
		t.Fatalf("the offer was not recorded with what was offered: %+v", got)
	}
	r.Deregister(names[0])
	if contains(r.Offered(), names[0]) {
		t.Fatal("an absent operation kept its seat")
	}
	if len(saved) != 1 {
		t.Fatalf("a deactivation rewrote the identity's choice: %v", saved)
	}
	registerPlugin(t, r, "com.example.memory", "store")
	if st, _, _, _ := r.State(names[0]); st != StateOffered {
		t.Fatalf("the returning operation is %s, not offered", st)
	}
	if !contains(r.PromptNames(), names[0]) {
		t.Fatal("the returning operation is not in the prompt's tool list")
	}
}

// A restart is a new registry reading the record back: what was offered
// is offered again as each operation registers, in any order.
func TestAStandingOfferSurvivesARestart(t *testing.T) {
	first := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	first.SetStandingOffer(nil, func(seats []StandingSeat) error { record = seats; return nil })
	registerPlugin(t, first, "com.example.memory", "store", "search")
	for _, op := range []string{"store", "search"} {
		if _, err := first.Offer(op); err != nil {
			t.Fatal(err)
		}
	}

	second := NewRegistry(t.TempDir(), nil, Timeouts{})
	second.SetStandingOffer(record, func(seats []StandingSeat) error { record = seats; return nil })
	if len(second.Offered()) != 0 {
		t.Fatal("a seat was taken by an operation that is not registered")
	}
	names := registerPlugin(t, second, "com.example.memory", "search", "store")
	for _, n := range names {
		if st, _, _, _ := second.State(n); st != StateOffered {
			t.Fatalf("%s came back %s after the restart", n, st)
		}
	}
	if held := second.WithheldOffers(); len(held) != 0 {
		t.Fatalf("an unchanged operation was reported withheld: %+v", held)
	}

	if _, err := second.Release("store"); err != nil {
		t.Fatal(err)
	}
	got := seatNames(record)
	if contains(got, "pl_com_example_memory_store") || !contains(got, "pl_com_example_memory_search") {
		t.Fatalf("a release did not leave the record: %v", got)
	}
}

// A seat comes back for what was offered, not for a name. The second
// release of the plugin declares a wider effect class under the same
// operation id: the identity never saw that operation, so it is not
// offered, and the identity is told so until it chooses again.
func TestAChangedOperationIsWithheldAfterARestart(t *testing.T) {
	first := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	first.SetStandingOffer(nil, func(seats []StandingSeat) error { record = seats; return nil })
	registerPlugin(t, first, "com.example.memory", "store")
	name, err := first.Offer("store")
	if err != nil {
		t.Fatal(err)
	}
	offered := record[0].Print

	second := NewRegistry(t.TempDir(), nil, Timeouts{})
	second.SetStandingOffer(record, func(seats []StandingSeat) error { record = seats; return nil })
	changed := opWith("com.example.memory", "store", func(d *Discovery) { d.Version, d.Effects = "0.2.0", "write.local" })
	if err := second.RegisterDynamic(changed, "com.example.memory"); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := second.State(name); st != StateInspectOnly {
		t.Fatalf("an operation that changed its effect class came back %s", st)
	}
	held := second.WithheldOffers()
	if len(held) != 1 || held[0].Name != name || held[0].Operation != "store" || held[0].Version != "0.2.0" || held[0].Unrecorded {
		t.Fatalf("the withheld seat was not named: %+v", held)
	}

	if _, err := second.Offer("store"); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := second.State(name); st != StateOffered {
		t.Fatalf("offered again after the change, it is %s", st)
	}
	if len(record) != 1 || record[0].Print == offered || record[0].Print != Fingerprint(changed) {
		t.Fatalf("the record does not name what was offered the second time: %+v", record)
	}
	if held := second.WithheldOffers(); len(held) != 0 {
		t.Fatalf("an operation offered again is still reported withheld: %+v", held)
	}
}

// What an offer promises is the operation's contract. A new release that
// changes only the version, the wording and the hints keeps the seat.
func TestAnUnchangedOperationComesBackWhateverItsVersionAndWording(t *testing.T) {
	first := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	first.SetStandingOffer(nil, func(seats []StandingSeat) error { record = seats; return nil })
	registerPlugin(t, first, "com.example.memory", "store")
	name, err := first.Offer("store")
	if err != nil {
		t.Fatal(err)
	}
	second := NewRegistry(t.TempDir(), nil, Timeouts{})
	second.SetStandingOffer(record, nil)
	reworded := opWith("com.example.memory", "store", func(d *Discovery) {
		d.Version, d.Summary, d.Keywords, d.Examples, d.Family = "0.9.0", "Keeps a note", []string{"note"}, []string{"store text=..."}, "memory"
	})
	if err := second.RegisterDynamic(reworded, "com.example.memory"); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := second.State(name); st != StateOffered {
		t.Fatalf("an operation with the same contract came back %s", st)
	}
}

// Each part of the contract counts: the argument schema, the result
// schema, the capabilities and the operator's confirmation.
func TestEachPartOfTheContractWithholdsTheSeat(t *testing.T) {
	base := opWith("com.example.memory", "store", nil)
	for label, changed := range map[string]Tool{
		"arguments": paramTool{opTool: base, params: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}},
		"result": opWith("com.example.memory", "store", func(d *Discovery) {
			d.OutputSchema = map[string]any{"type": "object"}
		}),
		"capabilities": opWith("com.example.memory", "store", func(d *Discovery) { d.Capabilities = []string{"ring4.kv", "ring4.memory"} }),
		"confirmation": opWith("com.example.memory", "store", func(d *Discovery) { d.OperatorConfirms = true }),
		"result bound": opWith("com.example.memory", "store", func(d *Discovery) { d.MaxResultBytes = 1 << 20 }),
	} {
		if Fingerprint(changed) == Fingerprint(base) {
			t.Errorf("%s: a changed contract prints the same", label)
			continue
		}
		r := NewRegistry(t.TempDir(), nil, Timeouts{})
		r.SetStandingOffer([]StandingSeat{{Name: base.Name(), Print: Fingerprint(base)}}, nil)
		if err := r.RegisterDynamic(changed, "com.example.memory"); err != nil {
			t.Fatal(err)
		}
		if contains(r.Offered(), base.Name()) {
			t.Errorf("%s: a changed operation took the seat", label)
		}
	}
	reordered := opWith("com.example.memory", "store", func(d *Discovery) { d.Capabilities = []string{"ring4.kv"} })
	if Fingerprint(reordered) != Fingerprint(base) {
		t.Error("the same capabilities print differently")
	}
}

// A live update is the same promise kept: the operation that changed
// gives up its seat at the supersede, the one that did not keeps it.
func TestALiveUpdateThatChangesAnOfferedOperationWithdrawsItsSeat(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	r.SetStandingOffer(nil, nil)
	names := registerPlugin(t, r, "com.example.memory", "store", "search")
	for _, op := range []string{"store", "search"} {
		if _, err := r.Offer(op); err != nil {
			t.Fatal(err)
		}
	}
	next := []Tool{
		opWith("com.example.memory", "store", func(d *Discovery) { d.Version, d.Effects = "0.2.0", "write.external" }),
		opWith("com.example.memory", "search", func(d *Discovery) { d.Version = "0.2.0" }),
	}
	if err := r.SupersedeOriginIf("com.example.memory", next, nil, nil); err != nil {
		t.Fatal(err)
	}
	if contains(r.Offered(), names[0]) {
		t.Fatal("the operation that changed kept its seat across the update")
	}
	if !contains(r.Offered(), names[1]) {
		t.Fatal("the unchanged operation lost its seat in the update")
	}
	if held := r.WithheldOffers(); len(held) != 1 || held[0].Name != names[0] {
		t.Fatalf("the withdrawn seat was not named: %+v", held)
	}
}

// A record written before seats carried fingerprints names operations
// without saying what was offered. A name is not an offer: those seats
// are withheld and named, and one offer puts each back.
func TestASeatWithoutAFingerprintIsWithheld(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	r.SetStandingOffer([]StandingSeat{{Name: "pl_com_example_memory_store"}}, func(seats []StandingSeat) error { record = seats; return nil })
	names := registerPlugin(t, r, "com.example.memory", "store")
	if contains(r.Offered(), names[0]) {
		t.Fatal("a seat with no fingerprint was reseated by name")
	}
	if held := r.WithheldOffers(); len(held) != 1 || !held[0].Unrecorded {
		t.Fatalf("the unrecorded seat was not named as such: %+v", held)
	}
	if _, err := r.Offer("store"); err != nil {
		t.Fatal(err)
	}
	if len(record) != 1 || record[0].Print != Fingerprint(opWith("com.example.memory", "store", nil)) {
		t.Fatalf("offered again, the seat still has no fingerprint: %+v", record)
	}
}

// Told once per process: a withheld seat named to a turn that completed
// is not named again until the next start, when the record still holds
// it; releasing it ends the matter.
func TestAWithheldSeatIsToldOnceAndCanBeReleased(t *testing.T) {
	record := []StandingSeat{{Name: "pl_com_example_memory_store", Print: "not-what-is-registered"}}
	start := func() *Registry {
		r := NewRegistry(t.TempDir(), nil, Timeouts{})
		r.SetStandingOffer(record, func(seats []StandingSeat) error { record = seats; return nil })
		registerPlugin(t, r, "com.example.memory", "store")
		return r
	}
	r := start()
	held := r.WithheldOffers()
	if len(held) != 1 {
		t.Fatalf("withheld: %+v", held)
	}
	r.MarkOfferNoticesTold([]string{held[0].Name})
	if again := r.WithheldOffers(); len(again) != 0 {
		t.Fatalf("a seat already told was named again in the same process: %+v", again)
	}
	r = start()
	if again := r.WithheldOffers(); len(again) != 1 {
		t.Fatalf("after a restart the withheld seat was not named again: %+v", again)
	}
	if _, err := r.Release(held[0].Name); err != nil {
		t.Fatalf("a withheld seat could not be released: %v", err)
	}
	if len(record) != 0 || len(r.WithheldOffers()) != 0 {
		t.Fatalf("released, the seat lingers: record %+v", record)
	}
}

// Dormant choices remain remembered when a present operation is selected.
func TestAbsentChoicesAreRetainedWhenAnOperationIsOffered(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	var absent []StandingSeat
	for i := 0; i < MaxOffered; i++ {
		absent = append(absent, StandingSeat{Name: "pl_gone_op" + string(rune('a'+i)), Print: "p"})
	}
	r.SetStandingOffer(absent, func(seats []StandingSeat) error { record = seats; return nil })
	names := registerPlugin(t, r, "com.example.memory", "store")
	if _, err := r.Offer("store"); err != nil {
		t.Fatalf("a full choice of absent operations refused a present one: %v", err)
	}
	if len(record) != MaxOffered+1 || !slices.Equal(record[:MaxOffered], absent) || record[MaxOffered].Name != names[0] {
		t.Fatalf("an absent choice was changed: %v", seatNames(record))
	}
}

// Host plumbing never holds a seat, whatever a record says.
func TestHostPlumbingNeverTakesAStandingSeat(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	send := opTool{name: "pl_x_send", disc: Discovery{Plugin: "x", Operation: "send"}}
	r.SetStandingOffer([]StandingSeat{{Name: send.name, Print: Fingerprint(send)}}, nil)
	if err := r.RegisterHostOp(send, "x"); err != nil {
		t.Fatal(err)
	}
	if contains(r.Offered(), "pl_x_send") || contains(r.PromptNames(), "pl_x_send") {
		t.Fatal("a host operation took a seat from a standing offer")
	}
	if held := r.WithheldOffers(); len(held) != 0 {
		t.Fatalf("host plumbing was named as a withheld offer: %+v", held)
	}
}

// heldRecord wires a record hook whose first write blocks until the
// test lets it go; every write after it is kept in writes. The returned
// release is idempotent and also runs at cleanup, so a failed assertion
// never leaves a goroutine parked.
func heldRecord(t *testing.T, r *Registry) (entered <-chan struct{}, release func(), writes func() [][]StandingSeat) {
	t.Helper()
	in, out := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var kept [][]StandingSeat
	first := true
	r.SetStandingOffer(nil, func(seats []StandingSeat) error {
		mu.Lock()
		kept = append(kept, seats)
		hold := first
		first = false
		mu.Unlock()
		if hold {
			close(in)
			<-out
		}
		return nil
	})
	release = func() { once.Do(func() { close(out) }) }
	t.Cleanup(release)
	return in, release, func() [][]StandingSeat {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(kept)
	}
}

// answers fails the test unless fn returns within the bound. A call held
// behind the blocked record would not return until the test released it.
func answers(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: no answer in 5s", what)
	}
}

// parkedInChange waits until a goroutine is asleep on a lock inside
// changeStanding: a second change to the choice, queued behind a record
// still being written.
func parkedInChange(t *testing.T) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); runtime.Gosched() {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			head, _, _ := strings.Cut(g, "\n")
			if strings.Contains(head, "Mutex.Lock]") && strings.Contains(g, "(*Registry).changeStanding(") {
				return
			}
		}
	}
	t.Fatal("the second change never reached its lock")
}

// THE RECORD IS WRITTEN OUTSIDE THE REGISTRY LOCK. It is a store write
// (runtime_meta, under the store's own lock); held under regMu, a slow
// one stalled every tool call, every prompt and every registration
// behind a choice the identity made. While it is held: reads answer,
// operations register and leave, and a second change waiting its turn
// holds nothing a reader needs.
func TestASlowStandingRecordHoldsNoReader(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := registerPlugin(t, r, "com.example.memory", "store", "search")
	entered, release, _ := heldRecord(t, r)
	offered := make(chan error, 2)
	go func() { _, err := r.Offer("store"); offered <- err }()
	answers(t, "the first offer reaching its record", func() { <-entered })
	reads := func() {
		r.Offered()
		r.PromptNames()
		r.State(names[0])
		r.WithheldOffers()
		r.Brief()
		r.Get(names[1])
		r.ParallelSafe(names[0])
	}
	answers(t, "a read while the record is held", reads)
	var regErr error
	answers(t, "a registration while the record is held", func() {
		if regErr = r.RegisterDynamic(opWith("com.example.notes", "write", nil), "com.example.notes"); regErr == nil {
			r.Deregister("pl_com_example_notes_write")
		}
		r.MarkOfferNoticesTold([]string{names[1]})
	})
	if regErr != nil {
		t.Fatal(regErr)
	}
	go func() { _, err := r.Offer("search"); offered <- err }()
	parkedInChange(t)
	answers(t, "a read behind a change queued on the held record", reads)
	release()
	answers(t, "both offers once the record is released", func() {
		for range 2 {
			if err := <-offered; err != nil {
				t.Error(err)
			}
		}
	})
}

// THE RECORD FOLLOWS THE ORDER OF THE CHOICES. A change waits for the
// record before it to be written before it is made, so the last record
// is always the latest choice; a change made first and recorded after
// a newer one would leave the identity's offer, at the next restart,
// one choice behind.
func TestStandingRecordsFollowTheirChanges(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := registerPlugin(t, r, "com.example.memory", "store", "search")
	entered, release, writes := heldRecord(t, r)
	offered := make(chan error, 2)
	go func() { _, err := r.Offer("store"); offered <- err }()
	answers(t, "the first offer reaching its record", func() { <-entered })
	go func() { _, err := r.Offer("search"); offered <- err }()
	parkedInChange(t)
	var now []string
	answers(t, "the offer while the record is held", func() { now = r.Offered() })
	if len(now) != 0 {
		t.Fatalf("a choice was published before persistence completed: offered %v", now)
	}
	release()
	answers(t, "both offers once the record is released", func() {
		for range 2 {
			if err := <-offered; err != nil {
				t.Error(err)
			}
		}
	})
	got := writes()
	if len(got) != 2 || !reflect.DeepEqual(seatNames(got[0]), names[:1]) || !reflect.DeepEqual(seatNames(got[1]), names) {
		t.Fatalf("records out of the order of their changes: %v", got)
	}
}

// Changes racing from many turns leave a record equal to the choice the
// registry holds, round after round.
func TestRacingChangesLeaveTheLatestRecord(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := registerPlugin(t, r, "com.example.memory", "store", "search", "recall", "forget")
	var record []StandingSeat
	r.SetStandingOffer(nil, func(seats []StandingSeat) error { record = seats; return nil })
	for round := range 200 {
		var wg sync.WaitGroup
		for _, n := range names {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Even rounds start from no offer and end offering every
				// name; odd rounds start offering every name and end with
				// none. Each step is a change that is recorded.
				steps := []func(string) (string, error){r.Offer, r.Release, r.Offer}
				if round%2 == 1 {
					steps = []func(string) (string, error){r.Release, r.Offer, r.Release}
				}
				for _, step := range steps {
					if _, err := step(n); err != nil {
						t.Error(err)
					}
				}
			}()
		}
		wg.Wait()
		r.regMu.RLock()
		want := slices.Clone(r.standing)
		r.regMu.RUnlock()
		if !slices.Equal(record, want) {
			t.Fatalf("round %d: the record %v is not the registry's choice %v", round, seatNames(record), seatNames(want))
		}
	}
}
