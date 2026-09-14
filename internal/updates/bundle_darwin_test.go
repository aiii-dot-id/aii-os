//go:build darwin

package updates

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
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
	// .
	for _, target := range []string{filepath.Join(macos, "aii"), app} {
		if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", identity, target).CombinedOutput(); err != nil {
			t.Skipf("codesign unavailable: %v: %s", err, out)
		}
	}
	return app
}

// .
// .
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

// .
// .
// .
// .
func TestAStagedBundleFromAnotherIdentityIsRefused(t *testing.T) {
	dir := t.TempDir()
	installed := makeBundle(t, filepath.Join(dir, "inst"), "Installed", "-")
	staged := makeBundle(t, filepath.Join(dir, "stage"), "Staged", "-")

	err := verifyStagedBundle(staged, installed)
	if err == nil {
		t.Fatal("an ad-hoc signed bundle carries no Developer ID and must not be admitted as an update")
	}
	// .
	// .
	if !strings.Contains(err.Error(), "Team Identifier") && !strings.Contains(err.Error(), "DIFFERENT identity") {
		t.Fatalf("the refusal must name the identity problem, got: %v", err)
	}
}

// .
// .
func TestTheBundleArchiveMustCarryExactlyOneApp(t *testing.T) {
	dir := t.TempDir()

	// .
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

	// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

	// .
	// .
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

// .
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
