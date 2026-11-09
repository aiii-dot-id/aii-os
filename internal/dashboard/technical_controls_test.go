package dashboard

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// NOTHING ON THIS PAGE SHOWS OR HIDES A CONTROL BY A SETTING (spec 6b, "A
// switch is not depth"; step 4). The box "Show technical controls"
// (dashboard.expert) is retired: no shipped file reads the flag from the
// status or sends it, the shell carries no box, and the names an
// identity's own copy of a view may import from views/expert.js stand in
// as "shown, and no box". The notice route it revealed is a switch of its
// own on Settings' Messages card (notice_route_browser_test.go), and the
// route and direct addresses on Settings → Dashboard are always drawn
// (operator_doors_browser_test.go).
func TestNothingShowsOrHidesAControlBySetting(t *testing.T) {
	reads := regexp.MustCompile(`stats\.expert\b|\bst\.expert\b|dashboard\.expert`)
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".html")) {
			return err
		}
		b, err := staticFS.ReadFile(path)
		if err != nil {
			return err
		}
		// What the code does, not what its comments recall.
		var code []string
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				code = append(code, line)
			}
		}
		src := strings.Join(code, "\n")
		if m := reads.FindString(src); m != "" {
			t.Errorf("%s reads or sends the retired setting (%s)", path, m)
		}
		for _, mark := range []string{`id="expert-bar"`, "data-expert", "data-technical="} {
			if strings.Contains(src, mark) {
				t.Errorf("%s still carries the retired box's markup: %s", path, mark)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := staticFS.ReadFile("static/views/expert.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, standIn := range []string{
		"export function expertShown() { return true; }",
		"export function expertBoxHTML() { return ''; }",
		"export function wireExpertBoxes() {}",
	} {
		if !strings.Contains(string(b), standIn) {
			t.Errorf("views/expert.js lost a stand-in an identity's own copy of a view imports: %s", standIn)
		}
	}
}
