package ledger

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
func TestRewrapKeepsEverySeqAndFootnotesEarlierReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	lg, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]interface{}{
		"fingerprint": kp.Fingerprint(), "public_key": kp.PublicKeyB64(),
	}, kp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := lg.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{"id": "e", "content": "x"}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lg.Append(EventSystemWitnessed, kp.Fingerprint(), 0, map[string]interface{}{
		"receipt": map[string]interface{}{"ledger_ordinal": 3, "ledger_hash": lg.LastHash(), "witness_key_id": "k", "witness_sig_b64": "AAAA"},
	}, kp); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	if _, err := lg.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{"id": "cites", "content": "sees seq 2", "cites_seq": 2}, kp); err != nil {
		t.Fatal(err)
	}
	lg.Close()

	if n, err := EarlierReceipts(path); err != nil || n != 1 {
		t.Fatalf("EarlierReceipts = %d, %v; want 1", n, err)
	}
	n, err := Rewrap(path, kp, "", func(string) error { return nil })
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if n != 5 {
		t.Fatalf("rewrapped %d records, want 5 — nothing is dropped", n)
	}
	events, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if e.Seq != uint64(i+1) {
			t.Fatalf("record %d has seq %d — seqs must not move", i+1, e.Seq)
		}
	}
	fn := events[3]
	if fn.Type != EventSystemWitnessed {
		t.Fatalf("record 4 is %s, want the witnessed record kept in place", fn.Type)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(fn.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if _, has := payload["receipt"]; has {
		t.Fatal("the receipt survived as a receipt — it attests a shape that no longer exists")
	}
	if _, has := payload["receipt_before_rewrap"]; !has {
		t.Fatal("the footnote is missing")
	}
	if n, err := EarlierReceipts(path); err != nil || n != 0 {
		t.Fatalf("after the re-wrap EarlierReceipts = %d, %v; want 0", n, err)
	}
	if m, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || m != 5 {
		t.Fatalf("verify: n=%d err=%v", m, err)
	}
	// .
	if _, err := Rewrap(path, kp, "", func(string) error { return nil }); err != nil {
		t.Fatalf("second rewrap: %v", err)
	}
	events, _ = ReadAll(path)
	json.Unmarshal(events[3].Payload, &payload)
	if _, has := payload["receipt_before_rewrap"]; !has || len(payload) != 1 {
		t.Fatalf("the footnote changed on a second re-wrap: %s", events[3].Payload)
	}
}
