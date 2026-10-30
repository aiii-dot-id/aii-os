package dashboard

import (
	"path"
	"regexp"
	"strings"
	"testing"
)

// settingsCardModules are the modules that draw the cards of Settings:
// settings.js, and every module it imports that draws a card (util.js,
// cardHead's own, aside). Read from
// the imports, so a card module Settings takes in later is held too.
func settingsCardModules(t *testing.T) []string {
	t.Helper()
	src, err := staticFS.ReadFile("static/views/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := []string{"static/views/settings.js"}
	for _, m := range regexp.MustCompile(`from '(\.\.?/[^']+\.js)'`).FindAllStringSubmatch(string(src), -1) {
		p := path.Clean(path.Join("static/views", m[1]))
		body, err := staticFS.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// util.js is where cardHead itself writes the heading.
		if p != "static/util.js" && strings.Contains(string(body), `class="card`) {
			modules = append(modules, p)
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
	call := regexp.MustCompile(`cardHead\('((?:[^'\\]|\\.)*)', '((?:[^'\\]|\\.)*)'\)`)
	calls := 0
	for _, path := range settingsCardModules(t) {
		src, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if i := strings.Index(string(src), "<h3"); i >= 0 {
			line := 1 + strings.Count(string(src[:i]), "\n")
			t.Errorf("%s:%d: a card's heading is written as markup, with no sentence for a resting pointer; draw it with cardHead", path, line)
		}
		for _, m := range call.FindAllStringSubmatch(string(src), -1) {
			calls++
			label, tip := m[1], m[2]
			if len(tip) < 20 || !strings.HasSuffix(tip, ".") || strings.EqualFold(tip, label) {
				t.Errorf("%s: the card %s has no sentence of its own: %q", path, label, tip)
			}
		}
	}
	if calls < 30 {
		t.Fatalf("the Settings modules name %d card sentences; the reading of them is broken", calls)
	}
}
