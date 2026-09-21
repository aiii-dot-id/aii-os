package tools

import (
	"reflect"
	"strings"
	"testing"
)

func seatNames(seats []StandingSeat) []string {
	var out []string
	for _, s := range seats {
		out = append(out, s.Name)
	}
	return out
}

// .
// .
func opWith(plugin, op string, change func(*Discovery)) opTool {
	name := "pl_" + strings.NewReplacer(".", "_", "-", "_").Replace(plugin) + "_" + strings.ReplaceAll(op, ".", "_")
	d := Discovery{Plugin: plugin, Version: "0.1.0", Tier: "T1", Operation: op, Summary: "Operation " + op + " of " + plugin,
		Effects: "read.internal", Capabilities: []string{"ring4.kv"}, MaxResultBytes: 4096, Keywords: []string{"remember"}}
	if change != nil {
		change(&d)
	}
	return opTool{name: name, disc: d}
}

// .
type paramTool struct {
	opTool
	params map[string]interface{}
}

func (p paramTool) Parameters() map[string]interface{} { return p.params }

// .
// .
// .
// .
// .
func TestAStandingOfferComesBackWithItsPlugin(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var saved [][]StandingSeat
	r.SetStandingOffer(nil, func(seats []StandingSeat) { saved = append(saved, seats) })
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
	if st, _, _ := r.State(names[0]); st != StateOffered {
		t.Fatalf("the returning operation is %s, not offered", st)
	}
	if !contains(r.PromptNames(), names[0]) {
		t.Fatal("the returning operation is not in the prompt's tool list")
	}
}

// .
// .
func TestAStandingOfferSurvivesARestart(t *testing.T) {
	first := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	first.SetStandingOffer(nil, func(seats []StandingSeat) { record = seats })
	registerPlugin(t, first, "com.example.memory", "store", "search")
	for _, op := range []string{"store", "search"} {
		if _, err := first.Offer(op); err != nil {
			t.Fatal(err)
		}
	}

	second := NewRegistry(t.TempDir(), nil, Timeouts{})
	second.SetStandingOffer(record, func(seats []StandingSeat) { record = seats })
	if len(second.Offered()) != 0 {
		t.Fatal("a seat was taken by an operation that is not registered")
	}
	names := registerPlugin(t, second, "com.example.memory", "search", "store")
	for _, n := range names {
		if st, _, _ := second.State(n); st != StateOffered {
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

// .
// .
// .
// .
func TestAChangedOperationIsWithheldAfterARestart(t *testing.T) {
	first := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	first.SetStandingOffer(nil, func(seats []StandingSeat) { record = seats })
	registerPlugin(t, first, "com.example.memory", "store")
	name, err := first.Offer("store")
	if err != nil {
		t.Fatal(err)
	}
	offered := record[0].Print

	second := NewRegistry(t.TempDir(), nil, Timeouts{})
	second.SetStandingOffer(record, func(seats []StandingSeat) { record = seats })
	changed := opWith("com.example.memory", "store", func(d *Discovery) { d.Version, d.Effects = "0.2.0", "write.local" })
	if err := second.RegisterDynamic(changed, "com.example.memory"); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := second.State(name); st != StateInspectOnly {
		t.Fatalf("an operation that changed its effect class came back %s", st)
	}
	held := second.WithheldOffers()
	if len(held) != 1 || held[0].Name != name || held[0].Operation != "store" || held[0].Version != "0.2.0" || held[0].Unrecorded {
		t.Fatalf("the withheld seat was not named: %+v", held)
	}

	if _, err := second.Offer("store"); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := second.State(name); st != StateOffered {
		t.Fatalf("offered again after the change, it is %s", st)
	}
	if len(record) != 1 || record[0].Print == offered || record[0].Print != Fingerprint(changed) {
		t.Fatalf("the record does not name what was offered the second time: %+v", record)
	}
	if held := second.WithheldOffers(); len(held) != 0 {
		t.Fatalf("an operation offered again is still reported withheld: %+v", held)
	}
}

// .
// .
func TestAnUnchangedOperationComesBackWhateverItsVersionAndWording(t *testing.T) {
	first := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	first.SetStandingOffer(nil, func(seats []StandingSeat) { record = seats })
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
	if st, _, _ := second.State(name); st != StateOffered {
		t.Fatalf("an operation with the same contract came back %s", st)
	}
}

// .
// .
func TestEachPartOfTheContractWithholdsTheSeat(t *testing.T) {
	base := opWith("com.example.memory", "store", nil)
	for label, changed := range map[string]Tool{
		"arguments": paramTool{opTool: base, params: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"text": map[string]interface{}{"type": "string"}}}},
		"result": opWith("com.example.memory", "store", func(d *Discovery) {
			d.OutputSchema = map[string]interface{}{"type": "object"}
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

// .
// .
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

// .
// .
// .
func TestASeatWithoutAFingerprintIsWithheld(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	r.SetStandingOffer([]StandingSeat{{Name: "pl_com_example_memory_store"}}, func(seats []StandingSeat) { record = seats })
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

// .
// .
// .
func TestAWithheldSeatIsToldOnceAndCanBeReleased(t *testing.T) {
	record := []StandingSeat{{Name: "pl_com_example_memory_store", Print: "not-what-is-registered"}}
	start := func() *Registry {
		r := NewRegistry(t.TempDir(), nil, Timeouts{})
		r.SetStandingOffer(record, func(seats []StandingSeat) { record = seats })
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

// .
// .
// .
func TestAFullStandingOfferYieldsAnAbsentName(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	var record []StandingSeat
	var absent []StandingSeat
	for i := 0; i < MaxOffered; i++ {
		absent = append(absent, StandingSeat{Name: "pl_gone_op" + string(rune('a'+i)), Print: "p"})
	}
	r.SetStandingOffer(absent, func(seats []StandingSeat) { record = seats })
	names := registerPlugin(t, r, "com.example.memory", "store")
	if _, err := r.Offer("store"); err != nil {
		t.Fatalf("a full choice of absent operations refused a present one: %v", err)
	}
	if len(record) != MaxOffered || record[0].Name != absent[1].Name || record[MaxOffered-1].Name != names[0] {
		t.Fatalf("the oldest absent name did not give way: %v", seatNames(record))
	}
}

// .
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
