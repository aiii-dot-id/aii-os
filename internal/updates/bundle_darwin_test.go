//go:build darwin

package updates

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeBundle builds a minimal .app and signs it with the given identity
// ("-" is ad-hoc). Real tools, real signatures — the checks under test
// are codesign and spctl, so stubbing them would test nothing.
func makeBundle(t *testing.T, dir, name, identity string) string {
	t.Helper()
	return makeBundleSaying(t, dir, name, identity, "")
}

// makeBundleSaying is makeBundle whose two executables print says when run,
// so a test can tell by running the installed bundle which one it is.
func makeBundleSaying(t *testing.T, dir, name, identity, says string) string {
	t.Helper()
	app := filepath.Join(dir, name+".app")
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.c")
	if err := os.WriteFile(src, []byte("#include <stdio.h>\nint main(){fputs(\""+says+"\", stdout);return 0;}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, exe := range []string{name, "aii"} {
		if out, err := exec.Command("cc", "-o", filepath.Join(macos, exe), src).CombinedOutput(); err != nil {
			t.Skipf("no working compiler on this host: %v: %s", err, out)
		}
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>` + name + `</string>
<key>CFBundleIdentifier</key><string>test.aii.` + name + `</string>
<key>CFBundleName</key><string>` + name + `</string>
<key>CFBundleVersion</key><string>1</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	// Inside-out, the same order build-dmg.sh signs in.
	for _, target := range []string{filepath.Join(macos, "aii"), app} {
		if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", identity, target).CombinedOutput(); err != nil {
			t.Skipf("codesign unavailable: %v: %s", err, out)
		}
	}
	return app
}

// The swap is the one irreversible step, so it must be exactly a swap:
// both names present, contents exchanged, nothing missing in between.
func TestTheAtomicSwapExchangesBothPaths(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "A")
	b := filepath.Join(dir, "B")
	for p, body := range map[string]string{a: "alpha", b: "beta"} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "mark"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := swapBundles(a, b); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string {
		bts, err := os.ReadFile(filepath.Join(p, "mark"))
		if err != nil {
			t.Fatalf("%s vanished across the swap: %v", p, err)
		}
		return string(bts)
	}
	if read(a) != "beta" || read(b) != "alpha" {
		t.Fatalf("contents not exchanged: A=%q B=%q", read(a), read(b))
	}
}

// A bundle signed by a DIFFERENT identity than the installed one is
// refused even though its own signature is perfectly valid. "Validly
// signed" admits any certificate on earth; the question that matters is
// whether it is the same publisher.
func TestAStagedBundleFromAnotherIdentityIsRefused(t *testing.T) {
	dir := t.TempDir()
	installed := makeBundle(t, filepath.Join(dir, "inst"), "Installed", "-")
	staged := makeBundle(t, filepath.Join(dir, "stage"), "Staged", "-")

	err := verifyStagedBundle(staged, installed)
	if err == nil {
		t.Fatal("an ad-hoc signed bundle carries no Developer ID and must not be admitted as an update")
	}
	// Ad-hoc signatures have no Team Identifier at all, which is itself
	// disqualifying — a release payload must be Developer ID signed.
	if !strings.Contains(err.Error(), "Team Identifier") && !strings.Contains(err.Error(), "DIFFERENT identity") {
		t.Fatalf("the refusal must name the identity problem, got: %v", err)
	}
}

// A payload must contain exactly one .app: none is nothing to install,
// and more than one leaves the choice ambiguous.
func TestTheBundleArchiveMustCarryExactlyOneApp(t *testing.T) {
	dir := t.TempDir()

	// An archive with no .app at all.
	emptySrc := filepath.Join(dir, "nothing")
	if err := os.MkdirAll(emptySrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emptySrc, "readme.txt"), []byte("not an app"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.zip")
	if out, err := exec.Command("/usr/bin/ditto", "-c", "-k", emptySrc, empty).CombinedOutput(); err != nil {
		t.Fatalf("ditto could not archive a plain directory: %v: %s", err, out)
	}
	dest := t.TempDir()
	if _, err := extractBundleArchive(empty, dest); err == nil {
		t.Fatal("an archive with no .app must be refused")
	} else if !strings.Contains(err.Error(), "no .app") {
		t.Fatalf("the refusal must say what was missing: %v", err)
	}

	// Two bundles in one archive.
	two := filepath.Join(dir, "two")
	os.MkdirAll(two, 0o755)
	makeBundle(t, two, "One", "-")
	makeBundle(t, two, "Two", "-")
	twoZip := filepath.Join(dir, "two.zip")
	if out, err := exec.Command("/usr/bin/ditto", "-c", "-k", two, twoZip).CombinedOutput(); err != nil {
		t.Fatalf("ditto could not archive two bundles: %v: %s", err, out)
	}
	dest2 := t.TempDir()
	if _, err := extractBundleArchive(twoZip, dest2); err == nil {
		t.Fatal("an archive carrying two .app bundles is ambiguous and must be refused")
	} else if !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("the refusal must name the ambiguity: %v", err)
	}
}

// bundleForTest is one identity's bundle update as applyBundleUpdate has
// it once the staged bundle is verified: OLD installed, NEW staged beside
// it, the identity's boot marker armed.
func bundleForTest(t *testing.T) (tgt target, exe, dataDir, staged string) {
	t.Helper()
	// By its real path, as an update names its target (/var is a link here).
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(dir, "AII OS.app")
	writeBundle(t, installed, "OLD")
	exe = filepath.Join(installed, "Contents", "MacOS", "aii")
	dataDir = t.TempDir()
	writeFile(t, dataDir, markerFile, "ok")
	staged = filepath.Join(dir, bundleStagePrefix+"test", "AII OS.app")
	writeBundle(t, staged, "NEW")
	return targetForTest(t, exe), exe, dataDir, staged
}

// installForTest installs that update as the release named version.
func installForTest(t *testing.T, version string) (tgt target, exe, dataDir string, err error) {
	t.Helper()
	tgt, exe, dataDir, staged := bundleForTest(t)
	already, err := installBundle(tgt, staged, dataDir, ownerForTest(t, dataDir), version)
	if already {
		t.Fatal("a different bundle was found already installed")
	}
	return tgt, exe, dataDir, err
}

// cutOff stands for a process that dies at the step a test names.
type cutOff struct{}

// cutAt runs step with the bundle exchange replaced by cut, and returns
// when cut ends it the way a power loss would: nothing after it runs.
func cutAt(t *testing.T, cut func(a, b string) error, step func()) {
	t.Helper()
	exchangeBundles = cut
	defer func() { exchangeBundles = swapBundles }()
	defer func() {
		if r := recover(); r != (cutOff{}) {
			panic(r)
		}
	}()
	step()
	t.Fatal("the step was not cut off")
}

// A bundle install is one exchange of the slot with the installed bundle:
// the new bundle live, the previous one kept beside it, the record naming
// the release and both trees, and the identity's boot marker retired, so
// that the update's first boot is counted as a binary's is.
func TestABundleInstallExchangesTheNewBundleInAndKeepsThePreviousBeside(t *testing.T) {
	tgt, _, dataDir, err := installForTest(t, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if readBundle(t, tgt.path) != "NEW" || readBundle(t, tgt.backup) != "OLD" {
		t.Fatalf("installed %q, kept %q: want NEW installed and OLD kept", readBundle(t, tgt.path), readBundle(t, tgt.backup))
	}
	if exists(filepath.Join(dataDir, markerFile)) {
		t.Fatal("the install left the boot marker armed: a boot of the new bundle that dies would never be counted")
	}
	pend, present, err := readRecord(tgt.record)
	if !present || err != nil {
		t.Fatalf("no record of the install: %v", err)
	}
	newHash, _ := imageHash(tgt.path)
	oldHash, _ := imageHash(tgt.backup)
	if pend.Attempts != 0 || pend.Version != "9.9.9" || pend.NewSHA256 != newHash || pend.BackupSHA256 != oldHash {
		t.Fatalf("the record does not name the release and both bundles: %+v", pend)
	}
}

// An exchange that fails leaves the machine exactly as it was: the old
// bundle installed, nothing in the slot, no record, and the marker back.
func TestABundleInstallWhoseExchangeFailsLeavesTheMachineAsItWas(t *testing.T) {
	exchangeBundles = func(string, string) error { return errors.New("injected: the exchange is refused") }
	t.Cleanup(func() { exchangeBundles = swapBundles })
	tgt, _, dataDir, err := installForTest(t, "9.9.9")
	if err == nil || !strings.Contains(err.Error(), "exchange the new bundle in") {
		t.Fatalf("the refused exchange must fail the install and say so: %v", err)
	}
	if readBundle(t, tgt.path) != "OLD" {
		t.Fatalf("the installed bundle is %q, want the OLD one untouched", readBundle(t, tgt.path))
	}
	if exists(tgt.backup) || exists(tgt.record) {
		t.Fatal("a failed install left its slot or its record standing")
	}
	if !exists(filepath.Join(dataDir, markerFile)) {
		t.Fatal("a failed install did not put the boot marker back: the old bundle's next boot reads as unproven")
	}
}

func TestABundleUpdateThatNeverFinishesABootIsExchangedBack(t *testing.T) {
	tgt, exe, dataDir, err := installForTest(t, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if rolled := checkRollbackAt(dataDir, exe); rolled != "" {
		t.Fatalf("the update's first boot rolled back: %q", rolled)
	}
	if pend, _, _ := readRecord(tgt.record); pend.Attempts != 1 {
		t.Fatalf("the update's first boot was not counted: %+v", pend)
	}
	// That boot never finished: no marker. The next start rolls back.
	if !rollbackArmedAt(dataDir, exe) {
		t.Fatal("RollbackArmed does not say the next start rolls back")
	}
	if rolled := checkRollbackAt(dataDir, exe); rolled != tgt.path {
		t.Fatalf("a bundle that never finished a boot was not exchanged back: rolled %q", rolled)
	}
	if readBundle(t, tgt.path) != "OLD" {
		t.Fatalf("after the rollback %q is installed, want OLD", readBundle(t, tgt.path))
	}
	if exists(tgt.backup) || exists(tgt.record) {
		t.Fatal("the rollback left the failed bundle or the record standing")
	}
	var failed *FailedHereError
	if err := tgt.refusedHere("9.9.9"); !errors.As(err, &failed) {
		t.Fatalf("the release that failed is not refused: %v", err)
	}
}

// THE BUNDLE ROLLBACK END TO END, ON REAL SIGNED BUNDLES. Two .app
// bundles compiled and signed here (ad hoc: a Developer ID signature is
// the operator's to make, never a test's), the second installed over the
// first by the install path that follows admission, its first boot counted
// and never finished, and the next boot: the previous bundle is exchanged
// back, running the installed path runs the previous program (the boot's
// re-exec runs the path it was started from), the release is refused, and
// at each exchange both bundles' seals still verify, since an exchange
// renames and writes nothing.
//
// Admission itself is not passed through: verifyStagedBundle refuses an
// ad-hoc bundle for its missing Team Identifier, which is the point of it
// (TestAStagedBundleFromAnotherIdentityIsRefused); the refusal is asserted
// here too, so a change that let one through would fail this test.
func TestARealSignedBundleThatNeverFinishesABootIsExchangedBackSealed(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installed := makeBundleSaying(t, dir, "Drill", "-", "OLD")
	staged := makeBundleSaying(t, filepath.Join(dir, bundleStagePrefix+"drill"), "Drill", "-", "NEW")
	sealed := func(when string, paths ...string) {
		t.Helper()
		for _, p := range paths {
			if out, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", p).CombinedOutput(); err != nil {
				t.Fatalf("%s: the seal of %s does not verify: %v: %s", when, p, err, out)
			}
		}
	}
	says := func(app string) string {
		t.Helper()
		out, err := exec.Command(filepath.Join(app, "Contents", "MacOS", "aii")).Output()
		if err != nil {
			t.Fatalf("run %s: %v", app, err)
		}
		return string(out)
	}
	sealed("as built", installed, staged)
	if err := verifyStagedBundle(staged, installed); err == nil {
		t.Fatal("an ad-hoc bundle was admitted as an update")
	}

	exe := filepath.Join(installed, "Contents", "MacOS", "aii")
	tgt := targetForTest(t, exe)
	dataDir := t.TempDir()
	writeFile(t, dataDir, markerFile, "ok")
	exchanges := 0
	exchangeBundles = func(a, b string) error {
		if err := swapBundles(a, b); err != nil {
			return err
		}
		exchanges++
		sealed(fmt.Sprintf("exchange %d", exchanges), a, b)
		return nil
	}
	t.Cleanup(func() { exchangeBundles = swapBundles })

	if already, err := installBundle(tgt, staged, dataDir, ownerForTest(t, dataDir), "9.9.9"); err != nil || already {
		t.Fatalf("install: already=%v err=%v", already, err)
	}
	if got := says(tgt.path); got != "NEW" {
		t.Fatalf("after the install the installed bundle runs %q, want NEW", got)
	}
	if rolled := checkRollbackAt(dataDir, exe); rolled != "" {
		t.Fatalf("the update's first boot rolled back: %q", rolled)
	}
	// That boot fails before its marker: nothing writes it.
	if rolled := checkRollbackAt(dataDir, exe); rolled != tgt.path {
		t.Fatalf("the next boot did not exchange the previous bundle back: %q", rolled)
	}
	if exchanges != 2 {
		t.Fatalf("%d exchanges, want the install's and the rollback's", exchanges)
	}
	if got := says(tgt.path); got != "OLD" {
		t.Fatalf("after the rollback the installed bundle runs %q, want OLD", got)
	}
	sealed("after the rollback", tgt.path)
	var failed *FailedHereError
	if err := tgt.refusedHere("9.9.9"); !errors.As(err, &failed) {
		t.Fatalf("the release that failed is not refused: %v", err)
	}
}

// A healthy boot of the update settles it as a binary's: the marker is
// written and the record and the previous bundle are withdrawn.
func TestAHealthyBootSettlesABundleUpdate(t *testing.T) {
	tgt, exe, dataDir, err := installForTest(t, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	bootHealthy(dataDir, exe)
	if !exists(filepath.Join(dataDir, markerFile)) || exists(tgt.record) || exists(tgt.backup) {
		t.Fatal("a healthy boot did not settle the update: marker, record and previous bundle")
	}
	if rolled := checkRollbackAt(dataDir, exe); rolled != "" || readBundle(t, tgt.path) != "NEW" {
		t.Fatalf("a settled update was rolled back: %q, installed %q", rolled, readBundle(t, tgt.path))
	}
	if exists(tgt.failed) {
		t.Fatal("a release that booted is remembered as failed")
	}
}

// A rollback cut off after its exchange leaves the two bundles standing as
// an install cut off before its exchange does. The next boot retires the
// state either way; only the rollback remembers a failed release, which is
// why it writes that fact before it exchanges.
func TestACutOffBundleExchangeBarsOnlyTheReleaseThatFailed(t *testing.T) {
	t.Run("rollback cut off after its exchange", func(t *testing.T) {
		tgt, exe, dataDir, err := installForTest(t, "9.9.9")
		if err != nil {
			t.Fatal(err)
		}
		checkRollbackAt(dataDir, exe) // counted; it never finished
		cutAt(t, func(a, b string) error {
			if err := swapBundles(a, b); err != nil {
				return err
			}
			panic(cutOff{})
		}, func() { checkRollbackAt(dataDir, exe) })
		if rolled := checkRollbackAt(dataDir, exe); rolled != "" {
			t.Fatalf("the bundle already put back was exchanged again: %q", rolled)
		}
		if readBundle(t, tgt.path) != "OLD" || exists(tgt.backup) || exists(tgt.record) {
			t.Fatalf("installed %q; the state must be retired with OLD installed", readBundle(t, tgt.path))
		}
		var failed *FailedHereError
		if err := tgt.refusedHere("9.9.9"); !errors.As(err, &failed) {
			t.Fatalf("the release that failed is forgotten: %v", err)
		}
	})
	t.Run("install cut off before its exchange", func(t *testing.T) {
		tgt, exe, dataDir, staged := bundleForTest(t)
		cutAt(t, func(string, string) error { panic(cutOff{}) }, func() {
			installBundle(tgt, staged, dataDir, ownerForTest(t, dataDir), "9.9.9")
		})
		checkRollbackAt(dataDir, exe) // the old bundle's boot, counted; it never finished
		if rolled := checkRollbackAt(dataDir, exe); rolled != "" {
			t.Fatalf("a bundle that was never exchanged in was rolled back: %q", rolled)
		}
		if readBundle(t, tgt.path) != "OLD" || exists(tgt.backup) || exists(tgt.record) {
			t.Fatalf("installed %q; the state must be retired with OLD installed", readBundle(t, tgt.path))
		}
		if exists(tgt.failed) {
			t.Fatal("a release that never booted is barred")
		}
	})
}

func writeBundle(t *testing.T, path, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Contents", "MacOS", "aii"), []byte(marker), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readBundle(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path, "Contents", "MacOS", "aii"))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// healthy boot settles it, the record and the backup together, as a
// binary's are.
func TestASecondReplacementCannotRemoveTheFirstsBundleBackup(t *testing.T) {
	// By its real path: an update's target is named by where the program
	// really is, and this system's temporary directory is reached through
	// a link (/var is /private/var). Written with the link in it, the
	// expectation below was wrong wherever TMPDIR is the system's own.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(dir, "AII OS.app")
	writeBundle(t, installed, "OLD")
	exe := filepath.Join(installed, "Contents", "MacOS", "aii")
	tgt := targetForTest(t, exe)
	if !tgt.bundle || tgt.backup != filepath.Join(dir, previousBundleName) {
		t.Fatalf("the target of a bundled executable is its bundle: %+v", tgt)
	}
	dirA, dirB := t.TempDir(), t.TempDir()
	for _, d := range []string{dirA, dirB} {
		writeFile(t, d, markerFile, "ok")
	}
	staged := func(marker string) string {
		p := filepath.Join(t.TempDir(), "AII OS.app")
		writeBundle(t, p, marker)
		return p
	}

	if already, err := installBundle(tgt, staged("NEW"), dirA, ownerForTest(t, dirA), "2"); err != nil || already {
		t.Fatalf("A's replacement: already=%v err=%v", already, err)
	}
	if readBundle(t, installed) != "NEW" || readBundle(t, tgt.backup) != "OLD" {
		t.Fatalf("A's replacement must install NEW and keep OLD: installed %q backup %q", readBundle(t, installed), readBundle(t, tgt.backup))
	}
	want, _ := imageHash(installed)
	record, _ := os.ReadFile(tgt.record)
	untouched := func(when string) {
		t.Helper()
		if got := readBundle(t, tgt.backup); got != "OLD" {
			t.Fatalf("%s REPLACED THE FIRST REPLACEMENT'S BACKUP: it holds %q", when, got)
		}
		if have, _ := imageHash(installed); have != want {
			t.Fatalf("%s replaced the installed bundle", when)
		}
		if got, _ := os.ReadFile(tgt.record); !bytes.Equal(got, record) {
			t.Fatalf("%s changed the first replacement's record: %s", when, got)
		}
	}

	already, err := installBundle(tgt, staged("NEW"), dirB, ownerForTest(t, dirB), "2")
	if err != nil || !already {
		t.Fatalf("the same bundle must be found already installed: already=%v err=%v", already, err)
	}
	untouched("a second replacement with the same bundle")

	newer := staged("NEWER")
	if _, err := installBundle(tgt, newer, dirB, ownerForTest(t, dirB), "3"); !errors.Is(err, ErrUpdatePending) {
		t.Fatalf("a different bundle must be refused while A's record stands: %v", err)
	}
	untouched("a second replacement with a different bundle")
	if readBundle(t, newer) != "NEWER" {
		t.Fatal("the refused staged bundle was consumed")
	}

	bootHealthy(dirB, exe)
	untouched("B's healthy boot")

	bootHealthy(dirA, exe)
	if exists(tgt.record) {
		t.Fatal("A's healthy boot did not retire A's record")
	}
	if exists(tgt.backup) {
		t.Fatal("A's healthy boot settled its update and left the bundle backup standing")
	}

	if already, err := installBundle(tgt, newer, dirB, ownerForTest(t, dirB), "3"); err != nil || already {
		t.Fatalf("once A's record is retired, B's replacement goes through: already=%v err=%v", already, err)
	}
	if readBundle(t, installed) != "NEWER" || readBundle(t, tgt.backup) != "NEW" {
		t.Fatalf("B's replacement installs NEWER and keeps NEW: installed %q backup %q", readBundle(t, installed), readBundle(t, tgt.backup))
	}
}

// And across processes: while another process holds the bundle's
// exclusion, a replacement here is refused as already applying and
// touches neither the bundle nor its backup.
func TestABundleBeingReplacedByAnotherProcessIsNotReplacedAgain(t *testing.T) {
	if sharedChild() {
		return
	}
	dir := t.TempDir()
	installed := filepath.Join(dir, "AII OS.app")
	writeBundle(t, installed, "NEW")
	tgt := targetForTest(t, filepath.Join(installed, "Contents", "MacOS", "aii"))
	writeBundle(t, tgt.backup, "OLD")
	holder := startSharedChild(t, "TestABundleBeingReplacedByAnotherProcessIsNotReplacedAgain",
		sharedChildPlan{Exe: filepath.Join(installed, "Contents", "MacOS", "aii"), Hold: true})
	holder.expect(t, "HELD")

	newer := filepath.Join(t.TempDir(), "AII OS.app")
	writeBundle(t, newer, "NEWER")
	if _, err := installBundle(tgt, newer, t.TempDir(), ownerForTest(t, t.TempDir()), "3"); !errors.Is(err, ErrAlreadyApplying) {
		t.Fatalf("a replacement while another process holds the bundle must refuse as already applying: %v", err)
	}
	if readBundle(t, installed) != "NEW" || readBundle(t, tgt.backup) != "OLD" || exists(tgt.record) {
		t.Fatal("a refused replacement touched the bundle, its backup or its record")
	}
	holder.resume(t)
	holder.wait(t)
}
