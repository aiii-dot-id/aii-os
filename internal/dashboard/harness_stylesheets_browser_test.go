//go:build !windows

package dashboard

import "testing"

// THE HARNESS RUNS A PAGE ONLY ONCE ITS STYLESHEETS HAVE LOADED. On Linux's
// WebKit a test's module ran before the page's stylesheet had arrived
// (2026-11-01: a toast without its pointer-events:none answered the hit
// test, "sheets 0, link loaded false"), so two tests failed one run in a
// few. Here the page adds a stylesheet in the same task that calls run():
// a stylesheet always finishes loading in a later task, so a body run at
// once can only see the page without it, in every engine, and a body that
// waits for it sees its rule.
func TestTheHarnessRunsAPageOnlyOnceItsStylesheetsHaveLoaded(t *testing.T) {
	page := `<!doctype html><html><head></head><body><p id="x">x</p>
<script type="module">
import { run } from '/__harness.js';
document.head.append(Object.assign(document.createElement('link'), { rel: 'stylesheet',
  href: URL.createObjectURL(new Blob(['#x { color: rgb(1, 2, 3); }'], { type: 'text/css' })) }));
run(assert => {
  const color = getComputedStyle(document.getElementById('x')).color;
  assert(color === 'rgb(1, 2, 3)', 'the body ran before its stylesheet had loaded: ' + color);
});
</script></body></html>`
	runPageInEngines(t, page, nil)
}
