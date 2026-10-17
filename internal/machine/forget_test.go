//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// linkAs proves kp for slot over a pipe, as the operator's aii register
// does (register) or as an identity host does at boot.
func (r *storeRig) linkAs(kp *crypto.KeyPair, slot string, register bool) (*rawLink, error) {
	r.t.Helper()
	a, b := net.Pipe()
	go r.srv.serveConn(r.ctx, a)
	r.t.Cleanup(func() { b.Close() })
	_ = b.SetDeadline(time.Now().Add(10 * time.Second))
	m, err := readMessage(b)
	if err != nil {
		r.t.Fatal(err)
	}
	var h helloParams
	if m.Method != MsgHello || json.Unmarshal(m.Params, &h) != nil {
		r.t.Fatalf("the link did not begin with hello: %+v", m)
	}
	l := &rawLink{t: r.t, conn: b, hello: h}
	_, err = l.ask(MsgHandshake, l.signed(kp, slot, register))
	return l, err
}

// slotState is what the state on disk holds of one slot: its record, its
// references and its gone entry, encoded.
func slotState(st State, slot string) string {
	rec, _ := st.record(slot)
	var refs []Ref
	for _, r := range st.Refs {
		if r.Slot == slot {
			refs = append(refs, r)
		}
	}
	raw, _ := json.Marshal(struct {
		Record Record
		Refs   []Ref
		Gone   string
	}{rec, refs, st.Gone[slot]})
	return string(raw)
}

func TestForgetReleasesOnlyItsOwnSlotsRecordAndReferences(t *testing.T) {
	root := t.TempDir()
	slotA, slotB := slotIn(t, root, "identity-0"), slotIn(t, root, "identity-1")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	ka, kb := newKey(t), newKey(t)
	la, lb := r.link(ka, slotA), r.link(kb, slotB)
	shared := viewWork(f.model("shared.bin", []byte("both identities' model")))
	onlyA := viewWork(f.model("a.bin", []byte("the first identity's model alone")))
	onlyB := viewWork(f.model("b.bin", []byte("the second identity's model alone")))
	la.needUntil(NeedParams{Plugin: "org.example.p", Works: []pluginhost.StoreWork{shared, onlyA}}, NeedPresent, 10*time.Second)
	lb.needUntil(NeedParams{Plugin: "org.example.p", Works: []pluginhost.StoreWork{shared, onlyB}}, NeedPresent, 10*time.Second)
	// The first slot was once found gone (a volume unmounted a while): the
	// entry goes with its record.
	if err := r.srv.updateState(func(st *State) error {
		st.Gone = map[string]string{slotA: r.clock.Now().UTC().Format(time.RFC3339)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := r.diskState()
	keepB := slotState(before, slotB)

	if _, err := la.ask(MsgForget, nil); err != nil {
		t.Fatalf("forget: %v", err)
	}
	after := r.diskState()
	if _, ok := after.record(slotA); ok {
		t.Fatal("the forgotten slot is still recorded")
	}
	if got := slotState(after, slotA); got != slotState(State{}, slotA) {
		t.Fatalf("the forgotten slot keeps something in the state: %s", got)
	}
	if got := slotState(after, slotB); got != keepB {
		t.Fatalf("the other slot changed:\n before %s\n after  %s", keepB, got)
	}

	// The collector, an hour on, takes what was the forgotten slot's alone:
	// its view, and an hour after the view, the blob no view links any more.
	r.collect()
	r.clock.advance(storeGrace + time.Minute)
	r.collect()
	if r.exists(onlyA.Entry) {
		t.Error("the forgotten slot's own view is still in the store an hour on")
	}
	r.clock.advance(storeGrace + time.Minute)
	r.collect()
	if r.exists(blobEntry(onlyA.View.Models[0])) {
		t.Error("the forgotten slot's own blob is still in the store an hour after its view went")
	}
	for _, e := range []string{shared.Entry, onlyB.Entry, blobEntry(shared.View.Models[0]), blobEntry(onlyB.View.Models[0])} {
		if !r.exists(e) {
			t.Errorf("%s, which the other slot still refers to, was taken", e)
		}
	}
}

// EVERY LINK OF A FORGOTTEN SLOT IS CLOSED, and links again only through
// the operator's register: its startup registration is gone (unregister
// removed it before it forgot), and so is its record.
func TestAForgottenSlotsOtherLinksAreClosedAndRelinkOnlyThroughRegister(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-0")
	r := newStoreRig(t, "", newFetches(), Options{})
	kp := newKey(t)
	first := r.link(kp, slot)
	second, err := r.linkAs(kp, slot, false)
	if err != nil {
		t.Fatalf("a recorded slot's identity host links: %v", err)
	}
	if _, err := first.ask(MsgForget, nil); err != nil {
		t.Fatalf("forget: %v", err)
	}
	_ = second.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var one [1]byte
	if _, err := second.conn.Read(one[:]); !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("the slot's other link was not closed: %v", err)
	}
	if _, err := r.linkAs(kp, slot, false); !isRefusal(err, ReasonUnregistered) {
		t.Fatalf("a forgotten slot linked again without register: %v", err)
	}
	if _, err := r.linkAs(kp, slot, true); err != nil {
		t.Fatalf("the operator's register: %v", err)
	}
	if rec, ok := r.diskState().record(slot); !ok || rec.How != RecordedByRegister {
		t.Fatalf("register did not record the slot again: %+v", rec)
	}
}

// FORGET NAMES NO IDENTITY: a request that names a slot or an identity is
// refused, and nothing of anyone's is let go.
func TestForgetNamesNoIdentity(t *testing.T) {
	root := t.TempDir()
	slotA, slotB := slotIn(t, root, "identity-0"), slotIn(t, root, "identity-1")
	r := newStoreRig(t, "", newFetches(), Options{})
	la := r.link(newKey(t), slotA)
	r.link(newKey(t), slotB)
	before := r.diskState()
	for _, p := range []any{map[string]string{"slot": slotB}, map[string]string{"identity": "x"}, []string{slotB}} {
		if _, err := la.ask(MsgForget, p); !isRefusal(err, ReasonBadRequest) {
			t.Errorf("forget with %v: %v, want %s", p, err, ReasonBadRequest)
		}
	}
	after := r.diskState()
	for _, s := range []string{slotA, slotB} {
		if slotState(after, s) != slotState(before, s) {
			t.Errorf("a refused forget changed %s's state", s)
		}
	}
}

// A NEED THAT ARRIVES ON A FORGOTTEN SLOT'S LINK REFERS TO NOTHING: its
// record is gone, so nothing of it is recorded again.
func TestANeedAfterForgetRecordsNothing(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-0")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	l := r.link(newKey(t), slot)
	if err := r.srv.forgetSlot(slot); err != nil {
		t.Fatal(err)
	}
	work := viewWork(f.model("late.bin", []byte("asked after the forget")))
	if _, err := l.need(NeedParams{Plugin: "org.example.p", Works: []pluginhost.StoreWork{work}}); !isRefusal(err, ReasonUnregistered) {
		t.Fatalf("a need on a forgotten slot's link: %v, want %s", err, ReasonUnregistered)
	}
	if refs := r.diskState().Refs; len(refs) != 0 {
		t.Fatalf("a forgotten slot's need was recorded: %+v", refs)
	}
}

// A SERVICE THAT DOES NOT SPEAK FORGET REFUSES THE UNREGISTER'S STEP
// ALONE, naming the message and both builds; the link itself is served.
func TestAServiceWithoutForgetRefusesOnlyTheUnregisterStep(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Options{Dir: dir, Build: Build{Version: "0.1.15", Commit: "older0000000"}})
	older := speaks()
	older.Messages = slices.DeleteFunc(older.Messages, func(m string) bool { return m == MsgForget })
	srv.say = &older
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan struct{})
	go func() { defer close(ended); _ = srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-ended })
	kp := newKey(t)
	waitLink(t, dir)
	if _, err := Register(t.Context(), dir, slot, kp, "", 5*time.Second); err != nil {
		t.Fatalf("register on the older service: %v", err)
	}
	err := Forget(t.Context(), dir, slot, kp, "")
	var m *MissingError
	if !errors.As(err, &m) || m.Message != MsgForget || m.Needer != NeederForget || m.Service.Commit != "older0000000" || m.Identity != Self() {
		t.Fatalf("forget on a service that does not speak it: %v", err)
	}
	if _, ok := mustLoad(t, dir).record(slot); !ok {
		t.Fatal("the older service let go of the record it was never asked to")
	}
	// A service that speaks it forgets.
	cancel()
	<-ended
	ctx2, cancel2 := context.WithCancel(context.Background())
	ended2 := make(chan struct{})
	go func() { defer close(ended2); _ = NewServer(Options{Dir: dir}).Serve(ctx2) }()
	t.Cleanup(func() { cancel2(); <-ended2 })
	waitLink(t, dir)
	if err := Forget(t.Context(), dir, slot, kp, ""); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if _, ok := mustLoad(t, dir).record(slot); ok {
		t.Fatal("the slot is still recorded after forget")
	}
	// Forgetting a slot already forgotten is done: there is nothing of it.
	if err := Forget(t.Context(), dir, slot, kp, ""); err != nil {
		t.Fatalf("forget again: %v", err)
	}
}

// RECORDED SLOTS DROP A FORGOTTEN SLOT: the masks H6 reads from the state
// no longer name it (C1 masks it as a sibling while it is there).
func TestRecordedSlotsDropAForgottenSlot(t *testing.T) {
	root := t.TempDir()
	slotA, slotB := slotIn(t, root, "identity-0"), slotIn(t, root, "identity-1")
	r := newStoreRig(t, "", newFetches(), Options{})
	la := r.link(newKey(t), slotA)
	r.link(newKey(t), slotB)
	if _, err := la.ask(MsgForget, nil); err != nil {
		t.Fatal(err)
	}
	got, err := RecordedSlots(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{slotB}) {
		t.Fatalf("recorded slots after forget: %v, want %v", got, []string{slotB})
	}
}

func waitLink(t *testing.T, dir string) {
	t.Helper()
	path, err := LinkPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := dialLink(context.Background(), path, ""); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the service never listened")
}

func mustLoad(t *testing.T, dir string) State {
	t.Helper()
	st, err := loadState(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	return st
}
