//go:build !windows

package dashboard

import "testing"

// THE MODEL WE RECOMMEND IS CHOSEN, AND THE REST ARE BEHIND "Other models";
// A SUBSCRIPTION IS A CHOICE OF ITS OWN (the walk, 2026-11-03: Gemini's forty
// models as one flat list). On the shipped page and its
// socket: a service with a recommended model shows that model, chosen, and
// "Other models" opens the whole list and keeps it open; a service with no
// recommendation shows its list. Claude's and ChatGPT's subscriptions stand
// as their own rows, one not signed in carrying the daemon's line, and Begin
// waits for its sign-in as it waits for a model. A longer line the daemon
// keeps, a path in it, wraps inside its row at a phone's width.
const firstRunModelsJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets;
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const pause = ms => new Promise(r => setTimeout(r, ms));
const shown = el => !!el && el.getClientRects().length > 0;
const forty = Array.from({ length: 40 }, (_, i) => i === 7 ? 'gemini-2.5-flash' : 'gemini-model-' + i);
const SIGN_IN = 'Sign in to Claude Code on this computer and your subscription works here. No key is needed.';
const providers = [
  { name: 'Gemini', endpoint: 'https://gemini.test/v1', models: forty, default_model: 'gemini-2.5-flash', chat: true, has_key: true },
  { name: 'Claude (Max/Pro)', credential: 'claude-code', status: 'no_credential', status_reason: SIGN_IN, models: ['claude-a', 'claude-b'], default_model: 'claude-a', chat: true },
  { name: 'ChatGPT (Plus/Pro)', credential: 'codex', status: 'ok', models: ['gpt-a', 'gpt-b'], default_model: 'gpt-a', chat: true },
  { name: 'ChatGPT (work)', credential: 'codex', status: 'credential_expired', status_reason: 'the credential at /Users/someone/.codex/accounts/work/authentication.json expired', models: ['gpt-a'], default_model: 'gpt-a', chat: true },
  { name: 'Plain', endpoint: 'https://plain.test/v1', models: ['p1', 'p2', 'p3'], chat: true, has_key: true },
];
const rowOf = name => document.querySelector('[data-row="' + CSS.escape(name) + '"]');
const pick = name => { const r = rowOf(name).querySelector('input'); r.checked = true; r.dispatchEvent(new Event('change')); };
const other = name => { pick('other'); const sel = $('fb-provider'); sel.value = String(providers.findIndex(p => p.name === name)); sel.dispatchEvent(new Event('change')); };
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); sockets[0].say({ type: 'status', stats: { name: '(not born)' } });
  await until(() => $('fb-birth') && $('fb-choices'), 5000);
  sockets[0].say({ type: 'providers', providers }); await pause(200);
  // The subscriptions are rows of their own; the one not signed in says how.
  if (!rowOf('Claude (Max/Pro)') || !rowOf('ChatGPT (Plus/Pro)')) return report('FAIL: the subscriptions are not rows of their own: ' + [...document.querySelectorAll('[data-row]')].map(r => r.dataset.row).join(', '));
  check(rowOf('Claude (Max/Pro)') && rowOf('Claude (Max/Pro)').textContent.includes(SIGN_IN), 'the subscription not signed in does not say how');
  check(rowOf('ChatGPT (Plus/Pro)') && !/Sign in/.test(rowOf('ChatGPT (Plus/Pro)').textContent), 'a subscription signed in still says to sign in');
  const work = rowOf('ChatGPT (work)'), workEnd = work ? Math.round(work.getBoundingClientRect().right) : 0;
  check(work && document.documentElement.scrollWidth <= innerWidth && workEnd <= innerWidth, 'at ' + innerWidth + ' px a longer line runs past the screen: its row ends at ' + workEnd + ', the page is ' + document.documentElement.scrollWidth + ' wide');
  pick('Claude (Max/Pro)'); await pause(100);
  check($('fb-birth').disabled, 'Begin can be pressed for a subscription not signed in');
  check(!shown($('fb-key-row')), 'a subscription asks for a key');
  pick('ChatGPT (Plus/Pro)'); await pause(100);
  check(!$('fb-birth').disabled, 'Begin waits for a subscription that is signed in');
  // The recommended model is chosen; the rest are behind Other models.
  other('Gemini'); await pause(100);
  const chosen = $('fb-model-chosen'), more = $('fb-model-more');
  check(shown(chosen) && chosen.textContent.includes('gemini-2.5-flash') && chosen.textContent.includes('Recommended'), 'the recommended model is not shown chosen: ' + chosen.textContent);
  check(!shown($('fb-model')) && $('fb-model').value === 'gemini-2.5-flash', 'the forty are drawn as one list, or another is chosen: ' + $('fb-model').value);
  check(!$('fb-birth').disabled, 'Begin waits with the recommended model chosen');
  more.click(); await pause(50);
  check(shown($('fb-model')) && $('fb-model').options.length === 40 && document.activeElement === $('fb-model'), 'Other models does not open the whole list');
  check(!shown(chosen), 'the chosen line stays over the open list');
  $('fb-model').value = 'gemini-model-3'; $('fb-model').dispatchEvent(new Event('change')); await pause(50);
  check(shown($('fb-model')) && $('fb-model').value === 'gemini-model-3', 'a model chosen from the list closed it');
  // A service with no recommendation shows its list.
  other('Plain'); await pause(100);
  check(shown($('fb-model')) && !shown($('fb-model-chosen')), 'a service with no recommended model hides its list');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTheRecommendedModelIsChosenAndSubscriptionsAreRowsOfTheirOwn(t *testing.T) {
	for _, size := range [][2]int{{1280, 800}, {390, 844}} {
		runRealPageAtSize(t, statusFakeSocketJS, firstRunModelsJS, homeStyles, size[0], size[1])
	}
}
