package app

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// seededDocKind is one of the three docs the platform seeds, as these tests
// drive it: its template and answer key (swapped to stand in another
// build), its seeder, where it lands and what the running build deploys.
type seededDocKind struct {
	name      string
	prefix    string // its fixtures' file prefix in testdata/public-editions
	template  *[]byte
	shipped   *[]string
	normalize func([]byte) []byte
	seed      func(*App)
	path      func(*App) string
	current   func() []byte
}

func seededDocKinds() []seededDocKind {
	return []seededDocKind{
		{"SKILLS.md", "SKILLS-", &skillsMD, &skillsShippedSeeds, normalizeSkillsStamp,
			(*App).seedSkillsDoc, (*App).skillsPath, func() []byte { return skillsTemplate(BuildIdentity()) }},
		{"METHOD.md", "METHOD-", &methodMD, &methodShippedSeeds, nil,
			(*App).seedMethodDoc, func(a *App) string { return a.seededDocPath(methodFileName) }, func() []byte { return methodMD }},
		{"the overlay README", "overlay_README-", &overlayREADME, &overlayShippedSeeds, nil,
			(*App).seedOverlayREADME, func(a *App) string { return filepath.Join(a.uiOverlayDir(), "README.md") }, func() []byte { return overlayREADME }},
	}
}

// editionHome is an identity's home in the stock layout, config in
// config/ and the record and the store in data/, and an App that seeds it.
func editionHome(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{ConfigDirName, "data"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &App{cfg: &Config{
		SourcePath: filepath.Join(home, ConfigDirName, "config.json"),
		Identity:   IdentityConfig{LedgerPath: filepath.Join(home, "data", "ledger.jsonl")},
	}}, home
}

// asBuild runs seed as another build would: one whose template is text and
// whose answer key is this build's and text's. The template and the key
// are this build's again when it returns.
func asBuild(kind seededDocKind, text []byte, seed func()) {
	template, shipped := *kind.template, *kind.shipped
	defer func() { *kind.template, *kind.shipped = template, shipped }()
	*kind.template = text
	*kind.shipped = append(slices.Clip(shipped), docSeedKey(kind.normalize, text))
	seed()
}

func writeDoc(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// publicEditions are the docs public builds wrote into identities,
// exported unedited from the public repository (testdata/public-editions/
// README.md says from which commits). stamp is the describes-build value
// the public build at that commit wrote into SKILLS.md.
var publicEditions = []struct{ file, stamp string }{
	{"SKILLS-v0.1.12.md", "a84ffd088a9a"},
	{"SKILLS-c9d7197.md", "c9d71976387f"},
	{"SKILLS-v0.1.14.md", "3f9adc45b98b"},
	{"SKILLS-86ead06.md", "86ead060f2f6"},
	{"SKILLS-v0.1.15.md", "0bc45f44415e"},
	{"SKILLS-ffe0bfe.md", "ffe0bfe4344f"},
	{"SKILLS-v0.1.16.md", "2365aa69af18"},
	{"METHOD-v0.1.12.md", ""},
	{"overlay_README-v0.1.12.md", ""},
	{"overlay_README-v0.1.14.md", ""},
	{"overlay_README-v0.1.15.md", ""},
}

// AN IDENTITY HOLDING ANY PUBLIC EDITION UPGRADES AT ITS NEXT START, and
// the upgrade that waited beside it in .new is retired. Each edition is
// deployed as its build deployed it, with an older upgrade waiting beside
// it, as the live identities held them.
func TestAnIdentityHoldingAnyPublicEditionUpgradesAtItsStart(t *testing.T) {
	dir := filepath.Join("testdata", "public-editions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "README.md" && !slices.ContainsFunc(publicEditions, func(p struct{ file, stamp string }) bool { return p.file == e.Name() }) {
			t.Errorf("%s/%s is in no row of publicEditions, so nothing tests it", dir, e.Name())
		}
	}
	kinds := seededDocKinds()
	for _, edition := range publicEditions {
		t.Run(edition.file, func(t *testing.T) {
			i := slices.IndexFunc(kinds, func(k seededDocKind) bool { return strings.HasPrefix(edition.file, k.prefix) })
			if i < 0 {
				t.Fatalf("%s names none of the seeded docs", edition.file)
			}
			kind := kinds[i]
			deployed := mustRead(t, filepath.Join(dir, edition.file))
			if edition.stamp != "" {
				marker := []byte(skillsStampPrefix + skillsStampMarker)
				if !bytes.Contains(deployed, marker) {
					t.Fatalf("%s does not carry the stamp marker its build replaced", edition.file)
				}
				deployed = bytes.Replace(deployed, marker, []byte(skillsStampPrefix+edition.stamp), 1)
			}
			a, _ := editionHome(t)
			path := kind.path(a)
			writeDoc(t, path, deployed)
			writeDoc(t, path+sidecarSuffix, []byte("the version a later build left beside the doc it took for an edit\n"))

			kind.seed(a)

			if got := mustRead(t, path); !bytes.Equal(got, kind.current()) {
				t.Fatalf("an identity holding the public edition %s (key %s) still holds it after a start: the platform's own version was taken for the identity's edit", edition.file, docSeedKey(kind.normalize, deployed))
			}
			absent(t, path+sidecarSuffix)
		})
	}
}

// A VERSION NO ANSWER KEY NAMES IS STILL THE PLATFORM'S WHILE NOBODY HAS
// EDITED IT. A build whose own key names its text (a branch, a release
// candidate) seeds the doc; the next build's key does not name that text.
// Untouched, the doc upgrades at the next start and nothing waits beside
// it. Edited, it stays the identity's, and the current version waits
// beside it.
func TestAVersionNoAnswerKeyNamesUpgradesUntouchedAndStaysEdited(t *testing.T) {
	for _, kind := range seededDocKinds() {
		for _, edited := range []bool{false, true} {
			t.Run(kind.name+map[bool]string{false: ", untouched", true: ", edited"}[edited], func(t *testing.T) {
				branch := append(append([]byte(nil), *kind.template...), "\n<!-- a change only a branch build carries -->\n"...)
				a, _ := editionHome(t)
				path := kind.path(a)
				asBuild(kind, branch, func() { kind.seed(a) })
				held := mustRead(t, path)
				if !bytes.Contains(held, []byte("a change only a branch build carries")) {
					t.Fatalf("the branch build did not seed its own text: %.120q", held)
				}
				if edited {
					held = append(held, "\nA note the identity wrote.\n"...)
					writeDoc(t, path, held)
				}

				kind.seed(a)

				got := mustRead(t, path)
				if !edited {
					if !bytes.Equal(got, kind.current()) {
						t.Fatalf("an untouched doc a branch build wrote did not upgrade: the platform's own version was taken for the identity's edit")
					}
					absent(t, path+sidecarSuffix)
					return
				}
				if !bytes.Equal(got, held) {
					t.Fatal("the identity's edit of a doc a branch build wrote was replaced")
				}
				if side := mustRead(t, path+sidecarSuffix); !bytes.Equal(side, kind.current()) {
					t.Fatal("the current version does not wait beside the identity's edited doc")
				}
				// THE RECORD NEVER NAMES AN EDIT. Were the identity's key in
				// it, the next start would take the edit for the platform's
				// and replace it: the record is read now, and the edit held
				// through another start of this build and one of a later.
				editKey := docSeedKey(kind.normalize, held)
				for _, name := range []string{skillsFileName, methodFileName, overlayRecordName} {
					if rec, err := os.ReadFile(a.seedRecordPath(name)); err == nil && strings.Contains(string(rec), editKey) {
						t.Fatalf("the record %s names the identity's edit (%s)", name, editKey)
					}
				}
				kind.seed(a)
				if got := mustRead(t, path); !bytes.Equal(got, held) {
					t.Fatal("another start of the same build replaced the identity's edit")
				}
				later := append(append([]byte(nil), *kind.template...), "\n<!-- a later build -->\n"...)
				asBuild(kind, later, func() { kind.seed(a) })
				if got := mustRead(t, path); !bytes.Equal(got, held) {
					t.Fatal("a later build replaced the identity's edit")
				}
			})
		}
	}
}
