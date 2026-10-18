package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestDatabaseCompressionSurvivesRestoreAndPutBack(t *testing.T) {
	r := newRestoreRig(t)
	t.Chdir(r.dir)
	r.cfg.Identity.DBFormat = "zstd"
	at, snapshot := r.lived(t, 1, "compressed-before", true)
	later, _ := r.lived(t, 1, "compressed-after", false)
	a := r.boot(t)
	defer func() { stopApp(t, a) }()
	if err := askRestore(t, a, continuity.SourceSnapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)
	a = r.boot(t)
	if got := a.databaseState(); got.Active != "zstd" || got.Notice != "" || a.ledger.LastSeq() != at {
		t.Fatalf("restored snapshot did not activate its preference: %+v, seq=%d want=%d", got, a.ledger.LastSeq(), at)
	}
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 {
		t.Fatalf("set-aside inventory: %+v", sets)
	}
	kept, err := store.OpenReadOnly(filepath.Join(sets[0].Path, continuity.SnapshotDB))
	if err != nil {
		t.Fatal(err)
	}
	format, err := kept.DatabaseFormat(t.Context())
	closeErr := kept.Close()
	if err != nil || closeErr != nil || format != "zstd" {
		t.Fatalf("original compressed data not kept: %q %v %v", format, err, closeErr)
	}
	if err := askRestore(t, a, continuity.SourceSetAside, sets[0].Name); err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)
	a = r.boot(t)
	if got := a.databaseState(); got.Active != "zstd" || got.Notice != "" || a.ledger.LastSeq() != later {
		t.Fatalf("put-back did not restore current history: %+v, seq=%d want=%d", got, a.ledger.LastSeq(), later)
	}
}

// Compression is a host storage preference, not identity truth. Both kinds
// of recovery snapshot from a compressed source must work through firstboot's
// existing restore door, with or without compression on the receiving host.
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
			var snapshot dashboard.ContinuitySnapshot
			var snapshotPath, identity string
			const conversation = "runtime data that ledger replay cannot reconstruct"
			func() {
				a := r.boot(t)
				defer stopApp(t, a)
				if state := a.databaseState(); state.Active != "zstd" || state.Notice != "" {
					t.Fatalf("source is not compressed: %+v", state)
				}
				appendFixtureEvent(t, a, r.keyPath, "exp_compressed_portable")
				if err := a.store.AddConversationTurn("operator", conversation); err != nil {
					t.Fatal(err)
				}
				var err error
				sealed, _, err = continuityDoor(a).EscrowCreate([]byte(newMachinePass))
				if err != nil {
					t.Fatal(err)
				}
				if encrypted {
					if _, err := continuityDoor(a).EscrowCheck(sealed, []byte(newMachinePass)); err != nil {
						t.Fatal(err)
					}
				}
				snapshot, err = askedSnapshot(t, a)
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
					defer stopApp(t, a)
					a.cfg.Identity.DBFormat = preference // startup-only fixture input
					if _, err := a.continuity.NewMachineKeys(sealed, []byte(newMachinePass)); err != nil {
						t.Fatal(err)
					}
					if encrypted {
						raw, err := os.ReadFile(snapshotPath)
						if err != nil {
							t.Fatal(err)
						}
						if err := a.continuity.NewMachineUpload(snapshot.Name, "", bytes.NewReader(raw)); err != nil {
							t.Fatal(err)
						}
					} else {
						members, err := continuitytest.Members(snapshotPath)
						if err != nil {
							t.Fatal(err)
						}
						for _, member := range append(members, continuity.SnapshotSums) {
							raw, err := os.ReadFile(filepath.Join(snapshotPath, filepath.FromSlash(member)))
							if err != nil {
								t.Fatal(err)
							}
							if err := a.continuity.NewMachineUpload(snapshot.Name, member, bytes.NewReader(raw)); err != nil {
								t.Fatal(err)
							}
						}
					}
					done, err := a.continuity.NewMachineRestore(t.Context(), snapshot.Name, nil)
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
					if continuity.RestoringOnNewMachine(continuitySettingsOf(a.configSnapshot())) || len(continuityDoor(a).View().SetAside) != 0 {
						t.Fatal("completed blank-machine restore left a pending marker or false set-aside")
					}
				})
			}
		})
	}
}
