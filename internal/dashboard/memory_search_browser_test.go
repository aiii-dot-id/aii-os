//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// THE MEMORY SEARCH SAYS WHAT IT DOES (operator report, 2026-09-05:
// "search memory does nothing"). Driven through the real memory module
// in both engines: typing filters what is loaded; a filter with no match
// says so instead of emptying the cards silently; Enter sends a recall
// query for the whole record; and the answer renders as its own card.
func TestTheBeliefsSearchNarrowsSaysNoMatchAndRecallsOnEnter(t *testing.T) {
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
                 experiences: [{content:'Quinn said the plan holds', provenance:'operator'}, {content:'ran the suite', provenance:'self'}] };
  S.view = 'memory';
  renderMemory();
  const stack = () => document.getElementById('memory-stack');
  const beliefs = () => [...stack().querySelectorAll('.c-belief')].map(e => e.querySelector('.c-belief__statement').textContent);
  // Their experiences are About → Experiences' (step 11), not drawn here.
  assert(beliefs().length === 2 && !stack().textContent.includes('Quinn said the plan holds'), 'their beliefs render before searching, and their experiences are not drawn under them');
  const ms = () => document.getElementById('mem-search');
  ms().value = 'anchor';
  ms().dispatchEvent(new Event('input', { bubbles: true }));
  assert(beliefs().length === 1 && beliefs()[0].includes('anchor'), 'typing narrows their beliefs to the one match: ' + JSON.stringify(beliefs()));
  assert(ms().value === 'anchor', 'the typed text survives the re-render');
  ms().value = 'orchids';
  ms().dispatchEvent(new Event('input', { bubbles: true }));
  assert(stack().textContent.includes('Nothing loaded here matches. Press Enter to ask them to recall from the whole record.'), 'no match says so instead of going silently empty');
  ms().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  assert(sent.length === 1 && sent[0][0] === 'recall' && sent[0][1].q === 'orchids', 'Enter recalls the whole record: ' + JSON.stringify(sent));
  S.recall = { query: 'anchor', text: 'Recall for "anchor": 2 matches\n  [seq 12] The anchor held' };
  renderMemory();
  const card = document.getElementById('mem-recall');
  assert(card && card.textContent.includes('[seq 12] The anchor held'), 'the recall answer renders as its own card');
  assert(stack().textContent.includes('Your AI identity recalled, for "anchor"'), 'the recall card names its cue');
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

func TestARecallHitOpensItsNoteWholeOnTheRecordPage(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="memory-stack"></div>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderMemory } from '/views/memory.js';
run(async (assert) => {
  S.identity = { beliefs: [], intentions: [], experiences: [] };
  S.view = 'memory';
  S.recall = { query: 'kingfisher', text: 'Experiences:\n' +
    '  [seq 4127, exp_k1, self | rank 1, words, score 0.01639 (fused 0.01639 × strength 1.00 × graph 1.00)] the kingfisher struck <the> water...\n' +
    'Conversation:\n' +
    '  [seq 88, turn 88, operator, 2026-10-20T10:00:00Z | rank 2, words, score 0.01613 (fused 0.01613 × strength 1.00 × graph 1.00)] a kingfisher, you said' };
  renderMemory();
  const card = document.getElementById('mem-recall');
  const links = [...card.querySelectorAll('a[data-mem-record]')];
  assert(links.length === 1, 'want one hit opening a row of the record, the note\'s, and none for the conversation turn: ' + links.map(a => a.getAttribute('href')).join(' '));
  assert(links[0].getAttribute('href') === '#/identity/record/4127', 'the note\'s hit does not open its row of the record: ' + links[0].getAttribute('href'));
  assert(card.textContent.includes('[seq 4127, exp_k1, self') && card.textContent.includes('struck <the> water'), 'the answer is not shown as the text it is: ' + card.textContent);
  assert(!card.querySelector('the'), 'a hit\'s words were drawn as markup');
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
	modules["/ws.js"] = []byte(`export function query() { return true; } export function send() { return true; } export function wsReady() { return true; }`)
	runPageInEngines(t, page, modules)
}
