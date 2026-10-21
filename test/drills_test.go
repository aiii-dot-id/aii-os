package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every drill parses, answers --help without touching an empty root,
// and the evidence template has a row for each of the ten journeys.
func TestBetaDrillsAreRunnableAndSideEffectFree(t *testing.T) {
	requireShell(t)
	root := repoRoot(t)
	drills, _ := filepath.Glob(filepath.Join(root, "packaging", "drills", "*.sh"))
	if len(drills) < 6 {
		t.Fatalf("expected lib.sh and five drills, found %d", len(drills))
	}
	empty := t.TempDir()
	for _, d := range drills {
		if out, err := exec.Command("sh", "-n", d).CombinedOutput(); err != nil {
			t.Fatalf("%s does not parse: %s", d, out)
		}
		if strings.HasSuffix(d, "lib.sh") {
			continue
		}
		cmd := exec.Command("sh", d, "--help")
		cmd.Env = append(os.Environ(), "AII_ROOT="+empty, "AII=/nonexistent/aii")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "Usage:") {
			t.Fatalf("%s --help: %v\n%s", d, err, out)
		}
		if entries, _ := os.ReadDir(empty); len(entries) != 0 {
			t.Fatalf("%s --help wrote into the root: %v", d, entries)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "docs", "BETA1_EVIDENCE.md"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ {
		if !strings.Contains(string(b), "| "+strconv.Itoa(i)+" | ") {
			t.Fatalf("evidence template has no row for journey %d", i)
		}
	}
}

// runLib sources lib.sh with LOG pointed at a temp slot and runs a shell
// snippet against it; the snippet exits non-zero to signal a failure.
func runLib(t *testing.T, script string) (string, bool) {
	t.Helper()
	root := repoRoot(t)
	slot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(slot, "s", "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	full := ". packaging/drills/lib.sh\n" + script
	cmd := exec.Command("sh", "-c", full)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AII_ROOT="+slot, "AII_SLOT=s", "AII=/bin/true")
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func TestTheDrillMarkerDistinguishesAFreshBootFromAnOldLine(t *testing.T) {
	// (a) log_gen: none when absent, a value when present, and it CHANGES
	// when the file is rotated (renamed aside + a fresh one created).
	if out, ok := runLib(t, `
		[ "$(log_gen)" = none ] || { echo "absent log is not none: $(log_gen)"; exit 1; }
		: > "$LOG"; g0=$(log_gen)
		[ "$g0" != none ] || { echo "present log read as none"; exit 1; }
		mv "$LOG" "$LOG.1"; : > "$LOG"; g1=$(log_gen)
		[ "$g0" != "$g1" ] || { echo "rotation did not change the generation: $g0 = $g1"; exit 1; }
	`); !ok {
		t.Fatalf("log_gen semantics: %s", out)
	}

	// (b) fresh_boot SUCCEEDS only after a rotation carrying the boot line.
	if out, ok := runLib(t, `
		: > "$LOG"; gen=$(log_gen)
		mv "$LOG" "$LOG.1"; echo "2026/09/02 Boot identity: AII OS v0.1.0" > "$LOG"
		fresh_boot "$gen" 3 || { echo "a real fresh boot was not detected"; exit 1; }
	`); !ok {
		t.Fatalf("fresh_boot positive: %s", out)
	}

	// (c) fresh_boot TIMES OUT when the boot line is in the SAME
	// generation (no rotation) — an old line cannot pass a new check.
	if out, ok := runLib(t, `
		echo "2020/01/01 Boot identity: AII OS v0.0.1" > "$LOG"; gen=$(log_gen)
		if fresh_boot "$gen" 2; then echo "an old boot line in the same generation was accepted"; exit 1; fi
	`); !ok {
		t.Fatalf("fresh_boot false-positive guard: %s", out)
	}
}

// ONE BOOT IS NOT ALWAYS ONE FILE. The live log rolls within a boot at
// logs.max_bytes, so what a drill looks for since the fresh boot may lie
// in a file rolled since: read from the live file alone, a category
// written before a roll was not seen, and the SAFE drill's "the restored
// pair did not enter SAFE" is an absence. saw_since_fresh reads the
// boot's files, back to the one that carries the boot line, and nothing
// older.
func TestADrillReadsEveryFileOfTheBootAndNoOlderOne(t *testing.T) {
	if out, ok := runLib(t, `
		dir=$(dirname "$LOG")
		# The boot before: it entered SAFE. Rotated at the start of this one.
		printf '%s\n' "Boot identity: AII OS v0.1.0" "[safe.start] entering" > "$dir/aii-20261012-090000.log"
		touch -t 202610120900 "$dir/aii-20261012-090000.log"
		# This boot: its first file, rolled at a size, then a second, then the live one.
		printf '%s\n' "Boot identity: AII OS v0.2.0" "[ledger.start] opened" > "$dir/aii-20261013-100000.log"
		touch -t 202610131000 "$dir/aii-20261013-100000.log"
		printf '%s\n' "[plugins.start] loaded" > "$dir/aii-20261013-100500.log"
		touch -t 202610131005 "$dir/aii-20261013-100500.log"
		printf '%s\n' "[turn.start] first" > "$LOG"
		saw_since_fresh "turn.start" || { echo "a line in the live file was not seen"; exit 1; }
		saw_since_fresh "plugins.start" || { echo "a line rolled out of the live file was not seen"; exit 1; }
		saw_since_fresh "ledger.start" || { echo "a line in the first file of the boot was not seen"; exit 1; }
		if saw_since_fresh "safe.start"; then echo "a line of the boot before was read as this one's"; exit 1; fi
		[ "$(boot_logs | wc -l | tr -d ' ')" = 3 ] || { echo "the files of the boot are not three: $(boot_logs | tr '\n' ' ')"; exit 1; }
		# No roll since the boot: the live file alone, as it always was.
		printf '%s\n' "Boot identity: AII OS v0.2.0" "[turn.start] first" > "$LOG"
		[ "$(boot_logs | wc -l | tr -d ' ')" = 1 ] || { echo "a boot that has not rolled reads more than its live file"; exit 1; }
		if saw_since_fresh "plugins.start"; then echo "a rolled file of an earlier boot was read"; exit 1; fi
	`); !ok {
		t.Fatalf("a drill's reading of one boot's log: %s", out)
	}
}

func TestTheSafeDrillFindsTheNewestCompleteSnapshot(t *testing.T) {
	out, ok := runLib(t, `
mkdir -p "$DATA/backups/ledger-20260902T040000Z-seq5" "$DATA/backups/ledger-20260904T040000Z-seq20" "$DATA/backups/ledger-20260905T040000Z-seq30"
: > "$DATA/backups/ledger-20260901T040000Z-seq1.jsonl"
: > "$DATA/backups/ledger-20260901T040000Z-seq1.jsonl.sha256"
: > "$DATA/backups/ledger-20260902T040000Z-seq5/SHA256SUMS"
: > "$DATA/backups/ledger-20260904T040000Z-seq20/SHA256SUMS"
got=$(newest_snapshot)
[ "$got" = "$DATA/backups/ledger-20260904T040000Z-seq20" ] || { echo "newest_snapshot=$got"; exit 1; }
# A snapshot that was asked for carries its class in its name and is a
# snapshot like any other: the newest of either class is the newest.
mkdir -p "$DATA/backups/ledger-20260904T181500Z-seq22-ondemand"
: > "$DATA/backups/ledger-20260904T181500Z-seq22-ondemand/SHA256SUMS"
got=$(newest_snapshot)
[ "$got" = "$DATA/backups/ledger-20260904T181500Z-seq22-ondemand" ] || { echo "with one asked for newest_snapshot=$got"; exit 1; }
: > "$DATA/backups/ledger-20260904T190000Z-seq23-ondemand.tar.age"
got=$(newest_snapshot)
[ "$got" = "$DATA/backups/ledger-20260904T190000Z-seq23-ondemand.tar.age" ] || { echo "with an encrypted one asked for newest_snapshot=$got"; exit 1; }
rm -rf "$DATA/backups/ledger-20260904T181500Z-seq22-ondemand" "$DATA/backups/ledger-20260904T190000Z-seq23-ondemand.tar.age"
rm "$DATA/backups/ledger-20260904T040000Z-seq20/SHA256SUMS" "$DATA/backups/ledger-20260902T040000Z-seq5/SHA256SUMS"
got=$(newest_snapshot)
[ -z "$got" ] || { echo "without sums newest_snapshot=$got"; exit 1; }
`)
	if !ok {
		t.Fatalf("snapshot discovery: %s", out)
	}
}

func TestALineFromARolledBackBootIsReadAcrossTheReexec(t *testing.T) {
	out, ok := runLib(t, `
mkdir -p "$SLOTDIR/log"
echo "rolled back to previous binary" > "$SLOTDIR/log/aii-20260903-old.log"
mark_start
if saw_since_mark "rolled back to previous binary"; then echo "a line older than the mark counted"; exit 1; fi
echo "Boot identity: x" > "$SLOTDIR/log/aii.log"
saw_since_mark "rolled back to previous binary" && { echo "found a line that is not there"; exit 1; }
echo "updates: rolled back to previous binary — re-executing" > "$SLOTDIR/log/aii-20260904-193239.log"
saw_since_mark "rolled back to previous binary" || { echo "the rotated generation's line was not read"; exit 1; }
`)
	if !ok {
		t.Fatalf("read across the re-exec: %s", out)
	}
}

// gone_within is true once its command fails within the bound, and
// false when it keeps succeeding — the shape an asynchronous stop needs.
func TestGoneWithinWaitsForTheThingToGo(t *testing.T) {
	out, ok := runLib(t, `
f="$SLOTDIR/present"; : > "$f"
( sleep 2; rm -f "$f" ) &
gone_within 6 test -e "$f" || { echo "did not see it go"; exit 1; }
: > "$f"
if gone_within 1 test -e "$f"; then echo "reported gone while present"; exit 1; fi
`)
	if !ok {
		t.Fatalf("gone_within: %s", out)
	}
}

func TestTheManifestInventoryReadsProjectManifests(t *testing.T) {
	out, ok := runLib(t, `
mkdir -p "$SLOTDIR/projects/beta"
printf '{"name":"beta"}' > "$SLOTDIR/projects/beta/project.json"
inv=$(manifest_inventory)
case "$inv" in *"./projects/beta/project.json"*) ;; *) echo "inventory: $inv"; exit 1;; esac
printf '{"name":"beta","note":"changed"}' > "$SLOTDIR/projects/beta/project.json"
[ "$(manifest_inventory)" != "$inv" ] || { echo "a changed project manifest left the inventory unchanged"; exit 1; }
`)
	if !ok {
		t.Fatalf("project manifest inventory: %s", out)
	}
}

func TestTheManifestInventoryIsByContentNotCount(t *testing.T) {
	out, ok := runLib(t, `
		mkdir -p "$SLOTDIR/p1" "$SLOTDIR/p2"
		echo A > "$SLOTDIR/p1/manifest.json"; echo A > "$SLOTDIR/p2/manifest.json"
		inv0=$(manifest_inventory)
		[ "$(printf '%s\n' "$inv0" | wc -l)" -eq 2 ] || { echo "expected two manifests"; exit 1; }
		echo CHANGED > "$SLOTDIR/p2/manifest.json"    # same COUNT, different content
		[ "$(manifest_inventory)" != "$inv0" ] || { echo "a content change was invisible to the inventory"; exit 1; }
	`)
	if !ok {
		t.Fatalf("manifest inventory semantics: %s", out)
	}
}

// drillFixture is a stub aii and a fake slot: enough for the drills'
// verbs, stamps and log to be driven without an identity.
type drillFixture struct {
	root, name, slot, stub, log, versionFile string
}

func newDrillFixture(t *testing.T) drillFixture {
	t.Helper()
	root := t.TempDir()
	name := uniqueSlotName(t)
	slot := filepath.Join(root, name)
	for _, d := range []string{"data", "log"} {
		if err := os.MkdirAll(filepath.Join(slot, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "aii")
	version := filepath.Join(stubDir, "version.txt")
	script := "#!/bin/sh\ncase \"$1\" in\n  -version) cat \"$(dirname \"$0\")/version.txt\" ;;\n  *) exit 0 ;;\nesac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return drillFixture{root: root, name: name, slot: slot, stub: stub, log: filepath.Join(slot, "log", "aii.log"), versionFile: version}
}

func (f drillFixture) setVersion(t *testing.T, stamp string) {
	t.Helper()
	if err := os.WriteFile(f.versionFile, []byte(stamp+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f drillFixture) swapBinary(t *testing.T, legacy bool) {
	t.Helper()
	evidence := []string{filepath.Join(filepath.Dir(f.stub), ".aii.update.json"), filepath.Join(filepath.Dir(f.stub), ".aii.previous")}
	if legacy {
		evidence = []string{filepath.Join(f.slot, "data", "aii.previous"), filepath.Join(f.slot, "data", ".update_pending")}
	}
	for _, p := range evidence {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	current, err := os.ReadFile(f.stub)
	if err != nil {
		t.Fatal(err)
	}
	staged := f.stub + ".next"
	defer os.Remove(staged)
	if err := os.WriteFile(staged, append(current, []byte("# swapped\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, f.stub); err != nil {
		t.Fatal(err)
	}
}

// rotateLogWithBanner does what a boot does: rotates the live log aside
// and opens a fresh one carrying the given boot banner.
func (f drillFixture) rotateLogWithBanner(t *testing.T, stamp string) {
	t.Helper()
	_ = os.Rename(f.log, f.log+".1")
	if err := os.WriteFile(f.log, []byte("2026/09/02 Boot identity: "+stamp+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f drillFixture) env() []string {
	return append(os.Environ(),
		"AII="+f.stub, "AII_ROOT="+f.root, "AII_SLOT="+f.name, "EVIDENCE="+filepath.Join(f.root, "ev.tsv"),
		"AII_DRILL_POLL=1", "AII_DRILL_BOOT_WAIT=3", "AII_DRILL_STOP_WAIT=2")
}

func (f drillFixture) evidence(t *testing.T) string {
	b, _ := os.ReadFile(filepath.Join(f.root, "ev.tsv"))
	return string(b)
}

// drillOutput observes the existing first stdout line: update.sh emits its
// installed-build announcement only AFTER reading stamp, hash and log generation.
// The exec stdout copier is its sole writer; String is read only after Wait.
type drillOutput struct {
	buffer bytes.Buffer // named: an embedded Buffer promotes ReadFrom, bypassing Write
	ready  chan struct{}
	once   sync.Once
}

func (o *drillOutput) Write(p []byte) (int, error) {
	n, err := o.buffer.Write(p)
	if bytes.IndexByte(o.buffer.Bytes(), '\n') >= 0 {
		o.once.Do(func() { close(o.ready) })
	}
	return n, err
}

func (o *drillOutput) String() string { return o.buffer.String() }

func TestDrillOutputSignalsTheBaselineThroughIOCopy(t *testing.T) {
	out := &drillOutput{ready: make(chan struct{})}
	for i, part := range []string{"baseline", "\nnext line\n"} {
		// Like an exec pipe copy, the source need not implement WriterTo.
		if _, err := io.Copy(out, struct{ io.Reader }{strings.NewReader(part)}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-out.ready:
			if i == 0 {
				t.Fatal("partial baseline announced readiness")
			}
		default:
			if i == 1 {
				t.Fatal("stdout copy bypassed the baseline readiness signal")
			}
		}
	}
	if out.String() != "baseline\nnext line\n" {
		t.Fatal("drill output was lost")
	}
}

// runUpdateDrill starts update.sh and waits for its actual baseline announcement.
// A 1.5-second sleep could run the scenario BEFORE a loaded child read its
// baseline, leaving it waiting 30 minutes for a change that already happened.
func runUpdateDrill(t *testing.T, f drillFixture, scenario func()) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(repoRoot(t), "packaging", "drills", "update.sh"))
	cmd.WaitDelay = 2 * time.Second // a killed shell must not leave inherited pipes holding Wait
	cmd.Env = f.env()
	out := &drillOutput{ready: make(chan struct{})}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var result error
	go func() { result = cmd.Wait(); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-out.ready:
		scenario()
	case <-done:
		return fmt.Errorf("update drill exited before baseline: %v; stdout=%s stderr=%s", result, out.String(), stderr.String())
	}
	<-done
	if result != nil {
		return fmt.Errorf("update drill: %w; stdout=%s stderr=%s", result, out.String(), stderr.String())
	}
	return nil
}

func TestTheUpdateDrillRefusesAnOldProcessRestart(t *testing.T) {
	f := newDrillFixture(t)
	const old, newer = "AII OS v0.1.0 (build oldoldold)", "AII OS v0.2.0 (build newnewnew)"
	f.setVersion(t, old)
	f.rotateLogWithBanner(t, old)
	err := runUpdateDrill(t, f, func() {
		f.setVersion(t, newer)        // the file on disk now reports the new build
		f.swapBinary(t, false)        // and its bytes changed
		f.rotateLogWithBanner(t, old) // but the OLD process merely restarted
	})
	if err == nil {
		t.Fatalf("the drill recorded PASS for an old process restart:\n%s", f.evidence(t))
	}
	ev := f.evidence(t)
	if !strings.Contains(ev, "\tFAIL\t") || !strings.Contains(ev, "NEW build's banner") {
		t.Fatalf("the failure must be recorded and name the cause:\n%s", ev)
	}
}

func TestTheUpdateDrillPassesAFreshBootOfTheNewBuild(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "beside the program", true: "data directory"}[legacy], func(t *testing.T) {
			f := newDrillFixture(t)
			const old, newer = "AII OS v0.1.0 (build oldoldold)", "AII OS v0.2.0 (build newnewnew)"
			f.setVersion(t, old)
			f.rotateLogWithBanner(t, old)
			err := runUpdateDrill(t, f, func() {
				f.setVersion(t, newer)
				f.swapBinary(t, legacy)
				f.rotateLogWithBanner(t, newer)
			})
			if err != nil {
				t.Fatalf("the honest update was refused (%v):\n%s", err, f.evidence(t))
			}
			if ev := f.evidence(t); !strings.Contains(ev, "\tPASS\t") || !strings.Contains(ev, "new build's banner") {
				t.Fatalf("PASS not recorded with its evidence:\n%s", ev)
			}
		})
	}
}

// stop_slot must VERIFY the process is gone. It used to `|| true; sleep
// 2`, so every destructive drill could act under a live process.
func TestStopSlotRefusesWhileTheProcessLives(t *testing.T) {
	requireShell(t)
	f := newDrillFixture(t)
	// A process whose command line carries the slot's -dir marker, which
	// the stub's `stop` does not kill (it exits 0 and does nothing).
	live, _ := liveSlotProcess(t, f.slot)
	run := func() error {
		c := exec.Command("sh", "-c", ". packaging/drills/lib.sh; stop_slot")
		c.Dir = repoRoot(t)
		c.Env = f.env()
		return c.Run()
	}
	if err := run(); err == nil {
		t.Fatal("stop_slot returned success while the process was alive")
	}
	live.Process.Kill()
	live.Wait()
	time.Sleep(200 * time.Millisecond)
	if err := run(); err != nil {
		t.Fatalf("stop_slot failed with no process to stop: %v", err)
	}
}

// slot_pid FINDS ITS OWN SLOT AND NO OTHER. It matched a prefix of the
// slot's directory — identity-1 found identity-10 — and on Windows the
// slot's name anywhere on an aii.exe command line; the drills stop, kill
// and restore under what it finds. A neighbour whose directory begins
// with this slot's is not found; this slot's own process is.
func TestSlotPidFindsOnlyItsOwnSlot(t *testing.T) {
	requireShell(t)
	f := newDrillFixture(t)
	slotPid := func() string {
		t.Helper()
		c := exec.Command("sh", "-c", ". packaging/drills/lib.sh; slot_pid")
		c.Dir = repoRoot(t)
		c.Env = f.env()
		out, _ := c.Output()
		return strings.TrimSpace(string(out))
	}
	neighbour, _ := liveSlotProcess(t, f.slot+"0")
	if got := slotPid(); got != "" {
		t.Fatalf("slot_pid found %s for this slot; the only live process is the neighbour %d (%s0)", got, neighbour.Process.Pid, f.slot)
	}
	own, _ := liveSlotProcess(t, f.slot)
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := slotPid()
		if got == strconv.Itoa(own.Process.Pid) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot_pid = %q, want this slot's process %d", got, own.Process.Pid)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// requireShell skips a drill test where no POSIX shell is on PATH. The
// drills are shell scripts on every platform; on Windows they run under
// Git for Windows' sh.exe, which the session must have on PATH.
func requireShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no POSIX shell on PATH (on Windows, put Git for Windows' usr\\bin on PATH): %v", err)
	}
}

// A RESTORE NEVER LEAVES ANOTHER DATABASE'S WRITE-AHEAD LOG BESIDE THE ONE
// IT PUTS DOWN — not the first time, and not the second. The SAFE drill
// restores, boots, and when that boot fails its trap restores again; the
// failed boot has by then written its own -wal beside the restored
// database. A restore that moved the old files aside only once copied the
// snapshot's aii.db in under that log, and the next open replayed a failed
// run over a snapshot that had just been put back clean. Every call moves
// all three files aside, under a name no earlier call used, and nothing is
// deleted: the first aside is the operator's own database.
func TestARestoreMovesEveryDatabaseFileAsideEveryTime(t *testing.T) {
	if out, ok := runLib(t, `
mkdir -p "$DATA/backups/snap"
printf snapshot-db > "$DATA/backups/snap/aii.db"
printf snapshot-tail > "$DATA/backups/snap/ledger.jsonl"
printf live-db > "$DATA/aii.db"; printf live-wal > "$DATA/aii.db-wal"; printf live-shm > "$DATA/aii.db-shm"
printf damaged-tail > "$LEDGER"
aside="$DATA/aii.db.drill-aside-T"

restore_pair "$DATA/backups/snap" "$aside" || { echo "the first restore failed"; exit 1; }
[ "$(cat "$DATA/aii.db")" = snapshot-db ] && [ "$(cat "$LEDGER")" = snapshot-tail ] || { echo "the pair was not put back"; exit 1; }
[ ! -e "$DATA/aii.db-wal" ] && [ ! -e "$DATA/aii.db-shm" ] || { echo "the first restore left the old database's log beside the new one"; exit 1; }
[ "$(cat "$aside.1")" = live-db ] && [ "$(cat "$aside.1-wal")" = live-wal ] && [ "$(cat "$aside.1-shm")" = live-shm ] || { echo "the operator's database was not kept whole"; exit 1; }

# The boot after the restore fails, having written a log of its own.
printf failed-boot-wal > "$DATA/aii.db-wal"; printf failed-boot-shm > "$DATA/aii.db-shm"
restore_pair "$DATA/backups/snap" "$aside" || { echo "the second restore failed"; exit 1; }
[ ! -e "$DATA/aii.db-wal" ] && [ ! -e "$DATA/aii.db-shm" ] || { echo "THE FAILED BOOT'S WRITE-AHEAD LOG IS STILL BESIDE THE RESTORED DATABASE"; exit 1; }
[ "$(cat "$DATA/aii.db")" = snapshot-db ] || { echo "the second restore did not put the snapshot back"; exit 1; }
[ "$(cat "$aside.2")" = snapshot-db ] && [ "$(cat "$aside.2-wal")" = failed-boot-wal ] && [ "$(cat "$aside.2-shm")" = failed-boot-shm ] || { echo "the failed boot's files were not kept together"; exit 1; }
[ "$(cat "$aside.1")" = live-db ] && [ "$(cat "$aside.1-wal")" = live-wal ] || { echo "THE OPERATOR'S DATABASE WAS OVERWRITTEN BY THE SECOND ASIDE"; exit 1; }
`); !ok {
		t.Fatalf("restore_pair: %s", out)
	}
	// And the drill uses it, rather than a copy of its own that could
	// drift back to moving aside once.
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging", "drills", "safe.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `put_back(){ restore_pair "$snap" "$aside"; }`) || strings.Contains(string(src), `cp "$snap/aii.db"`) {
		t.Fatal("safe.sh does not restore through restore_pair")
	}
}

// ONE SET, TWO KINDS. A snapshot is a directory with its checksum list or
// an encrypted `.tar.age` file, and the newest is the newest by stamp
// whichever it is; a plaintext one opens as itself, with nothing unpacked.
func TestTheDrillLibrarySeesTheMixedSnapshotSet(t *testing.T) {
	if out, ok := runLib(t, `
mkdir -p "$DATA/backups/ledger-20260917T040000Z-seq5" "$DATA/backups/ledger-20260919T040000Z-seq9"
: > "$DATA/backups/ledger-20260917T040000Z-seq5/SHA256SUMS"
: > "$DATA/backups/ledger-20260918T040000Z-seq7.tar.age"
got=$(newest_snapshot)
[ "$got" = "$DATA/backups/ledger-20260918T040000Z-seq7.tar.age" ] || { echo "newest=$got (a directory with no checksum list is not a snapshot; the encrypted file is)"; exit 1; }
: > "$DATA/backups/ledger-20260919T040000Z-seq9/SHA256SUMS"
got=$(newest_snapshot)
[ "$got" = "$DATA/backups/ledger-20260919T040000Z-seq9" ] || { echo "newest=$got"; exit 1; }
opened=$(open_snapshot "$got" "$DATA/backups/.opened") || { echo "a plaintext snapshot did not open as itself"; exit 1; }
[ "$opened" = "$got" ] && [ ! -e "$DATA/backups/.opened" ] || { echo "opened=$opened — a plaintext snapshot must open as itself, unpacking nothing"; exit 1; }
`); !ok {
		t.Fatalf("mixed snapshot set: %s", out)
	}
}

// A RESTORING DRILL'S EXIT. The restore reads the opened snapshot, so it
// comes first and the plaintext goes only after it landed. When the
// identity will not stop, nothing is restored under it and the operator
// is told to restore by hand FROM THE SNAPSHOT — which the trap used to
// delete with its very next statement. A restore that fails leaves it too.
func TestARestoringDrillNeverRemovesTheSnapshotItTellsTheOperatorToUse(t *testing.T) {
	setup := `
mkdir -p "$DATA/backups/.drill-opened-x"
printf snapshot-db > "$DATA/backups/.drill-opened-x/aii.db"
printf snapshot-ledger > "$DATA/backups/.drill-opened-x/ledger.jsonl"
printf damaged-db > "$DATA/aii.db"; printf damaged-ledger > "$LEDGER"
snap="$DATA/backups/.drill-opened-x"; opened="$snap"; aside="$DATA/aii.db.drill-aside"; restored=0
`
	// The identity will not stop: nothing restored, the snapshot LEFT, and said.
	if out, ok := runLib(t, setup+`
stop_slot(){ return 1; }
said=$(restore_on_exit 2>&1)
[ -s "$opened/aii.db" ] || { echo "THE SNAPSHOT THE OPERATOR WAS TOLD TO RESTORE FROM WAS REMOVED"; exit 1; }
[ "$(cat "$DATA/aii.db")" = damaged-db ] || { echo "restored under a running identity"; exit 1; }
case "$said" in *"by hand"*"LEFT in place"*"$opened"*) ;; *) echo "the operator was not told the snapshot is left, and where: $said"; exit 1;; esac
`); !ok {
		t.Fatalf("identity still running: %s", out)
	}
	// It stops and the restore lands: the pair is back and the plaintext is gone.
	if out, ok := runLib(t, setup+`
stop_slot(){ return 0; }
restore_on_exit
[ "$(cat "$DATA/aii.db")" = snapshot-db ] && [ "$(cat "$LEDGER")" = snapshot-ledger ] || { echo "the pair was not restored"; exit 1; }
[ ! -e "$opened" ] || { echo "the opened snapshot — the identity in the clear — was left after a restore that landed"; exit 1; }
`); !ok {
		t.Fatalf("restore lands: %s", out)
	}
	// It stops and the restore FAILS: the snapshot is left, and said.
	if out, ok := runLib(t, setup+`
stop_slot(){ return 0; }
rm "$opened/ledger.jsonl"
said=$(restore_on_exit 2>&1)
[ -s "$opened/aii.db" ] || { echo "a failed restore removed the snapshot it failed to restore from"; exit 1; }
case "$said" in *"THE RESTORE FAILED"*) ;; *) echo "a failed restore said nothing: $said"; exit 1;; esac
`); !ok {
		t.Fatalf("restore fails: %s", out)
	}
	// The drill already restored and proved it: only the plaintext goes.
	if out, ok := runLib(t, setup+`
stop_slot(){ echo "stop_slot was called after the drill had restored"; exit 1; }
restored=1; restore_on_exit
[ ! -e "$opened" ] || { echo "the plaintext was left after a drill that finished"; exit 1; }
[ "$(cat "$DATA/aii.db")" = damaged-db ] || { echo "restored a second time"; exit 1; }
`); !ok {
		t.Fatalf("already restored: %s", out)
	}
	// Both restoring drills use the library's, not a copy that can drift.
	for _, drill := range []string{"safe.sh", "rollback.sh"} {
		src, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging", "drills", drill))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "\nsignals_are_exits") {
			t.Errorf("%s sets an EXIT trap and does not turn signals into exits", drill)
		}
	}
	// And a restore that fails in the drill's own flow is a FAIL row, not a
	// silent exit under set -e.
	safe, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging", "drills", "safe.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(safe), "\nput_back || die ") || !strings.Contains(string(safe), "\ntrap restore_on_exit EXIT") {
		t.Error("safe.sh does not die loudly on a failed restore, or does not restore through the library on exit")
	}
}

// A SIGNAL IS AN EXIT. Under dash — /bin/sh on the hosts the drills run
// on — an uncaught INT or TERM ends the shell WITHOUT running the EXIT
// trap: a Ctrl-C between a drill's damage and its restore left the damage,
// no restore, and the unpacked identity on disk.
func TestAnInterruptedDrillStillRunsItsExitTrap(t *testing.T) {
	for _, sig := range []string{"INT", "TERM", "HUP"} {
		marker := filepath.Join(t.TempDir(), "trap-ran")
		script := filepath.Join(t.TempDir(), "drill.sh")
		body := ". packaging/drills/lib.sh\ntrap ': > \"" + marker + "\"' EXIT\nsignals_are_exits\nkill -" + sig + " $$\nsleep 5\n"
		if err := os.WriteFile(script, []byte(strings.ReplaceAll(body, "\\n", "\n")), 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", script)
		cmd.Dir = repoRoot(t)
		cmd.Env = append(os.Environ(), "AII_ROOT="+t.TempDir(), "AII_SLOT=s", "AII=/bin/true")
		out, _ := cmd.CombinedOutput()
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("SIG%s: the EXIT trap never ran (%s)", sig, strings.TrimSpace(string(out)))
		}
	}
}
