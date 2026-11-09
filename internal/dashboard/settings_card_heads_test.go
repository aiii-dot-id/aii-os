package dashboard

import (
	"path"
	"regexp"
	"strings"
	"testing"
)

// settingsImport reads a module's static imports, as written.
var settingsImport = regexp.MustCompile(`from '(\.\.?/[^']+\.js)'`)

// settingsGroup is Settings as its code is laid out: the index and every
// module under views/settings/ it reaches through imports, each import read
// from its own module's folder. A walk, not a list, so a module the group
// takes in later, or one reached only through another, is read too.
func settingsGroup(t *testing.T) []string {
	t.Helper()
	const index = "static/views/settings.js"
	seen := map[string]bool{index: true}
	group := []string{index}
	for i := 0; i < len(group); i++ {
		src, err := staticFS.ReadFile(group[i])
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range settingsImport.FindAllStringSubmatch(string(src), -1) {
			p := path.Clean(path.Join(path.Dir(group[i]), m[1]))
			if !seen[p] && strings.HasPrefix(p, "static/views/settings/") {
				seen[p] = true
				group = append(group, p)
			}
		}
	}
	if len(group) < 12 {
		t.Fatalf("the walk from %s reaches %d modules of Settings; it no longer reads their imports", index, len(group))
	}
	return group
}

type settingsWalked struct {
	path string
	card bool // it draws a card: the index, or a module with class="card
}

// settingsWalk is every module of Settings' group (settingsGroup) and what they import, each
// once, util.js, cardHead's own, aside. Read from the imports, so a module Settings takes in
// later is held too.
func settingsWalk(t *testing.T) []settingsWalked {
	t.Helper()
	group := settingsGroup(t)
	seen := map[string]bool{}
	for _, p := range group {
		seen[p] = true
	}
	var modules []settingsWalked
	for _, g := range group {
		src, err := staticFS.ReadFile(g)
		if err != nil {
			t.Fatal(err)
		}
		modules = append(modules, settingsWalked{g, g == group[0] || strings.Contains(string(src), `class="card`)})
		for _, m := range settingsImport.FindAllStringSubmatch(string(src), -1) {
			p := path.Clean(path.Join(path.Dir(g), m[1]))
			if seen[p] {
				continue
			}
			seen[p] = true
			body, err := staticFS.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			// util.js is where cardHead itself writes the heading.
			if p != "static/util.js" {
				modules = append(modules, settingsWalked{p, strings.Contains(string(body), `class="card`)})
			}
		}
	}
	return modules
}

// EVERY SETTINGS CARD'S HEADING IS DRAWN WITH ITS SENTENCE. A heading
// written as bare markup has no sentence for a resting pointer, so none
// is written that way in the modules that draw Settings: each goes through
// cardHead (util.js), and each sentence named there is a sentence. This
// holds the cards a browser test cannot draw at once, such as a restore
// under way, by reading the source.
func TestEverySettingsCardHeadingCarriesASentence(t *testing.T) {
	call := regexp.MustCompile(`\bcardHead\s*\(\s*'((?:[^'\\]|\\.)*)', '((?:[^'\\]|\\.)*)'\s*\)`)
	anyCall := regexp.MustCompile(`\bcardHead\s*\(`)
	for _, m := range settingsWalk(t) {
		src, err := staticFS.ReadFile(m.path)
		if err != nil {
			t.Fatal(err)
		}
		// A module that draws a card writes no heading as markup. One drawn as its board
		// draws it writes its own, and carries its sentence as a visible hint.
		if i := strings.Index(string(src), "<h3"); m.card && i >= 0 {
			line := 1 + strings.Count(string(src[:i]), "\n")
			t.Errorf("%s:%d: a card's heading is written as markup, with no sentence for a resting pointer; draw it with cardHead", m.path, line)
		}
		// Any call that exists, in any module, carries its sentence.
		read := call.FindAllStringSubmatch(string(src), -1)
		for _, c := range read {
			label, tip := c[1], c[2]
			if len(tip) < 20 || !strings.HasSuffix(tip, ".") || strings.EqualFold(tip, label) {
				t.Errorf("%s: the card %s has no sentence of its own: %q", m.path, label, tip)
			}
		}
		if n := len(anyCall.FindAllString(string(src), -1)); n != len(read) {
			t.Errorf("%s: %d cardHead calls and %d of them read as a label and a sentence", m.path, n, len(read))
		}
	}
	// The parts drawn as their boards draw them (App, Backups, step 16a's,
	// People, Access, Voice and Model) carry their sentence as a visible
	// hint, not through cardHead, and TestEverySettingsCardSaysWhatItDoes
	// reads those in a browser ("The model" by name). So no number of
	// cardHead calls is owed here.
}
