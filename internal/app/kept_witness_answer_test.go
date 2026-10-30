package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// keepWitnessAnswer writes the witness's answer beside the ledger, as the
// anchorer keeps it when it latches one (internal/witness).
func keepWitnessAnswer(t *testing.T, ledgerPath string, kc witness.KeptConflict) {
	t.Helper()
	raw, err := json.Marshal(kc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(witness.ConflictPath(filepath.Dir(ledgerPath)), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAKeptWitnessAnswerHoldsTheBootSafe(t *testing.T) {
	const at = "http://127.0.0.1:1"
	for _, tc := range []struct {
		name    string
		kept    string // the file's bytes; "" writes answer
		answer  witness.KeptConflict
		url     string
		trigger safeTrigger // 0: the boot runs
		said    string
	}{
		{name: "the witness's word", url: at, trigger: safeWitnessSays, said: "witness bookmark rollback or fork",
			answer: witness.KeptConflict{WitnessURL: at, Answer: "witness conflict (witness 409): witness bookmark rollback or fork", Record: 3, KeptAt: "2026-10-20T12:00:00Z"}},
		{name: "evidence checked here", url: at + "/", trigger: safeWitnessConflict, said: "witness state fork",
			answer: witness.KeptConflict{WitnessURL: at, Local: true, Answer: "witness conflict (local witness-state check): witness state fork", Record: 3, KeptAt: "2026-10-20T12:00:00Z"}},
		{name: "an answer that does not read", url: at, trigger: safeWitnessSays, said: "does not read", kept: "{"},
		{name: "taken off the witness", url: "",
			answer: witness.KeptConflict{WitnessURL: at, Answer: "witness conflict (witness 409): witness bookmark rollback or fork", Record: 3, KeptAt: "2026-10-20T12:00:00Z"}},
		{name: "another witness", url: "http://127.0.0.1:2",
			answer: witness.KeptConflict{WitnessURL: at, Answer: "witness conflict (witness 409): witness bookmark rollback or fork", Record: 3, KeptAt: "2026-10-20T12:00:00Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Disputed", nil)
			if tc.kept != "" {
				if err := os.WriteFile(witness.ConflictPath(dir), []byte(tc.kept), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				keepWitnessAnswer(t, ledgerPath, tc.answer)
			}
			cfg := safebootConfig(t, dir, "Disputed", keyPath, ledgerPath, dbPath)
			cfg.Witness.URL = tc.url
			a := New(cfg)
			if err := startLiveForTest(a); err != nil {
				t.Fatal(err)
			}
			defer stopApp(t, a)
			reason, safe := a.SafeMode()
			if tc.trigger == 0 {
				if safe || !a.live.Load() {
					t.Fatalf("an answer that is not the configured witness's held the boot (safe %v): %s", safe, reason)
				}
				return
			}
			trigger, _, _ := a.safeCause()
			if !safe || a.live.Load() || trigger != tc.trigger {
				t.Fatalf("A WITNESS'S ANSWER KEPT BESIDE THE LEDGER DID NOT HOLD THE BOOT: safe %v, live %v, trigger %d", safe, a.live.Load(), trigger)
			}
			if !strings.HasPrefix(reason, safePostures[tc.trigger].what+": ") || !strings.Contains(reason, witness.ConflictFileName) || !strings.Contains(reason, tc.said) {
				t.Fatalf("the reason does not say the answer is kept, and what it was: %s", reason)
			}
			if ring0 := a.rings.GetContent(ring.Ring0); !strings.Contains(ring0, safePostures[tc.trigger].why) || !strings.Contains(ring0, tc.said) {
				t.Fatalf("the resident is not told why:\n%s", ring0)
			}
			if a.anchorer != nil || a.timeFac != nil {
				t.Fatal("the SAFE boot wired the anchorer or TIME")
			}
		})
	}
}

// A RESTORE SETS THE KEPT ANSWER ASIDE WITH THE RECORD IT CONCERNED: the
// operator's route out of a witness's SAFE is a restore, and an answer left
// behind would hold the restored record SAFE for an answer about another.
func TestARestoreSetsTheWitnessesKeptAnswerAsideWithItsRecord(t *testing.T) {
	r := newRestoreRig(t)
	_, snap := r.lived(t, 2, "before", true)
	r.cfg.Witness.URL = "http://127.0.0.1:1"
	keepWitnessAnswer(t, r.ledgerPath, witness.KeptConflict{WitnessURL: r.cfg.Witness.URL, Answer: "witness conflict (witness 409): witness bookmark rollback or fork", Record: 3, KeptAt: "2026-10-20T12:00:00Z"})
	held := New(r.cfg)
	if err := startLiveForTest(held); err != nil {
		t.Fatal(err)
	}
	if trigger, _, safe := held.safeCause(); !safe || trigger != safeWitnessSays {
		stopApp(t, held)
		t.Fatalf("rig: the kept answer did not hold the boot (trigger %d)", trigger)
	}
	plan, err := continuityDoor(held).RestorePlan(continuity.SourceSnapshot, snap)
	if err != nil {
		stopApp(t, held)
		t.Fatal(err)
	}
	if err := continuityDoor(held).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
		stopApp(t, held)
		t.Fatalf("the restore asked for from the witness's SAFE: %v", err)
	}
	stopApp(t, held)

	a := r.boot(t) // fails the test if it is SAFE
	defer stopApp(t, a)
	if _, err := os.Lstat(witness.ConflictPath(r.dir)); !os.IsNotExist(err) {
		t.Fatalf("the answer about the record set aside is still beside the restored one: %v", err)
	}
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 {
		t.Fatalf("the pair the answer was about must be set aside: %+v", sets)
	}
	if kept, err := witness.ReadKeptConflict(sets[0].Path); err != nil || kept == nil || !kept.Of(r.cfg.Witness.URL) {
		t.Fatalf("the answer did not go aside with its record: %+v (%v)", kept, err)
	}
}

func TestAHeldStartAsksTheWitnessAgainAndStaysHeldWhenItNoLongerShowsTheFork(t *testing.T) {
	f := newSealedFixture(t)
	anchoredThrough := f.bootAndAnchor(t)
	keepWitnessAnswer(t, f.ledgerPath, witness.KeptConflict{WitnessURL: f.cfg.Witness.URL, Answer: "witness conflict (witness 409): witness bookmark rollback or fork", Record: anchoredThrough, KeptAt: "2026-10-20T12:00:00Z"})

	// The witness refuses again: SAFE holds, the answer is kept again.
	f.witness.Refuses(http.StatusConflict, `{"error":"witness bookmark rollback or fork"}`)
	logs := logsink.CaptureForTest(t)
	held := New(f.cfg)
	if err := startLiveForTest(held); err != nil {
		t.Fatal(err)
	}
	if trigger, _, safe := held.safeCause(); !safe || trigger != safeWitnessSays {
		stopApp(t, held)
		t.Fatalf("rig: the kept answer did not hold the boot (trigger %d)", trigger)
	}
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(logs.String(), "still shows the fork or did not answer") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	stopApp(t, held)
	if !strings.Contains(logs.String(), "still shows the fork or did not answer") || held.restarting.Load() {
		t.Fatalf("a witness that refused again was not asked, or let the start go (restarting %v):\n%s", held.restarting.Load(), logs.String())
	}
	if kept, err := witness.ReadKeptConflict(f.dir); err != nil || kept == nil {
		t.Fatalf("a refusal asked again lifted the kept answer: %+v (%v)", kept, err)
	}

	// The witness's row is put right: the answer no longer shows the fork.
	// The page is told, and the start stays held.
	f.witness.Refuses(0, "")
	again := New(f.cfg)
	if err := startLiveForTest(again); err != nil {
		t.Fatal(err)
	}
	entered, _ := again.SafeMode()
	const told = "no longer shows that this record moved back or split"
	page := func() string {
		cont, err := again.continuityState()
		if err != nil {
			return err.Error()
		}
		raw, _ := json.Marshal(cont)
		return string(raw)
	}
	for !strings.Contains(page(), told) && !again.restarting.Load() && time.Now().Before(deadline.Add(20*time.Second)) {
		time.Sleep(20 * time.Millisecond)
	}
	restarted := again.restarting.Load()
	reason, safe := again.SafeMode()
	shown := page()
	at := again.ledger.LastSeq()
	stopApp(t, again)
	if restarted {
		t.Fatal("A HELD START WHOSE WITNESS NO LONGER SHOWS THE FORK RESTARTED ITSELF: SAFE left by itself")
	}
	if strings.Contains(entered, "made again") || strings.Contains(entered, "asks the witness again") {
		t.Fatalf("the reason the identity reads, and every refusal quotes, says the start leaves SAFE by itself: %s", entered)
	}
	if !safe || reason != entered {
		t.Fatalf("the start did not stay held under the reason it entered with (safe %v):\n%s\nwas\n%s", safe, reason, entered)
	}
	if !strings.Contains(shown, told) || !strings.Contains(shown, "restart the identity, and the start takes the record through its checks") {
		t.Fatalf("the page is not told that the witness no longer shows the fork and that a restart takes the start through its checks: %s", shown)
	}
	if _, err := os.Lstat(witness.ConflictPath(f.dir)); !os.IsNotExist(err) {
		t.Fatalf("the kept answer still holds the record: %v", err)
	}
	aside, _ := filepath.Glob(filepath.Join(f.dir, witness.ClearedPrefix+"*.json"))
	if len(aside) != 1 || !strings.Contains(shown, filepath.Base(aside[0])) {
		t.Fatalf("the kept answer was not set aside beside the ledger with its time, or the page does not name it: %v", aside)
	}
	if at != anchoredThrough {
		t.Fatalf("the held start appended to the record: it ends at %d, was %d", at, anchoredThrough)
	}

	// The operator restarts: that start runs and carries the witness's
	// receipt.
	runs := New(f.cfg)
	if err := startLiveForTest(runs); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, runs)
	if reason, safe := runs.SafeMode(); safe {
		t.Fatalf("the start the operator made is SAFE: %s", reason)
	}
	if runs.ledger.LastSeq() <= anchoredThrough {
		t.Fatalf("the receipt kept while held was not minted by the running identity: the record ends at %d", runs.ledger.LastSeq())
	}
}

// HOW OFTEN A HELD START ASKS THE WITNESS IS THE OPERATOR'S: one setting,
// its range refused at load by the file's key and at the settings door by
// the door's, an hour where nothing is named.
func TestHowOftenAHeldStartAsksTheWitnessIsASetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for body, ok := range map[string]bool{`{}`: true, `{"witness_server":{"ask_again_seconds":60}}`: true, `{"witness_server":{"ask_again_seconds":86400}}`: true,
		`{"witness_server":{"ask_again_seconds":59}}`: false, `{"witness_server":{"ask_again_seconds":86401}}`: false, `{"witness_server":{"ask_again_seconds":-1}}`: false} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadConfig(path)
		if ok != (err == nil) || (err != nil && !strings.Contains(err.Error(), "witness_server.ask_again_seconds must be zero")) {
			t.Errorf("%s: loaded = %v: %v", body, err == nil, err)
		}
	}
	if got := witnessAskAgain(WitnessConfig{}); got != time.Hour {
		t.Fatalf("the default wait between asks is %v, not an hour", got)
	}
	cfg := defaultConfig()
	cfg.SourcePath = path
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if _, err := a.applyConfigChange(map[string]any{"witness.ask_again_seconds": 30.0}); err == nil || !strings.Contains(err.Error(), "witness.ask_again_seconds must be zero") {
		t.Errorf("the door must refuse 30 s by its own key: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{"witness.ask_again_seconds": 600.0}); err != nil {
		t.Fatalf("the door refused a number in the range: %v", err)
	}
	if kept, err := LoadConfig(path); err != nil || witnessAskAgain(kept.Witness) != 10*time.Minute {
		t.Fatalf("what the door took is not what the next start reads: %+v, %v", kept.Witness, err)
	}
}
