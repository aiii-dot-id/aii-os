package ledger

import (
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestTheRecordHasExactlyTheseMembers(t *testing.T) {
	evt := &Event{
		Seq: 7, Prev: "prev-probe", Timestamp: "2026-08-30T00:00:00Z", Type: EventBeliefUpsert,
		Ring: 3, Content: "content-probe", Payload: json.RawMessage(`{"id":"probe","statement":"present"}`), Sig: "sig-probe",
	}
	line, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"entry", "payload", "sig"} {
		if _, ok := record[want]; !ok {
			t.Errorf("record lacks %q", want)
		}
	}
	if len(record) != 3 {
		t.Errorf("record carries %d members, want exactly entry, payload, sig", len(record))
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(record["entry"], &entry); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"content", "prev", "ring", "seq", "ts", "type"} {
		if _, ok := entry[want]; !ok {
			t.Errorf("entry lacks %q", want)
		}
	}
	if len(entry) != 6 {
		t.Errorf("entry carries %d members, want exactly six", len(entry))
	}
	canon, err := canonicaljson.CanonicalizeV1(line)
	if err != nil {
		t.Fatal(err)
	}
	if string(canon) != string(line) {
		t.Fatalf("a written record is not in canonical form:\n got %s\nwant %s", line, canon)
	}

	var back Event
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatalf("a written record does not read back: %v", err)
	}
	again, err := json.Marshal(&back)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(line) {
		t.Fatalf("read-then-write changed the bytes:\n got %s\nwant %s", again, line)
	}

	// .
	evt.Sig = ""
	sealed, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	if string(sealed) != `{"entry":`+string(evt.EntryBytes())+`,"payload":`+string(evt.Payload)+`}` {
		t.Fatalf("sealed record shape: %s", sealed)
	}
	var sealedBack Event
	if err := json.Unmarshal(sealed, &sealedBack); err != nil {
		t.Fatalf("a sealed record does not read back: %v", err)
	}
	if sealedBack.Sig != "" || sealedBack.EntryHash() != evt.EntryHash() {
		t.Fatal("a sealed record read back is not the same record")
	}
}
