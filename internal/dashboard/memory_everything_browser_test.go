//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// THE MEMORY PAGE SHOWS EVERY EXPERIENCE AND COUNTS NONE IT DOES NOT SHOW
// Its heading read "EXPERIENCES (2 recent · 3 private)": a number
// standing in for notes the operator was not given. Nothing an identity
// holds is kept from the operator, so the page has no such count to print,
// whatever a payload carries, and a note that was once marked is a row
// like its neighbours. Their experiences are About → Experiences':
// driven through the real experiences module in every engine.
func TestTheMemoryPageShowsEveryExperienceAndCountsNoneWithheld(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="experiences-stack"></div>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderExperiences } from '/views/identity/experiences.js';
run(async (assert) => {
  S.identity = { beliefs: [], intentions: [],
                 experiences: [{content:'ran the suite', provenance:'self'},
                               {content:'QUILLFEATHER the harbour ledger was balanced by lamplight', provenance:'self'}],
                 private_count: 3 };
  S.view = 'experiences';
  renderExperiences();
  const stack = document.getElementById('experiences-stack');
  const rows = [...stack.querySelectorAll('.experience')].map(e => e.textContent);
  assert(rows.length === 2, 'both experiences are rows: ' + JSON.stringify(rows));
  assert(rows.some(r => r.includes('QUILLFEATHER the harbour ledger was balanced by lamplight')), 'the once-marked note is shown with its words: ' + JSON.stringify(rows));
  const scope = (stack.querySelector('.experiences__scope') || {}).textContent || '';
  assert(scope.startsWith('The newest 50 experiences'), 'the page does not say what it lists: ' + scope);
  assert(!/private/i.test(stack.textContent), 'the page tells the operator nothing is held back from them, because nothing is: ' + stack.textContent);
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/identity/experiences.js", "static/state.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/ws.js"] = []byte(`export const sent = []; export function query(q, extra) { sent.push([q, extra || {}]); return true; } export function send() { return true; } export function wsReady() { return true; }`)
	runPageInEngines(t, page, modules)
}
