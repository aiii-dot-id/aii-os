//go:build !windows

package dashboard

import "testing"

// A TOOL'S INTERNAL NAME IS NOT A PERSON'S WORD. The host's own "work" tool has no words of its
// own, so in a receipt it is among the other steps, and a failure of it is "A step failed". Each
// record, a disclosure further, keeps its own name.
func TestTheWorkToolIsNotNamedInAReceipt(t *testing.T) {
	page := `<!doctype html><link rel="stylesheet" href="/layout.css">
<div id="thread" style="height:400px;width:600px;overflow:auto"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import { S } from '/state.js';
import { renderInteractionPages } from '/views/chat.js';
import { assert, run } from '/__harness.js';
run(async () => {
  S.stats = { name: 'Fixture' }; S.identityExists = true;
  const at = s => '2026-09-26T15:' + s + 'Z';
  const step = (n, tool, outcome, args) => [
    { id: 'c' + n, sequence: String(2 * n), kind: 'tool_call', role: 'system', turn_id: 't1', recorded_at: at('53:' + (10 + n)), content: args || '{}', details: { tool } },
    { id: 'r' + n, related_id: 'c' + n, sequence: String(2 * n + 1), kind: 'tool_result', role: 'system', turn_id: 't1', recorded_at: at('53:' + (10 + n)), outcome, content: outcome === 'failed' ? 'it did not go' : 'ok', details: { tool } }];
  const rows = [{ id: 'q', sequence: '1', kind: 'message', role: 'operator', content: 'Go on', turn_id: 't1', recorded_at: at('52:49'), details: {} },
    ...step(1, 'shell', 'succeeded'), ...step(2, 'work', 'succeeded', '{"action":"update"}'), ...step(3, 'work', 'failed', '{"action":"update"}'), ...step(4, 'work', 'succeeded', '{"action":"update"}')];
  renderInteractionPages(new Map([['recorded', { identity: 'fixture', incarnation: 'disk', rows, has_older: false, has_newer: false }], ['transient', { identity: 'fixture', incarnation: 'memory', rows: [] }]]), () => {}, false);
  const receipt = document.querySelector('.turn-receipt');
  assert(receipt, 'no receipt is drawn');
  const names = [...receipt.querySelectorAll('.receipt-groups .g-name')].map(e => e.textContent);
  assert(names.join('|') === 'Running a command|3 other steps', 'the receipt says: ' + names.join('|'));
  assert(![...receipt.querySelectorAll('.receipt-groups .g-name')].some(e => e.classList.contains('tool')), 'a step is set as a tool name');
  const count = [...receipt.querySelectorAll('.receipt-groups .g-count')].map(e => e.textContent).join('|');
  assert(count === '|1 failed', 'the counts say: ' + count);
  const notes = receipt.querySelector('.receipt-notes').textContent;
  assert(!/\bwork\b/.test(notes) && notes.includes('A step failed'), 'the note says: ' + notes);
});
</script>`
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	modules := chatModuleRig(t)
	modules["/layout.css"] = css
	runPageInEngines(t, page, modules)
}
