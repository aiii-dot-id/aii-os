package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSeedSkillsDoc pins the seed contract, the upgrade path, and the
// normalization that makes both possible: absent seeds, identity
// edits win, OUR older seed upgrades, identity edits survive OUR
// upgrade. The normalization is load-bearing — without it the guard
// cannot tell "our doc from an older build" from "an identity's
// edited doc," and one of the two silently dies.
func TestSeedSkillsDoc(t *testing.T) {
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "data", "ledger.jsonl")
	cfgPath := filepath.Join(dir, "config.json")
	a := &App{cfg: &Config{Identity: IdentityConfig{LedgerPath: ledgerPath}, SourcePath: cfgPath}}
	a.snapshotUILayoutPath(ledgerPath)
	seedPath := filepath.Join(dir, skillsFileName)

	// Absent → seeded, stamp substituted, marker absent.
	a.seedSkillsDoc()
	got, err := os.ReadFile(seedPath)
	if err != nil || len(got) == 0 {
		t.Fatalf("absent must seed, got err=%v len=%d", err, len(got))
	}
	if bytes.Contains(got, []byte(skillsStampMarker)) {
		t.Fatalf("deployed doc must carry the real stamp, not the marker")
	}
	wantFirst := skillsTemplate(BuildIdentity())
	if !bytes.Equal(got, wantFirst) {
		t.Fatalf("seed must equal template-with-stamp, got %d bytes", len(got))
	}

	// Identity's own doc → untouched.
	identity := append([]byte(nil), got...)
	identity = append(identity, []byte("\n<!-- my annotation -->\n")...)
	if err := os.WriteFile(seedPath, identity, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedSkillsDoc()
	got, _ = os.ReadFile(seedPath)
	if !bytes.Equal(got, identity) {
		t.Fatalf("identity edits must win, got trailing bytes %q", got[len(got)-40:])
	}

	// OUR seed restamped by a previous build — SAME content, different
	// stamp — re-seeds. This is the restamp path, not the upgrade path:
	// it proves only that the stamp line is erased before comparing.
	old := skillsTemplate("000000000000")
	if err := os.WriteFile(seedPath, old, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedSkillsDoc()
	got, err = os.ReadFile(seedPath)
	if err != nil || !bytes.Equal(got, wantFirst) {
		t.Fatalf("our older seed must upgrade to the current template, got err=%v equal=%v", err, bytes.Equal(got, wantFirst))
	}

	// Identity's doc SURVIVES an upgrade pass: identity-edited bytes
	// from before the rebuild must never be clobbered by the new
	// template — theirs wins even when we ship new bytes.
	identitySurvives := skillsTemplate("111111111111")
	identitySurvives = append(identitySurvives, []byte("\n<!-- keep me through upgrades -->\n")...)
	if err := os.WriteFile(seedPath, identitySurvives, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedSkillsDoc()
	got, _ = os.ReadFile(seedPath)
	if !bytes.Equal(got, identitySurvives) {
		t.Fatalf("identity edits must survive an upgrade, got %d bytes vs %d", len(got), len(identitySurvives))
	}

	// Removed → seeds again.
	if err := os.Remove(seedPath); err != nil {
		t.Fatal(err)
	}
	a.seedSkillsDoc()
	got, err = os.ReadFile(seedPath)
	if err != nil || !bytes.Equal(got, wantFirst) {
		t.Fatalf("re-seed after removal failed: err=%v", err)
	}

	// A PREVIOUS BUILD'S CONTENT — different bytes, present in the
	// answer key — upgrades. This is the actual upgrade path, and the
	// case the pre-key seeder misclassified as an identity edit: the
	// original version of this test never constructed it, only the
	// stamp swap above, so "upgrades work" was green while every
	// content upgrade was a no-op.
	oldContent := []byte("---\ndescribes-build: aaaa\n---\nthe older, shorter index\n")
	if err := os.WriteFile(seedPath, oldContent, 0o644); err != nil {
		t.Fatal(err)
	}
	skillsShippedSeeds = append(skillsShippedSeeds, docSeedKey(normalizeSkillsStamp, oldContent))
	defer func() { skillsShippedSeeds = skillsShippedSeeds[:len(skillsShippedSeeds)-1] }()
	a.seedSkillsDoc()
	got, _ = os.ReadFile(seedPath)
	if !bytes.Equal(got, wantFirst) {
		t.Fatalf("an older SHIPPED version did not upgrade — the answer key is not consulted")
	}
}

// TestNormalizeSkillsStamp is the negative control: the upgrade path
// exists ONLY through normalization — if the describes-build line is
// not erased on both sides, old-seed vs template compare unequal and
// upgrades never fire. This test pins the mechanism itself.
func TestNormalizeSkillsStamp(t *testing.T) {
	base := []byte("---\nname: x\ndescribes-build: aaaabbbbcccc\nrest: 1\n---\n")
	other := []byte("---\nname: x\ndescribes-build: ddddeeeeffff\nrest: 1\n---\n")
	if !bytes.Equal(normalizeSkillsStamp(base), normalizeSkillsStamp(other)) {
		t.Fatalf("stamp-normalized docs from different builds must compare equal")
	}
	edited := []byte("---\nname: x\ndescribes-build: ddddeeeeffff\nrest: 2\n---\n")
	if bytes.Equal(normalizeSkillsStamp(base), normalizeSkillsStamp(edited)) {
		t.Fatalf("a real edit must not be erased by normalization")
	}
}

// A NOTE ON THE STAMP LINE IS THE IDENTITY'S. Normalization erases the
// stamp the seeder writes, not the line: a value in no stamp's shape (a
// note after the build, or in its place) keeps the doc's own key, so the
// doc is the identity's and stays, with the upgrade beside it. Every shape
// a seeder has written is still erased.
func TestANoteOnTheStampLineMakesTheDocTheIdentitys(t *testing.T) {
	for _, stamp := range []string{skillsStampMarker, "unknown", "a84ffd088a9a", "aa8e855125d6 (dirty)", BuildIdentity()} {
		if got, want := normalizeSkillsStamp(skillsTemplate(stamp)), normalizeSkillsStamp(skillsMD); !bytes.Equal(got, want) {
			t.Errorf("the stamp %q is not erased", stamp)
		}
	}
	for _, value := range []string{"a84ffd088a9a  <- checked against my boot line", "my own words", "a84ffd088a9a (dirty) mine", "A84FFD088A9A"} {
		if bytes.Equal(normalizeSkillsStamp(skillsTemplate(value)), normalizeSkillsStamp(skillsMD)) {
			t.Errorf("%q on the describes-build line was erased as a stamp", value)
		}
	}

	a, _ := editionHome(t)
	a.seedSkillsDoc()
	path := a.skillsPath()
	line := []byte(skillsStampPrefix + BuildIdentity())
	noted := bytes.Replace(mustRead(t, path), line, append(append([]byte(nil), line...), "  <- checked against my boot line"...), 1)
	writeDoc(t, path, noted)
	a.seedSkillsDoc()
	if got := mustRead(t, path); !bytes.Equal(got, noted) {
		t.Fatal("a note on the describes-build line was replaced at the next start, by the same build")
	}
	if side := mustRead(t, path+sidecarSuffix); !bytes.Equal(side, skillsTemplate(BuildIdentity())) {
		t.Fatal("the current version does not wait beside the noted doc")
	}
}
