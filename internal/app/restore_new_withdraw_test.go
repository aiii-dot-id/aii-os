package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// homeAsItIs is every file under dir with its bytes, and every directory.
func homeAsItIs(t *testing.T, dir string) map[string]string {
	t.Helper()
	home := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			home[rel+"/"] = ""
			return nil
		}
		raw, err := os.ReadFile(path)
		home[rel] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// sameHome fails the test, naming each path that differs, when the home is
// not what it was.
func sameHome(t *testing.T, what string, was, is map[string]string) {
	t.Helper()
	if maps.Equal(was, is) {
		return
	}
	for path, now := range is {
		if before, ok := was[path]; !ok {
			t.Errorf("%s: %s is here and was not", what, path)
		} else if before != now {
			t.Errorf("%s: %s changed", what, path)
		}
	}
	for path := range was {
		if _, ok := is[path]; !ok {
			t.Errorf("%s: %s is gone", what, path)
		}
	}
	t.FailNow()
}

// doorRefusal holds err to the door's typed refusal for reason.
func doorRefusal(t *testing.T, err error, reason string) *dashboard.RestoreNewRefusal {
	t.Helper()
	var refused *dashboard.RestoreNewRefusal
	if !errors.As(err, &refused) || refused.Reason != reason {
		t.Fatalf("want the door's typed refusal %q, got: %v", reason, err)
	}
	return refused
}

const newMachineMarkerFile = "restore-new-machine.json"

// withdrawn is the withdrawal's answer where the snapshots it names are not
// what the test is about.
func withdrawn(c *continuity.Runtime) error {
	_, err := c.NewMachineWithdraw()
	return err
}

// A BIRTH OVER KEYS THE DOOR PLACED IS REFUSED BEFORE ANYTHING IS SENT. The
// mint refused it already, as the ceremony's last step: by then the provider
// had listed its models, the founding prompt had been sent and answered, and
// the provider was written into the configuration. Now the provider is never
// asked, nothing is written, the refusal is typed and says the two ways on;
// and once the keys are withdrawn the same birth goes through.
func TestABirthOverKeysTheRestoreDoorPlacedIsRefusedBeforeAnythingIsSent(t *testing.T) {
	left := oldMachine(t)
	var asked atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"birth-model"}]}`)
		case "/v1/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello, I am Mira."},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	a := birthApp(t, providerEntry{
		Name: "Local", APIType: "openai", URL: provider.URL + "/v1",
		APIKey: "stored-birth-key", DefaultModel: "birth-model", Default: true,
	})
	defer stopApp(t, a)
	dir := filepath.Dir(a.configSnapshot().Identity.KeyPath)
	birth := &dashboard.GenesisRequest{Provider: "Local", Endpoint: provider.URL + "/v1", Model: "birth-model"}

	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatalf("the keys: %v", err)
	}
	placed := homeAsItIs(t, dir)
	_, err := a.handleGenesis(t.Context(), birth)
	refused := doorRefusal(t, err, dashboard.RestoreNewStands)
	if n := asked.Load(); n != 0 {
		t.Fatalf("THE PROVIDER WAS ASKED %d TIMES for a birth that could never be: the refusal must come before anything is sent", n)
	}
	sameHome(t, "a refused birth wrote to the home", placed, homeAsItIs(t, dir))
	const said = "Nothing was sent, and no identity was born. The keys of identity %s… are on this machine: they were put here from an escrow file at \"Restore an identity from a backup instead\", and a new identity is not born over them. Open that door below, then either restore that identity from a snapshot, or press \"Withdraw these keys\" and press Birth again."
	if want := fmt.Sprintf(said, left.identity[:16]); refused.Text != want {
		t.Errorf("the refusal is not the approved sentence:\n got %s\nwant %s", refused.Text, want)
	}
	if a.live.Load() || fileExists(a.configSnapshot().Identity.LedgerPath) {
		t.Fatal("a refused birth made something")
	}

	// THE WAY ON: the keys withdrawn, the same press of Birth is a birth.
	if err := withdrawn(a.continuity); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	greeting, err := a.handleGenesis(t.Context(), birth)
	if err != nil || greeting != "Hello, I am Mira." || !a.live.Load() {
		t.Fatalf("a birth after the keys were withdrawn: %q %v", greeting, err)
	}
	if asked.Load() == 0 {
		t.Fatal("rig: the birth that went through never asked the provider, so the count above proves nothing")
	}
	if a.keyPair == nil || a.keyPair.Fingerprint() == left.identity {
		t.Fatal("the born identity signs with the key that was withdrawn")
	}
}

// WITHDRAW PUTS THE HOME BACK AS IT WAS: byte for byte what it was before
// the door was opened, the next start is first boot, a start between the
// two acts changes nothing (the act reads the disk), and the same act asked
// for again is refused as nothing to do.
func TestWithdrawingTheKeysPutsTheHomeBackAsItWas(t *testing.T) {
	left := oldMachine(t)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	before := homeAsItIs(t, dir)

	keys, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass))
	if err != nil || !keys.HoldsSnapshotKey {
		t.Fatalf("the keys: %+v %v", keys, err)
	}
	for _, wrote := range []string{a.configSnapshot().Identity.KeyPath, a.continuity.SnapshotKeyPathForTest(), filepath.Join(dir, newMachineMarkerFile)} {
		if !fileExists(wrote) {
			t.Fatalf("rig: the door did not write %s", wrote)
		}
	}

	// A START BETWEEN PLACING AND WITHDRAWING: another process, the disk
	// only. It is first boot, the door says whose restore stands, and the
	// act is performed from what is on the disk.
	b := New(a.cfg)
	if choice, err := b.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("a start over placed keys: %v %v", choice, err)
	}
	if here := b.continuity.NewMachineBegun(); here == nil || here.Identity != left.identity {
		t.Fatalf("A RELOADED DOOR DOES NOT SAY WHOSE RESTORE STANDS HERE: %+v", here)
	}
	if err := withdrawn(b.continuity); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	sameHome(t, "THE HOME IS NOT AS IT WAS BEFORE THE DOOR", before, homeAsItIs(t, dir))
	if choice, err := New(a.cfg).chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("A START AFTER THE KEYS WERE WITHDRAWN IS NOT FIRST BOOT: %v %v", choice, err)
	}
	if here := b.continuity.NewMachineBegun(); here != nil || continuity.RestoringOnNewMachine(continuitySettingsOf(a.configSnapshot())) {
		t.Errorf("the door still says a restore stands here: %+v", here)
	}

	// Asked for twice: nothing is here to withdraw, and nothing is touched.
	doorRefusal(t, withdrawn(b.continuity), dashboard.RestoreNewNone)
	sameHome(t, "a second withdrawal touched the home", before, homeAsItIs(t, dir))

	// The door is as it was, too: the keys go in again, and come out again,
	// and the key this start held in memory goes with them.
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil || a.keyPair == nil {
		t.Fatalf("the first act after a withdrawal: %v", err)
	}
	if err := withdrawn(a.continuity); err != nil || a.keyPair != nil {
		t.Fatalf("withdraw again (the key still held in memory: %t): %v", a.keyPair != nil, err)
	}
	sameHome(t, "the home after the second round", before, homeAsItIs(t, dir))
}

// KEYS ARE NEVER WITHDRAWN FROM A HOME AN IDENTITY LIVES ON. A born
// identity has its key, its record and its database, and is refused on the
// record even under a marker that names this identity. With the record
// moved away the database refuses the same.
func TestKeysAreNeverWithdrawnFromAHomeAnIdentityLivesOn(t *testing.T) {
	r := newRestoreRig(t)
	a := New(r.cfg) // not started: the act reads the disk
	kp, err := crypto.LoadKeyPair(r.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	marker, _ := json.Marshal(map[string]any{"started_at": "2026-10-13T00:00:00Z", "identity": kp.Fingerprint()})
	if err := os.WriteFile(filepath.Join(r.dir, newMachineMarkerFile), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	marked := homeAsItIs(t, r.dir)
	refused := doorRefusal(t, withdrawn(a.continuity), dashboard.RestoreNewNotFirstBoot)
	if !strings.Contains(refused.Text, r.ledgerPath) || !strings.Contains(refused.Text, "keys beside it are that identity's own") {
		t.Errorf("the refusal does not name the record it met and why: %s", refused.Text)
	}
	sameHome(t, "AN IDENTITY'S OWN KEY WAS WITHDRAWN on the word of a marker", marked, homeAsItIs(t, r.dir))

	// The record is not at its path, and the database is.
	if err := os.Rename(r.ledgerPath, r.ledgerPath+".moved"); err != nil {
		t.Fatal(err)
	}
	moved := homeAsItIs(t, r.dir)
	refused = doorRefusal(t, withdrawn(a.continuity), dashboard.RestoreNewNotFirstBoot)
	if !strings.Contains(refused.Text, r.dbPath) {
		t.Errorf("the refusal does not name the database it met: %s", refused.Text)
	}
	sameHome(t, "a key was withdrawn beside an identity's database", moved, homeAsItIs(t, r.dir))
	if !fileExists(r.keyPath) {
		t.Fatal("THE IDENTITY'S KEY IS GONE")
	}
}

// AN ACT OF THE DOOR WAITS FOR THE ONE BEFORE IT. A withdrawal asked for in
// the middle of the restore's moves waits; the restore completes under the
// keys; and the withdrawal then finds no restore standing and touches
// nothing: the identity it put in place has its keys.
func TestAWithdrawalAskedDuringARestoreWaitsAndTakesNothing(t *testing.T) {
	left := oldMachine(t)
	a, _ := blankMachine(t)
	defer stopApp(t, a)
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	during := make(chan error, 1)
	asked, answeredEarly := false, false
	a.continuity.RestoreStep = func(string) {
		if asked {
			return
		}
		asked = true
		go func() { during <- withdrawn(a.continuity) }()
		select {
		case err := <-during:
			answeredEarly = true
			during <- err
		case <-time.After(300 * time.Millisecond):
		}
	}
	done, err := a.continuity.NewMachineRestore(t.Context(), left.snapshotName, nil)
	a.continuity.RestoreStep = nil
	if !asked {
		t.Fatal("rig: the restore passed no step at which to ask")
	}
	if answeredEarly {
		t.Fatalf("A WITHDRAWAL WAS ANSWERED IN THE MIDDLE OF A RESTORE: %v", <-during)
	}
	if err != nil || done.RestoredTo != left.through || !a.live.Load() {
		t.Fatalf("THE RESTORE DID NOT COMPLETE under a withdrawal that was asked for: %+v %v", done, err)
	}
	doorRefusal(t, <-during, dashboard.RestoreNewNone)
	if !fileExists(a.configSnapshot().Identity.KeyPath) || !fileExists(a.continuity.SnapshotKeyPathForTest()) {
		t.Fatal("A RESTORED IDENTITY'S KEYS WERE WITHDRAWN")
	}
}

// AN UPLOAD THAT IS ARRIVING HOLDS THE DOOR, AND ONLY WHILE IT ARRIVES. A
// withdrawal asked for meanwhile waits for it (the upload's own bound is
// maintenance.restore_upload_idle_seconds), and then goes: the upload, cut
// short, removed what it had written, and the home is as it was before the
// door.
func TestAWithdrawalWaitsForAnUploadThatIsArriving(t *testing.T) {
	left := oldMachine(t)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	before := homeAsItIs(t, dir)
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	arriving, peer := io.Pipe()
	ended := make(chan error, 1)
	go func() { ended <- a.continuity.NewMachineUpload(left.snapshotName, "", arriving) }()
	// One byte taken: the upload is inside its copy, holding the door.
	if _, err := peer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	withdrew := make(chan error, 1)
	go func() { withdrew <- withdrawn(a.continuity) }()
	select {
	case err := <-withdrew:
		t.Fatalf("A WITHDRAWAL WENT AHEAD UNDER AN UPLOAD STILL ARRIVING: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	peer.CloseWithError(errors.New("the peer went away"))
	if err := <-ended; err == nil {
		t.Fatal("rig: an upload that was cut short was taken as whole")
	}
	if err := <-withdrew; err != nil {
		t.Fatalf("the withdrawal after the upload ended: %v", err)
	}
	sameHome(t, "THE HOME IS NOT AS IT WAS BEFORE THE DOOR", before, homeAsItIs(t, dir))
}

func TestAWithdrawalTakesOnlyWhatTheDoorWrote(t *testing.T) {
	var otherSnapshotKey []byte
	left := oldMachine(t)
	other := oldMachineLiving(t, func(old *App) {
		otherSnapshotKey, _ = continuitytest.SnapshotKey(t, old.configSnapshot().Identity.KeyPath)
	}, nil)
	pass := []byte(newMachinePass)
	placedDoor := func(t *testing.T) (*App, string) {
		t.Helper()
		a, dir := blankMachine(t)
		t.Cleanup(func() { stopApp(t, a) })
		if _, err := a.continuity.NewMachineKeys(left.escrowFile, pass); err != nil {
			t.Fatal(err)
		}
		return a, dir
	}
	escrowedKey, _, err := continuitytest.EscrowedKey(left.escrowFile, pass)
	if err != nil {
		t.Fatal(err)
	}

	// THE ESCROW'S OWN KEY, PUT HERE BEFORE THE DOOR WAS OPENED (by `aii
	// escrow restore`, or a copy the person keeps): the door goes past it,
	// as a key it may go on with, and does not note it. It reads as the
	// escrow's, and the door did not write it, so it stays and is named.
	t.Run("the escrow's key, put here before the door was opened", func(t *testing.T) {
		a, dir := blankMachine(t)
		defer stopApp(t, a)
		blank := homeAsItIs(t, dir)
		keyPath := a.configSnapshot().Identity.KeyPath
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if published, err := crypto.PublishKeyFile(escrowedKey, keyPath); err != nil || !published {
			t.Fatalf("rig: %v %v", published, err)
		}
		if _, err := a.continuity.NewMachineKeys(left.escrowFile, pass); err != nil {
			t.Fatalf("the door did not go on past the escrow's own key: %v", err)
		}
		placed := homeAsItIs(t, dir)
		refused := doorRefusal(t, withdrawn(a.continuity), dashboard.RestoreNewNotItsKey)
		if !strings.Contains(refused.Text, keyPath) || !strings.Contains(refused.Text, "not what this page wrote there") || !strings.Contains(refused.Text, "move it off this machine by hand") {
			t.Errorf("the refusal does not name the key it leaves, and the way on: %s", refused.Text)
		}
		sameHome(t, "A KEY THE DOOR DID NOT WRITE WAS WITHDRAWN because it reads as the escrow's", placed, homeAsItIs(t, dir))
		// By hand, as the refusal says; then the door takes what it wrote.
		if err := os.Remove(keyPath); err != nil {
			t.Fatal(err)
		}
		if err := withdrawn(a.continuity); err != nil {
			t.Fatalf("the withdrawal of what the door wrote: %v", err)
		}
		sameHome(t, "the home after the withdrawal", blank, homeAsItIs(t, dir))
	})

	// What the door wrote, replaced since by another hand: another
	// identity's key, a file that is not a key, another snapshot key.
	otherKey, _, err := continuitytest.EscrowedKey(other.escrowFile, pass)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		what  string
		where func(a *App) string
		bytes []byte
	}{
		{"another identity's key", func(a *App) string { return a.configSnapshot().Identity.KeyPath }, otherKey},
		{"a file that is not a key", func(a *App) string { return a.configSnapshot().Identity.KeyPath }, []byte("not a key")},
		{"another snapshot key", func(a *App) string { return a.continuity.SnapshotKeyPathForTest() }, otherSnapshotKey},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a, dir := placedDoor(t)
			path := tc.where(a)
			if err := os.WriteFile(path, tc.bytes, 0o600); err != nil {
				t.Fatal(err)
			}
			placed := homeAsItIs(t, dir)
			refused := doorRefusal(t, withdrawn(a.continuity), dashboard.RestoreNewNotItsKey)
			if !strings.Contains(refused.Text, path) || !strings.Contains(refused.Text, "move it off this machine by hand") {
				t.Errorf("the refusal does not say which file, and the way on: %s", refused.Text)
			}
			sameHome(t, "A FILE THE DOOR DID NOT WRITE WAS TOUCHED", placed, homeAsItIs(t, dir))
			if choice, err := New(a.cfg).chooseBoot(); err != nil || choice != bootFirstboot {
				t.Fatalf("a refused withdrawal closed the door: %v %v", choice, err)
			}
		})
	}

	// A RESTORE AN EARLIER VERSION BEGAN noted nothing of what it wrote:
	// refused, with the three files named for removal by hand. Opening the
	// escrow file again does not make its keys the door's: this act goes
	// past them and does not note them.
	t.Run("a restore an earlier version began", func(t *testing.T) {
		a, dir := blankMachine(t)
		defer stopApp(t, a)
		blank := homeAsItIs(t, dir)
		if _, err := a.continuity.NewMachineKeys(left.escrowFile, pass); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(dir, newMachineMarkerFile)
		earlier, _ := json.Marshal(map[string]any{"started_at": "2026-10-01T00:00:00Z", "identity": left.identity})
		if err := os.WriteFile(marker, earlier, 0o600); err != nil {
			t.Fatal(err)
		}
		placed := homeAsItIs(t, dir)
		keyPath, snapPath := a.configSnapshot().Identity.KeyPath, a.continuity.SnapshotKeyPathForTest()
		refused := doorRefusal(t, withdrawn(a.continuity), dashboard.RestoreNewNotItsKey)
		for _, named := range []string{marker, keyPath, snapPath, "earlier version"} {
			if !strings.Contains(refused.Text, named) {
				t.Errorf("the refusal of an earlier version's restore does not name %s: %s", named, refused.Text)
			}
		}
		sameHome(t, "a withdrawal under an earlier version's marker touched the home", placed, homeAsItIs(t, dir))
		if _, err := a.continuity.NewMachineKeys(left.escrowFile, pass); err != nil {
			t.Fatal(err)
		}
		refused = doorRefusal(t, withdrawn(a.continuity), dashboard.RestoreNewNotItsKey)
		if !strings.Contains(refused.Text, keyPath) {
			t.Errorf("A KEY AN EARLIER VERSION WROTE WAS TAKEN AS THE DOOR'S once the escrow was opened again: %s", refused.Text)
		}
		for _, path := range []string{keyPath, snapPath} {
			if !fileExists(path) {
				t.Fatalf("%s is gone", path)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if err := withdrawn(a.continuity); err != nil {
			t.Fatalf("the withdrawal once the earlier version's keys were removed by hand: %v", err)
		}
		sameHome(t, "the home after the withdrawal", blank, homeAsItIs(t, dir))
	})

	// What the door does not put here, and without which the next start
	// would not be first boot: a snapshot in the backups directory, a set an
	// earlier restore kept aside.
	for _, tc := range []struct {
		what  string
		write func(a *App, dir string) (string, error)
	}{
		{"a snapshot put in the backups directory by hand", func(a *App, dir string) (string, error) {
			backups := a.backupsDir(a.configSnapshot())
			there := filepath.Join(backups, left.snapshotName)
			if err := os.MkdirAll(backups, 0o700); err != nil {
				return "", err
			}
			return there, os.WriteFile(there, left.snapshotBytes, 0o600)
		}},
		{"a set an earlier restore kept aside", func(a *App, dir string) (string, error) {
			set := filepath.Join(dir, "set-aside", "ledger-20260920T101500Z-before-restore-to-seq7")
			return set, os.MkdirAll(set, 0o700)
		}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a, dir := placedDoor(t)
			there, err := tc.write(a, dir)
			if err != nil {
				t.Fatal(err)
			}
			placed := homeAsItIs(t, dir)
			refused := doorRefusal(t, withdrawn(a.continuity), dashboard.RestoreNewNotFirstBoot)
			if !strings.Contains(refused.Text, there) || !strings.Contains(refused.Text, "not first boot") {
				t.Errorf("the refusal does not say where it is and why it is in the way: %s", refused.Text)
			}
			sameHome(t, "A REFUSED WITHDRAWAL TOUCHED THE HOME", placed, homeAsItIs(t, dir))
		})
	}
}

// A RESTORE THAT STOPPED HALF WAY IS ENDED, OR GONE ON WITH, AFTER A
// RESTART. Three places it can stop, each built as it would be left on the
// disk: a first act that died after its marker and before a key; a
// withdrawal that died after the snapshot key and before the identity's;
// a withdrawal that died after the identity's key and before the marker. In
// each the next start is first boot, the door says whose restore stands, a
// birth is refused before anything is sent (it used to go through beside a
// marker with no key, and leave the marker in the born identity's home), and
// from the page the restore is either withdrawn, which leaves the home as it
// was before the door and lets a birth through, or gone on with, by the same
// first act.
func TestARestoreThatStoppedHalfWayIsEndedOrGoneOnWithAfterARestart(t *testing.T) {
	left := oldMachine(t)
	pass := []byte(newMachinePass)
	// stopAt fails the nth directory sync of a withdrawal, as a machine
	// that lost power there would.
	stopAt := func(n int) func(t *testing.T, a *App, dir string) {
		return func(t *testing.T, a *App, _ string) {
			t.Helper()
			syncs := 0
			a.continuity.SyncDir = func(string) error {
				if syncs++; syncs == n {
					return errors.New("the machine stopped here")
				}
				return nil
			}
			err := withdrawn(a.continuity)
			a.continuity.SyncDir = nil
			var refused *dashboard.RestoreNewRefusal
			if err == nil || errors.As(err, &refused) || syncs != n || !strings.Contains(err.Error(), "Withdraw these keys") {
				t.Fatalf("rig: the withdrawal did not stop at sync %d (%d made): %v", n, syncs, err)
			}
		}
	}
	for _, tc := range []struct {
		stopped string
		stop    func(t *testing.T, a *App, dir string)
		key     bool // the identity's key still stands
	}{
		{"a first act, after the marker and before a key", func(t *testing.T, a *App, dir string) {
			t.Helper()
			marker, err := os.ReadFile(filepath.Join(dir, newMachineMarkerFile))
			if err != nil {
				t.Fatal(err)
			}
			if err := withdrawn(a.continuity); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, newMachineMarkerFile), marker, 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a withdrawal, between the two keys", stopAt(2), true},
		{"a withdrawal, after the identity's key and before the marker", stopAt(4), false},
	} {
		for _, then := range []string{"withdrawn", "gone on with"} {
			t.Run(tc.stopped+", then "+then, func(t *testing.T) {
				a, dir := blankMachine(t)
				defer stopApp(t, a)
				before := homeAsItIs(t, dir)
				if _, err := a.continuity.NewMachineKeys(left.escrowFile, pass); err != nil {
					t.Fatal(err)
				}
				tc.stop(t, a, dir)
				if !fileExists(filepath.Join(dir, newMachineMarkerFile)) {
					t.Fatal("THE MARKER WENT BEFORE WHAT IT STANDS FOR: a start now would not come back to the door")
				}
				if fileExists(a.continuity.SnapshotKeyPathForTest()) || fileExists(a.configSnapshot().Identity.KeyPath) != tc.key {
					t.Fatal("rig: the home is not in the state this case is about")
				}

				// THE RESTART: another process, the disk only.
				b := New(a.cfg)
				if choice, err := b.chooseBoot(); err != nil || choice != bootFirstboot {
					t.Fatalf("the start after it: %v %v", choice, err)
				}
				if here := b.continuity.NewMachineBegun(); here == nil || here.Identity != left.identity {
					t.Fatalf("THE DOOR DOES NOT SAY WHOSE RESTORE STANDS HERE: %+v", here)
				}
				_, err := b.handleGenesis(t.Context(), &dashboard.GenesisRequest{})
				refused := doorRefusal(t, err, dashboard.RestoreNewStands)
				says := "The keys of identity " + left.identity[:16] + "… are on this machine"
				if !tc.key {
					says = "A restore of identity " + left.identity[:16] + "… was begun on this machine"
				}
				if !strings.HasPrefix(refused.Text, "Nothing was sent, and no identity was born. "+says) || !strings.Contains(refused.Text, "Withdraw these keys") {
					t.Errorf("the birth's refusal does not say what stands and the way on: %s", refused.Text)
				}

				if then == "gone on with" {
					keys, err := b.continuity.NewMachineKeys(left.escrowFile, pass)
					if err != nil || keys.Identity != left.identity || !keys.HoldsSnapshotKey {
						t.Fatalf("THE SAME FIRST ACT DID NOT GO ON: %+v %v", keys, err)
					}
				}
				if err := withdrawn(b.continuity); err != nil {
					t.Fatalf("THE RESTORE COULD NOT BE ENDED FROM THE PAGE: %v", err)
				}
				sameHome(t, "the home is not as it was before the door", before, homeAsItIs(t, dir))
				c := New(a.cfg)
				if choice, err := c.chooseBoot(); err != nil || choice != bootFirstboot {
					t.Fatalf("the start after the withdrawal: %v %v", choice, err)
				}
				if c.continuity.NewMachineBegun() != nil || c.continuity.NewMachineRefusesBirth() != nil {
					t.Fatal("A MARKER OUTLIVED ITS RESTORE: a birth here would still be refused, or would leave it in the born identity's home")
				}
			})
		}
	}
}

// A DOOR MARKER NEVER SENDS A BORN IDENTITY'S HOME TO FIRST BOOT. A marker
// left in such a home (an earlier version let a birth through beside one)
// was asked before the record's sealed segments and before anything else of
// the identity, so once the record's tail was lost the home went to the
// restore door of a new machine instead of to its own. A record or a
// database on the machine outranks the marker.
func TestADoorMarkerNeverSendsABornIdentitysHomeToFirstBoot(t *testing.T) {
	r := newRestoreRig(t)
	if _, snap := r.lived(t, 1, "kept", true); snap == "" {
		t.Fatal("rig: no snapshot")
	}
	stale, _ := json.Marshal(map[string]any{"started_at": "2026-10-01T00:00:00Z", "identity": strings.Repeat("a", 64)})
	if err := os.WriteFile(filepath.Join(r.dir, newMachineMarkerFile), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if choice, err := New(r.cfg).chooseBoot(); err != nil || choice != bootLive {
		t.Fatalf("a home with its record: %v %v", choice, err)
	}
	// The record is lost; the key, the database and a snapshot are here. It
	// starts at its own door, where a restore is asked for.
	if err := os.Rename(r.ledgerPath, r.ledgerPath+".moved"); err != nil {
		t.Fatal(err)
	}
	if choice, err := New(r.cfg).chooseBoot(); err != nil || choice != bootLive {
		t.Fatalf("A BORN IDENTITY THAT LOST ITS RECORD WAS SENT TO A NEW MACHINE'S RESTORE DOOR by a marker: %v %v", choice, err)
	}
	// And with nothing to restore from it is refused, as it was: never
	// first boot while its database stands.
	a := New(r.cfg)
	if err := os.RemoveAll(a.backupsDir(a.configSnapshot())); err != nil {
		t.Fatal(err)
	}
	if choice, err := a.chooseBoot(); err == nil {
		t.Fatalf("a home with an identity's key and database was given a boot by a marker: %v", choice)
	}
}

func TestWithdrawingRemovesTheSnapshotsTheDoorReceived(t *testing.T) {
	mine, other := oldMachine(t), anotherOldMachine(t)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	before := homeAsItIs(t, dir)
	if _, err := a.continuity.NewMachineKeys(mine.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(t.TempDir(), mine.snapshotName)
	if err := os.WriteFile(own, other.snapshotBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sent, err := os.Open(own)
	if err != nil {
		t.Fatal(err)
	}
	err = a.continuity.NewMachineUpload(mine.snapshotName, "", sent)
	sent.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.continuity.NewMachineRestore(t.Context(), mine.snapshotName, nil); err == nil {
		t.Fatal("rig: another identity's snapshot was restored")
	}
	if err := a.continuity.NewMachineUpload(mine.snapshotName, "", bytes.NewReader(mine.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	if err := a.continuity.NewMachineUpload("ledger-20260920T101500Z-seq7", "ledger.jsonl", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if here := a.continuity.NewMachineBegun(); here == nil || len(here.Received) != 1 || here.Received[0] != mine.snapshotName {
		t.Fatalf("THE DOOR DOES NOT SAY WHICH SNAPSHOTS IT RECEIVED, for the question to name: %+v", here)
	}
	if fileExists(a.backupsDir(a.configSnapshot())) {
		t.Fatal("WHAT THE DOOR RECEIVED WAS PUT AMONG AN IDENTITY'S OWN SNAPSHOTS before any identity was restored")
	}

	snapshots, err := a.continuity.NewMachineWithdraw()
	if err != nil {
		t.Fatalf("A WITHDRAWAL WAS REFUSED OVER SNAPSHOTS THIS DOOR RECEIVED: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0] != mine.snapshotName {
		t.Errorf("the withdrawal names %v, want [%s]", snapshots, mine.snapshotName)
	}
	sameHome(t, "THE HOME IS NOT AS IT WAS BEFORE THE DOOR", before, homeAsItIs(t, dir))
	if choice, err := New(a.cfg).chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("the start after the withdrawal: %v %v", choice, err)
	}
	if still, err := os.ReadFile(own); err != nil || !bytes.Equal(still, other.snapshotBytes) {
		t.Fatalf("THE PERSON'S OWN COPY WAS TOUCHED: %v", err)
	}
}

// A RESTORE THE DOOR BEGAN AND A START FINISHED ENDS THE DOOR TOO. The
// process dies after the restore's journal and its first moves; the next
// start rolls the restore forward, as any journalled restore is, and the
// door's marker and directory go with it: the snapshot it received is one
// of the identity's kept snapshots, and nothing of the door is left in a
// live identity's home.
func TestARestoreTheDoorBeganAndAStartFinishedEndsTheDoor(t *testing.T) {
	left := oldMachine(t)
	a, dir := blankMachine(t)
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	a.continuity.RestoreStep = func(s string) {
		if s == "moved" {
			panic(errRestoreKilled)
		}
	}
	func() {
		defer func() {
			if v := recover(); v != errRestoreKilled {
				panic(v)
			}
		}()
		a.continuity.NewMachineRestore(t.Context(), left.snapshotName, nil)
		t.Fatal("rig: the restore was not interrupted after its moves")
	}()
	stopApp(t, a)
	if !fileExists(filepath.Join(dir, newMachineMarkerFile)) {
		t.Fatal("rig: the marker did not outlive the interrupted restore")
	}

	b := New(a.cfg)
	defer stopApp(t, b)
	if choice, err := b.chooseBoot(); err != nil || choice != bootLive {
		t.Fatalf("the start after a journalled restore: %v %v", choice, err)
	}
	if err := b.StartEmbedded(); err != nil {
		t.Fatalf("the start that rolls the restore forward: %v", err)
	}
	if b.ledger == nil || b.ledger.LastSeq() != left.through {
		t.Fatal("the restore was not rolled forward")
	}
	if fileExists(filepath.Join(dir, newMachineMarkerFile)) || fileExists(filepath.Join(dir, "restore-new-machine")) {
		t.Fatal("THE DOOR OUTLIVED THE RESTORE IT BEGAN in a live identity's home")
	}
	if !fileExists(filepath.Join(b.backupsDir(b.configSnapshot()), left.snapshotName)) {
		t.Error("the snapshot the door received is not among the identity's kept snapshots")
	}
}
