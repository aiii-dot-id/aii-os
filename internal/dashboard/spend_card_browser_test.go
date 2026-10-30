//go:build !windows

package dashboard

import "testing"

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
  let q = asked();
  assert(q.length === 1 && q[0].days === 7, 'the card asks for the last seven days: ' + JSON.stringify(q));
  assert(document.querySelector('[data-spend]'), 'the substrate section draws the spend card');
  renderSettings();
  assert(asked().length === 0, 'a redraw while the spend is asked for does not ask again');

  S.spend = { zone: 'Pacific/Auckland', days: [
    { day: '2026-10-22', tokens: 6570, floor: true, unknown: 1, lines: [
      { role: 'sub-agent:bench', model: 'bench-1', calls: 0, tokens: 5000, floor: true },
      { role: 'turns', provider: 'P', model: 'm', calls: 4, tokens: 1500, input: 1350, output: 150, cached: 600, unknown: 1 },
      { role: 'facility:dream', provider: 'P', model: 'm', calls: 1, tokens: 70 } ] },
    { day: '2026-10-21', tokens: 400, lines: [ { role: 'turns', provider: 'L', model: 'local', calls: 3, tokens: 400 } ] } ] };
  renderSettings();
  assert(asked().length === 0, 'a card that holds the spend does not ask again on every draw');
  const day = document.querySelector('[data-spend-day="2026-10-22"]');
  assert(day, 'the newest day is drawn');
  const total = day.querySelector('[data-spend-total]').textContent;
  assert(total === 'at least ' + (6570).toLocaleString(), 'a floor of a day says at least: ' + total);
  assert(day.textContent.includes('1 record of unknown spend'), 'the day says what it could not count: ' + day.textContent);
  const lines = [...day.querySelectorAll('[data-spend-line]')].map(li => li.textContent);
  assert(lines.length === 3, 'each role and model of the day is a line: ' + JSON.stringify(lines));
  assert(lines[0].startsWith('sub-agent bench — bench-1: at least ' + (5000).toLocaleString()), 'a child is named by its role, its total a floor: ' + lines[0]);
  assert(lines[1].startsWith('turns — P · m: ' + (1500).toLocaleString() + ' (4 calls; in ' + (1350).toLocaleString() + ', out 150, 600 read from the cache; 1 record of unknown spend)'), 'the turns say their calls, split and unknown records: ' + lines[1]);
  assert(lines[2].startsWith('facility dream — P · m: 70 (1 call)'), 'a facility is named: ' + lines[2]);
  const older = document.querySelector('[data-spend-day="2026-10-21"] [data-spend-total]').textContent;
  assert(older === (400).toLocaleString(), 'an exact day says its total alone: ' + older);
  assert(document.querySelector('[data-spend]').textContent.includes('Days are counted in Pacific/Auckland.'), 'the card names the timezone');

  const pick = document.getElementById('spend-days');
  pick.value = '30';
  pick.dispatchEvent(new Event('change'));
  q = asked();
  assert(q.length === 1 && q[0].days === 30, 'another choice asks for that many days: ' + JSON.stringify(q));
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}
