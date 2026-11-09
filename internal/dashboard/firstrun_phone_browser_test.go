//go:build !windows

package dashboard

import "testing"

// AT A PHONE'S WIDTH THE CHOICE AND BEGIN COME FIRST (the walk, 2026-11-03;
// the lead's item 4). On the shipped page, at 390 by 844, with the rows a new
// computer shows (both subscriptions not signed in, saying how), the title
// and one line come first, then the card, and Begin is on the first screen;
// the page's order is what the eye meets, so a screen reader and the Tab key
// meet it too. At 1280 the card stands beside the words, as the board draws.
const firstRunPhoneJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets;
const $ = id => document.getElementById(id);
const q = s => document.querySelector(s);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const box = s => q(s).getBoundingClientRect();
const shown = s => !!q(s) && q(s).getClientRects().length > 0;
const providers = [
  { name: 'Claude (Max/Pro)', credential: 'claude-code', status: 'no_credential', status_reason: 'Sign in to Claude Code on this computer and your subscription works here. No key is needed.', models: ['c'], default_model: 'c', chat: true },
  { name: 'ChatGPT (Plus/Pro)', credential: 'codex', status: 'no_credential', status_reason: 'Sign in to the Codex app on this computer and your subscription works here. No key is needed.', models: ['g'], default_model: 'g', chat: true },
  { name: 'OpenAI', endpoint: 'https://api.openai.test/v1', models: ['o'], default_model: 'o', chat: true, can_sign_in: true },
  { name: 'Anthropic', endpoint: 'https://api.anthropic.test/v1', models: ['a'], default_model: 'a', chat: true },
];
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); sockets[0].say({ type: 'status', stats: { name: '(not born)' } });
  await until(() => $('fb-birth') && $('fb-choices'), 5000);
  sockets[0].say({ type: 'providers', providers });
  await until(() => document.querySelectorAll('[data-row]').length === 5, 5000);
  const order = [...q('#firstboot > .first-run').children].map(e => e.className.split(' ')[0]).join(' ');
  check(order === 'first-run__head first-run__card first-run__what', 'the page is not in a phone\'s order: ' + order);
  const at = innerWidth + ' px: ';
  if (innerWidth < 768) {
    const pitch = box('.first-run__pitch'), lh = parseFloat(getComputedStyle(q('.first-run__pitch')).lineHeight);
    check(shown('.first-run__pitch') && pitch.height <= lh + 1, at + 'the line under the title is not one line: ' + Math.round(pitch.height) + ' for ' + lh);
    check(!shown('.first-run__lede'), at + 'the lede is said as well as the line');
    check(box('.first-run__title').bottom <= pitch.top && pitch.bottom <= box('.first-run__card').top, at + 'the title, the line and the card are not in that order');
    check(box('#fb-birth').bottom <= innerHeight, at + 'Begin is not on the first screen: it ends at ' + Math.round(box('#fb-birth').bottom) + ' in ' + innerHeight);
    check(document.documentElement.scrollWidth <= innerWidth, at + 'the page scrolls sideways');
  } else {
    check(!shown('.first-run__pitch') && shown('.first-run__lede'), at + 'the lede is not the words beside the card');
    check(box('.first-run__card').left >= box('.first-run__title').right && Math.abs(box('.first-run__card').top - box('.first-run__title').top) < 2, at + 'the card does not stand beside the words');
    check(box('.first-run__what').top >= box('.first-run__head').bottom, at + 'the words are not under the title');
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK\nat ' + innerWidth + ' px Begin ends at ' + Math.round(box('#fb-birth').bottom) + ' of ' + innerHeight);
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAtAPhonesWidthTheChoiceAndBeginComeFirst(t *testing.T) {
	for _, size := range [][2]int{{390, 844}, {1280, 800}} {
		runRealPageAtSize(t, statusFakeSocketJS, firstRunPhoneJS, homeStyles, size[0], size[1])
	}
}
