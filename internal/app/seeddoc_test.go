package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seeddoc_test.go — the deploy rule for platform-authored docs, tested
// against the case the old seeders got wrong: OUR OLDER CONTENT.

func TestSeedDocReplacesOnlyWhatThePlatformShipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	oldShipped := []byte("v1 of the doc\n")
	current := []byte("v2 of the doc, expanded\n")
	shipped := []string{docSeedKey(nil, oldShipped), docSeedKey(nil, current)}

	// Absent → seeded.
	seedDoc(path, current, nil, shipped, "[test] seed")
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, current) {
		t.Fatalf("absent must seed: err=%v got=%q", err, got)
	}

	// An untouched OLDER shipped version → upgraded. THE CASE THE OLD
	// SEEDERS CLASSIFIED AS AN EDIT: our own older bytes differ from
	// the current template exactly the way an identity's edit does,
	// and only the answer key can tell the two apart.
	if err := os.WriteFile(path, oldShipped, 0o644); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, current, nil, shipped, "[test] seed")
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, current) {
		t.Fatalf("an older SHIPPED version did not upgrade, got %q", got)
	}

	// An identity's edit — bytes in no shipped version — is theirs,
	// forever.
	edited := []byte("v2 of the doc, expanded\n\nmy own notes\n")
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, current, nil, shipped, "[test] seed")
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, edited) {
		t.Fatalf("an identity's edit was clobbered, got %q", got)
	}
}

// A doc that already carries exactly the bytes we would write is not
// rewritten. The old seeders renamed over it on every boot — harmless
// until something watches the file, and the overlay dir HAS a watcher.
func TestSeedDocDoesNotChurnAnUnchangedDoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	current := []byte("the doc\n")
	if err := os.WriteFile(path, current, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, current, nil, []string{docSeedKey(nil, current)}, "[test] seed")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Fatal("an unchanged doc was rewritten — every boot churns the file and wakes its watcher")
	}
}

// --- The enforced discipline -------------------------------------
//
// The answer keys work only if every shipped version's hash is in
// them. These two tests hold the door: change a template without
// appending its new key, and the gate goes red — printing the exact
// value to append.

func TestSkillsShippedSeedsEndWithTheCurrentTemplate(t *testing.T) {
	want := docSeedKey(normalizeSkillsStamp, skillsMD)
	if n := len(skillsShippedSeeds); n == 0 || skillsShippedSeeds[n-1] != want {
		t.Fatalf("SKILLS.md changed without updating its answer key.\n"+
			"Append this to skillsShippedSeeds (and never remove old entries —\n"+
			"identities holding those versions stop receiving upgrades):\n\t%q", want)
	}
}

func TestOverlayShippedSeedsEndWithTheCurrentTemplate(t *testing.T) {
	want := docSeedKey(nil, overlayREADME)
	if n := len(overlayShippedSeeds); n == 0 || overlayShippedSeeds[n-1] != want {
		t.Fatalf("overlay_README.md changed without updating its answer key.\n"+
			"Append this to overlayShippedSeeds (and never remove old entries —\n"+
			"identities holding those versions stop receiving upgrades):\n\t%q", want)
	}
}
