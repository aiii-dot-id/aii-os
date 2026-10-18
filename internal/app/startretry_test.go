package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// whereAnEndedStartIsStartedAgain stands the test where a service manager
// starts an ended start again (Linux, macOS), whatever it runs on: a test
// of a start that ends asks for it, and on Windows, where nothing starts
// it again, such a start holds the page instead (endedStartsRestart).
func whereAnEndedStartIsStartedAgain(t *testing.T) {
	t.Helper()
	prev := endedStartsRestart
	t.Cleanup(func() { endedStartsRestart = prev })
	endedStartsRestart = true
}

// A cause that may pass with time, though not at once: a disk read that
// failed, a network home gone stale. Not a cause of the moment (that ends
// the start), and not one a person has to put right.
func TestWhichCausesMayPassWithTime(t *testing.T) {
	for _, tc := range []struct {
		errno syscall.Errno
		may   bool
	}{
		{syscall.EIO, true}, {syscall.ESTALE, true}, {syscall.EMFILE, true}, {syscall.ENFILE, true}, {syscall.ENOMEM, true},
		{syscall.EACCES, false}, {syscall.ENOSPC, false}, {syscall.ENOENT, false}, {syscall.EAGAIN, false},
	} {
		err := stopped("open verified ledger", &os.PathError{Op: "open", Path: "ledger.jsonl", Err: tc.errno})
		if got := passesWithTime(err); got != tc.may {
			t.Errorf("%v: may pass with time = %v, want %v", tc.errno, got, tc.may)
		}
	}
}

// armedRetries stands in for the timer a page arms: it keeps each delay and
// the act, and fires nothing.
type armedRetries struct {
	delays []time.Duration
	acts   []func()
}

func catchRetries(t *testing.T) *armedRetries {
	t.Helper()
	caught := &armedRetries{}
	prev := startRetryAfter
	t.Cleanup(func() { startRetryAfter = prev })
	startRetryAfter = func(d time.Duration, f func()) *time.Timer {
		caught.delays = append(caught.delays, d)
		caught.acts = append(caught.acts, f)
		return time.NewTimer(time.Hour)
	}
	return caught
}

// A START HELD AT ITS PAGE BY A CAUSE THAT MAY PASS WITH TIME IS TRIED AGAIN
// BY ITSELF. The page waited for a person whatever held it, so a network
// home that came back a minute later stayed down until someone restarted
// the identity. The page now says when the start is tried again, and the
// retry is the restart the page's own button asks for, carrying which retry
// it is; each waits twice as long as the one before, up to the limit, and
// past it the page says it is not tried again. A cause that is not of that
// kind holds the page, as before.
func TestAStartHeldByACauseThatMayPassIsTriedAgainByItself(t *testing.T) {
	transient := func() error {
		return stopped("open verified ledger", &os.PathError{Op: "open", Path: "ledger.jsonl", Err: syscall.EIO})
	}
	page := func(t *testing.T, attempt int, failed error) (*App, string) {
		t.Helper()
		dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Retried", nil)
		a := New(safebootConfig(t, dir, "Retried", keyPath, ledgerPath, dbPath))
		t.Cleanup(func() { stopApp(t, a) })
		if _, err := a.claimIdentity(dir); err != nil {
			t.Fatal(err)
		}
		a.startAttempt = attempt
		if !a.couldNotStart(a.configSnapshot(), &failed) {
			t.Fatalf("rig: the step must hold the page: %v", failed)
		}
		reason, _ := a.SafeMode()
		return a, reason
	}

	t.Run("the first retry", func(t *testing.T) {
		caught := catchRetries(t)
		a, reason := page(t, 0, transient())
		if len(caught.delays) != 1 || caught.delays[0] != defaultStartRetryInterval {
			t.Fatalf("A START HELD BY A CAUSE THAT MAY PASS ARMED NO RETRY, or not after the interval: %v", caught.delays)
		}
		if !strings.Contains(reason, "; this start is tried again by itself at ") || !strings.Contains(reason, "(retry 1 of 6), as the cause may pass with time") {
			t.Fatalf("the page does not say when the start is tried again: %s", reason)
		}
		t.Setenv(startRetryEnv, "")
		caught.acts[0]()
		if os.Getenv(startRetryEnv) != "1" || !a.restarting.Load() {
			t.Fatalf("the retry did not ask for the restart carrying which retry it is: %q, restarting %v", os.Getenv(startRetryEnv), a.restarting.Load())
		}
	})
	t.Run("the third retry waits four intervals", func(t *testing.T) {
		caught := catchRetries(t)
		_, reason := page(t, 2, transient())
		if len(caught.delays) != 1 || caught.delays[0] != 4*defaultStartRetryInterval || !strings.Contains(reason, "(retry 3 of 6)") {
			t.Fatalf("the third retry is armed after %v, said %s", caught.delays, reason)
		}
	})
	t.Run("past the limit", func(t *testing.T) {
		caught := catchRetries(t)
		_, reason := page(t, 6, transient())
		if len(caught.delays) != 0 || !strings.Contains(reason, "; it was tried again by itself 6 time(s) (identity.start_retry_limit), and is not tried again") {
			t.Fatalf("a start past its retries armed another, or the page does not say it is not tried again: %v %s", caught.delays, reason)
		}
	})
	t.Run("a cause a person puts right", func(t *testing.T) {
		caught := catchRetries(t)
		_, reason := page(t, 0, stopped("open verified ledger", &os.PathError{Op: "open", Path: "ledger.jsonl", Err: syscall.EACCES}))
		if len(caught.delays) != 0 || strings.Contains(reason, "tried again") {
			t.Fatalf("a start held by a cause a person puts right was armed to try again: %v %s", caught.delays, reason)
		}
	})
}

// A KEY FILE THAT DOES NOT READ AT THIS MOMENT is tried again too, through
// the live start, which takes which retry it is from the environment; a key
// that is not there holds the page for a person.
func TestAKeyThatDoesNotReadAtThisMomentIsTriedAgain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause syscall.Errno
		retry bool
	}{
		{"a read the disk failed", syscall.EIO, true},
		{"a key that is not there", syscall.ENOENT, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caught := catchRetries(t)
			prev := loadKeyPair
			t.Cleanup(func() { loadKeyPair = prev })
			loadKeyPair = func(path string) (*crypto.KeyPair, error) {
				return nil, &os.PathError{Op: "open", Path: path, Err: tc.cause}
			}
			t.Setenv(startRetryEnv, "1")
			dir, keyPath, ledgerPath, dbPath := recordedBy(t, "KeyRead", nil)
			a, reason := safeAt(t, safebootConfig(t, dir, "KeyRead", keyPath, ledgerPath, dbPath), "the rig must stop at the key's page")
			defer stopApp(t, a)
			if !strings.HasPrefix(reason, safePostures[safeKeyMissing].what) {
				t.Fatalf("rig: not the key's page: %s", reason)
			}
			if _, set := os.LookupEnv(startRetryEnv); set {
				t.Fatal("the start left which retry it was in its environment")
			}
			if tc.retry && (len(caught.delays) != 1 || caught.delays[0] != 2*defaultStartRetryInterval || !strings.Contains(reason, "(retry 2 of 6)")) {
				t.Fatalf("A KEY FILE THAT DID NOT READ AT THIS MOMENT HELD THE PAGE FOR A PERSON: armed %v, said %s", caught.delays, reason)
			}
			if !tc.retry && (len(caught.delays) != 0 || strings.Contains(reason, "tried again")) {
				t.Fatalf("a key that is not there was armed to try again: %v %s", caught.delays, reason)
			}
		})
	}
}

// A stop disarms the retry a page armed: nothing restarts an identity that
// was stopped.
func TestAStopDisarmsTheRetryAPageArmed(t *testing.T) {
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Stopped", nil)
	a := New(safebootConfig(t, dir, "Stopped", keyPath, ledgerPath, dbPath))
	if _, err := a.claimIdentity(dir); err != nil {
		t.Fatal(err)
	}
	failed := stopped("open verified ledger", &os.PathError{Op: "open", Path: filepath.Join(dir, "ledger.jsonl"), Err: syscall.ESTALE})
	if !a.couldNotStart(a.configSnapshot(), &failed) || a.startRetryTimer == nil {
		t.Fatalf("rig: a page with a retry armed: %v", failed)
	}
	stopApp(t, a)
	if a.startRetryTimer.Stop() {
		t.Fatal("A RETRY OUTLIVED THE STOP: it would restart a stopped identity")
	}
}

// identity.start_retry_seconds and identity.start_retry_limit are the
// retry's numbers: absent for the defaults, zero seconds for the default
// interval, a limit of zero for no retry at all (and the page says so), and
// refused past their bounds or negative, by their keys. A limit of zero
// survives a save: it is the operator's "off", not "absent".
func TestTheStartRetrysNumbersAreTheOperators(t *testing.T) {
	a := New(defaultConfig())
	if a.startRetryInterval() != 30*time.Second || a.startRetryLimit() != 6 {
		t.Fatalf("the defaults are %s and %d, want 30s and 6", a.startRetryInterval(), a.startRetryLimit())
	}
	path := filepath.Join(t.TempDir(), "config.json")
	load := func(t *testing.T, body string) (*Config, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(path)
	}
	cfg, err := load(t, `{"identity":{"start_retry_seconds":10,"start_retry_limit":3}}`)
	if err != nil {
		t.Fatal(err)
	}
	if b := New(cfg); b.startRetryInterval() != 10*time.Second || b.startRetryLimit() != 3 {
		t.Fatalf("the file says 10s and 3, the start takes %s and %d", b.startRetryInterval(), b.startRetryLimit())
	}
	cfg, err = load(t, fmt.Sprintf(`{"identity":{"start_retry_seconds":%d,"start_retry_limit":%d}}`, maxStartRetrySeconds, maxStartRetryLimit))
	if err != nil {
		t.Fatalf("the largest values must load: %v", err)
	}
	if b := New(cfg); b.startRetryInterval() != 24*time.Hour || b.startRetryLimit() != 100 {
		t.Fatalf("the file says a day and 100, the start takes %s and %d", b.startRetryInterval(), b.startRetryLimit())
	}

	t.Run("a limit of zero is no retry, and the page says so", func(t *testing.T) {
		cfg, err := load(t, `{"identity":{"start_retry_limit":0}}`)
		if err != nil {
			t.Fatal(err)
		}
		if n := New(cfg).startRetryLimit(); n != 0 {
			t.Fatalf("A LIMIT OF ZERO WAS TAKEN FOR THE DEFAULT: %d retries", n)
		}
		cfg.SourcePath = path
		if _, err := saveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		saved, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if n := New(saved).startRetryLimit(); n != 0 {
			t.Fatalf("A SAVE DROPPED THE LIMIT OF ZERO: read back as %d retries", n)
		}
		caught := catchRetries(t)
		dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Off", nil)
		c := safebootConfig(t, dir, "Off", keyPath, ledgerPath, dbPath)
		c.Identity.StartRetryLimit = saved.Identity.StartRetryLimit
		b := New(c)
		defer stopApp(t, b)
		if _, err := b.claimIdentity(dir); err != nil {
			t.Fatal(err)
		}
		failed := stopped("open verified ledger", &os.PathError{Op: "open", Path: ledgerPath, Err: syscall.EIO})
		if !b.couldNotStart(b.configSnapshot(), &failed) {
			t.Fatalf("rig: the step must hold the page: %v", failed)
		}
		reason, _ := b.SafeMode()
		if len(caught.delays) != 0 || !strings.Contains(reason, "; it is not tried again by itself (identity.start_retry_limit is 0)") {
			t.Fatalf("WITH THE RETRY OFF a retry was armed, or the page does not say it is off: %v %s", caught.delays, reason)
		}
	})

	// An interval that is no power-of-two fraction of a day: doubled once
	// it is 100000 s, past a day, and the cap is what holds it there.
	for _, attempt := range []int{1, maxStartRetryLimit - 1} {
		t.Run(fmt.Sprintf("no wait is longer than a day: retry %d", attempt+1), func(t *testing.T) {
			caught := catchRetries(t)
			dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Long", nil)
			c := safebootConfig(t, dir, "Long", keyPath, ledgerPath, dbPath)
			c.Identity.StartRetrySeconds = 50000
			limit := maxStartRetryLimit
			c.Identity.StartRetryLimit = &limit
			b := New(c)
			defer stopApp(t, b)
			if _, err := b.claimIdentity(dir); err != nil {
				t.Fatal(err)
			}
			b.startAttempt = attempt
			failed := stopped("open verified ledger", &os.PathError{Op: "open", Path: ledgerPath, Err: syscall.EIO})
			if !b.couldNotStart(b.configSnapshot(), &failed) || len(caught.delays) != 1 {
				t.Fatalf("rig: the retry must be armed: %v %v", failed, caught.delays)
			}
			if caught.delays[0] != 24*time.Hour {
				t.Fatalf("RETRY %d WAITS %s: the doubling ran past a day", attempt+1, caught.delays[0])
			}
		})
	}

	for _, bad := range []struct{ body, key string }{
		{`{"identity":{"start_retry_seconds":-1}}`, "identity.start_retry_seconds"},
		{fmt.Sprintf(`{"identity":{"start_retry_seconds":%d}}`, maxStartRetrySeconds+1), "identity.start_retry_seconds"},
		{`{"identity":{"start_retry_seconds":9223372036}}`, "identity.start_retry_seconds"},
		{`{"identity":{"start_retry_limit":-1}}`, "identity.start_retry_limit"},
		{fmt.Sprintf(`{"identity":{"start_retry_limit":%d}}`, maxStartRetryLimit+1), "identity.start_retry_limit"},
	} {
		if _, err := load(t, bad.body); err == nil || !strings.HasPrefix(err.Error(), bad.key+" is ") {
			t.Fatalf("%s must be refused by its key: %v", bad.body, err)
		}
	}
}

// THE WHOLE OF A JOINED CAUSE IS ASKED. A key that is gone (ENOENT) joined
// to a record whose read failed (EIO) may pass with time: the read may come
// back. errors.As stops at the first system error it meets, which was the
// key's.
func TestAJoinedCauseMayPassWithTimeIfAnyPartMay(t *testing.T) {
	gone := &os.PathError{Op: "open", Path: "identity.sec", Err: syscall.ENOENT}
	failed := &os.PathError{Op: "read", Path: "ledger.jsonl", Err: syscall.EIO}
	if !passesWithTime(errors.Join(gone, failed)) {
		t.Fatal("A KEY GONE JOINED TO A RECORD THAT FAILED ITS READ IS HELD FOR A PERSON: the record's read may come back")
	}
	if passesWithTime(errors.Join(gone, &os.PathError{Op: "open", Path: "ledger.jsonl", Err: syscall.EACCES})) {
		t.Fatal("a key gone and a record this user may not read are said to pass with time")
	}
}

// WHAT DECIDES IS THE STEP'S OWN ERROR. The teardown's failure is joined to
// the step's, and an EIO there armed a retry for a step that carried no
// system error at all, and an EINTR there ended the start.
func TestTheRetryReadsTheStepsOwnErrorNotTheTeardowns(t *testing.T) {
	for _, teardown := range []syscall.Errno{syscall.EIO, syscall.EINTR} {
		caught := catchRetries(t)
		dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Torn", nil)
		a := New(safebootConfig(t, dir, "Torn", keyPath, ledgerPath, dbPath))
		if _, err := a.claimIdentity(dir); err != nil {
			t.Fatal(err)
		}
		failed := errors.Join(stopped("LLM substrate", errors.New("no provider entry is named an-entry-that-is-not-there")),
			&os.PathError{Op: "close", Path: "aii.db", Err: teardown})
		if !a.couldNotStart(a.configSnapshot(), &failed) {
			stopApp(t, a)
			t.Fatalf("A TEARDOWN'S %v ENDED A START whose step carried no system error", teardown)
		}
		reason, _ := a.SafeMode()
		stopApp(t, a)
		if len(caught.delays) != 0 || strings.Contains(reason, "tried again") {
			t.Fatalf("A TEARDOWN'S %v ARMED A RETRY for a step that needs a person: %v %s", teardown, caught.delays, reason)
		}
	}
}

// A SHORTAGE OF OPEN FILES IS TRIED AGAIN WITH THE BACKOFF, not ended: a
// limit that stays low restarted the identity every few seconds for ever.
func TestAShortageOfOpenFilesIsTriedAgainWithTheBackoff(t *testing.T) {
	caught := catchRetries(t)
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Files", nil)
	a := New(safebootConfig(t, dir, "Files", keyPath, ledgerPath, dbPath))
	defer stopApp(t, a)
	if _, err := a.claimIdentity(dir); err != nil {
		t.Fatal(err)
	}
	failed := stopped("open verified ledger", &os.PathError{Op: "open", Path: ledgerPath, Err: syscall.EMFILE})
	if !a.couldNotStart(a.configSnapshot(), &failed) || len(caught.delays) != 1 {
		t.Fatalf("A START SHORT OF OPEN FILES ENDED, or holds the page with no retry: %v %v", failed, caught.delays)
	}
}

// WHERE NOTHING STARTS AN ENDED START AGAIN (Windows) the start that would
// end holds the page and is tried again by itself: a cause of the moment, and
// a start while an update's rollback is armed, whose retry restores the
// previous program. It ended, and the identity was down until the next
// sign-in. Elsewhere a service manager starts it again, and it still ends.
func TestWhereNothingStartsAnEndedStartAgainItHoldsThePage(t *testing.T) {
	armUpdate := func(t *testing.T, dir string) {
		t.Helper()
		backup := []byte("the previous binary")
		sum := sha256.Sum256(backup)
		if err := os.WriteFile(filepath.Join(dir, "aii.previous"), backup, 0o755); err != nil {
			t.Fatal(err)
		}
		record := `{"attempts":0,"backup_sha256":"` + hex.EncodeToString(sum[:]) + `","new_sha256":"` + strings.Repeat("0", 64) + `"}`
		if err := os.WriteFile(filepath.Join(dir, ".update_pending"), []byte(record), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prev := endedStartsRestart
	t.Cleanup(func() { endedStartsRestart = prev })
	endedStartsRestart = false

	t.Run("a cause of the moment", func(t *testing.T) {
		caught := catchRetries(t)
		dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Moment", nil)
		a := New(safebootConfig(t, dir, "Moment", keyPath, ledgerPath, dbPath))
		defer stopApp(t, a)
		if _, err := a.claimIdentity(dir); err != nil {
			t.Fatal(err)
		}
		failed := stopped("open verified ledger", &os.PathError{Op: "open", Path: ledgerPath, Err: syscall.EAGAIN})
		if !a.couldNotStart(a.configSnapshot(), &failed) || len(caught.delays) != 1 {
			t.Fatalf("A START WITH NOTHING TO START IT AGAIN ENDED ON A CAUSE OF THE MOMENT: %v %v", failed, caught.delays)
		}
	})
	t.Run("an update's rollback armed", func(t *testing.T) {
		caught := catchRetries(t)
		dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Armed", nil)
		armUpdate(t, dir)
		cfg := safebootConfig(t, dir, "Armed", keyPath, ledgerPath, dbPath)
		cfg.LLM = LLMConfig{Provider: "an-entry-that-is-not-there", Model: "m"}
		a, reason := safeAt(t, cfg, "A START WITH NOTHING TO START IT AGAIN ENDED WHILE AN UPDATE'S ROLLBACK WAS ARMED: the identity is down until the next sign-in")
		defer stopApp(t, a)
		if len(caught.delays) != 1 || !strings.Contains(reason, "that start restores the previous program") || !updates.RollbackArmed(dir) {
			t.Fatalf("the page does not try the start again, or does not say the retry restores the previous program: %v %s", caught.delays, reason)
		}
	})
	t.Run("a key gone while the rollback is armed", func(t *testing.T) {
		caught := catchRetries(t)
		dir, keyPath, ledgerPath, dbPath := recordedBy(t, "ArmedKey", nil)
		armUpdate(t, dir)
		if err := os.Remove(keyPath); err != nil {
			t.Fatal(err)
		}
		a, reason := safeAt(t, safebootConfig(t, dir, "ArmedKey", keyPath, ledgerPath, dbPath), "A START WITH NOTHING TO START IT AGAIN ENDED ON ITS KEY WHILE AN UPDATE'S ROLLBACK WAS ARMED")
		defer stopApp(t, a)
		if len(caught.delays) != 1 || !strings.Contains(reason, "load identity key") {
			t.Fatalf("the key's start under an armed rollback is not tried again: %v %s", caught.delays, reason)
		}
	})
}

// WHICH RETRY THIS IS LEAVES THE ENVIRONMENT AT EVERY START, a first boot's
// included: a first boot carried it into every process it started.
func TestEveryStartTakesWhichRetryItIsOutOfTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	cfg := safebootConfig(t, dir, "First", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db"))
	a := New(cfg)
	defer stopApp(t, a)
	t.Setenv(startRetryEnv, "3")
	release, err := a.prepareStart()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, set := os.LookupEnv(startRetryEnv); set || a.startAttempt != 3 {
		t.Fatalf("A START BEFORE ITS CHOICE LEFT WHICH RETRY IT IS IN THE ENVIRONMENT, or did not take it: set=%v attempt=%d", set, a.startAttempt)
	}
}
