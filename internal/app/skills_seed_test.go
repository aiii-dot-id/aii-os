package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestSeedSkillsDoc(t *testing.T) {
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "data", "ledger.jsonl")
	cfgPath := filepath.Join(dir, "config.json")
	a := &App{cfg: &Config{Identity: IdentityConfig{LedgerPath: ledgerPath}, SourcePath: cfgPath}}
	a.snapshotUILayoutPath(ledgerPath)
	seedPath := filepath.Join(dir, skillsFileName)

	// .
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

	// .
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

	// .
	// .
	// .
	old := skillsTemplate("000000000000")
	if err := os.WriteFile(seedPath, old, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedSkillsDoc()
	got, err = os.ReadFile(seedPath)
	if err != nil || !bytes.Equal(got, wantFirst) {
		t.Fatalf("our older seed must upgrade to the current template, got err=%v equal=%v", err, bytes.Equal(got, wantFirst))
	}

	// .
	// .
	// .
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

	// .
	if err := os.Remove(seedPath); err != nil {
		t.Fatal(err)
	}
	a.seedSkillsDoc()
	got, err = os.ReadFile(seedPath)
	if err != nil || !bytes.Equal(got, wantFirst) {
		t.Fatalf("re-seed after removal failed: err=%v", err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
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
