//go:build !windows

package dashboard

import "testing"

// THE NO AND WHO IT WAS STAND TOGETHER. "Who was it?" is only ever an answer to the No,
// so it stands beside it at every width. A name and Enter answers No with that name;
// Enter in an empty field answers nothing, because nobody has said anything yet.
const voiceCardJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets, sent = window.__sent;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const r = el => el.getBoundingClientRect();
const round = b => [b.left, b.top, b.right, b.bottom].map(Math.round).join(',');
const asked = () => sent.filter(m => m.type === 'ask');
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  sockets[0].say({ type: 'asks', asks: [{ id: 'ask-1', kind: 'voice', from: 'identity', text: 'I heard someone just now, and I do not know that voice yet. Was that you?', heard: 'Can you finish the grant letter today?', proposed: '2026-09-12T16:00:07Z' }, { id: 'ask-2', kind: 'voice', from: 'identity', text: 'And that one?', heard: '', proposed: '2026-09-12T16:00:08Z' }] });
  await until(() => document.querySelector('.c-ask__input'), 8000);
  check(document.querySelectorAll('.c-ask__hint').length === 1, 'a card with nothing it can prove still quotes: ' + document.querySelectorAll('.c-ask__hint').length);
  const who = document.querySelector('.c-ask__input'), buttons = [...document.querySelectorAll('.c-ask button')];
  const yes = buttons.find(b => b.textContent === "Yes, that's me"), no = buttons.find(b => b.textContent === "No, that's not me");
  const w = r(who), n = r(no), y = r(yes), card = r(document.querySelector('.c-ask'));
  check(Math.abs((w.top + w.bottom) - (n.top + n.bottom)) <= 4, 'at ' + innerWidth + ' px the field at ' + round(w) + ' does not stand beside the No at ' + round(n));
  check(w.right <= n.left, 'at ' + innerWidth + ' px the field at ' + round(w) + ' does not come before the No at ' + round(n));
  check(w.width >= 120, 'at ' + innerWidth + ' px the field is ' + Math.round(w.width) + ' px wide, too narrow for a name');
  check(y.bottom <= n.top || y.right <= w.left, 'at ' + innerWidth + ' px the Yes at ' + round(y) + ' lies between or over the field and the No');
  // One line each, one height: a No that wraps stretches the field beside it.
  check(n.height <= y.height + 1, 'at ' + innerWidth + ' px the No is ' + Math.round(n.height) + ' px tall beside a Yes of ' + Math.round(y.height) + ': its words wrap');
  check(Math.abs(w.height - y.height) <= 1, 'at ' + innerWidth + ' px the field is ' + Math.round(w.height) + ' px tall beside a Yes of ' + Math.round(y.height));
  for (const [el, what] of [[who, 'the field'], [no, 'the No'], [yes, 'the Yes']]) {
    const b = r(el), at = document.elementFromPoint((b.left + b.right) / 2, (b.top + b.bottom) / 2);
    check(b.left >= card.left && b.right <= card.right, what + ' at ' + round(b) + ' leaves the card at ' + round(card));
    check(at === el || el.contains(at), what + ' at ' + innerWidth + ' px does not take its own press');
  }
  check(document.documentElement.scrollWidth <= innerWidth, 'the page scrolls sideways at ' + innerWidth + ' px');
  const form = who.closest('form');
  form.requestSubmit();
  check(asked().length === 0, 'Enter in an empty field answered: ' + JSON.stringify(asked()));
  who.value = ' Mary ';
  form.requestSubmit();
  await until(() => asked().length > 0, 3000).catch(() => {});
  const a = asked()[0];
  check(asked().length === 1 && a.ask.id === 'ask-1' && a.ask.answer === 'no' && a.ask.text === 'Mary', 'a name and Enter sent: ' + JSON.stringify(asked()));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTheNoAndWhoItWasStandTogether(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"small phone", 360, 740}, {"phone", 390, 844}, {"window", 1280, 800}} {
		t.Run(size.name, func(t *testing.T) {
			runRealPageAtSize(t, statusFakeSocketJS, voiceCardJS, homeStyles, size.width, size.height)
		})
	}
}
