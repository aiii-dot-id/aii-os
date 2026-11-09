//go:build !windows

package dashboard

import (
	"os"
	"strings"
	"testing"
)

// homeStyles are the stylesheets the conversation's head and its reply box
// depend on: the note, the question, the box.
var homeStyles = []string{"tokens/tokens.css", "views/home/home.css", "components/note/note.css", "components/ask/ask.css", "components/reply-box/reply-box.css", "components/button/button.css"}

// runRealPage runs measureJS on the shipped page (runStatusMeasurement's
// page) with the named stylesheets served as well.
func runRealPage(t *testing.T, socketJS, measureJS string, styles []string) {
	t.Helper()
	page, modules := realPage(t, socketJS, measureJS, styles)
	runPageInEnginesWithHeaders(t, page, modules, map[string]string{"Content-Security-Policy": uiCSP})
}

// runRealPageAtSize is runRealPage in a window of w by h.
func runRealPageAtSize(t *testing.T, socketJS, measureJS string, styles []string, w, h int) {
	t.Helper()
	page, modules := realPage(t, socketJS, measureJS, styles)
	runPageInEnginesAtSizeWithHeaders(t, page, modules, map[string]string{"Content-Security-Policy": uiCSP}, w, h)
}

// runPageInEnginesAtSizeWithHeaders is runPageInEnginesAtSize with headers
// on every response, as runPageInEnginesWithHeaders sends them.
func runPageInEnginesAtSizeWithHeaders(t *testing.T, page string, modules map[string][]byte, headers map[string]string, w, h int) {
	t.Helper()

	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}

	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, modules, headers, w, h, 0)
		})
	}
}

// runRealPageServing is runRealPage with more paths served, such as the
// record's pages (GET /record) a view reads.
func runRealPageServing(t *testing.T, socketJS, measureJS string, styles []string, served map[string][]byte) {
	t.Helper()
	page, modules := realPage(t, socketJS, measureJS, styles)
	for path, body := range served {
		modules[path] = body
	}
	runPageInEnginesWithHeaders(t, page, modules, map[string]string{"Content-Security-Policy": uiCSP})
}

func realPage(t *testing.T, socketJS, measureJS string, styles []string) (string, map[string][]byte) {
	t.Helper()
	read := func(p string) []byte {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return b
	}
	frame := string(read("index.html"))
	const first = `<script src="./theme-boot.js"></script>`
	if !strings.Contains(frame, first) || !strings.Contains(frame, "</body>") {
		t.Fatal("index.html no longer has the shape this test loads it by")
	}
	page := strings.Replace(frame, first, `<script src="/__socket.js"></script>`+first, 1)
	page = strings.Replace(page, "</body>", `<script type="module" src="/__measure.js"></script></body>`, 1)
	modules := map[string][]byte{
		"/__socket.js":  []byte(socketJS),
		"/__measure.js": []byte(measureJS),
		"/theme.css":    read("theme.css"),
		"/layout.css":   read("layout.css"),
		"/custom.css":   read("custom.css"),
	}
	for _, s := range styles {
		modules["/"+s] = read(s)
	}
	return page, modules
}

// A browser that last showed Home six days ago, at record entry 100, and
// remembers no view: the page opens on Home.
const sixDaysAgoJS = `try { localStorage.setItem('aii.lastSeenAt', String(Date.now() - 6 * 86400000 - 60000)); localStorage.setItem('aii.lastSeenSeq', '100'); localStorage.removeItem('aii.view'); } catch (e) {}
`

// A RELOAD IS THE SAME VISIT. What the page says of the last visit (About →
// Between you, the note opening when it is new) must not change
// when the page is reloaded, though this visit has already been kept for the
// next. Coming to the page again is a new visit, and reads what this one kept.
func TestAReloadIsTheSameVisit(t *testing.T) {
	visit, err := os.ReadFile("static/shell/visit.js")
	if err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><script type="module">
import { arrivalOfThisVisit } from '/shell/visit.js';
import { run } from '/__harness.js';
const stage = sessionStorage.getItem('test.stage');
if (!stage) {
  localStorage.setItem('aii.lastSeenAt', '1000'); localStorage.setItem('aii.lastSeenSeq', '5');
  const first = arrivalOfThisVisit();
  if (first.at !== 1000 || first.seq !== 5) run(assert => assert(false, 'the first read did not hold the last visit: ' + JSON.stringify(first)));
  else {
    localStorage.setItem('aii.lastSeenAt', '2000'); localStorage.setItem('aii.lastSeenSeq', '9');
    sessionStorage.setItem('test.stage', 'reloaded');
    location.reload();
  }
} else if (stage === 'reloaded') {
  const again = arrivalOfThisVisit();
  if (again.at !== 1000 || again.seq !== 5) run(assert => assert(false, 'a reload changed what the page says of the last visit: ' + JSON.stringify(again)));
  else { sessionStorage.setItem('test.stage', 'new'); location.assign('/?again'); }
} else {
  run(assert => { const next = arrivalOfThisVisit(); assert(next.at === 2000 && next.seq === 9, 'coming again read the old visit: ' + JSON.stringify(next)); });
}
</script>`
	runPageInEngines(t, page, map[string][]byte{"/shell/visit.js": visit})
}
