//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const charterApprovalJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets, sent = window.__sent;
const say = m => sockets[sockets.length - 1].say(m);
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const card = () => [...document.querySelectorAll('#identity-stack .card')].find(c => norm((c.querySelector('h3') || {}).textContent) === 'CHARTER — RING 1');
const block = () => card() && card().querySelector('[data-charter-approval]');
const part = sel => (block() && block().querySelector(sel)) || null;
const text = sel => norm(sel ? (part(sel) || {}).textContent : (block() || {}).textContent);
const notes = () => [...block().querySelectorAll('[data-approval-note]')];
const problems = [], seen = [];
const expect = (ok, what) => { if (!ok) problems.push(what); };

const CHARTER = 'We build this together. You tell me what you see.';
const MARKUP = '<b>Yes</b>, I approve. <img src="x" alt="i"> <a href="#top">it</a> <script>window.__ran = 1</script> & "that" is final';
const identity = approval => ({ beliefs: [], intentions: [], experiences: [], synthesis: '', brief: '', charter: CHARTER,
  trust_level: 'building', autonomy_level: 'supervised', approval });

async function show(name, approval, mode) {
  say({ type: 'continuity', continuity: { mode: mode || 'normal', safe_reason: mode === 'safe' ? 'the record failed verification' : '', ledger_seq: 9, lifetime_ticks: 3 } });
  say({ type: 'identity', identity: identity(approval) });
  await until(() => block() && norm(card().textContent).includes(CHARTER));
  expect(block(), name + ': the charter card has no approval block: ' + (card() ? norm(card().textContent) : 'no charter card'));
  if (!block()) return false;
  const read = block().innerText.split('\n').map(norm).filter(Boolean).map(l => l.length > 240 ? l.slice(0, 80) + ' [… ' + (l.length - 140) + ' more …] ' + l.slice(-60) : l);
  seen.push(name.padEnd(15) + ' | ' + read.join(' / '));
  expect(card().children[1] === block(), name + ': the approval is not the first thing under the charter heading');
  expect(block().checkVisibility({ checkVisibilityCSS: true, visibilityProperty: true }) && !block().closest('[inert],[aria-hidden="true"]'),
    name + ': the approval is not visible on the Identity page without a click');
  expect(!block().querySelector('b, img, a, script'), name + ': markup in the approval became markup: ' + block().innerHTML);
  return true;
}

async function main() {
  sockets[0].open();
  say({ type: 'status', stats: { name: 'Vega', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 3, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0, voice_state: 'setup' } });
  document.querySelector('.nav-item[data-view="identity"]').click();
  expect(sent.some(f => f.type === 'query' && f.query === 'identity'), 'opening Identity did not ask for the identity: ' + JSON.stringify(sent.map(f => f.query || f.type)));

  if (await show('said', { state: 'said', excerpt: MARKUP, turn: 42, said_at: '2026-09-29T07:57:17.123456789Z', record: 17 })) {
    expect(part('[data-approval-words]') && part('[data-approval-words]').textContent === MARKUP, 'the words are not shown verbatim, as text: ' + (part('[data-approval-words]') || block()).innerHTML);
    expect(/^The words the identity cites as your approval — turn 42, .*2026/.test(text('[data-approval-head]')), 'the label does not name turn 42 and when it was said: ' + text('[data-approval-head]'));
    expect(notes().length === 0, 'words that check out carry no warning: ' + text());
    expect(window.__ran === undefined, 'a script in the words ran');
  }

  const long = 'Yes — I approve this charter as you wrote it, and I will read it again tomorrow. '.repeat(13).slice(0, 1000);
  if (await show('said, cut', { state: 'said', excerpt: long, chars: 1500, turn: 42, said_at: '2026-09-29T07:57:17.123456789Z', record: 17 })) {
    const w = part('[data-approval-words]'), box = w.getBoundingClientRect(), style = getComputedStyle(w);
    expect(w.textContent === long + '…', 'a cut is not marked where it was made: ' + JSON.stringify(w.textContent.slice(-12)));
    expect(/1500 characters in all\. The whole is turn 42 in the conversation\./.test(text()), 'a cut does not say how long the whole is and where it is: ' + text());
    const lines = Math.round((box.height - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom)) / parseFloat(style.lineHeight));
    seen.push('  (1000 characters of words: ' + lines + ' lines, ' + Math.round(box.height) + 'px tall and ' + Math.round(box.width) + 'px wide, in a ' + innerWidth + 'x' + innerHeight + ' window)');
  }

  if (await show('gone', { state: 'gone', excerpt: 'Yes, I approve.', turn: 42, record: 17 })) {
    expect(text('[data-approval-head]') === 'The words the identity cites as your approval — turn 42', 'a gone turn claims a time: ' + text('[data-approval-head]'));
    expect(text('[data-approval-words]') === 'Yes, I approve.', 'the words of a gone turn are not shown');
    expect(notes().some(n => /Turn 42 is no longer in the conversation/.test(n.textContent) && /record 17/.test(n.textContent) && n.classList.contains('bad-text')), 'a gone turn is not said, in warning colour: ' + text());
  }
  if (await show('gone, cut', { state: 'gone', excerpt: 'x'.repeat(1000), chars: 1200, turn: 42, record: 17 })) {
    expect(/The whole is record 17 of the signed record\./.test(text()), 'a cut whose turn is gone does not route to the signed record: ' + text());
  }
  if (await show('differs', { state: 'differs', excerpt: 'Yes, I approve.', turn: 42, record: 17 })) {
    expect(notes().some(n => /Turn 42 in the conversation does not hold these words of yours now/.test(n.textContent) && n.classList.contains('bad-text')), 'a turn that differs is not said: ' + text());
  }

  if (await show('uncited', { state: 'uncited', record: 23 })) {
    expect(!part('[data-approval-words]'), 'no words were cited, yet words are shown');
    expect(text() === 'No approval of this charter is recorded: the signed entry that wrote it, record 23, cites no words of yours.', 'an uncited charter is not declared: ' + JSON.stringify(text()));
  }
  if (await show('uncited, words', { state: 'uncited', excerpt: 'yes', record: 24 })) {
    expect(text('[data-approval-head]') === 'The words the identity cites as your approval — no turn cited' && /record 24, cites no turn for these words/.test(text()), 'words with no turn are not said as such: ' + text());
  }

  if (await show('unreadable', { state: 'unreadable', error: 'charter approval: record 12 did not write the charter rel_x holds — the projection and the record disagree' })) {
    expect(/could not be read: charter approval: record 12 .* disagree/.test(text()), 'a failed read is not said: ' + text());
  }
  if (await show('not sent', undefined)) {
    expect(/could not be read: the runtime sent no account of them/.test(text()), 'a missing account is not said: ' + text());
  }

  if (await show('safe', { state: 'said', excerpt: MARKUP, turn: 42, said_at: '2026-09-29T07:57:17.123456789Z', record: 17 }, 'safe')) {
    expect(document.querySelector('#identity-stack [data-safe-restore]'), 'the page is not drawn in SAFE');
    expect(part('[data-approval-words]') && part('[data-approval-words]').textContent === MARKUP, 'in SAFE the words are not shown: ' + text());
  }
}

const finish = () => report(problems.length ? 'FAIL: ' + problems.join('\n  - ') + '\n' + seen.join('\n') : 'OK\n' + seen.join('\n'));
main().then(finish, e => { problems.push('the run stopped: ' + ((e && e.stack) || e)); finish(); });
`

func TestTheIdentityPageShowsTheOperatorsCitedWordsFirstInTheCharter(t *testing.T) {
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
	page = strings.Replace(page, "</body>", `<script type="module" src="/__check.js"></script></body>`, 1)
	runPageInEnginesWithHeaders(t, page, map[string][]byte{
		"/__socket.js": []byte(statusFakeSocketJS),
		"/__check.js":  []byte(charterApprovalJS),
		"/theme.css":   read("theme.css"),
		"/layout.css":  read("layout.css"),
		"/custom.css":  read("custom.css"),
	}, map[string]string{"Content-Security-Policy": uiCSP})
}
