package machine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A LINK IS PROVED BY THE IDENTITY'S OWN KEY. The operator's register
// records the slot with its key; a link signed with that key is accepted
// as known, and the service's own state file says so.
func TestALinkIsProvedByTheIdentitysOwnKey(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	kp := newKey(t)
	serve(t, Options{Dir: dir})

	how, err := Register(context.Background(), dir, slot, kp, "", 5*time.Second)
	if err != nil || how != RecordedByRegister {
		t.Fatalf("register: %q, %v", how, err)
	}
	l := dialRaw(t, dir)
	raw, err := l.ask(MsgHandshake, l.signed(kp, slot, false))
	if err != nil {
		t.Fatalf("a link signed with the registered key was refused: %v", err)
	}
	var res handshakeResult
	if err := json.Unmarshal(raw, &res); err != nil || res.Identity != kp.Fingerprint() || res.Recorded != "known" {
		t.Fatalf("handshake result = %s, %v", raw, err)
	}
	st, err := loadState(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := st.record(slot); !ok || r.Identity != kp.Fingerprint() || r.How != RecordedByRegister || r.PublicKey != kp.PublicKeyB64() {
		t.Fatalf("state.json does not hold the registered record: %+v", st.Identities)
	}
	if strings.Contains(mustRead(t, filepath.Join(dir, stateFileName)), "PRIVATE") {
		t.Fatal("state.json holds more than public material")
	}
}

// A WRONG KEY IS REFUSED, in both of its shapes: another identity's key
// for a recorded slot, and a signature that does not verify under the key
// it presents.
func TestALinkSignedWithAnotherKeyIsRefused(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	owner, other := newKey(t), newKey(t)
	serve(t, Options{Dir: dir})
	if _, err := Register(context.Background(), dir, slot, owner, "", 5*time.Second); err != nil {
		t.Fatal(err)
	}

	l := dialRaw(t, dir)
	_, err := l.ask(MsgHandshake, l.signed(other, slot, false))
	if refusal(err) != ReasonWrongKey {
		t.Fatalf("another identity's key for a recorded slot: want %s, got %v", ReasonWrongKey, err)
	}
	if !strings.Contains(err.Error(), "aii register identity-0") {
		t.Errorf("the refusal does not carry its remedy: %v", err)
	}

	// The owner's fingerprint and key, signed by someone else.
	l = dialRaw(t, dir)
	forged := l.signed(other, slot, false)
	forged.Identity, forged.PublicKey = owner.Fingerprint(), owner.PublicKeyB64()
	if _, err := l.ask(MsgHandshake, forged); refusal(err) != ReasonBadSignature {
		t.Fatalf("a signature that does not verify under the presented key: want %s, got %v", ReasonBadSignature, err)
	}
	// A key that is not the fingerprint's.
	l = dialRaw(t, dir)
	mismatched := l.signed(other, slot, false)
	mismatched.Identity = owner.Fingerprint()
	if _, err := l.ask(MsgHandshake, mismatched); refusal(err) != ReasonBadSignature {
		t.Fatalf("a key that is not the claimed identity's: want %s, got %v", ReasonBadSignature, err)
	}
}

// AN UNREGISTERED SLOT IS REFUSED: no record, and the account's startup
// registrations do not name it. Nothing is recorded by the attempt.
func TestAnUnregisteredSlotIsRefused(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-4")
	serve(t, Options{Dir: dir, StartupSlots: startupSlots(filepath.Join(root, "identity-0"))})

	l := dialRaw(t, dir)
	_, err := l.ask(MsgHandshake, l.signed(newKey(t), slot, false))
	if refusal(err) != ReasonUnregistered {
		t.Fatalf("want %s, got %v", ReasonUnregistered, err)
	}
	if !strings.Contains(err.Error(), "aii register identity-4") {
		t.Errorf("the refusal does not carry its remedy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); !os.IsNotExist(err) {
		t.Fatalf("a refused link wrote the state: %v", err)
	}
}

// A REPLAYED NONCE IS REFUSED. A handshake captured on one connection,
// sent whole on another, names a nonce that connection never issued; and a
// proved link that sends a second handshake has spent its nonce.
func TestAReplayedNonceIsRefused(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	kp := newKey(t)
	serve(t, Options{Dir: dir, StartupSlots: startupSlots(slot)})

	first := dialRaw(t, dir)
	captured := first.signed(kp, slot, false)
	if _, err := first.ask(MsgHandshake, captured); err != nil {
		t.Fatalf("rig: the first handshake must link: %v", err)
	}
	if _, err := first.ask(MsgHandshake, captured); refusal(err) != ReasonReplayedNonce {
		t.Fatalf("a second handshake on a proved link: want %s, got %v", ReasonReplayedNonce, err)
	}
	second := dialRaw(t, dir)
	if second.hello.Nonce == first.hello.Nonce {
		t.Fatal("two connections were issued the same nonce")
	}
	if _, err := second.ask(MsgHandshake, captured); refusal(err) != ReasonReplayedNonce {
		t.Fatalf("a captured handshake on another connection: want %s, got %v", ReasonReplayedNonce, err)
	}
}

// AN IDENTITY REGISTERED BEFORE THE SERVICE RECORDS ITSELF ONCE, AND ONLY
// WHERE THE ACCOUNT STARTS ITS SLOT. A slot the account's startup
// registrations name, with no record, is recorded at its first link with
// proof of possession; a slot they do not name is refused; a later key for
// the recorded slot is refused until the operator registers it again,
// which replaces the record.
func TestAnIdentityRecordsItselfOnlyWhereTheAccountStartsItsSlot(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	started := slotDir(t, root, "identity-0")
	unstarted := slotDir(t, root, "identity-1")
	kp, later := newKey(t), newKey(t)
	// The registration names the slot through a link: one directory, two
	// spellings.
	alias := filepath.Join(root, "alias-0")
	if err := os.Symlink(started, alias); err != nil {
		t.Fatal(err)
	}
	serve(t, Options{Dir: dir, StartupSlots: startupSlots(alias)})

	l := dialRaw(t, dir)
	raw, err := l.ask(MsgHandshake, l.signed(kp, started, false))
	if err != nil {
		t.Fatalf("a started slot with no record must record itself: %v", err)
	}
	var res handshakeResult
	if err := json.Unmarshal(raw, &res); err != nil || res.Recorded != RecordedAtFirstLink {
		t.Fatalf("recorded = %q, want %q", res.Recorded, RecordedAtFirstLink)
	}
	st, _ := loadState(filepath.Join(dir, stateFileName))
	if r, ok := st.record(started); !ok || r.How != RecordedAtFirstLink || r.At == "" {
		t.Fatalf("the self-registration is not recorded with its time: %+v", st.Identities)
	}

	l = dialRaw(t, dir)
	if _, err := l.ask(MsgHandshake, l.signed(newKey(t), unstarted, false)); refusal(err) != ReasonUnregistered {
		t.Fatalf("a slot the account does not start: want %s, got %v", ReasonUnregistered, err)
	}
	l = dialRaw(t, dir)
	if _, err := l.ask(MsgHandshake, l.signed(later, started, false)); refusal(err) != ReasonWrongKey {
		t.Fatalf("a later key for a self-registered slot: want %s, got %v", ReasonWrongKey, err)
	}
	if _, err := Register(context.Background(), dir, started, later, "", 5*time.Second); err != nil {
		t.Fatalf("the operator's register must replace the record: %v", err)
	}
	l = dialRaw(t, dir)
	if _, err := l.ask(MsgHandshake, l.signed(later, started, false)); err != nil {
		t.Fatalf("after the operator's register the slot's new key links: %v", err)
	}
	l = dialRaw(t, dir)
	if _, err := l.ask(MsgHandshake, l.signed(kp, started, false)); refusal(err) != ReasonWrongKey {
		t.Fatalf("the replaced key: want %s, got %v", ReasonWrongKey, err)
	}
}

// A REQUEST ON A LINK IS ITS OWN IDENTITY'S. The status a link reads names
// that link's identity and counts the others without naming them; a
// request that tries to name an identity is refused.
func TestARequestOnALinkIsItsOwnIdentitys(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	a, b := slotDir(t, root, "identity-0"), slotDir(t, root, "identity-1")
	ka, kb := newKey(t), newKey(t)
	serve(t, Options{Dir: dir, StartupSlots: startupSlots(a, b)})

	la, lb := dialRaw(t, dir), dialRaw(t, dir)
	if _, err := la.ask(MsgHandshake, la.signed(ka, a, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := lb.ask(MsgHandshake, lb.signed(kb, b, false)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		l  *rawLink
		fp string
	}{{la, ka.Fingerprint()}, {lb, kb.Fingerprint()}} {
		raw, err := c.l.ask(MsgStatus, nil)
		if err != nil {
			t.Fatal(err)
		}
		var st Status
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}
		if st.Identity != c.fp || st.Linked != 2 {
			t.Fatalf("status on a link = %+v; want its own identity and two linked", st)
		}
		other := ka.Fingerprint()
		if c.fp == other {
			other = kb.Fingerprint()
		}
		if strings.Contains(string(raw), other) {
			t.Fatalf("one link's status names another identity: %s", raw)
		}
	}
	if _, err := la.ask(MsgStatus, map[string]string{"identity": kb.Fingerprint()}); refusal(err) != ReasonBadRequest {
		t.Fatalf("a request naming an identity: want %s, got %v", ReasonBadRequest, err)
	}
	if _, err := la.ask("open.paths", nil); refusal(err) != ReasonNotSpoken {
		t.Fatalf("a message this service does not speak: want %s, got %v", ReasonNotSpoken, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
