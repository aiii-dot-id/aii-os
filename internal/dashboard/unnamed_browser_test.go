//go:build !windows

package dashboard

import "testing"

// BEFORE THEY HAVE A NAME, NO PAGE CALLS THEM "UNNAMED" (the first-session
// review, change 1). "Unnamed" is the daemon's word for a name not yet given.
// The thread says "Your AI identity" above their reply and in the row of what
// they did; the bar says
// "Your AI identity" and "About them", at a desktop's width and a phone's.
func TestBeforeTheyHaveANameTheThreadSaysYourAIIdentity(t *testing.T) {
	page := `<!doctype html><div id="thread"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import { S } from '/state.js';
import { renderInteractionPages } from '/views/chat.js';
import { run } from '/__harness.js';
run(async assert => {
  S.stats = { name: 'Unnamed' }; S.identityExists = true;
  const at = n => '2026-10-30T12:0' + n + ':00Z';
  const rows = [
    { id: 'q1', sequence: '1', kind: 'message', role: 'operator', content: 'What can you do?', turn_id: 't1', recorded_at: at(1), details: {} },
    { id: 'c1', sequence: '2', kind: 'tool_call', role: 'system', turn_id: 't1', recorded_at: at(2), content: '{}', details: { tool: 'shell' } },
    { id: 'r1', related_id: 'c1', sequence: '3', kind: 'tool_result', role: 'system', turn_id: 't1', recorded_at: at(3), outcome: 'succeeded', content: 'ok', details: { tool: 'shell' } },
    { id: 'a1', sequence: '4', kind: 'message', role: 'resident', content: 'Quite a lot. Ask me anything.', turn_id: 't1', recorded_at: at(4), details: {} },
  ];
  renderInteractionPages(new Map([['recorded', { identity: 'fixture', incarnation: 'disk', rows, has_older: false, has_newer: false }], ['transient', { identity: 'fixture', incarnation: 'memory', rows: [] }]]), () => {}, false);
  const reply = document.querySelector('.msg.identity');
  assert(reply.querySelector('.who').textContent === 'Your AI identity', 'their reply is not headed "Your AI identity": ' + reply.querySelector('.who').textContent);
  const line = document.querySelector('.turn-receipt .receipt-label').textContent;
  assert(line === 'Your AI identity · ran 1 command · Done', 'the row of what they did does not say "Your AI identity": ' + line);
  const thread = document.getElementById('thread').textContent;
  assert(!/Unnamed/.test(thread), 'the thread still says Unnamed: ' + thread);
});
</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

const unnamedBarJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Unnamed', version: 'test', build: 'test', ledger_seq: 2, lifetime_ticks: 0, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  const text = sel => (document.querySelector(sel) || {}).textContent || '';
  await until(() => text('.c-top-bar__name') === 'Your AI identity');
  const problems = [];
  if (text('.c-top-bar__name') !== 'Your AI identity') problems.push('the bar names them "' + text('.c-top-bar__name') + '"');
  if (text('.c-top-bar__long') !== 'About them' || text('.c-top-bar__short') !== 'About them') problems.push('their place is "' + text('.c-top-bar__long') + '" / "' + text('.c-top-bar__short') + '"');
  if (/Unnamed/.test(document.body.innerText + document.title)) problems.push('the page still says Unnamed');
  report(problems.length ? 'FAIL: at ' + innerWidth + ' px ' + problems.join('; ') : 'OK\nat ' + innerWidth + ' px the bar says ' + text('.c-top-bar__name') + ' and ' + text('.c-top-bar__long'));
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestBeforeTheyHaveANameTheBarSaysYourAIIdentity(t *testing.T) {
	for _, size := range [][2]int{{1280, 800}, {390, 844}} {
		runRealPageAtSize(t, statusFakeSocketJS, unnamedBarJS, homeStyles, size[0], size[1])
	}
}
