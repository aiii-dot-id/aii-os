package updates

import (
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"path/filepath"
	"slices"
	"testing"
)

// never the program: beside a bundle its lock, its record, the previous
// bundle, the release a rollback undid and the newest release verified
// (highestVerified); beside a bare binary the same,
// named for the binary. Uninstall
func TestTheLeftoversAreTheUpdatersNamesBesideTheProgram(t *testing.T) {
	apps := filepath.Join(string(filepath.Separator), "Applications")
	exe := filepath.Join(apps, "AII OS.app", "Contents", "MacOS", "aii")
	got := Leftovers(exe)
	want := []string{filepath.Join(apps, ".aii-os-update.lock"), filepath.Join(apps, ".aii-os-update.json"), filepath.Join(apps, previousBundleName),
		// What a rollback keeps of the release it undid (failedUpdate).
		filepath.Join(apps, ".aii-os-update.failed.json"),
		// The newest release this program verified (highestVerified).
		filepath.Join(apps, ".aii-os-update.highest.json")}
	if !slices.Equal(got, want) {
		t.Fatalf("beside a bundle: %q, want %q", got, want)
	}
	bin := filepath.Join(string(filepath.Separator), "opt", "aii", "aii")
	got = Leftovers(bin)
	dir := filepath.Dir(bin)
	want = []string{filepath.Join(dir, ".aii.update.lock"), filepath.Join(dir, ".aii.update.json"), filepath.Join(dir, ".aii.previous"),
		// What a rollback keeps of the release it undid (failedUpdate).
		filepath.Join(dir, ".aii.update.failed.json"),
		filepath.Join(dir, ".aii.update.highest.json"),
		// And the note of an update that installed aii alone (incompleteInstall).
		filepath.Join(dir, ".aii.update.incomplete.json")}
	// And what an update keeps of each file it installs beside the binary,
	// hidden and named after it as the binary's own backup is.
	for _, name := range installedBeside(packagefmt.HostPlatform()) {
		want = append(want, filepath.Join(dir, "."+name+".previous"))
	}
	if len(want) == 6 {
		t.Fatal("this platform's release installs nothing beside the binary")
	}
	if !slices.Equal(got, want) {
		t.Fatalf("beside a binary: %q, want %q", got, want)
	}
	for _, p := range append(Leftovers(exe), Leftovers(bin)...) {
		if p == exe || p == bin || p == filepath.Join(apps, "AII OS.app") {
			t.Fatalf("the program itself is named a leftover: %s", p)
		}
	}
}
