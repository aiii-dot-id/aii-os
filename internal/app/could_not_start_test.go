package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// could_not_start_test.go — a live boot that stops at a step outside the
// typed SAFE triggers. Each of these ended the process with one line and no
// page; each now comes up at the minimal SAFE page carrying its reason, with
// the claim held and nothing added to the record.

// recordedBy births an identity, builds its projection, and appends what
// more does through the raw ledger: validly signed, so the chain verifies,
// and never shown to admission.
func recordedBy(t *testing.T, name string, more func(lg *ledger.Ledger, kp *crypto.KeyPair) error) (dir, keyPath, ledgerPath, dbPath string) {
	t.Helper()
	dir = t.TempDir()
	keyPath, ledgerPath, dbPath = birthFixture(t, dir, name)
	buildPriorProjection(t, ledgerPath, dbPath)
	if more == nil {
		return
	}
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	err = more(lg, kp)
	lg.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.VerifyChain(ledgerPath, kp.PublicKeyBytes(), nil); err != nil {
		t.Fatalf("rig: the record must still verify (what was appended is validly signed): %v", err)
	}
	return
}

// safeAt boots and returns the SAFE reason, failing the test when the start
// ended instead.
func safeAt(t *testing.T, cfg *Config, ended string) (*App, string) {
	t.Helper()
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		stopApp(t, a)
		t.Fatalf("%s: %v", ended, err)
	}
	reason, safe := a.SafeMode()
	if !safe {
		stopApp(t, a)
		t.Fatal("rig: the start is neither SAFE nor ended")
	}
	return a, reason
}

// AN ENTRY THIS BINARY DOES NOT TAKE: here one of a kind it does not know,
// an older program on a record a newer one wrote. The record verifies; the
// page names the entry and says which version to run, in its own words, and
// the prior projection is still there to read.
func TestARecordHoldingAnEntryOfAKindThisBinaryDoesNotKnowBootsSafe(t *testing.T) {
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Newer", func(lg *ledger.Ledger, kp *crypto.KeyPair) error {
		_, err := lg.Append(ledger.EventType("a.kind.of.the.future"), 3, map[string]any{"id": "x"}, kp)
		return err
	})
	a, reason := safeAt(t, safebootConfig(t, dir, "Newer", keyPath, ledgerPath, dbPath),
		"AN OLDER PROGRAM ON A RECORD A NEWER ONE WROTE ENDED WITH ONE LINE AND NO PAGE")
	defer stopApp(t, a)
	for _, want := range []string{safePostures[safeEntryRefused].what, "(a.kind.of.the.future): unknown event type: a.kind.of.the.future", "run that version again"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not say %q: %s", want, reason)
		}
	}
	if strings.Contains(reason, safePostures[safeRecord].what) || strings.Contains(reason, "snapshot") {
		t.Errorf("a record that verified is said to have failed its checks, or its operator sent to a snapshot: %s", reason)
	}
	var rels int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&rels); err != nil || rels != 1 {
		t.Fatalf("the prior projection is not mounted as it was (relationships=%d, %v)", rels, err)
	}
}

// ANY OTHER RECORDED ENTRY THE MATERIALIZER'S RULES REFUSE takes the same
// page: here an intention created twice under one id, which the rules call
// a create that creates nothing. The page names the record it stands at.
func TestARecordedEntryTheMaterializerRefusesBootsSafe(t *testing.T) {
	twice := map[string]any{"id": "int_recorded_twice", "statement": "water the fig", "why": "it wilts"}
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Twice", func(lg *ledger.Ledger, kp *crypto.KeyPair) error {
		if _, err := lg.Append(ledger.EventIntentionCreate, 3, twice, kp); err != nil {
			return err
		}
		_, err := lg.Append(ledger.EventIntentionCreate, 3, twice, kp)
		return err
	})
	a, reason := safeAt(t, safebootConfig(t, dir, "Twice", keyPath, ledgerPath, dbPath),
		"A VERIFIED RECORD THE MATERIALIZER REFUSES ENDED THE PROCESS, where the same refusal while running enters SAFE")
	defer stopApp(t, a)
	for _, want := range []string{safePostures[safeEntryRefused].what, "(intention.create): "} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not say %q: %s", want, reason)
		}
	}
}

// A CAUSE OF THE MOMENT STILL ENDS THE START. A page does not start again,
// and a service does: the database held by another connection, a call
// interrupted, a resource the system is short of now. What somebody has to
// put right (a file this user may not read, a disk with no room) is what
// the page is for.
func TestACauseOfTheMomentIsNotHeldAtAPage(t *testing.T) {
	whereAnEndedStartIsStartedAgain(t)
	for _, tc := range []struct {
		errno  syscall.Errno
		passes bool
	}{
		{syscall.EAGAIN, true}, {syscall.EINTR, true},
		// A shortage of files or memory may last: it is tried again with
		// the page's backoff, not ended (passesWithTime).
		{syscall.EMFILE, false}, {syscall.ENFILE, false}, {syscall.ENOMEM, false},
		{syscall.EACCES, false}, {syscall.ENOSPC, false}, {syscall.ENOENT, false},
	} {
		err := stopped("open verified ledger", &os.PathError{Op: "open", Path: "ledger.jsonl", Err: tc.errno})
		if got := mayPassByItself(err); got != tc.passes {
			t.Errorf("%v: may pass by itself = %v, want %v", tc.errno, got, tc.passes)
		}
	}
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Moment", nil)
	a := New(safebootConfig(t, dir, "Moment", keyPath, ledgerPath, dbPath))
	defer stopApp(t, a)
	err := stopped("open verified ledger", &os.PathError{Op: "open", Path: ledgerPath, Err: syscall.EAGAIN})
	if a.couldNotStart(a.configSnapshot(), &err) || err == nil {
		t.Fatal("A START THAT MET A CAUSE OF THE MOMENT WAS HELD AT A PAGE: nothing starts it again")
	}
	if _, safe := a.SafeMode(); safe {
		t.Fatal("the start entered SAFE on a cause of the moment")
	}
}

// A STEP THAT IS NO FINDING ABOUT THE RECORD: here a provider entry that
// does not resolve. The start stops at the page with the step's own words,
// keeps the identity's claim, holds no record open, and mounts the database
// the replay had just rebuilt.
func TestAStartThatStopsAtAStepItCouldNotCompleteHoldsAPage(t *testing.T) {
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Stopped", nil)
	cfg := safebootConfig(t, dir, "Stopped", keyPath, ledgerPath, dbPath)
	cfg.LLM = LLMConfig{Provider: "an-entry-that-is-not-there", Model: "m"}
	a, reason := safeAt(t, cfg, "A START THAT COULD NOT RESOLVE ITS PROVIDER ENDED WITH ONE LINE AND NO PAGE, so Settings, where it is put right, could not be reached")
	defer stopApp(t, a)
	for _, want := range []string{safePostures[safeCouldNotStart].what, "LLM substrate: ", "restart"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not say %q: %s", want, reason)
		}
	}
	if _, err := (&App{}).claimIdentity(dir); !errors.Is(err, errIdentityRunning) {
		t.Errorf("THE PAGE IS SERVED WITHOUT THE IDENTITY'S CLAIM: a second start would open the record beside it (%v)", err)
	}
	if a.ledger != nil {
		t.Error("the page holds the record open")
	}
	if a.store == nil {
		t.Fatal("no database is mounted behind the page")
	}
	var rels int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&rels); err != nil || rels != 1 {
		t.Errorf("the database behind the page is not the identity's (relationships=%d, %v)", rels, err)
	}
	if a.rings.Ring0IsConstitution() {
		t.Error("Ring 0 behind the page is the constitution the stopped boot had sealed, not the platform's SAFE posture")
	}
}

// WHILE AN UPDATE'S ROLLBACK IS ARMED THE START ENDS, AS IT DID: a page does
// not restart, and the next start is what restores the previous program.
func TestAStartThatStopsEndsWhileAnUpdatesRollbackIsArmed(t *testing.T) {
	whereAnEndedStartIsStartedAgain(t)
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Updated", nil)
	backup := []byte("the previous binary")
	sum := sha256.Sum256(backup)
	if err := os.WriteFile(filepath.Join(dir, "aii.previous"), backup, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"attempts":0,"backup_sha256":"` + hex.EncodeToString(sum[:]) + `","new_sha256":"` + strings.Repeat("0", 64) + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".update_pending"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := safebootConfig(t, dir, "Updated", keyPath, ledgerPath, dbPath)
	cfg.LLM = LLMConfig{Provider: "an-entry-that-is-not-there", Model: "m"}
	a := New(cfg)
	err := startLiveForTest(a)
	_, safe := a.SafeMode()
	stopApp(t, a)
	if err == nil || safe {
		t.Fatalf("A START THAT STOPPED HELD A PAGE WHILE AN UPDATE'S ROLLBACK WAS ARMED: nothing restarts it, so nothing restores the previous program (err %v, safe %v)", err, safe)
	}
	if !strings.Contains(err.Error(), "LLM substrate: ") || !updates.RollbackArmed(dir) {
		t.Fatalf("the start must end on its step, with the rollback still armed: armed=%v err=%v", updates.RollbackArmed(dir), err)
	}
	next := &App{}
	if _, cerr := next.claimIdentity(dir); cerr != nil {
		t.Fatalf("the start that ended kept the identity's claim: %v", cerr)
	}
	next.releaseIdentityClaim()
}

// A START THAT STOPS AT A PAGE SAYS NOTHING OF THE ADDRESS BOOK. The lines
// of a loaded book the settings door would refuse are said once, at a live
// start, and a SAFE boot says nothing of them (contacts.go). The saying
// stood after the store and the settled restore, and the steps that now
// stop at a page come after that: a start over such a book that stopped at
// its provider wrote the notice and the log line, and served the page.
func TestAStartThatStopsAtAPageSaysNothingOfTheAddressBook(t *testing.T) {
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Booked", nil)
	cfg := safebootConfig(t, dir, "Booked", keyPath, ledgerPath, dbPath)
	cfg.Contacts = refusedBook()
	cfg.LLM = LLMConfig{Provider: "an-entry-that-is-not-there", Model: "m"}
	logs := logsink.CaptureForTest(t)
	a, _ := safeAt(t, cfg, "the rig must stop at the page")
	defer stopApp(t, a)
	if strings.Contains(logs.String(), "[contacts]") {
		t.Fatalf("A START THAT STOPPED AT A PAGE SPOKE OF THE ADDRESS BOOK in the log:\n%s", logs.String())
	}
	if said := doorNotices(t, a); len(said) != 0 {
		t.Fatalf("A START THAT STOPPED AT A PAGE WROTE A NOTICE OF THE ADDRESS BOOK: %q", said)
	}
}

// A RECORD THAT HOLDS NO BIRTH IS A FINDING ABOUT THE RECORD, not a step
// that could not complete. It verifies (every record is validly signed) and
// replays, and its first record is not a birth, so Ring 0 cannot be
// loaded from it. The page is the record's, which sends the operator to
// verify it and to a snapshot; never the page that tells the identity the
// step "is not a finding about your record".
func TestARecordThatHoldsNoBirthIsAFindingAboutTheRecord(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.SaveKeyPair(kp, keyPath); err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "exp_unborn", "content": "a record with no birth"}, kp); err != nil {
		t.Fatal(err)
	}
	lg.Close()
	// The store the start opens for writing is held, to see it closed.
	var opened []*store.Store
	prev := openForStartup
	t.Cleanup(func() { openForStartup = prev })
	openForStartup = func(ctx context.Context, path string, head store.MirrorHead, replay store.EventSource) (*store.Store, error) {
		st, err := prev(ctx, path, head, replay)
		if st != nil {
			opened = append(opened, st)
		}
		return st, err
	}
	a, reason := safeAt(t, safebootConfig(t, dir, "Unborn", keyPath, ledgerPath, dbPath),
		"A RECORD THAT HOLDS NO BIRTH ENDED THE START")
	defer stopApp(t, a)
	// THE DATABASE THE START OPENED FOR WRITING IS CLOSED AT THE PAGE. It
	// stayed open beside the read-only mount for the life of the process,
	// and on Windows nothing could remove or replace the file meanwhile.
	if len(opened) != 1 {
		t.Fatalf("rig: the start opened %d stores for writing, want 1", len(opened))
	}
	if _, err := opened[0].OutboxIDsUnder("any"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("THE DATABASE THE START OPENED FOR WRITING IS STILL OPEN AT THE SAFE PAGE: a read through it answered %v", err)
	}
	if !strings.Contains(reason, safePostures[safeRecord].what) || !strings.Contains(reason, "the record's birth could not be loaded: no ring0.genesis event found in ledger") {
		t.Fatalf("A RECORD THAT HOLDS NO BIRTH IS NOT SAID AS A FINDING ABOUT THE RECORD: %s", reason)
	}
	if strings.Contains(reason, safePostures[safeCouldNotStart].what) {
		t.Fatalf("a record with no birth is said to be a step that could not complete: %s", reason)
	}
}

// THE OPERATOR'S LINE IS ONE LINE when the teardown before the page failed
// too. startLive joins the teardown's failure to the step's (errors.Join,
// a line each), and the page, the beacon and every refusal the identity
// meets carry the reason as one line.
func TestAStopAndAFailedTeardownAreSaidOnOneLine(t *testing.T) {
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Torn", nil)
	a := New(safebootConfig(t, dir, "Torn", keyPath, ledgerPath, dbPath))
	defer stopApp(t, a)
	if _, err := a.claimIdentity(dir); err != nil {
		t.Fatal(err)
	}
	failed := errors.Join(stopped("LLM substrate", errors.New("no provider entry is named an-entry-that-is-not-there")), errors.New("plugins that would not stop: a plugin"))
	if !a.couldNotStart(a.configSnapshot(), &failed) || failed != nil {
		t.Fatalf("rig: the stopped step must hold the page: %v", failed)
	}
	reason, safe := a.SafeMode()
	if !safe || strings.Contains(reason, "\n") || !strings.Contains(reason, "LLM substrate: no provider entry is named an-entry-that-is-not-there; plugins that would not stop: a plugin") {
		t.Fatalf("THE OPERATOR'S LINE FOR A STOP WITH A FAILED TEARDOWN IS NOT ONE LINE THAT SAYS BOTH: %q", reason)
	}
}

// A START THAT STOPPED AT A PAGE IS NOT A COMPLETED BOOT. The boot-health
// marker (.boot_completed beside the record) tells the updater the last boot
// finished; writing it retires the update's backup and record, and an
// update's swap leaves it retired (.boot_completed.retired) until a boot
// finishes. A start that stopped at a page wrote it, so a page reached over
// an update whose first boot was not counted settled that update on a boot
// that never completed. Here a page for each of three causes: a provider
// that does not resolve, an entry this version does not take, a key that is
// not there. The marker stays unwritten and the retired one stays.
func TestAStartThatStopsAtAPageIsNotACompletedBoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger safeTrigger
		ready   func(t *testing.T, dir, keyPath string, cfg *Config)
	}{
		{"a provider that does not resolve", safeCouldNotStart, func(t *testing.T, dir, keyPath string, cfg *Config) {
			cfg.LLM = LLMConfig{Provider: "an-entry-that-is-not-there", Model: "m"}
		}},
		{"a key that is not there", safeKeyMissing, func(t *testing.T, dir, keyPath string, cfg *Config) {
			if err := os.Remove(keyPath); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Unfinished", nil)
			if err := os.WriteFile(filepath.Join(dir, ".boot_completed.retired"), []byte("ok"), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := safebootConfig(t, dir, "Unfinished", keyPath, ledgerPath, dbPath)
			tc.ready(t, dir, keyPath, cfg)
			a, reason := safeAt(t, cfg, "the rig must stop at the page")
			defer stopApp(t, a)
			if !strings.HasPrefix(reason, safePostures[tc.trigger].what) {
				t.Fatalf("rig: the page is not the one for %s: %s", tc.name, reason)
			}
			if fileExists(filepath.Join(dir, ".boot_completed")) {
				t.Fatalf("A START THAT STOPPED AT A PAGE (%s) TOLD THE UPDATER ITS BOOT COMPLETED: the marker is written, and with it the update's backup is retired", tc.name)
			}
			if !fileExists(filepath.Join(dir, ".boot_completed.retired")) {
				t.Fatal("the marker the swap retired was cleared by a start that did not complete")
			}
		})
	}
	// An entry this version does not take: the record as a newer version left it.
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Newer", func(lg *ledger.Ledger, kp *crypto.KeyPair) error {
		_, err := lg.Append(ledger.EventType("a.kind.of.the.future"), 3, map[string]any{"id": "x"}, kp)
		return err
	})
	a, _ := safeAt(t, safebootConfig(t, dir, "Newer", keyPath, ledgerPath, dbPath), "the rig must stop at the page")
	defer stopApp(t, a)
	if fileExists(filepath.Join(dir, ".boot_completed")) {
		t.Fatal("A START THAT MET AN ENTRY IT DOES NOT TAKE TOLD THE UPDATER ITS BOOT COMPLETED")
	}

	rec, recKey, recLedger, recDB := recordedBy(t, "Damaged", nil)
	tamperChain(t, recKey, recLedger)
	b, reason := safeAt(t, safebootConfig(t, rec, "Damaged", recKey, recLedger, recDB), "the rig must stop at the page")
	defer stopApp(t, b)
	if !strings.HasPrefix(reason, safePostures[safeRecord].what) || !fileExists(filepath.Join(rec, ".boot_completed")) {
		t.Fatalf("a SAFE boot over a damaged record no longer counts as a completed boot: %s", reason)
	}
}

// A REPLAY THAT DID NOT END AT THE VERIFIED RECORD IS A FINDING ABOUT THE
// RECORD (safeRecord), as an invalid reference is: the record verified to
// one end and the replay of it ended at another. Here the stream the replay
// reads stops one record short of the end the record verified to.
func TestAReplayThatDoesNotEndAtTheVerifiedRecordIsAFindingAboutTheRecord(t *testing.T) {
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Short", nil)
	prev := recordStream
	t.Cleanup(func() { recordStream = prev })
	recordStream = func(path string, yield func(*ledger.Event) error) error {
		var held *ledger.Event
		return prev(path, func(evt *ledger.Event) error {
			// Hand on each record one step late: the last is never handed.
			if held != nil {
				if err := yield(held); err != nil {
					return err
				}
			}
			e := *evt
			held = &e
			return nil
		})
	}
	a, reason := safeAt(t, safebootConfig(t, dir, "Short", keyPath, ledgerPath, dbPath),
		"A REPLAY THAT DID NOT END AT THE VERIFIED RECORD ENDED THE START")
	defer stopApp(t, a)
	if !strings.HasPrefix(reason, safePostures[safeRecord].what) || !strings.Contains(reason, "ledger replay did not end at the verified record: replay terminal boundary differs from verified record") {
		t.Fatalf("A REPLAY THAT DID NOT END AT THE VERIFIED RECORD IS NOT SAID AS A FINDING ABOUT THE RECORD: %s", reason)
	}
}

// A RECORD THAT DOES NOT NAME ITS KEY IS TOLD HOW THE KEY GOES BACK, and is
// offered no form. The key's door holds a key from an escrow file to the key
// the record's birth names; with none named the form could not be used, and
// the page offered it all the same, its route sending the operator to it.
// Here the record's first entry is not a birth, and the key is gone.
func TestARecordThatNamesNoKeyIsToldHowTheKeyGoesBack(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "exp_unnamed", "content": "a record that names no key"}, kp); err != nil {
		t.Fatal(err)
	}
	lg.Close()
	a, reason := safeAt(t, safebootConfig(t, dir, "Unnamed", keyPath, ledgerPath, dbPath), "A RECORD THAT NAMES NO KEY, WITH ITS KEY GONE, ENDED THE START")
	defer stopApp(t, a)
	if !strings.HasPrefix(reason, safePostures[safeKeyUnnamed].what) || !strings.Contains(reason, "the record does not name the key that signs it") || strings.Contains(reason, "Put the key back from your escrow file in Settings") {
		t.Fatalf("A RECORD THAT NAMES NO KEY IS SENT TO A FORM THAT CANNOT HOLD A KEY TO IT: %s", reason)
	}
	v := continuityDoor(a).View()
	if !v.KeyUnnamed || v.KeyAbsent {
		t.Fatalf("the page offers the key form, or does not say the record names no key: %+v", v)
	}
}

// A RECORD THAT DOES NOT READ IS NEVER SAID TO NAME NO KEY. Its key gone or
// unreadable, and the record unreadable too (a mistaken sudo left both
// root-owned): the page is the key's, with the record's own read error,
// never the page of a record whose first entry names no key, which tells
// the identity a falsehood about their record and withholds the form.
//
// What makes a file unreadable is the system's (makeUnreadable): its mode
// here; on Windows, where neither a mode nor a handle that shares nothing
// stopped the reads, the start's reads of the key and the record answer
// ERROR_ACCESS_DENIED through its seams.
func TestAnUnreadableRecordIsNeverSaidToNameNoKey(t *testing.T) {
	for _, tc := range []struct {
		name      string
		removeKey bool
	}{{"the key unreadable", false}, {"the key gone", true}} {
		t.Run(tc.name, func(t *testing.T) {
			catchRetries(t)
			dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Unread", nil)
			if tc.removeKey {
				if err := os.Remove(keyPath); err != nil {
					t.Fatal(err)
				}
			} else {
				makeUnreadable(t, keyPath)
			}
			everyReader := makeUnreadable(t, ledgerPath)
			a, reason := safeAt(t, safebootConfig(t, dir, "Unread", keyPath, ledgerPath, dbPath), "the start must hold a page")
			defer stopApp(t, a)
			trigger, _, _ := a.safeCause()
			if trigger != safeKeyMissing || strings.Contains(reason, "does not name") || !strings.Contains(reason, "the record does not read either") {
				t.Fatalf("A RECORD THAT DOES NOT READ IS SAID TO NAME NO KEY, or its read is not said: trigger %d, %s", trigger, reason)
			}
			if continuityDoor(a).View().KeyUnnamed {
				t.Fatal("the page says the record names no key")
			}
			// Nor does the key's door, in its refusal or in the log. The
			// door reads the record itself, so this half asks a record
			// every reader meets unreadable (makeUnreadable).
			if !everyReader {
				t.Log("the record is unreadable to the start alone on this system: the key's door reads it whole, and is not asked")
				return
			}
			logs := logsink.CaptureForTest(t)
			if err := continuityDoor(a).KeyBack([]byte("an escrow file"), []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "does not read far enough") {
				t.Fatalf("the key's door over a record that does not read: %v", err)
			}
			if strings.Contains(logs.String(), "does not name its key") || !strings.Contains(logs.String(), "the record does not read, so it cannot say whose key it needs") {
				t.Fatalf("THE LOG SAYS A RECORD THAT DOES NOT READ NAMES NO KEY:\n%s", logs.String())
			}
		})
	}
}

// THE RETRY READS THE RECORD'S OWN READ ERROR. A key that is gone (ENOENT)
// waits for a person, and a record that did not read at that moment (EIO
// from a disk, ESTALE from a network home) may pass with time: the two are
// joined, and the start is tried again by itself because of the record's
// half. The record's read is stood in for (namedKey): no file a test can
// make fails its read with EIO.
func TestAKeyGoneBesideARecordThatDidNotReadIsTriedAgain(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EIO, syscall.ESTALE} {
		t.Run(errno.Error(), func(t *testing.T) {
			caught := catchRetries(t)
			dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Unread", nil)
			if err := os.Remove(keyPath); err != nil {
				t.Fatal(err)
			}
			prev := namedKey
			t.Cleanup(func() { namedKey = prev })
			namedKey = func(path string) ([]byte, string, error) {
				return nil, "", fmt.Errorf("read ledger: %w", &os.PathError{Op: "read", Path: path, Err: errno})
			}
			a, reason := safeAt(t, safebootConfig(t, dir, "Unread", keyPath, ledgerPath, dbPath), "the start must hold a page")
			defer stopApp(t, a)
			if trigger, _, _ := a.safeCause(); trigger != safeKeyMissing || !strings.Contains(reason, "the record does not read either") {
				t.Fatalf("rig: the key's page, with the record's read said: trigger %d, %s", trigger, reason)
			}
			if len(caught.delays) != 1 || !strings.Contains(reason, "this start is tried again by itself at ") {
				t.Fatalf("A RECORD THAT DID NOT READ AT THAT MOMENT, BESIDE A KEY THAT IS GONE, WAITS FOR A PERSON: the retry read the key's error alone: %v %s", caught.delays, reason)
			}
		})
	}
}

// A STALE HOME AT THE KEY IS TRIED AGAIN. The key's read fails as a network
// home gone stale does, and the record's place cannot be inspected either
// (the same mount): the start cannot tell a lost key from a lost home, and
// stops at a step it could not complete, tried again by itself, the page
// saying when. It ended with one line. The error is the system's own for a
// network home gone (goneNetworkHome): ESTALE here, ERROR_NETNAME_DELETED on
// Windows; the key's read and the record's place are stood in for, since no
// mode makes a place uninspectable on Windows.
func TestAStaleHomeAtTheKeyIsTriedAgain(t *testing.T) {
	caught := catchRetries(t)
	stale := func(path string) error { return &os.PathError{Op: "open", Path: path, Err: goneNetworkHome} }
	prevKey, prevPlace := loadKeyPair, recordPlace
	t.Cleanup(func() { loadKeyPair, recordPlace = prevKey, prevPlace })
	loadKeyPair = func(path string) (*crypto.KeyPair, error) { return nil, stale(path) }
	recordPlace = func(path string) (string, error) {
		return "", &os.PathError{Op: "lstat", Path: path, Err: goneNetworkHome}
	}
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Stale", nil)
	cfg := safebootConfig(t, dir, "Stale", keyPath, ledgerPath, dbPath)
	a := New(cfg)
	defer stopApp(t, a)
	if _, err := a.claimIdentity(filepath.Dir(ledgerPath)); err != nil {
		t.Fatal(err)
	}
	if err := a.startLive(); err != nil {
		t.Fatalf("A START WHOSE KEY AND RECORD'S PLACE WENT STALE ENDED, with no page and no retry: %v", err)
	}
	reason, safe := a.SafeMode()
	if !safe || !strings.HasPrefix(reason, safePostures[safeCouldNotStart].what) || !strings.Contains(reason, goneNetworkHome.Error()) || len(caught.delays) != 1 || !strings.Contains(reason, "tried again by itself") {
		t.Fatalf("the stale home is not held at the page with a retry: safe=%v armed=%v %s", safe, caught.delays, reason)
	}
}
