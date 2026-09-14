//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestTheMemorySearchFiltersSaysNoMatchAndRecallsOnEnter(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="memory-stack"></div>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { sent } from '/ws.js';
import { renderMemory } from '/views/memory.js';
run(async (assert) => {
  S.identity = { beliefs: [{id:'b1', statement:'Precision is honesty', ring:3, status:'new'}, {id:'b2', statement:'The anchor held', ring:3, status:'new'}],
                 intentions: [{statement:'Land the gate', state:'active'}],
                 experiences: [{content:'the operator said the plan holds', provenance:'operator'}, {content:'ran the suite', provenance:'self'}] };
  S.view = 'memory';
  renderMemory();
  const stack = () => document.getElementById('memory-stack');
  assert(stack().querySelectorAll('.item').length === 5, 'all five items render before filtering');
  const ms = () => document.getElementById('mem-search');
  ms().value = 'anchor';
  ms().dispatchEvent(new Event('input', { bubbles: true }));
  const after = [...stack().querySelectorAll('.item')].map(e => e.textContent);
  assert(after.length === 1 && after[0].includes('anchor'), 'typing filters to the one match: ' + JSON.stringify(after));
  assert(ms().value === 'anchor', 'the typed text survives the re-render');
  assert(stack().textContent.includes('no matches among what is loaded here'), 'cards with no match say so instead of going silently empty');
  ms().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  assert(sent.length === 1 && sent[0][0] === 'recall' && sent[0][1].q === 'anchor', 'Enter recalls the whole record: ' + JSON.stringify(sent));
  S.recall = { query: 'anchor', text: 'Recall for "anchor": 2 matches\n  [seq 12] The anchor held' };
  renderMemory();
  const card = document.getElementById('mem-recall');
  assert(card && card.textContent.includes('[seq 12] The anchor held'), 'the recall answer renders as its own card');
  assert(stack().textContent.includes('the whole record, for “anchor”'), 'the recall card names its query');
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
	// .
	// .
	modules["/ws.js"] = []byte(`export const sent = []; export function query(q, extra) { sent.push([q, extra || {}]); return true; } export function send() { return true; } export function wsReady() { return true; }`)
	runPageInEngines(t, page, modules)
}
