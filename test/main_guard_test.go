package test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// guardRig is where flock exists: the lock is util-linux's, and the gate
// says once that it is not serialized where there is none.
func guardRig(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the gate lock is flock(1), and the shared checkout it guards is on Linux: nothing to prove on " + runtime.GOOS)
	}
	for _, tool := range []string{"flock", "git", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is not on the path, and the guard cannot be proved without it: %v", tool, err)
		}
	}
}

// cleanEnv is the environment without any gate lock a surrounding gate or
// chain handed down: the rig's lock is its own.
func cleanEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "AII_GATE_") || strings.HasPrefix(kv, "GATE_ONLY=") || strings.HasPrefix(kv, "EVIDENCE_DIR=") || strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func lockFree(lock string) bool {
	return exec.Command("flock", "-n", lock, "true").Run() == nil
}

func waitFor(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited %s for %s", within, what)
}

// TWO GATES STARTED TOGETHER: THE SECOND WAITS, AND SAYS FOR WHOM. The
// stage is the gate's own self-test stage, which holds the lock exactly as
// a full gate does; a one-stage run of anything else takes no lock.
func TestTwoGatesStartedTogetherTheSecondWaitsAndSaysForWhom(t *testing.T) {
	guardRig(t)
	root := repoRoot(t)
	scratch := t.TempDir()
	lock, hold := filepath.Join(scratch, "gate.lock"), filepath.Join(scratch, "hold")
	gate := func(who, only, hold string) (*exec.Cmd, *lockedBuffer) {
		cmd := exec.Command("sh", "test/gate.sh")
		cmd.Dir = root
		cmd.Env = cleanEnv("GATE_ONLY="+only, "AII_GATE_LOCK="+lock, "AII_GATE_WHO="+who, "AII_GATE_SELFTEST_HOLD="+hold)
		out := &lockedBuffer{}
		cmd.Stdout, cmd.Stderr = out, out
		return cmd, out
	}
	// The first gate holds for as long as the file stands.
	write(t, scratch, "hold", "the first gate runs while this stands\n")
	first, firstOut := gate("first", "selftest-lock", hold)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	firstEnded := make(chan struct{})
	go func() { _ = first.Wait(); close(firstEnded) }()
	running := func(ended chan struct{}) bool {
		select {
		case <-ended:
			return false
		default:
			return true
		}
	}
	waitFor(t, "the first gate to hold the lock", 60*time.Second, func() bool {
		holder, _ := os.ReadFile(lock + ".holder")
		return strings.HasPrefix(string(holder), "who=first ") && !lockFree(lock)
	})

	// A one-stage run is not a gate: it takes no lock and waits on none.
	stage, stageOut := gate("stage", "selftest", "")
	if err := stage.Run(); err != nil || !running(firstEnded) || strings.Contains(stageOut.String(), "gate lock") {
		t.Fatalf("A ONE-STAGE RUN WAITED ON THE GATE LOCK, OR TOOK IT (%v; the first gate still running: %v)\n%s", err, running(firstEnded), stageOut)
	}

	second, secondOut := gate("second", "selftest-lock", "")
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	secondEnded := make(chan struct{})
	go func() { _ = second.Wait(); close(secondEnded) }()
	waitFor(t, "the second gate to say it is waiting", 60*time.Second, func() bool {
		return strings.Contains(secondOut.String(), "gate lock: waiting; held by: who=first what=gate of ")
	})
	if !running(secondEnded) || !running(firstEnded) || strings.Contains(secondOut.String(), "gate-selftest-lock") || strings.Contains(secondOut.String(), "GATE_RESULT") {
		t.Fatalf("THE SECOND GATE DID NOT WAIT FOR THE FIRST:\n%s", secondOut)
	}
	if strings.Contains(secondOut.String(), "token=") {
		t.Errorf("the waiting gate printed the holder's token:\n%s", secondOut)
	}

	// The first ends; the second takes the lock and runs.
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	for name, ended := range map[string]chan struct{}{"first": firstEnded, "second": secondEnded} {
		select {
		case <-ended:
		case <-time.After(120 * time.Second):
			t.Fatalf("the %s gate did not end:\nfirst:\n%s\nsecond:\n%s", name, firstOut, secondOut)
		}
	}
	for name, out := range map[string]*lockedBuffer{"first": firstOut, "second": secondOut} {
		if !strings.Contains(out.String(), "GATE_RESULT_PASS") || !strings.Contains(out.String(), "gate lock: held (") {
			t.Errorf("the %s gate did not take the lock and pass:\n%s", name, out)
		}
	}
	// Nothing has to remember to release: the holder goes with its gate.
	waitFor(t, "the lock to be free once both gates are gone", 10*time.Second, func() bool { return lockFree(lock) })
}

// lockedBuffer is output read while its process still writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// guardedRepo is a repository with the guard in force as it is installed:
// main at one commit, and the hook a copy of the shipped file.
type guardedRepo struct {
	t                   *testing.T
	dir, lock, evidence string
	token               string // a holder's token, when the mover is to hold the lock
}

func newGuardedRepo(t *testing.T) *guardedRepo {
	t.Helper()
	guardRig(t)
	base := t.TempDir()
	r := &guardedRepo{t: t, dir: filepath.Join(base, "repo"), lock: filepath.Join(base, "gate.lock"), evidence: filepath.Join(base, "records")}
	for _, d := range []string{r.dir, r.evidence} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.git("init", "-q", "-b", "main")
	r.commit("the first commit")
	install := exec.Command("sh", filepath.Join(repoRoot(t), "test", "support", "landing", "install-main-guard.sh"), r.dir)
	if out, err := install.CombinedOutput(); err != nil || !strings.Contains(string(out), "main guard in force") {
		t.Fatalf("installing the guard: %v\n%s", err, out)
	}
	return r
}

func (r *guardedRepo) run(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "user.name=rig", "-c", "user.email=rig@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = r.dir
	env := cleanEnv("AII_GATE_LOCK="+r.lock, "AII_GATE_EVIDENCE="+filepath.Join(r.evidence, "*-evidence.*"), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if r.token != "" {
		env = append(env, "AII_GATE_LOCK_TOKEN="+r.token)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *guardedRepo) git(args ...string) string {
	r.t.Helper()
	out, err := r.run(args...)
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(out)
}

var guardCommits int

// commit makes one commit on the current branch and returns it.
func (r *guardedRepo) commit(subject string) string {
	r.t.Helper()
	guardCommits++
	name := fmt.Sprintf("file-%d.txt", guardCommits) // a file of its own: a rebase of one commit over another never conflicts
	write(r.t, r.dir, name, subject+"\n")
	r.git("add", name)
	r.git("commit", "-q", "-m", subject)
	return r.git("rev-parse", "HEAD")
}

// candidate is a commit one ahead of main on a branch of its own, with
// main checked out again: what a landing fast-forwards to.
func (r *guardedRepo) candidate(name string) string {
	r.t.Helper()
	r.git("checkout", "-q", "-b", name, "main")
	c := r.commit("the work of " + name)
	r.git("checkout", "-q", "main")
	return c
}

// record writes a gate's record for commit as test/gate.sh writes one, and
// seals it with the shipped sealer. alter changes it first.
func (r *guardedRepo) record(name, commit string, alter func(header, verdict *string)) string {
	r.t.Helper()
	dir := filepath.Join(r.evidence, name+"-evidence.rig")
	header := fmt.Sprintf(`gate commit=%s tree=%s dirty=no go="go version rig" toolchain=/rig/go asked=/rig/go host=rig scope=full started=2026-10-12T00:00:00Z`,
		commit, r.git("rev-parse", commit+"^{tree}"))
	verdict := "verdict=PASS ended=2026-10-12T00:10:00Z"
	if alter != nil {
		alter(&header, &verdict)
	}
	write(r.t, dir, "gate.log", header+"\nstage=scope start=x end=y exit=0 log=scope.log\n"+verdict+"\n")
	write(r.t, dir, "scope.log", "ok\n")
	seal := exec.Command("sh", filepath.Join(repoRoot(r.t), "test", "evidence-seal.sh"), dir)
	if out, err := seal.CombinedOutput(); err != nil {
		r.t.Fatalf("sealing the rig's record: %v\n%s", err, out)
	}
	return dir
}

// refused is a move of main that the guard refuses: it returns what was
// said, and puts the checkout back as the refusal says how. A refused
// fast-forward or reset has already written its files (git runs no hook
// before that), which is why the refusal carries the remedy.
func (r *guardedRepo) refused(args ...string) string {
	r.t.Helper()
	out, err := r.run(args...)
	if err == nil || !strings.Contains(out, "main-guard: REFUSED: ") {
		r.t.Fatalf("MAIN MOVED, OR THE GUARD DID NOT SAY IT REFUSED (git %s): %v\n%s", strings.Join(args, " "), err, out)
	}
	if !strings.Contains(out, "'git reset --hard HEAD' in this checkout puts them back") {
		r.t.Fatalf("the refusal does not say how the checkout is put back:\n%s", out)
	}
	r.git("reset", "-q", "--hard", "HEAD")
	if left := r.git("status", "--porcelain"); left != "" {
		r.t.Fatalf("the remedy the refusal names left the checkout dirty:\n%s", left)
	}
	return out
}

func (r *guardedRepo) mainIs(want, why string) {
	r.t.Helper()
	if got := r.git("rev-parse", "main"); got != want {
		r.t.Fatalf("%s: main is %s, want %s", why, got, want)
	}
}

// A FAST-FORWARD OF AN UNGATED COMMIT IS REFUSED BY NAME, by whatever
// command moves main; a gated one lands; and nothing else in the
// repository is held.
func TestMainTakesOnlyACommitWithASealedFullScopePassRecord(t *testing.T) {
	r := newGuardedRepo(t)
	first := r.git("rev-parse", "main")
	c := r.candidate("ungated")

	if out, err := r.run("branch", "-f", "elsewhere", "ungated"); err != nil {
		t.Fatalf("A BRANCH THAT IS NOT MAIN WAS HELD: %v\n%s", err, out)
	}
	for _, move := range [][]string{
		{"merge", "-q", "--ff-only", "ungated"},
		{"update-ref", "refs/heads/main", c},
		{"reset", "-q", "--hard", c},
	} {
		if out := r.refused(move...); !strings.Contains(out, "main-guard: REFUSED: "+c+" has no sealed, full-scope PASS record") {
			t.Fatalf("AN UNGATED COMMIT WAS REFUSED WITHOUT ITS NAME (git %s):\n%s", strings.Join(move, " "), out)
		}
		r.mainIs(first, "after the refused git "+move[0])
	}
	// What a refused fast-forward leaves, before the remedy: the files of
	// the commit that did not land, staged. Said, not hidden.
	if _, err := r.run("merge", "-q", "--ff-only", "ungated"); err == nil {
		t.Fatal("rig: the fast-forward is refused")
	}
	if left := r.git("status", "--porcelain"); left == "" {
		t.Log("this git left a refused fast-forward's checkout clean; the refusal's remedy is then a no-op")
	}
	r.git("reset", "-q", "--hard", "HEAD")
	// A commit made on main itself is a move of main like any other.
	write(t, r.dir, "direct.txt", "typed on main\n")
	r.git("add", "direct.txt")
	r.refused("commit", "-q", "-m", "a commit made on main")
	r.mainIs(first, "after the refused commit on main")
	if out := r.refused("update-ref", "-d", "refs/heads/main"); !strings.Contains(out, "main is not deleted") {
		t.Fatalf("the refusal of a deletion does not say so:\n%s", out)
	}

	// Every record that is not a sealed, full-scope PASS of this commit and
	// tree is no record.
	other := r.candidate("another")
	short := map[string]func(header, verdict *string){
		"a one-stage receipt":   func(h, _ *string) { *h = strings.Replace(*h, "scope=full", "scope=sdkcompat", 1) },
		"a dirty tree":          func(h, _ *string) { *h = strings.Replace(*h, "dirty=no", "dirty=yes", 1) },
		"a failed gate":         func(_, v *string) { *v = "verdict=FAIL ended=2026-10-12T00:10:00Z" },
		"a pass later unsealed": func(_, v *string) { *v += "\nverdict=FAIL ended=2026-10-12T00:10:01Z (unsealed)" },
		"another commit's tree": func(h, _ *string) {
			*h = strings.Replace(*h, r.git("rev-parse", c+"^{tree}"), r.git("rev-parse", other+"^{tree}"), 1)
		},
		"another commit": func(h, _ *string) { *h = strings.Replace(*h, c, other, 1) },
	}
	for name, alter := range short {
		dir := r.record("short", c, alter)
		r.refused("merge", "-q", "--ff-only", "ungated")
		r.mainIs(first, "after "+name)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	// A record changed after its seal.
	dir := r.record("broken", c, nil)
	write(t, dir, "scope.log", "FAIL, edited after the seal\n")
	r.refused("merge", "-q", "--ff-only", "ungated")
	r.mainIs(first, "after the broken seal")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	// The gated commit lands, and says under which record.
	good := r.record("good", c, nil)
	out, err := r.run("merge", "-q", "--ff-only", "ungated")
	if err != nil || !strings.Contains(out, "main-guard: main moves to "+c) || !strings.Contains(out, good) {
		t.Fatalf("A GATED COMMIT DID NOT LAND, OR DID NOT SAY UNDER WHICH RECORD: %v\n%s", err, out)
	}
	r.mainIs(c, "after the gated fast-forward")
	if !lockFree(r.lock) {
		t.Errorf("the hook kept the gate lock after the move")
	}
}

// A GATED COMMIT FROM A SESSION THAT DOES NOT HOLD THE LOCK IS REFUSED
// WHILE A CUT HOLDS IT; whoever holds it moves main; and the same commit
// lands after the cut with no note posted.
func TestMainDoesNotMoveUnderSomeoneElsesHoldOfTheGateLock(t *testing.T) {
	r := newGuardedRepo(t)
	first := r.git("rev-parse", "main")
	c := r.candidate("gated")
	r.record("gated", c, nil)

	// The cut: a shell that takes the lock as a chain does, says its token
	// (what it hands to what it starts), and holds until its input closes.
	cut := exec.Command("sh", "-c", `. "$1/test/gate-lock.sh"; gate_lock_take cut "the release cut" || exit 1; echo "$AII_GATE_LOCK_TOKEN"; read -r _`, "cut", repoRoot(t))
	cut.Env = cleanEnv("AII_GATE_LOCK=" + r.lock)
	stdin, err := cut.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cut.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cut.Start(); err != nil {
		t.Fatal(err)
	}
	ended := false
	end := func() {
		if !ended {
			ended = true
			_ = stdin.Close()
			_ = cut.Wait()
		}
	}
	defer end()
	line := make([]byte, 256)
	n, err := stdout.Read(line)
	token := strings.TrimSpace(string(line[:n]))
	if err != nil || token == "" || lockFree(r.lock) {
		t.Fatalf("rig: the cut holds the lock and says its token: %q, %v, lock free %v", token, err, lockFree(r.lock))
	}

	out := r.refused("merge", "-q", "--ff-only", "gated")
	if !strings.Contains(out, "main-guard: REFUSED: the gate lock is held by: who=cut what=the release cut") {
		t.Fatalf("THE REFUSAL UNDER A CUT'S HOLD DID NOT SAY WHO HOLDS THE LOCK:\n%s", out)
	}
	if strings.Contains(out, token) {
		t.Errorf("the refusal printed the holder's token:\n%s", out)
	}
	r.mainIs(first, "under the cut's hold")

	// Whoever holds the lock moves main: the cut's own commit, gated.
	mine := r.candidate("the-cuts-own")
	r.record("cuts-own", mine, nil)
	r.token = token
	if out, err := r.run("merge", "-q", "--ff-only", "the-cuts-own"); err != nil {
		t.Fatalf("THE HOLDER OF THE LOCK COULD NOT MOVE MAIN: %v\n%s", err, out)
	}
	r.mainIs(mine, "by the holder")
	// A token is not a record: the holder's ungated commit is refused too.
	r.git("checkout", "-q", "-b", "the-cuts-ungated", "main")
	r.commit("ungated, by the holder")
	r.git("checkout", "-q", "main")
	if out := r.refused("merge", "-q", "--ff-only", "the-cuts-ungated"); !strings.Contains(out, "has no sealed, full-scope PASS record") {
		t.Fatalf("the holder's ungated commit was refused for another reason:\n%s", out)
	}
	r.token = ""

	// The cut ends. Nobody posts anything; the first session's commit, now
	// rebased onto main as a landing rebases it, is gated and lands.
	end()
	waitFor(t, "the lock to be free once the cut is gone", 5*time.Second, func() bool { return lockFree(r.lock) })
	r.git("checkout", "-q", "gated")
	r.git("rebase", "-q", "main")
	rebased := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "main")
	r.record("gated-again", rebased, nil)
	if out, err := r.run("merge", "-q", "--ff-only", "gated"); err != nil {
		t.Fatalf("THE SAME WORK DID NOT LAND AFTER THE CUT: %v\n%s", err, out)
	}
	r.mainIs(rebased, "after the cut")
}

// MAIN ONLY MOVES FORWARD. A record says a commit passed its gate, not that
// it holds what landed after it was gated. A gated commit drawn on an
// earlier main moved main back over everything since, with a valid record
// and no word; the landing chain asks for ancestry of its own branch, and
// a move by hand runs no chain.
func TestMainMovesOnlyToACommitThatContainsIt(t *testing.T) {
	r := newGuardedRepo(t)
	first := r.git("rev-parse", "main")

	landed := r.candidate("landed")
	r.record("landed", landed, nil)
	if out, err := r.run("merge", "-q", "--ff-only", "landed"); err != nil {
		t.Fatalf("rig: a gated commit ahead of main did not land: %v\n%s", err, out)
	}
	r.mainIs(landed, "after the first landing")

	// Work drawn on the first commit, gated there, and never rebased.
	r.git("checkout", "-q", "-b", "stale", first)
	stale := r.commit("work drawn on an earlier main")
	r.git("checkout", "-q", "main")
	r.record("stale", stale, nil)
	for _, move := range [][]string{
		{"update-ref", "refs/heads/main", stale},
		{"reset", "-q", "--hard", stale},
	} {
		out := r.refused(move...)
		if !strings.Contains(out, "main-guard: REFUSED: "+stale+" does not contain main ("+landed+")") {
			t.Fatalf("A GATED COMMIT THAT LEAVES OUT WHAT LANDED WAS REFUSED FOR ANOTHER REASON (git %s):\n%s", strings.Join(move, " "), out)
		}
		r.mainIs(landed, "after the refused git "+move[0])
	}
	// Going back to an earlier commit of main's own history is the same move.
	if out := r.refused("update-ref", "refs/heads/main", first); !strings.Contains(out, "does not contain main") {
		t.Fatalf("main moved back along its own history, or was refused for another reason:\n%s", out)
	}

	// Rebased onto main and gated again, the same work lands.
	r.git("checkout", "-q", "stale")
	r.git("rebase", "-q", "main")
	rebased := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "main")
	r.record("stale-again", rebased, nil)
	if out, err := r.run("merge", "-q", "--ff-only", "stale"); err != nil {
		t.Fatalf("THE SAME WORK, REBASED AND GATED, DID NOT LAND: %v\n%s", err, out)
	}
	r.mainIs(rebased, "after the rebased landing")
}
