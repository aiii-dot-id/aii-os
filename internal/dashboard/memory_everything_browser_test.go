//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestTheMemoryPageShowsEveryExperienceAndCountsNoneWithheld(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="memory-stack"></div>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderMemory } from '/views/memory.js';
run(async (assert) => {
  S.identity = { beliefs: [], intentions: [],
                 experiences: [{content:'ran the suite', provenance:'self'},
                               {content:'QUILLFEATHER the harbour ledger was balanced by lamplight', provenance:'self'}],
                 private_count: 3 };
  S.view = 'memory';
  renderMemory();
  const stack = document.getElementById('memory-stack');
  const rows = [...stack.querySelectorAll('.item')].map(e => e.textContent);
  assert(rows.length === 2, 'both experiences are rows: ' + JSON.stringify(rows));
  assert(rows.some(r => r.includes('QUILLFEATHER the harbour ledger was balanced by lamplight')), 'the once-marked note is shown with its words: ' + JSON.stringify(rows));
  const heading = [...stack.querySelectorAll('h3')].map(e => e.textContent).find(h => h.startsWith('EXPERIENCES')) || '';
  assert(heading.includes('2 recent'), 'the heading counts what is listed: ' + heading);
  assert(!/private/i.test(stack.textContent), 'the page tells the operator nothing is held back from them, because nothing is: ' + heading);
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/memory.js", "static/state.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/ws.js"] = []byte(`export const sent = []; export function query(q, extra) { sent.push([q, extra || {}]); return true; } export function send() { return true; } export function wsReady() { return true; }`)
	runPageInEngines(t, page, modules)
}
