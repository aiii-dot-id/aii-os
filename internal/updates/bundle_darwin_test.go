//go:build darwin

package updates

import (
	"bytes"
	"errors"
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
	app := filepath.Join(dir, name+".app")
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.c")
	if err := os.WriteFile(src, []byte("int main(){return 0;}"), 0o644); err != nil {
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

func TestAnUpdateThatCannotKeepARollbackCopyIsUndone(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "AII OS.app")
	staged := filepath.Join(dir, "staged.app")
	prev := filepath.Join(dir, previousBundleName)
	writeBundle(t, installed, "OLD")
	writeBundle(t, staged, "NEW")

	orig := renameOutgoing
	renameOutgoing = func(string, string) error { return errors.New("injected: cannot move the outgoing bundle") }
	t.Cleanup(func() { renameOutgoing = orig })

	keep, err := installSwapped(installed, staged, prev)
	if err == nil {
		t.Fatal("a failure to preserve the rollback copy must not be reported as success")
	}
	if keep {
		t.Error("the undo succeeded, so staging holds only the NEW bundle and may be cleaned up")
	}
	if !strings.Contains(err.Error(), "NOT applied") {
		t.Fatalf("the operator must be told the update did not happen: %v", err)
	}

	// THE MACHINE IS EXACTLY AS IT WAS: the old bundle is installed, and
	// the new one is back in staging where cleanup may remove it.
	if got := readBundle(t, installed); got != "OLD" {
		t.Fatalf("the installed bundle is %q — the swap was not undone, so the machine is running an image with no rollback", got)
	}
	if got := readBundle(t, staged); got != "NEW" {
		t.Fatalf("staging holds %q, want the rejected NEW bundle", got)
	}
	if _, err := os.Stat(prev); !os.IsNotExist(err) {
		t.Error("no rollback copy should exist — the update was undone")
	}
}

// The ordinary path still preserves the outgoing bundle.
func TestASuccessfulUpdateKeepsThePreviousBundle(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "AII OS.app")
	staged := filepath.Join(dir, "staged.app")
	prev := filepath.Join(dir, previousBundleName)
	writeBundle(t, installed, "OLD")
	writeBundle(t, staged, "NEW")

	keep, err := installSwapped(installed, staged, prev)
	if err != nil {
		t.Fatal(err)
	}
	if keep {
		t.Error("staging holds nothing irreplaceable after a successful install")
	}
	if got := readBundle(t, installed); got != "NEW" {
		t.Fatalf("installed bundle is %q, want NEW", got)
	}
	if got := readBundle(t, prev); got != "OLD" {
		t.Fatalf("rollback copy is %q, want the OLD bundle", got)
	}
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

	if already, keep, err := installBundle(tgt, staged("NEW"), ownerForTest(t, dirA)); err != nil || already || keep {
		t.Fatalf("A's replacement: already=%v keep=%v err=%v", already, keep, err)
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

	already, keep, err := installBundle(tgt, staged("NEW"), ownerForTest(t, dirB))
	if err != nil || !already || keep {
		t.Fatalf("the same bundle must be found already installed: already=%v keep=%v err=%v", already, keep, err)
	}
	untouched("a second replacement with the same bundle")

	newer := staged("NEWER")
	if _, _, err := installBundle(tgt, newer, ownerForTest(t, dirB)); !errors.Is(err, ErrUpdatePending) {
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
	if readBundle(t, tgt.backup) != "OLD" {
		t.Fatal("A's healthy boot removed the bundle backup — it stays for an operator until the next replacement")
	}

	if already, _, err := installBundle(tgt, newer, ownerForTest(t, dirB)); err != nil || already {
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
	if _, _, err := installBundle(tgt, newer, ownerForTest(t, t.TempDir())); !errors.Is(err, ErrAlreadyApplying) {
		t.Fatalf("a replacement while another process holds the bundle must refuse as already applying: %v", err)
	}
	if readBundle(t, installed) != "NEW" || readBundle(t, tgt.backup) != "OLD" || exists(tgt.record) {
		t.Fatal("a refused replacement touched the bundle, its backup or its record")
	}
	holder.resume(t)
	holder.wait(t)
}
