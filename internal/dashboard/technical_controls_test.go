package dashboard

import (
	"html"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var technicalPlaces = map[string][]string{
	"notices":          {"on Chat", "notices to you off the dashboard"},     // the top of Chat: the notice route's switch
	"route":            {"Settings → Dashboard", "route", "relay and port"}, // the Public Name card: the route chooser
	"direct-addresses": {"direct addresses"},                                // the Public Name card: the addresses a direct route publishes
}

func TestTheTechnicalControlsBoxNamesEveryPlaceItGoverns(t *testing.T) {
	sources := map[string]string{}
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".html")) {
			return err
		}
		b, err := staticFS.ReadFile(path)
		sources[path] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	marked := map[string]bool{}
	mark := regexp.MustCompile(`data-technical="([^"]+)"`)
	readsFlag := regexp.MustCompile(`stats\.expert\b|\bst\.expert\b`)
	var readers, drawers []string
	for path, src := range sources {
		for _, m := range mark.FindAllStringSubmatch(src, -1) {
			marked[m[1]] = true
			if technicalPlaces[m[1]] == nil {
				t.Errorf("%s marks %q as shown by the box, and the box's sentence does not name it (technicalPlaces)", path, m[1])
			}
		}
		if readsFlag.MatchString(src) {
			readers = append(readers, path)
		}
		if strings.Contains(src, "expertShown(") && path != "static/views/expert.js" {
			drawers = append(drawers, path)
		}
	}
	for place := range technicalPlaces {
		if !marked[place] {
			t.Errorf("no shipped markup marks %q, which the box's sentence names", place)
		}
	}
	if !slices.Equal(readers, []string{"static/views/expert.js"}) {
		t.Errorf("the flag is read outside expert.js, where the box's words are: %v", readers)
	}
	sort.Strings(drawers)
	if !slices.Equal(drawers, []string{"static/views/settings.js"}) {
		t.Errorf("the places that ask whether the technical controls are shown: %v; each is a place of technicalPlaces", drawers)
	}

	words := jsConst(t, sources["static/views/expert.js"], "EXPERT_WORDS")
	tip := jsConst(t, sources["static/views/expert.js"], "EXPERT_TIP")
	if words != "Show technical controls" || strings.Contains(strings.ToLower(words), "expert") {
		t.Errorf("the box reads %q", words)
	}
	for place, said := range technicalPlaces {
		for _, w := range said {
			if !strings.Contains(tip, w) {
				t.Errorf("the box's sentence does not name %s (%q): %s", place, w, tip)
			}
		}
	}
	if !strings.HasSuffix(tip, "Showing or hiding them changes nothing else.") {
		t.Errorf("the box's sentence does not say it only shows and hides: %s", tip)
	}

	frame := sources["static/index.html"]
	label := regexp.MustCompile(`<label class="expert-toggle" data-expert-label title="([^"]*)"><input type="checkbox" data-expert> <span data-expert-words>([^<]*)</span></label>`).FindStringSubmatch(frame)
	if label == nil {
		t.Fatal("the box at the top of Chat is not drawn as one label with its words and its sentence")
	}
	if html.UnescapeString(label[1]) != tip || html.UnescapeString(label[2]) != words {
		t.Errorf("the box at the top of Chat says other words than its other copies:\n %q %q\n %q %q", label[2], label[1], words, tip)
	}
}

// jsConst is the string an exported constant of a module is set to,
// unescaped as JavaScript would.
func jsConst(t *testing.T, src, name string) string {
	t.Helper()
	m := regexp.MustCompile(`export const ` + name + ` = '((?:[^'\\]|\\.)*)';`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no constant %s", name)
	}
	s, err := strconv.Unquote(`"` + strings.ReplaceAll(m[1], `"`, `\"`) + `"`)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}
