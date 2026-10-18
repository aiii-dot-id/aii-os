package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
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
	seedDoc(path, current, nil, shipped, "", "[test] seed")
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
	seedDoc(path, current, nil, shipped, "", "[test] seed")
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
	seedDoc(path, current, nil, shipped, "", "[test] seed")
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, edited) {
		t.Fatalf("an identity's edit was clobbered, got %q", got)
	}
}

// A seeder writes its doc through a temporary name it creates, never
// through a fixed one it opens: a symlink planted at <doc>.seed was
// followed, and the file it named was truncated and overwritten with the
// doc.
func TestSeedDocWritesThroughNoNameItDidNotCreate(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(victim, []byte("not the seeder's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "doc.md")
	if err := os.Symlink(victim, path+".seed"); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	current := []byte("the doc\n")
	seedDoc(path, current, nil, []string{docSeedKey(nil, current)}, "", "[test] seed")
	if got, _ := os.ReadFile(path); !bytes.Equal(got, current) {
		t.Fatalf("the doc was not seeded: %q", got)
	}
	if got, _ := os.ReadFile(victim); string(got) != "not the seeder's\n" {
		t.Fatalf("the seeder wrote through a planted name into another file: %q", got)
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
	seedDoc(path, current, nil, []string{docSeedKey(nil, current)}, "", "[test] seed")
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

func TestSkillsDoesNotTeachAWayPastTheNameCheck(t *testing.T) {
	doc := string(skillsMD)
	i := strings.Index(doc, "**The sandbox boundary**")
	if i < 0 {
		t.Fatal("the guide names the sandbox boundary: an identity that meets a refusal reads why")
	}
	section := doc[i:]
	if j := strings.Index(section, "\n\n"); j > 0 {
		section = section[:j]
	}
	for _, evasion := range []string{"workaround", "work around", "write around", "concatenation", "glob the path", "not bash"} {
		if strings.Contains(strings.ToLower(section), evasion) {
			t.Errorf("the name-check section teaches a way past the wall (%q): %s", evasion, section)
		}
	}
	for _, boundary := range []string{
		"protected identity files and directories remain\nclosed",
		"apply protected-name rules",
		"a separately granted project may contain its\nown config.json or providers.json",
		"A typed refusal names the boundary it\nencountered",
	} {
		if !strings.Contains(section, boundary) {
			t.Errorf("the boundary section must explain %q: %s", boundary, section)
		}
	}
}

func TestSkillsTellsAProposalItsFate(t *testing.T) {
	doc := strings.Join(strings.Fields(string(skillsMD)), " ")
	if n := strings.Count(doc, identity.SkillProposalFate); n != 2 {
		t.Errorf("SKILLS.md carries the proposal's fate %d times; want its two sites, the commit verb and the not-built list: %q", n, identity.SkillProposalFate)
	}
	for _, untrue := range []string{"decides promotion", "verified=none", "before promotion"} {
		if strings.Contains(doc, untrue) {
			t.Errorf("SKILLS.md still tells a proposal %q", untrue)
		}
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

func TestSkillsSaysWhatPromotionAsksAndNoAuthorshipRule(t *testing.T) {
	doc := strings.Join(strings.Fields(string(skillsMD)), " ")
	if n := strings.Count(doc, identity.BeliefPromotionRule); n != 1 {
		t.Errorf("SKILLS.md carries the promotion rule %d times; want once, in the commit entry: %q", n, identity.BeliefPromotionRule)
	}

	if n := strings.Count(doc, identity.OperatorWordRule); n != 1 {
		t.Errorf("SKILLS.md carries the rule for the operator's word %d times; want once, in the commit entry: %q", n, identity.OperatorWordRule)
	}
	for _, gone := range []string{"independent voice", "belief ladder needs", "your own voice alone", "authorship class"} {
		if strings.Contains(strings.ToLower(doc), gone) {
			t.Errorf("SKILLS.md still teaches the removed rule (%q)", gone)
		}
	}
}

// THE GUIDE SAYS WHAT A RECALL ANSWER SAYS. SKILLS.md told a resident that
// hits "are ranked by match and decayed by age and use"; the answer now
// names its order's key, shows each hit's score, shares its limit across
// sources and shows the meaning part of a mark. It carries the sentence
// the recall verb's description carries (identity.RecallOrder), once.
func TestSkillsSaysHowARecallIsOrderedAndCut(t *testing.T) {
	doc := strings.Join(strings.Fields(string(skillsMD)), " ")
	if n := strings.Count(doc, identity.RecallOrder); n != 1 {
		t.Errorf("SKILLS.md carries how a recall is ordered and cut %d times; want once, in the recall entry: %q", n, identity.RecallOrder)
	}
	if strings.Contains(doc, "ranked by match") {
		t.Errorf("SKILLS.md still says recall hits are ranked by match")
	}
	for _, said := range []string{"three decimals", "`both (exact_words + meaning 0.734)`", "leaves out its lowest-scored hits", "never by meaning"} {
		if !strings.Contains(doc, said) {
			t.Errorf("SKILLS.md's recall entry does not say %q", said)
		}
	}
}
