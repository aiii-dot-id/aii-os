package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestDatabaseCompressionSurvivesRestoreAndPutBack(t *testing.T) {
	r := newRestoreRig(t)
	t.Chdir(r.dir)
	r.cfg.Identity.DBFormat = "zstd"
	at, snapshot := r.lived(t, 1, "compressed-before", true)
	later, _ := r.lived(t, 1, "compressed-after", false)
	a := r.boot(t)
	defer func() { a.Stop() }()
	if err := a.requestRestore(sourceSnapshot, snapshot, a.restoreConfirmText()); err != nil {
		t.Fatal(err)
	}
	a.Stop()
	a = r.boot(t)
	if got := a.databaseState(); got.Active != "zstd" || got.Notice != "" || a.ledger.LastSeq() != at {
		t.Fatalf("restored snapshot did not activate its preference: %+v, seq=%d want=%d", got, a.ledger.LastSeq(), at)
	}
	sets := setAsideSets(a.configSnapshot())
	if len(sets) != 1 {
		t.Fatalf("set-aside inventory: %+v", sets)
	}
	kept, err := store.OpenReadOnly(filepath.Join(sets[0].Path, snapshotDB))
	if err != nil {
		t.Fatal(err)
	}
	format, err := kept.DatabaseFormat(t.Context())
	closeErr := kept.Close()
	if err != nil || closeErr != nil || format != "zstd" {
		t.Fatalf("original compressed data not kept: %q %v %v", format, err, closeErr)
	}
	if err := a.requestRestore(sourceSetAside, sets[0].Name, a.restoreConfirmText()); err != nil {
		t.Fatal(err)
	}
	a.Stop()
	a = r.boot(t)
	if got := a.databaseState(); got.Active != "zstd" || got.Notice != "" || a.ledger.LastSeq() != later {
		t.Fatalf("put-back did not restore current history: %+v, seq=%d want=%d", got, a.ledger.LastSeq(), later)
	}
}

// .
// .
// .
func TestDatabaseCompressionSurvivesNewMachineRestore(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		name := "plaintext"
		if encrypted {
			name = "encrypted"
		}
		t.Run(name, func(t *testing.T) {
			r := newRestoreRig(t)
			t.Chdir(r.dir)
			r.cfg.Identity.DBFormat = "zstd"
			var sealed []byte
			var snapshot publishedSnapshot
			var snapshotPath, identity string
			const conversation = "runtime data that ledger replay cannot reconstruct"
			func() {
				a := r.boot(t)
				defer a.Stop()
				if state := a.databaseState(); state.Active != "zstd" || state.Notice != "" {
					t.Fatalf("source is not compressed: %+v", state)
				}
				appendFixtureEvent(t, a, r.keyPath, "exp_compressed_portable")
				if err := a.store.AddConversationTurn("operator", conversation); err != nil {
					t.Fatal(err)
				}
				var err error
				sealed, _, err = a.escrowCreateHere([]byte(newMachinePass))
				if err != nil {
					t.Fatal(err)
				}
				if encrypted {
					if _, err := a.escrowCheckHere(sealed, []byte(newMachinePass)); err != nil {
						t.Fatal(err)
					}
				}
				snapshot, err = a.takeOnDemand(t.Context(), false, time.Now())
				if err != nil || snapshot.Encrypted != encrypted {
					t.Fatalf("snapshot: %+v %v", snapshot, err)
				}
				snapshotPath = filepath.Join(a.backupsDir(a.configSnapshot()), snapshot.Name)
				identity = a.keyPair.Fingerprint()
			}()
			for _, preference := range []string{"", "zstd"} {
				label := preference
				if label == "" {
					label = "default"
				}
				t.Run(label, func(t *testing.T) {
					a, dir := blankMachine(t)
					t.Chdir(dir)
					defer a.Stop()
					a.cfg.Identity.DBFormat = preference
					if _, err := a.newMachineKeys(sealed, []byte(newMachinePass)); err != nil {
						t.Fatal(err)
					}
					if encrypted {
						raw, err := os.ReadFile(snapshotPath)
						if err != nil {
							t.Fatal(err)
						}
						if err := a.newMachineUpload(snapshot.Name, "", bytes.NewReader(raw)); err != nil {
							t.Fatal(err)
						}
					} else {
						members, err := filesUnder(snapshotPath)
						if err != nil {
							t.Fatal(err)
						}
						for _, member := range append(members, escrow.SumsName) {
							raw, err := os.ReadFile(filepath.Join(snapshotPath, filepath.FromSlash(member)))
							if err != nil {
								t.Fatal(err)
							}
							if err := a.newMachineUpload(snapshot.Name, member, bytes.NewReader(raw)); err != nil {
								t.Fatal(err)
							}
						}
					}
					done, err := a.newMachineRestore(t.Context(), snapshot.Name, nil)
					if err != nil {
						t.Fatal(err)
					}
					if done.Identity != identity || done.RestoredTo != snapshot.Record {
						t.Fatalf("identity changed: %+v", done)
					}
					want := preference
					if want == "" {
						want = "sqlite"
					}
					if state := a.databaseState(); state.Active != want || state.Notice != "" {
						t.Fatalf("target format: %+v, want %s", state, want)
					}
					var n int
					if err := a.store.DB().QueryRow("SELECT count(*) FROM conversations WHERE content=?", conversation).Scan(&n); err != nil || n != 1 {
						t.Fatalf("conversation lost: %d %v", n, err)
					}
					if restoringOnNewMachine(a.configSnapshot()) || len(setAsideSets(a.configSnapshot())) != 0 {
						t.Fatal("completed blank-machine restore left a pending marker or false set-aside")
					}
				})
			}
		})
	}
}
