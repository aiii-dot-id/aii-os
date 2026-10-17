package app

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func TestAReplyThisProgramDoesNotRecogniseDoesNotFreezeTheIdentity(t *testing.T) {
	var clock sync.Mutex
	var read []time.Time
	lastTenth := time.Date(2027, 3, 1, 23, 59, 59, 900_000_000, time.UTC)
	prev := witnessNoticeNow
	t.Cleanup(func() { witnessNoticeNow = prev })
	witnessNoticeNow = func() time.Time {
		clock.Lock()
		defer clock.Unlock()
		at := lastTenth.Add(time.Duration(len(read)) * 250 * time.Millisecond)
		read = append(read, at)
		return at
	}

	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		t.Fatalf("the first anchor: %v", err)
	}
	anchoredAt := a.anchorer.LastAnchoredSeq()

	const page = "<html><body><h1>409 Conflict</h1>the gateway is busy</body></html>"
	f.witness.Refuses(http.StatusConflict, page)

	// What is proved is the outcome, not who brought it about: the anchorer
	// runs on its own clock as well, and either pass may meet the reply.
	var told []string
	for i := 0; i < 8 && len(told) == 0; i++ {
		appendFixtureEvent(t, a, f.keyPath, fmt.Sprintf("exp_while_a_gateway_answers_%d", i))
		_ = a.anchorer.CheckAndAnchor()
		for wait := 0; wait < 40 && len(told) == 0; wait++ {
			if ids, err := a.store.OutboxIDsUnder("witness_unrecognised_"); err == nil && len(ids) > 0 {
				told = ids
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	if len(told) == 0 {
		reason, safe := a.SafeMode()
		t.Fatalf("the operator was told nothing; conflict %v; SAFE %v (%s)", a.anchorer.IntegrityConflict(), safe, reason)
	}
	clock.Lock()
	if len(read) == 0 {
		clock.Unlock()
		t.Fatal("a notice was written and its clock was never read")
	}
	first := read[0]
	clock.Unlock()
	id, _ := unrecognisedWitnessReply(f.cfg.Witness.URL, &witness.ConflictError{Message: page}, first)
	if !slices.Equal(told, []string{id}) || !strings.HasSuffix(id, "_20270301") {
		t.Fatalf("THE NOTICE IS NOT FILED UNDER THE DAY THE CODE READ (%s): %q, want %s", first.Format(time.RFC3339Nano), told, id)
	}
	said, err := a.store.OutboxContent(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"does not recognise (a web page)", "not taken as evidence against the record, and the record was not checked against it", "keeps running", f.cfg.Witness.URL, "the gateway is busy"} {
		if !strings.Contains(said, want) {
			t.Errorf("the operator's notice does not say %q: %s", want, said)
		}
	}
	// Only what was checked is said: nothing in this path checks the
	// record, so nothing says it is intact.
	if strings.Contains(said, "intact") {
		t.Errorf("THE NOTICE SAYS THE RECORD IS INTACT, which nothing here checked: %s", said)
	}

	// A PAGE WHOSE WORDS CHANGE AT EVERY PASS IS ONE NOTICE. Each pass now
	// meets another page (a request id in it); the operator is told once
	// for the kind, and the notice quotes the first.
	for i := 0; i < 4; i++ {
		f.witness.Refuses(http.StatusConflict, fmt.Sprintf("<html><body>the gateway is busy, request %d</body></html>", i))
		appendFixtureEvent(t, a, f.keyPath, fmt.Sprintf("exp_while_pages_vary_%d", i))
		_ = a.anchorer.CheckAndAnchor()
	}
	if ids, err := a.store.OutboxIDsUnder("witness_unrecognised_"); err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("A PAGE WHOSE WORDS CHANGE MADE A NOTICE AT EVERY PASS: %q (%v), want only %s", ids, err, id)
	}
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("AN UNSIGNED REPLY THIS PROGRAM DOES NOT RECOGNISE PUT THE IDENTITY IN SAFE: %s", reason)
	}
	if ce := a.anchorer.IntegrityConflict(); ce != nil {
		t.Fatalf("the reply was latched as a conflict: %v", ce)
	}

	// The witness answers again: anchoring resumes by itself, with no
	// restart and nothing to clear.
	f.witness.Refuses(0, "")
	resumed := false
	for i := 0; i < 8 && !resumed; i++ {
		appendFixtureEvent(t, a, f.keyPath, fmt.Sprintf("exp_after_the_gateway_%d", i))
		_ = a.anchorer.CheckAndAnchor()
		resumed = a.anchorer.LastAnchoredSeq() > anchoredAt
	}
	if !resumed {
		t.Fatalf("anchoring did not resume once the witness answered again: still anchored at %d", a.anchorer.LastAnchoredSeq())
	}
}

// THE WITNESS'S WORD IS NOT SAID AS EVIDENCE CHECKED HERE. Its two integrity
// refusals (a rollback or a fork; a key it does not know for the identity)
// are lines of text in a 409 with nothing signed beside them. SAFE holds for
// them, and says they are the witness's word that this program has not
// checked; "holds signed evidence" is said only where a receipt the witness
// signed was verified here and contradicts the record (a local finding).
func TestTheWitnessesWordIsNotSaidAsEvidenceCheckedHere(t *testing.T) {
	for _, words := range []string{"witness bookmark rollback or fork", "identity public key does not match registered witness identity"} {
		ce := &witness.ConflictError{Message: words}
		if !ce.IsIntegrity() {
			t.Fatalf("rig: %q must be one of the witness's two integrity answers", words)
		}
		tr := witnessSafeTrigger(ce)
		for _, said := range []string{safePostures[tr].what, safePostures[tr].why} {
			if strings.Contains(said, "holds signed evidence") || !strings.Contains(said, "not checked") {
				t.Fatalf("THE WITNESS'S REFUSAL %q IS SAID AS EVIDENCE THIS PROGRAM CHECKED: %q", words, said)
			}
		}
	}
	if tr := witnessSafeTrigger(&witness.ConflictError{Local: true, Message: "receipt contradicts the record"}); tr != safeWitnessConflict || !strings.Contains(safePostures[tr].what, "holds signed evidence") {
		t.Fatalf("a contradiction found here in receipts the witness signed is not said as its signed evidence: trigger %d", tr)
	}
}

// A SECOND REPLY OF A KIND ON A DAY IS TOLD ALREADY, WHATEVER ITS WORDS. Its
// notice has the day's id and quotes another reply, and the once-notice
// refused it as holding other words: each flap of a gateway after a receipt
// or a restart logged that the operator's notice "could not be written",
// though it was. The second is a deliberate no-op, and says nothing. The
// day is the UTC day, whatever zone the clock is read in: a reply at 21:00
// five hours west of UTC is the next day's first, and one at midnight UTC
// is that same day's.
func TestASecondReplyOfAKindOnADayIsToldAlready(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	url := f.cfg.Witness.URL
	page := func(words string) *witness.ConflictError {
		return &witness.ConflictError{Message: "<html><body>" + words + "</body></html>"}
	}
	notices := func(t *testing.T, want ...string) {
		t.Helper()
		ids, err := a.store.OutboxIDsUnder("witness_unrecognised_")
		if err != nil || !slices.Equal(ids, want) {
			t.Fatalf("the notices are %q (%v), want %q", ids, err, want)
		}
	}
	quotes := func(t *testing.T, id, words string) {
		t.Helper()
		if content, err := a.store.OutboxContent(id); err != nil || !strings.Contains(content, words) {
			t.Fatalf("%s does not quote %q: %q %v", id, words, content, err)
		}
	}
	logs := logsink.CaptureForTest(t)
	lastSecond := time.Date(2026, 10, 14, 23, 59, 59, 0, time.UTC)

	a.tellUnrecognisedWitnessReply(url, page("busy, request 1"), lastSecond.Add(-12*time.Hour))
	a.tellUnrecognisedWitnessReply(url, page("busy, request 2"), lastSecond)
	notices(t, "witness_unrecognised_page_20261014")
	quotes(t, "witness_unrecognised_page_20261014", "busy, request 1")
	if strings.Contains(logs.String(), "could not be written") {
		t.Fatalf("A SECOND REPLY OF A KIND ON A DAY WAS SAID NOT TO BE WRITTEN:\n%s", logs.String())
	}

	a.tellUnrecognisedWitnessReply(url, page("busy, request 3"), time.Date(2026, 10, 14, 21, 0, 0, 0, time.FixedZone("five hours west", -5*60*60)))
	notices(t, "witness_unrecognised_page_20261014", "witness_unrecognised_page_20261015")
	quotes(t, "witness_unrecognised_page_20261015", "busy, request 3")
	a.tellUnrecognisedWitnessReply(url, page("busy, request 4"), lastSecond.Add(time.Second))
	notices(t, "witness_unrecognised_page_20261014", "witness_unrecognised_page_20261015")

	// Another kind on the same day is its own notice.
	a.tellUnrecognisedWitnessReply(url, &witness.ConflictError{Message: "the gateway is busy"}, lastSecond)
	notices(t, "witness_unrecognised_page_20261014", "witness_unrecognised_page_20261015", "witness_unrecognised_text_20261014")
	if strings.Contains(logs.String(), "could not be written") {
		t.Fatalf("a reply was said not to be written:\n%s", logs.String())
	}
}
