package witness

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
)

// A REPLY THIS CLIENT DOES NOT RECOGNISE IS TOLD ONCE FOR ITS KIND, until
// the witness answers again. The anchorer told the message when it differed
// from the one last told, so a page that carries a request id or the time
// was told at every pass. Here the replies' words change at every pass:
// pages and plain text, alternating. Each kind is told once, the first of
// it; once the witness has answered, the same kind is told again.
func TestAnUnrecognisedReplyIsToldOnceForItsKind(t *testing.T) {
	fw := newFakeWitness(t)
	replies := []string{
		"a gateway is busy (request 1)", "<html>busy, request 2</html>", "a gateway is busy (request 3)",
		"<html>busy, request 4</html>", "<html>busy, request 5</html>", "a gateway is busy (request 6)",
	}
	var at atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/witness/pubkey/hash", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]string{"witness_public_key_hash": sha256Prefixed(mustCanonical(t, fw.witnessEnv)), "key_id": fw.witnessEnv.KeyID})
	})
	mux.HandleFunc("/witness/pubkey", func(w http.ResponseWriter, r *http.Request) { writeTestJSON(w, fw.witnessEnv) })
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"max_range_entries": 4096})
	})
	mux.HandleFunc("/witness/bookmark", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, replies[(at.Add(1)-1)%int64(len(replies))], http.StatusConflict)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	lg, kp := testLedger(t, 3)
	var told []string
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	a.SetOnUnrecognisedConflict(func(ce *ConflictError) { told = append(told, ce.Message) })
	pass := func() {
		t.Helper()
		var ce *ConflictError
		if err := a.CheckAndAnchor(); !errors.As(err, &ce) || !ce.Unrecognised() {
			t.Fatalf("the reply must come back as a conflict this client does not recognise: %v", err)
		}
	}
	for range replies {
		pass()
	}
	want := []string{"a gateway is busy (request 1)", "<html>busy, request 2</html>"}
	if !slices.Equal(told, want) {
		t.Fatalf("A REPLY WHOSE WORDS CHANGE WAS TOLD %d TIMES in %d passes: %q, want once for each kind: %q", len(told), len(replies), told, want)
	}
	// The witness answers: a reply of a kind already told is told again.
	a.anchored(WitnessReceipt{LedgerOrdinal: 1})
	pass()
	if len(told) != 3 || told[2] != "a gateway is busy (request 1)" {
		t.Fatalf("after the witness answered, the next unrecognised reply was not told: %q", told)
	}
}

// The kind of a reply is its form, not its words.
func TestTheKindOfAReplyIsItsForm(t *testing.T) {
	for _, tc := range []struct {
		body string
		want ReplyKind
	}{
		{`{"error":"words this client does not know"}`, ReplyInWitnessForm},
		{"", ReplyEmpty},
		{"  <!DOCTYPE html><html>busy</html>", ReplyPage},
		{`{"message":"busy"}`, ReplyJSON},
		{"busy", ReplyText},
	} {
		if got := newConflictError([]byte(tc.body)).Kind(); got != tc.want {
			t.Errorf("%q is %q, want %q", tc.body, got, tc.want)
		}
	}
}
