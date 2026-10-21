package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
)

// A REAL START, A REAL UPDATER, A CONFIGURATION THE PROGRAM REFUSES. These
// run this command as it ships beside a hand-made update that is not yet
// settled: a record that names the identity as its owner, and a "previous
// program" that is a script printing one line. They hold what a test of
// the decision alone cannot: the call in main, the real claim, the real
// record, the hand-over, and the exit.

const restoredLine = "RESTORED-PREVIOUS-PROGRAM"

// refusedStartRig is one identity home and the program it runs.
type refusedStartRig struct {
	t                  *testing.T
	home, prog, record string
	config, note, data string
}

func newRefusedStartRig(t *testing.T, config string, withUpdate bool) *refusedStartRig {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &refusedStartRig{t: t, home: filepath.Join(root, "home"), prog: filepath.Join(root, "prog", "aii")}
	r.data = filepath.Join(r.home, "data")
	r.config = filepath.Join(r.home, "config", "config.json")
	r.note = r.config + ".refused-once"
	r.record = filepath.Join(root, "prog", ".aii.update.json")
	for _, d := range []string{r.data, filepath.Dir(r.config), filepath.Dir(r.prog), filepath.Join(root, "account")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	built, err := os.ReadFile(probeBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	r.write(r.prog, built, 0o755)
	r.setConfig(config)
	if withUpdate {
		previous := []byte("#!/bin/sh\necho \"" + restoredLine + " ran with: $*\"\nexit 0\n")
		r.write(filepath.Join(root, "prog", ".aii.previous"), previous, 0o755)
		sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
		r.write(r.record, []byte(fmt.Sprintf(`{"attempts":0,"backup_sha256":%q,"new_sha256":%q,"version":"9.9.9","owner":%q}`,
			sum(previous), sum(built), r.data)), 0o600)
	}
	return r
}

func (r *refusedStartRig) write(path string, data []byte, mode os.FileMode) {
	r.t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		r.t.Fatal(err)
	}
}

func (r *refusedStartRig) setConfig(body string) { r.write(r.config, []byte(body), 0o600) }

// start runs the program once in the identity's home and answers what it
// printed and its exit status.
func (r *refusedStartRig) start() (out string, exit int) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.prog, "-dir", r.home)
	cmd.Dir = filepath.Dir(r.prog)
	cmd.Env = []string{"HOME=" + filepath.Join(filepath.Dir(filepath.Dir(r.prog)), "account"), "PATH=/usr/bin:/bin"}
	raw, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		r.t.Fatalf("the start did not end: %s", raw)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(raw), ee.ExitCode()
	} else if err != nil {
		r.t.Fatalf("the start could not run: %v: %s", err, raw)
	}
	return string(raw), 0
}

// attempts is the boots the update's record has counted.
func (r *refusedStartRig) attempts() int {
	r.t.Helper()
	raw, err := os.ReadFile(r.record)
	if err != nil {
		r.t.Fatalf("the update's record: %v", err)
	}
	var rec struct {
		Attempts int `json:"attempts"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		r.t.Fatalf("the update's record does not read: %v: %s", err, raw)
	}
	return rec.Attempts
}

func (r *refusedStartRig) noted() bool { _, err := os.Stat(r.note); return err == nil }

// refused is a file every build refuses: a key no build declares.
const refusedConfig = `{"no_such_key":true}`

func mustRefuse(t *testing.T, out string, exit int) {
	t.Helper()
	if exit != 1 || !strings.Contains(out, "Config error") || !strings.Contains(out, "no_such_key") || strings.Contains(out, restoredLine) {
		t.Fatalf("a refused start must exit 1 and name the key: exit=%d\n%s", exit, out)
	}
}

// Two refused starts in a row of an unsettled update: the first is noted
// and says so, counting nothing against the update; the second restores
// the previous program, which runs with the same arguments.
func TestTwoRefusedStartsOfAnUnsettledUpdateRunThePreviousProgram(t *testing.T) {
	r := newRefusedStartRig(t, refusedConfig, true)

	out, exit := r.start()
	mustRefuse(t, out, exit)
	if !r.noted() || !strings.Contains(out, "a second refused start restores the previous program") {
		t.Fatalf("THE FIRST REFUSED START DID NOT SAY IT WAS NOTED: noted=%v\n%s", r.noted(), out)
	}
	if n := r.attempts(); n != 0 {
		t.Fatalf("THE FIRST REFUSED START COUNTED A BOOT AGAINST THE UPDATE (attempts %d): a start that then loads the corrected file would roll a good release back", n)
	}

	out, exit = r.start()
	if exit != 0 || !strings.Contains(out, restoredLine+" ran with: -dir "+r.home) {
		t.Fatalf("THE SECOND REFUSED START DID NOT HAND OVER TO THE PREVIOUS PROGRAM: exit=%d\n%s", exit, out)
	}
	if !strings.Contains(out, "Config error") || !strings.Contains(out, "no_such_key") {
		t.Fatalf("THE START THAT ROLLED BACK DID NOT NAME THE KEY IT REFUSED:\n%s", out)
	}
	if r.noted() {
		t.Fatal("the rollback left the refusal noted")
	}
}

// A boot of the update is already counted: a start that loaded its file and
// died before it was healthy. The live boot's own check restores the
// previous program at the next start whatever the file says, so a refused
// start does not promise that correcting the key keeps the update; it says
// what the next start does, and the next start, with the key corrected,
// does it.
func TestARefusedStartOverACountedBootDoesNotPromiseTheUpdate(t *testing.T) {
	r := newRefusedStartRig(t, refusedConfig, true)
	raw, err := os.ReadFile(r.record)
	if err != nil {
		t.Fatal(err)
	}
	r.write(r.record, []byte(strings.Replace(string(raw), `"attempts":0`, `"attempts":1`, 1)), 0o600)
	if r.attempts() != 1 {
		t.Fatal("rig: the record does not count one boot")
	}

	out, exit := r.start()
	mustRefuse(t, out, exit)
	if strings.Contains(out, "the update stands") || !strings.Contains(out, "the next start restores the previous program") || !r.noted() {
		t.Fatalf("A REFUSED START PROMISED AN UPDATE THE RECORD ALREADY COUNTS A BOOT AGAINST: noted=%v\n%s", r.noted(), out)
	}

	// The corrected file loads, and a record stands where the default
	// configuration keeps it, so the start is a live boot: its check comes
	// before anything reads that record.
	r.setConfig(`{}`)
	r.write(filepath.Join(r.data, "ledger.jsonl"), nil, 0o600)
	out, exit = r.start()
	if exit != 0 || !strings.Contains(out, restoredLine) {
		t.Fatalf("THE START AFTER IT DID NOT RESTORE THE PREVIOUS PROGRAM, so what the refused start said was not true: exit=%d\n%s", exit, out)
	}
	if r.noted() {
		t.Fatalf("A START THAT LOADED THE FILE LEFT THE REFUSAL NOTED\n%s", out)
	}
}

// The second refused start's restore does not complete (here the backup is
// not what the update recorded). No refused start counts a boot, so the
// record is as the update left it, the start says the update stands if
// the key is corrected, and the start that then loads the corrected file,
// the backup sound again by then, is the update's first boot: counted, and
// not rolled back.
func TestAStartThatLoadsAfterARefusedStartsRestoreDidNotCompleteKeepsTheUpdate(t *testing.T) {
	r := newRefusedStartRig(t, refusedConfig, true)
	out, exit := r.start()
	mustRefuse(t, out, exit)
	backup := filepath.Join(filepath.Dir(r.prog), ".aii.previous")
	previous, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	r.write(backup, previous[:len(previous)-1], 0o755)
	for i := 0; i < 2; i++ {
		out, exit = r.start()
		mustRefuse(t, out, exit)
		if !strings.Contains(out, "ROLLBACK REFUSED") || !strings.Contains(out, "No boot is counted against the update") || !strings.Contains(out, "the update stands") {
			t.Fatalf("A RESTORE THAT DID NOT COMPLETE WAS NOT SAID AS IT STANDS:\n%s", out)
		}
		if n := r.attempts(); n != 0 || !r.noted() {
			t.Fatalf("A REFUSED START COUNTED A BOOT AGAINST THE UPDATE (attempts %d, noted %v): a start that then loads the corrected file would be rolled back and the release barred\n%s", n, r.noted(), out)
		}
	}

	// The backup is sound again and the key corrected. A record stands
	// where the default configuration keeps it, so the start is a live
	// boot and its own check runs.
	r.write(backup, previous, 0o755)
	r.setConfig(`{}`)
	r.write(filepath.Join(r.data, "ledger.jsonl"), nil, 0o600)
	out, _ = r.start()
	if strings.Contains(out, restoredLine) || strings.Contains(out, "ROLLBACK") {
		t.Fatalf("A START THAT LOADED ITS FILE WAS ROLLED BACK FOR AN EARLIER REFUSED ONE:\n%s", out)
	}
	if n := r.attempts(); n != 1 || r.noted() {
		t.Fatalf("the start that loaded must be the update's first boot, counted, the refusal forgotten: attempts %d, noted %v\n%s", n, r.noted(), out)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.prog), ".aii.update.failed.json")); err == nil {
		t.Fatalf("THE RELEASE WAS BARRED ON THIS MACHINE for a start that never booted\n%s", out)
	}
}

// The operator corrects the key between the two: the start that loads the
// file forgets the refusal, and the update stands. A later refused start
// is the first again.
func TestARefusedStartIsForgottenByAStartThatLoadsTheFile(t *testing.T) {
	r := newRefusedStartRig(t, refusedConfig, true)
	out, exit := r.start()
	mustRefuse(t, out, exit)
	if !r.noted() {
		t.Fatalf("rig: the first refused start was not noted\n%s", out)
	}

	// The corrected file loads. This test holds the identity's claim, so
	// the start ends at it, having loaded its configuration and started
	// nothing.
	r.setConfig(`{}`)
	claim, err := os.OpenFile(filepath.Join(r.data, ".identity.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := filelock.Lock(claim); err != nil {
		t.Fatal(err)
	}
	out, exit = r.start()
	if exit == 0 || strings.Contains(out, "Config error") || !strings.Contains(out, "already running") || strings.Contains(out, restoredLine) {
		t.Fatalf("rig: the corrected start should load its file and end at the held claim: exit=%d\n%s", exit, out)
	}
	if r.noted() {
		t.Fatalf("A START THAT LOADED THE FILE LEFT THE REFUSAL NOTED: the next refused start would roll a good release back\n%s", out)
	}

	// While the claim is held, a refused start changes nothing at all.
	r.setConfig(refusedConfig)
	out, exit = r.start()
	mustRefuse(t, out, exit)
	if r.noted() || r.attempts() != 0 {
		t.Fatalf("A SECOND START OF A RUNNING IDENTITY TOUCHED ITS UPDATE: noted=%v attempts=%d\n%s", r.noted(), r.attempts(), out)
	}
	claim.Close()

	// Refused again with the claim free: the first again, not a rollback.
	out, exit = r.start()
	mustRefuse(t, out, exit)
	if !r.noted() || r.attempts() != 0 {
		t.Fatalf("after a start that loaded, a refused start must be the first again: noted=%v attempts=%d\n%s", r.noted(), r.attempts(), out)
	}
}

// No update of this identity's is unsettled: the operator's own typo. The
// refusal stands at every start and nothing is written beside the program
// or the configuration.
func TestARefusedStartWithNoUpdateChangesNothing(t *testing.T) {
	r := newRefusedStartRig(t, refusedConfig, false)
	for i := 0; i < 2; i++ {
		out, exit := r.start()
		mustRefuse(t, out, exit)
		if strings.Contains(out, "refused start") {
			t.Fatalf("with no update a refused start must promise no rollback:\n%s", out)
		}
	}
	for dir, want := range map[string]string{filepath.Dir(r.prog): "aii", filepath.Dir(r.config): "config.json"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		if strings.Join(names, " ") != want {
			t.Fatalf("a refused start with no update wrote into %s: %v", dir, names)
		}
	}
}

// THE ROLLBACK PUTS PROVIDERS.JSON BACK WITH THE PROGRAM. The update's first
// boot left providers.json as the previous version ran with it beside the
// file, and the newer version then wrote a field the previous one refuses.
// The second refused start hands over to the previous program with that
// file back in place, and the newer one set aside beside it.
func TestARefusedStartsRollbackPutsTheProvidersFileBack(t *testing.T) {
	r := newRefusedStartRig(t, refusedConfig, true)
	providers := filepath.Join(filepath.Dir(r.config), "providers.json")
	const previous = `{"providers":[{"name":"Hosted","api_type":"openai","url":"https://hosted.example/v1","default_model":"m","default":true}]}`
	const newer = `{"providers":[{"name":"Hosted","api_type":"openai","url":"https://hosted.example/v1","default_model":"m","default":true}],"model_capabilities":{"m":{"context_length":64000}}}`
	r.write(providers+".before-update", []byte(previous), 0o600)
	r.write(providers, []byte(newer), 0o600)

	out, exit := r.start()
	mustRefuse(t, out, exit)
	out, exit = r.start()
	if exit != 0 || !strings.Contains(out, restoredLine) {
		t.Fatalf("rig: the second refused start did not hand over: exit=%d\n%s", exit, out)
	}
	if got, err := os.ReadFile(providers); err != nil || string(got) != previous {
		t.Fatalf("THE PREVIOUS PROGRAM WAS HANDED A PROVIDERS FILE IT REFUSES: %q (%v)\n%s", got, err, out)
	}
	if got, err := os.ReadFile(providers + ".rolled-back"); err != nil || string(got) != newer {
		t.Fatalf("the newer version's providers.json was not set aside: %q (%v)", got, err)
	}
}
