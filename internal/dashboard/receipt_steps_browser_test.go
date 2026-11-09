//go:build !windows

package dashboard

import "testing"

// A STEP IS NAMED IN WORDS, AND THE REPLY'S OWN READING IS NOT A STEP (the
// first-session walk, 2026-11-03: every reply, even to "Thanks!", opened with
// "Unnamed · 1 other step · Done"). A reply whose only step read back the
// conversation it answers has no row, finished or still running; that reading
// is not counted beside other steps either, unless it did not succeed. The
// steps the host ships are named by what they did, a reminder by what work was
// asked; a plugin's step is still one of "other steps". A refusal gives the
// reason that came back, not the call.
func TestAStepIsNamedInWordsAndTheRepliesOwnReadingIsNotAStep(t *testing.T) {
	page := `<!doctype html><link rel="stylesheet" href="/layout.css">
<div id="thread"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import { S } from '/state.js';
import { renderInteractionPages, toggleThinkingDots } from '/views/chat.js';
import { run } from '/__harness.js';
run(async assert => {
  S.stats = { name: 'Mira' }; S.identityExists = true;
  let seq = 0;
  const at = () => '2026-11-03T22:' + String(10 + seq).padStart(2, '0') + ':00Z';
  const say = (role, content, turn) => ({ id: 'm' + (++seq), sequence: String(seq), kind: 'message', role, content, turn_id: turn, recorded_at: at(), details: {} });
  const step = (tool, args, outcome, back, turn) => {
    const call = { id: 'c' + (++seq), sequence: String(seq), kind: 'tool_call', role: 'system', turn_id: turn, recorded_at: at(), content: '→ ' + tool + '(' + JSON.stringify(args) + ')', details: { tool } };
    if (!outcome) return [call];
    const result = { id: 'r' + (++seq), related_id: call.id, sequence: String(seq), kind: 'tool_result', role: 'system', turn_id: turn, recorded_at: at(), outcome, content: call.content + '\n← ' + back, details: { tool } };
    return [call, result];
  };
  const conversation = { query: 'turn:1', source: 'conversation' };
  // A reason as long as the host's own refusals run, cut where a word ends.
  const LONG = 'data is kept by the host: your record, keys, credentials, backups and plugins\' state are the host\'s, and what you edit in it is your name and the dashboard\'s frame, the theme and the layout files. Ask your operator to change them.';
  // A long path: the cut falls at a folder, not inside a name, on either kind of path.
  const folders = n => Array.from({ length: n }, (_, i) => 'folder-' + i);
  const DENIED = 'Denied: /work/' + folders(40).join('/') + '/last.txt', DENIED_KEPT = 'Denied: /work/' + folders(19).join('/') + '…';
  const WINDOWS = 'Denied: C:\\' + folders(40).join('\\'), WINDOWS_KEPT = 'Denied: C:\\' + folders(20).join('\\') + '…';
  const base = [
    say('operator', 'Thanks!', 't1'), ...step('recall', conversation, 'succeeded', 'Thanks!', 't1'), say('resident', 'You are welcome.', 't1'),
    say('operator', 'Remind me to call my sister tomorrow at five.', 't2'), ...step('recall', conversation, 'succeeded', '…', 't2'),
    ...step('work', { action: 'alarm.set', when: '2026-11-04T17:00:00-05:00', message: 'Call your sister' }, 'succeeded', 'set', 't2'), say('resident', 'I will remind you tomorrow at five.', 't2'),
    say('operator', 'What is in data?', 't3'), ...step('ls', { path: 'data' }, 'refused', 'data is kept by the host.', 't3'), ...step('ls', { path: 'data/x' }, 'refused', LONG, 't3'), ...step('ls', { path: 'work' }, 'refused', DENIED, 't3'), ...step('ls', { path: 'c' }, 'refused', WINDOWS, 't3'),
    ...step('pl_id_example_plan', {}, 'succeeded', 'ok', 't3'), say('resident', 'I cannot look in there.', 't3'),
    say('operator', 'And earlier?', 't4'), ...step('recall', conversation, 'failed', 'the record could not be read', 't4'), say('resident', 'I could not read back what we said.', 't4'),
  ];
  const ask = say('operator', 'One more thing.', 't5');
  let rows = [...base, ask, ...step('recall', conversation, '', '', 't5')];
  const pages = () => new Map([['recorded', { identity: 'fixture', incarnation: 'disk', rows, has_older: false, has_newer: false }], ['transient', { identity: 'fixture', incarnation: 'memory', rows: [] }]]);
  S.thinking = true; toggleThinkingDots(true);
  renderInteractionPages(pages(), () => {}, false);
  const inner = document.getElementById('thread-inner');
  const shown = () => [...inner.querySelectorAll('.turn-receipt')].filter(el => !el.hidden && el.offsetParent !== null);
  const lines = () => shown().map(el => el.querySelector('.receipt-label').textContent);
  // The running reply, reading back the conversation, has no row either.
  assert(lines().join(' | ') === 'Mira · set 1 reminder · Done | Mira · looked in 4 folders · 1 other step · Refused | Mira · recalled 1 thing · Failed', 'the rows are: ' + lines().join(' | '));
  assert(inner.querySelectorAll('.turn-receipt').length === 5, 'every stretch keeps its records in the page: ' + inner.querySelectorAll('.turn-receipt').length);
  const notes = shown()[1].querySelector('.receipt-notes').textContent;
  assert(/^Looking in a folder was refused: data is kept by the host\./.test(notes.trim()) && !/→|\bls\b/.test(notes), 'the refusal does not say the step in words and the reason that came back: ' + notes);
  // The opened row says them as the note does; a plugin's keeps its name; a record keeps its own.
  const named = [...shown()[1].querySelectorAll('.receipt-groups .g-name')];
  assert(named.map(n => n.textContent).join() === 'Looking in a folder,pl_id_example_plan' && named[0].classList.contains('word') && named[1].classList.contains('tool'), 'the opened row does not say its steps in words: ' + named.map(n => n.textContent + '/' + n.className).join());
  assert(shown()[1].querySelector('.receipt-steps').textContent.includes('→ ls('), 'a step\'s record lost its own name');
  const said = [...shown()[1].querySelectorAll('.receipt-notes .c-os-note')].map(n => n.textContent);
  const cut = said.find(t => t.includes('plugins')), LONG_KEPT = 'data is kept by the host: your record, keys, credentials, backups and plugins\' state are the host\'s, and what you edit in it is your name and the dashboard\'s frame, the theme and the layout files. Ask…';
  assert(cut && cut.endsWith(LONG_KEPT) && !/change them\./.test(cut), 'a long reason does not end at its last word before the bound; it ends: ' + (cut && JSON.stringify(cut.slice(-50))));
  const denied = said.find(t => t.includes('Denied: /work/')), windows = said.find(t => t.includes('Denied: C:'));
  assert(denied && denied.endsWith(DENIED_KEPT), 'a long path was not cut at a folder: ' + (denied && JSON.stringify(denied.slice(-60))));
  assert(windows && windows.endsWith(WINDOWS_KEPT), 'a long Windows path was not cut at a folder: ' + (windows && JSON.stringify(windows.slice(-60))));
  // Finished with a second step, the running stretch has its row, and counts the step alone.
  rows = [...base, ask, ...step('recall', conversation, 'succeeded', '…', 't5'), ...step('read', { path: 'notes.md' }, 'succeeded', 'ok', 't5'), say('resident', 'Done.', 't5')];
  S.thinking = false; toggleThinkingDots(false);
  renderInteractionPages(pages(), () => {}, false);
  assert(lines().at(-1) === 'Mira · read 1 file · Done', 'the last stretch: ' + lines().at(-1));
});
</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}
