//go:build !windows

package dashboard

import "testing"

// THE SPEND SECTION FOLLOWS THE PROVIDERS ON SETTINGS → MODEL (open-rulings
// row 20; the board's order, host 0632 Q10). "This month" is the first
// choice and the default (host 1757, A): drawn, it asks for 31 days and
// keeps only the days of Today's month, by the strings the server sends,
// so on the 1st the last day of the month before is not counted. The
// heading names the span drawn. Each provider's tokens are summed in the
// board's no-price words, a floor says "at least", and what could not be
// counted is said in the summary. Each day is under "By day", whose open
// is kept through a redraw. The answer Status asked for (its own 32 days)
// is not drawn here; another choice of days asks again for that many.
func TestTheSpendCardShowsEachDayByRoleAndModelAndAsksForTheDaysChosen(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = { llm: { provider: '', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none' },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, cognition: {}, restart_required: [] };
  S.providers = []; S.providersLoaded = true; S.view = 'settings'; S.spend = null;
  renderSettings();
  const asked = () => { const q = frames.filter(f => f.type === 'query' && f.query === 'spend'); frames.length = 0; return q; };
  const head = () => document.getElementById('m-spend').textContent;
  let q = asked();
  assert(q.length === 1 && q[0].days === 31, 'this month, the default, asks for 31 days: ' + JSON.stringify(q));
  assert(document.querySelector('[data-spend]'), 'Model draws the spend section');
  assert(head() === 'Spend this month', 'the heading names this month: ' + head());
  renderSettings();
  assert(asked().length === 0, 'a redraw while the spend is asked for does not ask again');

  const month = { zone: 'Pacific/Auckland', today: '2026-11-01', days: [
    { day: '2026-11-01', tokens: 6570, floor: true, unknown: 1, lines: [
      { role: 'sub-agent:bench', model: 'bench-1', calls: 0, tokens: 5000, floor: true },
      { role: 'turns', provider: 'P', model: 'm', calls: 4, tokens: 1500, input: 1350, output: 150, cached: 600, unknown: 1 },
      { role: 'facility:dream', provider: 'P', model: 'm', calls: 1, tokens: 70 } ] },
    { day: '2026-10-31', tokens: 400, lines: [ { role: 'turns', provider: 'L', model: 'local', calls: 3, tokens: 400 } ] } ] };

  // Status's own read lands in the same slot: it is not this page's answer.
  S.spend = month; S.spendRequest = 'status-9';
  renderSettings();
  assert(!document.querySelector('[data-spend-day]') && !document.querySelector('[data-spend-providers]'), 'an answer to another request was drawn as this page\'s spend');
  assert(asked().length === 0, 'an answer to another request, while this page\'s is awaited, asked again');

  S.spendRequest = 'req-1';
  renderSettings();
  assert(asked().length === 0, 'a section that holds the spend does not ask again on every draw');
  assert(document.querySelector('[data-spend-day="2026-11-01"]'), 'the day of this month is drawn');
  assert(!document.querySelector('[data-spend-day="2026-10-31"]'), 'the last day of the month before was drawn under this month');
  assert(!document.querySelector('[data-spend-provider="L"]'), 'a provider spent on only last month was summed into this month');
  const sums = [...document.querySelectorAll('[data-spend-provider]')].map(li => li.textContent);
  assert(sums.length === 2, 'each provider of the span is one line: ' + JSON.stringify(sums));
  assert(sums[0] === 'At least ' + (5000).toLocaleString() + ' tokens by sub-agents. The record names no provider for them.', 'most tokens first; a floor says at least; a child names no provider: ' + sums[0]);
  assert(sums[1] === (1570).toLocaleString() + ' tokens with P. AII OS knows no price for them.', 'a provider\'s lines are summed in the no-price words: ' + sums[1]);
  const unknown = document.querySelector('[data-spend-unknown]');
  assert(unknown && unknown.textContent.includes('1 record of unknown spend'), 'the summary says what it could not count');

  const day = document.querySelector('[data-spend-day="2026-11-01"]');
  const byDay = document.querySelector('[data-spend-byday]');
  assert(byDay && byDay.contains(day) && !byDay.open, 'the days are under By day, closed at first');
  const total = day.querySelector('[data-spend-total]').textContent;
  assert(total === 'at least ' + (6570).toLocaleString(), 'a floor of a day says at least: ' + total);
  assert(day.textContent.includes('1 record of unknown spend'), 'the day says what it could not count: ' + day.textContent);
  const lines = [...day.querySelectorAll('[data-spend-line]')].map(li => li.textContent);
  assert(lines.length === 3, 'each role and model of the day is a line: ' + JSON.stringify(lines));
  assert(lines[0].startsWith('sub-agent bench — bench-1: at least ' + (5000).toLocaleString()), 'a child is named by its role, its total a floor: ' + lines[0]);
  assert(lines[1].startsWith('turns — P · m: ' + (1500).toLocaleString() + ' (4 calls; in ' + (1350).toLocaleString() + ', out 150, 600 read from the cache; 1 record of unknown spend)'), 'the turns say their calls, split and unknown records: ' + lines[1]);
  assert(lines[2].startsWith('facility dream — P · m: 70 (1 call)'), 'a facility is named: ' + lines[2]);
  assert(document.querySelector('[data-spend]').textContent.includes('Days are counted in Pacific/Auckland.'), 'the section names the timezone');

  byDay.open = true; byDay.dispatchEvent(new Event('toggle'));
  renderSettings();
  assert(document.querySelector('[data-spend-byday]').open, 'a redraw closed By day');

  const choose = v => { const pick = document.getElementById('spend-days'); pick.value = v; pick.dispatchEvent(new Event('change')); };
  choose('7');
  q = asked();
  assert(q.length === 1 && q[0].days === 7, 'another choice asks for that many days: ' + JSON.stringify(q));
  assert(head() === 'Spend in the last 7 days', 'the heading follows the choice: ' + head());
  S.spend = { zone: 'UTC', today: '2026-11-01', days: month.days }; S.spendRequest = 'req-1';
  renderSettings();
  assert(document.querySelector('[data-spend-day="2026-10-31"]'), 'a span of days keeps every day the server sent');
  assert(head() === 'Spend in the last 7 days', 'the heading stays with the span drawn: ' + head());

  choose('month');
  q = asked();
  assert(q.length === 1 && q[0].days === 31, 'this month again asks for 31 days: ' + JSON.stringify(q));
  S.spend = { zone: 'UTC', days: month.days }; S.spendRequest = 'req-1';
  renderSettings();
  assert(!document.querySelector('[data-spend-day]'), 'an answer without today drew another month\'s days under this month');
  assert(document.querySelector('[data-spend]').textContent.includes('Nothing was spent on these days.'), 'an answer without today says nothing was spent');
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// SPEND KEEPS ORIGIN AND UNCERTAINTY. Only a child's turns are "by sub-agents". A plain turn or a
// facility's that names no provider is nobody's child: an older store's is of unknown spend, and
// the host has counted it so. It is said in the summary, and no provider line is drawn for it.
func TestAPlainTurnWithNoProviderIsNotASubAgents(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
const config = { llm: { provider: '', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none' },
  dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, cognition: {}, restart_required: [] };
const draw = (today, lines) => {
  S.config = config; S.providers = []; S.providersLoaded = true; S.view = 'settings';
  S.spend = { zone: 'UTC', today, days: [{ day: today, tokens: lines.reduce((n, l) => n + (l.tokens || 0), 0), unknown: lines.reduce((n, l) => n + (l.unknown || 0), 0), lines }] };
  S.spendRequest = null;
  renderSettings();
  S.spendRequest = 'req-1';
  renderSettings();
  return { sums: [...document.querySelectorAll('[data-spend-provider]')].map(li => li.textContent), unknown: document.querySelector('[data-spend-unknown]')?.textContent || '' };
};
run(() => {
  const today = new Date().toISOString().slice(0, 10);
  // An older store's plain turn: no provider, no child, its spend unknown.
  let got = draw(today, [{ role: 'turns', calls: 2, tokens: 0, unknown: 1 }]);
  assert(got.sums.length === 0, 'a plain turn is drawn as a provider line: ' + JSON.stringify(got.sums));
  assert(got.unknown === '1 record of unknown spend', 'the summary says: ' + got.unknown);
  // One the host counted as unknown though it knows its tokens, and a facility's the same way.
  got = draw(today, [{ role: 'turns', calls: 1, tokens: 400, unknown: 1 }, { role: 'facility:dream', calls: 1, tokens: 90, unknown: 1 }]);
  assert(got.sums.length === 0, 'turns that name no provider are drawn as a provider line: ' + JSON.stringify(got.sums));
  assert(got.unknown === '2 records of unknown spend', 'the summary says: ' + got.unknown);
  // A child's are said as a child's, whether its role has a name or not, beside a provider's.
  got = draw(today, [{ role: 'sub-agent', tokens: 300, floor: true }, { role: 'sub-agent:bench', tokens: 200, floor: true }, { role: 'turns', provider: 'P', model: 'm', calls: 1, tokens: 70 }]);
  assert(got.sums.length === 2 && got.sums[0] === 'At least 500 tokens by sub-agents. The record names no provider for them.' && got.sums[1].startsWith('70 tokens with P.'), 'the lines read: ' + JSON.stringify(got.sums));
  assert(got.unknown === '', 'a child is not of unknown spend: ' + got.unknown);
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}
