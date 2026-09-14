package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func claimEvent(seq uint64, id, zone string) *ledger.Event {
	raw, _ := json.Marshal(PublicNamePayload{NameID: id, Name: "ui." + id + "." + zone, Zone: zone})
	return &ledger.Event{Seq: seq, Type: ledger.EventNetworkNameClaimed, Ring: 0, Timestamp: "2026-09-03T12:00:00Z", Payload: raw}
}

// .
// .
// .
// .
// .
func TestAPublicNameIsClaimedOnce(t *testing.T) {
	st, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const id = "abcdefghijklmnopqrstuvwxyz"
	if _, ok, err := st.PublicName(); err != nil || ok {
		t.Fatalf("a fresh projection claims a name: ok=%v err=%v", ok, err)
	}
	if _, err := st.db.Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (1, '', 't', 'x', 0, '{}', 'c', 's'), (2, '', 't', 'x', 0, '{}', 'c', 's'), (3, '', 't', 'x', 0, '{}', 'c', 's')`); err != nil {
		t.Fatal(err)
	}
	if err := st.materializePublicName(claimEvent(1, id, "example.test")); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	n, ok, err := st.PublicName()
	if err != nil || !ok || n.Name != "ui."+id+".example.test" || n.ClaimedSeq != 1 {
		t.Fatalf("projection: %+v ok=%v err=%v", n, ok, err)
	}
	if err := st.materializePublicName(claimEvent(2, "zzzzzzzzzzzzzzzzzzzzzzzzzz", "example.test")); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("A SECOND CLAIM LANDED: %v", err)
	}
	// .
	moved, _ := json.Marshal(PublicNamePayload{NameID: "zzzzzzzzzzzzzzzzzzzzzzzzzz", Name: "zzzzzzzzzzzzzzzzzzzzzzzzzz.aiios.id", Zone: "aiios.id"})
	if err := st.materializePublicName(&ledger.Event{Seq: 3, Type: ledger.EventNetworkNameClaimed, Ring: 0, Timestamp: "2026-09-06T12:00:00Z", Payload: moved}); err != nil {
		t.Fatalf("a claim under another zone must land: %v", err)
	}
	if n, ok, err := st.PublicName(); err != nil || !ok || n.Name != "zzzzzzzzzzzzzzzzzzzzzzzzzz.aiios.id" || n.Zone != "aiios.id" || n.ClaimedSeq != 3 {
		t.Fatalf("the moved name is not current: %+v ok=%v err=%v", n, ok, err)
	}
	// .
	// .
	for _, bad := range []PublicNamePayload{
		{NameID: "short", Name: "ui.short.example.test", Zone: "example.test"},
		{NameID: id, Name: "ui." + id + ".other.test", Zone: "example.test"},
		{NameID: id, Name: "ui." + id + ".example.test", Zone: ".example.test"},
		{NameID: strings.ToUpper(id), Name: "ui." + strings.ToUpper(id) + ".example.test", Zone: "example.test"},
	} {
		fresh, err := NewMemory()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fresh.db.Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (1, '', 't', 'x', 0, '{}', 'c', 's')`); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(bad)
		evt := &ledger.Event{Seq: 1, Type: ledger.EventNetworkNameClaimed, Ring: 0, Timestamp: "2026-09-03T12:00:00Z", Payload: raw}
		if err := fresh.materializePublicName(evt); err == nil {
			t.Errorf("the materializer accepted a malformed claim: %+v", bad)
		}
		fresh.Close()
	}
}
