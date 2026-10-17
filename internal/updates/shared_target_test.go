package updates

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// sharedRelease is one signed release of `binary`, served by this process
// and written to files a child process serves for itself.
type sharedRelease struct {
	version string
	binary  string
	root    *sigenvelope.PublicKeyEnvelope
	trust   string // a trust directory holding the root's revocation snapshot
	files   string // archive, sig, root.json — what a child reads
	release *githubRelease
	hits    *atomic.Int64
}

func newSharedRelease(t *testing.T, binary string) *sharedRelease {
	t.Helper()
	const ver = "9.9.9"
	archive := releaseArchive(t, []byte(binary))
	signer := newTestReleaseSigner(t)
	trust := t.TempDir()
	signer.provisionTrustDir(t, trust)
	sig := signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive), Version: ver,
		Platform: packagefmt.HostPlatform(), Arch: hostArchForTest(), SourceRev: "sharedtarget0",
	})
	files := t.TempDir()
	root, err := json.Marshal(signer.env)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"archive": archive, "sig": sig, "root.json": root} {
		if err := os.WriteFile(filepath.Join(files, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hits := new(atomic.Int64)
	srv := httptest.NewServer(releaseHandler(archive, sig, hits))
	t.Cleanup(srv.Close)
	return &sharedRelease{version: ver, binary: binary, root: signer.env, trust: trust, files: files,
		release: releaseAt(srv.URL, ver), hits: hits}
}

func releaseHandler(archive, sig []byte, hits *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/archive":
			_, _ = w.Write(archive)
		case "/sig":
			_, _ = w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	})
}

func releaseAt(url, ver string) *githubRelease {
	return &githubRelease{
		TagName: "v" + ver,
		Assets: []githubAsset{
			{Name: assetName(ver), BrowserDownloadURL: url + "/archive"},
			{Name: assetName(ver) + ".platform.sig", BrowserDownloadURL: url + "/sig"},
		},
	}
}

// identity is a fresh identity data directory: booted healthy on the old
// binary, with the release root's revocation snapshot provisioned as an
// operator running their own root must.
func (r *sharedRelease) identity(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, markerFile, "ok")
	if err := os.CopyFS(dir, os.DirFS(r.trust)); err != nil {
		t.Fatal(err)
	}
	return dir
}

// program is a fresh installed program holding the old binary.
func program(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	writeFile(t, bin, "aii", "old binary")
	return filepath.Join(bin, "aii")
}

// checker is the identity at dataDir's checker, with this release available.
func (r *sharedRelease) checker(dataDir string) *Checker {
	c := newTestChecker(r.root, dataDir, r.release)
	c.state.setAvailable(r.version)
	return c
}

// evidence is everything a replacement and a recovery touch, read whole,
// so "untouched" is one comparison.
type evidence struct {
	exe, record, backup     string
	marker, retiredMarker   bool
	legacyBackup, legacyRec bool
}

func readEvidence(t *testing.T, exe, dataDir string) evidence {
	t.Helper()
	tgt := targetForTest(t, exe)
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			return "<absent>"
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return evidence{exe: read(exe), record: read(tgt.record), backup: read(tgt.backup),
		marker: fileExists(dataDir, markerFile), retiredMarker: fileExists(dataDir, retiredMarkerFile),
		legacyBackup: fileExists(dataDir, previousFile), legacyRec: fileExists(dataDir, pendingFile)}
}

// bootHealthy is what a healthy boot of the identity at dataDir does to
// update state: the rollback check at its start, the marker at its end.
func bootHealthy(dataDir, exe string) {
	checkRollbackAt(dataDir, exe)
	writeBootMarkerAt(dataDir, exe)
}

func TestASecondIdentityFindsTheUpdateAlreadyInstalled(t *testing.T) {
	rel := newSharedRelease(t, "new binary")
	exe, dirA, dirB := program(t), rel.identity(t), rel.identity(t)
	tgt := targetForTest(t, exe)

	if err := rel.checker(dirA).applyTo(context.Background(), exe); err != nil {
		t.Fatalf("identity A's update: %v", err)
	}
	assertApplyFile(t, exe, "new binary")
	assertRecord(t, tgt, ownerForTest(t, dirA), 0, "old binary", "new binary")
	installed := readEvidence(t, exe, dirA)

	b := rel.checker(dirB)
	before := rel.hits.Load()
	if err := b.applyTo(context.Background(), exe); err != nil {
		t.Fatalf("identity B's apply of an update already on disk: %v", err)
	}
	if rel.hits.Load() == before {
		t.Fatal("B judged the image on disk without the verified release — the comparison is with a VERIFIED image")
	}
	snap := b.State().Snapshot("1.0.0")
	if !snap.NeedsRestart || snap.InstalledVersion != "9.9.9" || snap.LastError != "" {
		t.Errorf("B's state must say installed, restart to apply: %+v", snap)
	}
	if got := readEvidence(t, exe, dirA); got != installed {
		t.Errorf("B's apply changed A's update:\n got %+v\nwant %+v", got, installed)
	}
	if b := readEvidence(t, exe, dirB); !b.marker || b.legacyBackup || b.legacyRec {
		t.Errorf("B swapped nothing, so its marker stands and it keeps no recovery state: %+v", b)
	}
}

// ONLY THE OWNER SETTLES AN UPDATE. A applies; B — another identity on the
// same program — then meets A's pending update every way it can: a
// different release (refused, naming whose update stands), B's own boots,
// healthy or not, and the questions B's boot asks of it. None of them may
// replace or retire A's backup and record. A's first boot counts; A's
// healthy boot retires; and only then does B's update go through.
func TestAnotherIdentitysPendingUpdateIsNeitherReplacedNorRetired(t *testing.T) {
	rel := newSharedRelease(t, "new binary")
	newer := newSharedRelease(t, "newer binary")
	// B verifies the newer release, signed under its own root.
	exe, dirA, dirB := program(t), rel.identity(t), newer.identity(t)
	ownerA := ownerForTest(t, dirA)
	tgt := targetForTest(t, exe)

	if err := rel.checker(dirA).applyTo(context.Background(), exe); err != nil {
		t.Fatalf("A's update: %v", err)
	}
	pending := readEvidence(t, exe, dirA)
	untouched := func(when string) {
		t.Helper()
		if got := readEvidence(t, exe, dirA); got != pending {
			t.Fatalf("%s CHANGED A'S UPDATE:\n got %+v\nwant %+v", when, got, pending)
		}
	}

	err := newer.checker(dirB).applyTo(context.Background(), exe)
	if !errors.Is(err, ErrUpdatePending) || !strings.Contains(err.Error(), ownerA) {
		t.Fatalf("B's different release must be refused, naming A's data directory: %v", err)
	}
	untouched("B's apply of another release")

	bootHealthy(dirB, exe)
	untouched("B's healthy boot")
	if rollbackArmedAt(dirB, exe) {
		t.Error("B's next start was reported to roll back A's update")
	}
	if err := requireSettledAt(dirB, exe); err != nil {
		t.Errorf("B's data waits on A's update: %v", err)
	}
	// B's boot that never finishes: its rollback check runs twice with no
	// marker between — the shape of a failed boot. Still not B's.
	if err := os.Remove(filepath.Join(dirB, markerFile)); err != nil {
		t.Fatal(err)
	}
	checkRollbackAt(dirB, exe)
	checkRollbackAt(dirB, exe)
	untouched("B's failed boots")

	// A's first boot counts the attempt; its rollback is then armed — for
	// A alone — and A's data paths wait on it.
	checkRollbackAt(dirA, exe)
	assertRecord(t, tgt, ownerA, 1, "old binary", "new binary")
	if !rollbackArmedAt(dirA, exe) {
		t.Error("A's counted boot with no marker is not armed")
	}
	if err := requireSettledAt(dirA, exe); !errors.Is(err, ErrBootRecoveryPending) {
		t.Errorf("A's data paths must wait for A's boot to complete: %v", err)
	}
	pending = readEvidence(t, exe, dirA)
	bootHealthy(dirB, exe)
	untouched("B's healthy boot during A's first boot")

	// A's healthy boot is the one that retires it.
	writeBootMarkerAt(dirA, exe)
	if exists(tgt.backup) || exists(tgt.record) {
		t.Fatal("A's healthy boot did not retire A's update")
	}
	if err := newer.checker(dirB).applyTo(context.Background(), exe); err != nil {
		t.Fatalf("once A's update is retired, B's update must go through: %v", err)
	}
	assertRecord(t, tgt, ownerForTest(t, dirB), 0, "new binary", "newer binary")
}

// A RECORD NO ONE CAN BE SHOWN TO OWN — torn, foreign, or naming no
// identity — is kept and explained, never removed: every apply refuses
// over it, and no identity's boot retires or rolls it back.
func TestAnUnownedRecordBesideTheProgramIsKeptAndRefuses(t *testing.T) {
	rel := newSharedRelease(t, "new binary")
	for _, body := range []string{"", "{ torn", "[]", `{"attempts":1,"backup_sha256":"x","new_sha256":"y"}`} {
		t.Run(fmt.Sprintf("%q", body), func(t *testing.T) {
			exe, dirA := program(t), rel.identity(t)
			tgt := targetForTest(t, exe)
			if err := os.WriteFile(tgt.record, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tgt.backup, []byte("a backup someone kept"), 0o755); err != nil {
				t.Fatal(err)
			}
			before := readEvidence(t, exe, dirA)
			if err := rel.checker(dirA).applyTo(context.Background(), exe); !errors.Is(err, ErrUpdatePending) || !strings.Contains(err.Error(), tgt.record) {
				t.Fatalf("an apply over an unowned record must refuse and name it: %v", err)
			}
			if err := os.Remove(filepath.Join(dirA, markerFile)); err != nil {
				t.Fatal(err)
			}
			checkRollbackAt(dirA, exe)
			checkRollbackAt(dirA, exe)
			writeBootMarkerAt(dirA, exe)
			before.marker = true // the healthy boot wrote its own marker, and nothing else
			if got := readEvidence(t, exe, dirA); got != before {
				t.Fatalf("an unowned record or its backup was touched:\n got %+v\nwant %+v", got, before)
			}
		})
	}
}

// ONE PROGRAM, ONE TARGET, HOWEVER IT WAS STARTED. An identity started
// through a symlink and one started through the file it names take the
// same lock and read the same record; and the update replaces the FILE —
// replacing the symlink's own name left the file it named untouched and
// the link a loose copy.
func TestAnUpdateThroughASymlinkReplacesTheProgramItNames(t *testing.T) {
	rel := newSharedRelease(t, "new binary")
	exe, dirA, dirB := program(t), rel.identity(t), rel.identity(t)
	link := filepath.Join(t.TempDir(), "aii")
	if err := os.Symlink(exe, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if targetForTest(t, link) != targetForTest(t, exe) {
		t.Fatalf("one program, two targets: %+v vs %+v", targetForTest(t, link), targetForTest(t, exe))
	}
	if err := rel.checker(dirA).applyTo(context.Background(), link); err != nil {
		t.Fatalf("apply through the symlink: %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced instead of the program it names: %v %v", fi, err)
	}
	assertApplyFile(t, exe, "new binary")
	assertRecord(t, targetForTest(t, exe), ownerForTest(t, dirA), 0, "old binary", "new binary")
	if err := rel.checker(dirB).applyTo(context.Background(), exe); err != nil {
		t.Fatalf("B through the file itself finds A's update installed: %v", err)
	}
}

// THE INTERLEAVING, ACROSS PROCESSES. Identity A applies in a child
// process and stops at each boundary of its replacement — the lock taken,
// the record written, the backup written, its boot marker retired, the
// image published. There, identity B — this process — applies the same
// verified release and boots healthy, and A starts again here and runs
// its own rollback check: none of them may replace or retire anything. Then A either goes on, and A's own failed boot rolls back to
// the ORIGINAL image (the evidence survived), or A is killed there, and
// the state it leaves is one A's next boot settles while B still cannot
// take it over.
func TestTwoProcessesInterleavedOnOneProgramKeepTheOriginalRecovery(t *testing.T) {
	if sharedChild() {
		return
	}
	rel := newSharedRelease(t, "new binary")
	type state struct {
		exe                    string
		record, backup, marker bool
	}
	steps := []struct {
		name string
		left state // what a kill at this step leaves
	}{
		{"locked", state{"old binary", false, false, true}},
		{"recorded", state{"old binary", true, false, true}},
		{"backed-up", state{"old binary", true, true, true}},
		{"marker-retired", state{"old binary", true, true, false}},
		{"published", state{"new binary", true, true, false}},
	}
	for _, step := range steps {
		for _, kill := range []bool{false, true} {
			name := step.name + "/continue"
			if kill {
				name = step.name + "/killed"
			}
			t.Run(name, func(t *testing.T) {
				exe, dirA, dirB := program(t), rel.identity(t), rel.identity(t)
				tgt := targetForTest(t, exe)
				a := startSharedChild(t, "TestTwoProcessesInterleavedOnOneProgramKeepTheOriginalRecovery",
					sharedChildPlan{Exe: exe, DataDir: dirA, Files: rel.files, Version: rel.version, PauseAt: step.name})
				a.expect(t, "AT "+step.name)

				at := readEvidence(t, exe, dirA)
				err := rel.checker(dirB).applyTo(context.Background(), exe)
				if !errors.Is(err, ErrAlreadyApplying) {
					t.Errorf("B's apply while A's replacement holds the program must refuse as already applying, got %v", err)
				}
				if got := readEvidence(t, exe, dirA); got != at {
					t.Fatalf("B'S APPLY INTERLEAVED WITH A'S at %s:\n got %+v\nwant %+v", step.name, got, at)
				}
				bootHealthy(dirB, exe)
				if got := readEvidence(t, exe, dirA); got != at {
					t.Fatalf("B'S HEALTHY BOOT TOUCHED A'S UPDATE at %s:\n got %+v\nwant %+v", step.name, got, at)
				}
				if b := readEvidence(t, exe, dirB); !b.marker || b.retiredMarker || b.legacyBackup || b.legacyRec {
					t.Errorf("B swapped nothing, so its marker stands and it keeps no recovery state: %+v", b)
				}

				if rolled := checkRollbackAt(dirA, exe); rolled != "" {
					t.Fatalf("A's second start at %s rolled back: %q", step.name, rolled)
				}
				if got := readEvidence(t, exe, dirA); got != at {
					t.Fatalf("A'S SECOND START TOUCHED A'S OWN REPLACEMENT at %s:\n got %+v\nwant %+v", step.name, got, at)
				}

				if !kill {
					a.resume(t)
					a.expect(t, "DONE ok")
					a.wait(t)
					assertApplyFile(t, exe, "new binary")
					assertApplyFile(t, tgt.backup, "old binary")
					assertRecord(t, tgt, ownerForTest(t, dirA), 0, "old binary", "new binary")
					done := readEvidence(t, exe, dirA)
					b := rel.checker(dirB)
					if err := b.applyTo(context.Background(), exe); err != nil || !b.State().Snapshot("").NeedsRestart {
						t.Fatalf("B after A's update must find it already installed: %v", err)
					}
					bootHealthy(dirB, exe)
					checkRollbackAt(dirA, exe) // A's first boot of the update begins…
					bootHealthy(dirB, exe)     // …B boots meanwhile…
					if got := readEvidence(t, exe, dirA); got.backup != done.backup || got.exe != done.exe {
						t.Fatalf("A's recovery evidence changed before A's own decision: %+v", got)
					}
					// …and A's boot never finishes: A's next start rolls back
					// to the ORIGINAL image, the proof the evidence survived.
					if rolled := checkRollbackAt(dirA, exe); rolled == "" {
						t.Fatal("A's failed update did not roll back")
					}
					assertApplyFile(t, exe, "old binary")
					if exists(tgt.backup) || exists(tgt.record) {
						t.Error("A's rollback must retire its backup and record")
					}
					return
				}

				a.kill(t)
				left := readEvidence(t, exe, dirA)
				if got := (state{left.exe, exists(tgt.record), exists(tgt.backup), left.marker}); got != step.left {
					t.Fatalf("A killed at %s left %+v, want %+v", step.name, got, step.left)
				}
				if step.left.record {
					assertRecord(t, tgt, ownerForTest(t, dirA), 0, "old binary", "new binary")
				}
				// A's death released the lock, and B still cannot take over.
				err = rel.checker(dirB).applyTo(context.Background(), exe)
				switch {
				case !step.left.record:
					// A wrote nothing: there is no update of A's to protect.
					if err != nil {
						t.Fatalf("B after A died holding nothing: %v", err)
					}
					assertRecord(t, tgt, ownerForTest(t, dirB), 0, "old binary", "new binary")
					return
				case step.left.exe == "new binary":
					if err != nil {
						t.Fatalf("B after A died having published must find it installed: %v", err)
					}
				default:
					if !errors.Is(err, ErrUpdatePending) {
						t.Fatalf("B after A died mid-replacement must be refused by A's record, got %v", err)
					}
				}
				bootHealthy(dirB, exe)
				if got := readEvidence(t, exe, dirA); got != left {
					t.Fatalf("B TOOK OVER A'S INTERRUPTED UPDATE:\n got %+v\nwant %+v", got, left)
				}
				// A's next boot settles what A left.
				bootHealthy(dirA, exe)
				assertApplyFile(t, exe, step.left.exe)
				if exists(tgt.backup) || exists(tgt.record) {
					t.Fatalf("A's healthy boot did not settle the update it left at %s", step.name)
				}
				if err := rel.checker(dirB).applyTo(context.Background(), exe); err != nil {
					t.Fatalf("once A settled, B's apply must not be refused: %v", err)
				}
			})
		}
	}
}

// --- the child process ---

const sharedChildEnv = "AII_OS_UPDATES_SHARED_CHILD"

// sharedChildPlan is what a child process is told: as the identity at
// DataDir, apply the release in Files to Exe, stopping at PauseAt until
// told to go on; or, with Hold, only take Exe's target lock and keep it.
type sharedChildPlan struct {
	Exe, DataDir, Files, Version, PauseAt string
	Hold                                  bool
}

// sharedChild runs the plan when this process is a child, and exits; it
// returns false in the parent.
func sharedChild() bool {
	path := os.Getenv(sharedChildEnv)
	if path == "" {
		return false
	}
	say := func(format string, a ...any) { fmt.Fprintf(os.Stdout, format+"\n", a...) }
	fail := func(err error) { say("ERROR %v", err); os.Exit(2) }
	var plan sharedChildPlan
	raw, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		fail(err)
	}
	in := bufio.NewReader(os.Stdin)
	if plan.Hold {
		tgt, err := targetOf(plan.Exe)
		if err != nil {
			fail(err)
		}
		release, err := lockTarget(tgt)
		if err != nil {
			fail(err)
		}
		say("HELD")
		_, _ = in.ReadString('\n')
		release()
		os.Exit(0)
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(plan.Files, name))
		if err != nil {
			fail(err)
		}
		return b
	}
	var root sigenvelope.PublicKeyEnvelope
	if err := json.Unmarshal(read("root.json"), &root); err != nil {
		fail(err)
	}
	srv := httptest.NewServer(releaseHandler(read("archive"), read("sig"), new(atomic.Int64)))
	c := newTestChecker(&root, plan.DataDir, releaseAt(srv.URL, plan.Version))
	c.state.setAvailable(plan.Version)
	replaceStep = func(step string) {
		if step == plan.PauseAt {
			say("AT %s", step)
			_, _ = in.ReadString('\n')
		}
	}
	err = c.applyTo(context.Background(), plan.Exe)
	srv.Close()
	switch {
	case err == nil:
		say("DONE ok")
	case errors.Is(err, ErrAlreadyApplying):
		say("DONE already-applying")
	case errors.Is(err, ErrUpdatePending):
		say("DONE pending")
	default:
		say("DONE error %v", err)
	}
	os.Exit(0)
	return true
}

type sharedChildProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	stderr *bytes.Buffer
	waited bool
}

func startSharedChild(t *testing.T, testName string, plan sharedChildPlan) *sharedChildProc {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+testName+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), sharedChildEnv+"="+planPath)
	p := &sharedChildProc{cmd: cmd, stderr: new(bytes.Buffer)}
	cmd.Stderr = p.stderr
	if p.stdin, err = cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.out = bufio.NewReader(stdout)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !p.waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		cancel()
	})
	return p
}

// expect reads the child's stdout up to the line that starts with want.
func (p *sharedChildProc) expect(t *testing.T, want string) {
	t.Helper()
	for {
		line, err := p.out.ReadString('\n')
		if strings.HasPrefix(line, want) {
			return
		}
		if strings.HasPrefix(line, "ERROR ") || strings.HasPrefix(line, "DONE ") || err != nil {
			t.Fatalf("child: want %q, got %q (%v); stderr:\n%s", want, line, err, p.stderr.String())
		}
	}
}

func (p *sharedChildProc) resume(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, "go\n"); err != nil {
		t.Fatal(err)
	}
}

func (p *sharedChildProc) wait(t *testing.T) {
	t.Helper()
	p.waited = true
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("child: %v; stderr:\n%s", err, p.stderr.String())
	}
}

// kill ends the child the way a crash or a power cut ends a process.
func (p *sharedChildProc) kill(t *testing.T) {
	t.Helper()
	p.waited = true
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = p.cmd.Wait() // killed on purpose
}
