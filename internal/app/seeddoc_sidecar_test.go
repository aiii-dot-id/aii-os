package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// .
// .
// .

func TestAnEditedDocGetsTheNewVersionBesideIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	sidecar := path + sidecarSuffix
	v1 := []byte("v1 of the doc\n")
	v2 := []byte("v2 of the doc, expanded\n")
	shipped := []string{docSeedKey(nil, v1), docSeedKey(nil, v2)}

	// .
	// .
	edited := []byte("v1 of the doc\n\nmy own notes, mine\n")
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, v2, nil, shipped, "[test] seed")
	if got, _ := os.ReadFile(path); !bytes.Equal(got, edited) {
		t.Fatalf("the identity's doc was touched: %q", got)
	}
	got, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal("THE UPGRADE WAS WITHHELD — no sidecar beside the edited doc")
	}
	if !bytes.Equal(got, v2) {
		t.Fatalf("sidecar carries %q, want the current template", got)
	}

	// .
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(sidecar, old, old); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, v2, nil, shipped, "[test] seed")
	fi, err := os.Stat(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Fatal("an unchanged sidecar was rewritten — every boot churns it")
	}

	// .
	v3 := []byte("v3 of the doc, expanded again\n")
	shipped = append(shipped, docSeedKey(nil, v3))
	seedDoc(path, v3, nil, shipped, "[test] seed")
	if got, _ := os.ReadFile(sidecar); !bytes.Equal(got, v3) {
		t.Fatalf("the sidecar did not follow the template: %q", got)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, edited) {
		t.Fatalf("refreshing the sidecar touched the identity's doc: %q", got)
	}
}

func TestTheSidecarRetiresWhenTheDocIsOursAgain(t *testing.T) {
	v1 := []byte("v1\n")
	v2 := []byte("v2, expanded\n")
	shipped := []string{docSeedKey(nil, v1), docSeedKey(nil, v2)}

	// .
	// .
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, v2, nil, shipped, "[test] seed")
	if _, err := os.Stat(path + sidecarSuffix); err != nil {
		t.Fatal("fixture: sidecar must exist before adoption")
	}
	if err := os.WriteFile(path, v2, 0o644); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, v2, nil, shipped, "[test] seed")
	if _, err := os.Stat(path + sidecarSuffix); err == nil {
		t.Fatal("the sidecar outlived its adoption — a stale .new shadows a current doc")
	}

	// .
	// .
	dir2 := t.TempDir()
	path2 := filepath.Join(dir2, "doc.md")
	if err := os.WriteFile(path2, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedDoc(path2, v2, nil, shipped, "[test] seed")
	if err := os.WriteFile(path2, v1, 0o644); err != nil {
		t.Fatal(err)
	}
	seedDoc(path2, v2, nil, shipped, "[test] seed")
	if got, _ := os.ReadFile(path2); !bytes.Equal(got, v2) {
		t.Fatalf("an older shipped version did not upgrade: %q", got)
	}
	if _, err := os.Stat(path2 + sidecarSuffix); err == nil {
		t.Fatal("the sidecar outlived the re-seed")
	}
}

// .
// .
func TestNoSidecarAppearsBesideOurOwnDoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	current := []byte("the doc\n")
	shipped := []string{docSeedKey(nil, current)}

	seedDoc(path, current, nil, shipped, "[test] seed")
	seedDoc(path, current, nil, shipped, "[test] seed")
	if _, err := os.Stat(path + sidecarSuffix); err == nil {
		t.Fatal("a sidecar appeared beside the platform's own current doc")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestTheSkillsTemplateCarriesExactlyOneStampLine(t *testing.T) {
	if n := bytes.Count(skillsMD, []byte(skillsStampPrefix)); n != 1 {
		t.Fatalf("SKILLS.md carries %d occurrences of %q — normalizeSkillsStamp erases the FIRST, so every occurrence past one makes ownership detection unreliable", n, skillsStampPrefix)
	}
}
