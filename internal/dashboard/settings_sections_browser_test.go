//go:build !windows

package dashboard

import "testing"

// EVERY SETTINGS PAGE DRAWS ITS GROUP WHOLE. The nav lists each section
// once (SECTIONS in views/settings.js); opening one draws the page it
// belongs to: every section of its group, each in its own block, and no
// other page's. Model holds Substrate and Providers; every other page holds
// one section. No page draws an id twice.
const settingsSectionsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true; S.providersLoaded = true; S.providers = [];
  S.tools = []; S.sandbox = null; S.update = null; S.backups = null;
  S.config = {
    llm: { provider: 'OpenAI', model: 'gpt', resolved_provider: 'OpenAI', resolved_model: 'gpt', timeout_seconds: 120 },
    speech: { stt: {}, tts: {}, services: [] },
    dashboard: { host: '127.0.0.1', port: 8181, tls: false },
    witness: { url: '', interval_events: 0 }, prompt: { max_tokens: 0, recent_turns: 0 },
    agency: {}, logs: { dir: '', max_backups: 0, compress_days: 0 }, identity: {}, public_name: {},
    contacts: [], channels: [],
  };
  const marks = {
    substrate: '#cfg-provider', providers: '#pv-open-new', speech: '#speech-form', dashboard: '#cfg-dtls',
    backups: 'text:loading backups', sandbox: 'text:loading sandbox', tools: 'text:Tools · ',
    messages: '#contact-channels', updates: '[data-app]',
  };
  // The page each section is drawn on, named by its first section.
  const pageOf = id => id === 'providers' ? 'substrate' : id;
  const st = () => document.getElementById('settings-stack');
  const holds = (root, mark) => mark.startsWith('text:') ? root.textContent.includes(mark.slice(5)) : !!root.querySelector(mark);
  renderSettings();
  // Each page is reached by its address; no page draws a row of section
  // buttons (2b).
  const ids = Object.keys(marks);
  for (const id of ids) {
    S.settingsSection(id); S.requestSettingsSection(); renderSettings();
    assert(!st().querySelector('[data-sec]'), id + ': the page draws a row of section buttons');
    assert(st().dataset.section === pageOf(id), id + ' opened the page ' + st().dataset.section + ', not ' + pageOf(id));
    for (const other of ids) {
      const block = st().querySelector('[data-section="' + other + '"]');
      if (pageOf(other) === pageOf(id)) {
        assert(block, id + ': ' + other + ' has no block of its own on this page');
        assert(holds(block, marks[other]), id + ': ' + other + '\'s block does not draw its body (' + marks[other] + ')');
      } else {
        assert(!block, id + ': ' + other + ' has a block on a page not its own');
        assert(!holds(st(), marks[other]), id + ' draws ' + other + '\'s body (' + marks[other] + ')');
      }
    }
    const seen = new Set();
    for (const el of st().querySelectorAll('[id]')) {
      assert(!seen.has(el.id), id + ': the page draws the id ' + el.id + ' twice');
      seen.add(el.id);
    }
  }
});
</script>`

func TestEverySettingsSectionDrawsItsOwnBody(t *testing.T) {
	runPageInEngines(t, settingsSectionsPage, substrateModules(t))
}

// ARRIVING AT MODEL ASKS ITS SPEND AFRESH. The spend is Model's; the
// Substrate button used to ask it again. With the group drawn whole, each
// way of arriving at Model's page from another asks it again (a button,
// an address, a link elsewhere on the page), and moving within the page
// does not.
const settingsSpendAfreshPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true; S.providersLoaded = true; S.providers = []; S.view = 'settings';
  S.config = {
    llm: { provider: 'OpenAI', model: 'gpt', resolved_provider: 'OpenAI', resolved_model: 'gpt', timeout_seconds: 120 },
    speech: { stt: {}, tts: {}, services: [] }, prompt: { max_tokens: 0, recent_turns: 0 },
  };
  const st = document.getElementById('settings-stack');
  const asked = () => frames.filter(f => f.query === 'spend').length;
  // A day of Today's month: "this month" is the span drawn first (host 1757).
  const stale = () => ({ today: '2026-11-04', days: [{ day: '2026-11-03', tokens: 5, lines: [] }] });
  const showsStale = () => st.textContent.includes('2026-11-03');
  const nav = id => { S.settingsSection(id); S.requestSettingsSection(); renderSettings(); };
  renderSettings();
  S.spend = stale(); renderSettings();
  assert(showsStale(), 'the fixture spend was not drawn, so nothing below is measured');

  // Leave Model by another group's address (Speech is Voice's).
  nav('speech'); S.spend = stale();
  let before = asked();
  nav('providers');
  assert(!showsStale() && asked() === before + 1, 'the Providers button kept a stale spend on arriving at Model (asked ' + (asked() - before) + ')');

  S.spend = stale(); renderSettings(); before = asked();
  nav('substrate');
  assert(showsStale() && asked() === before, 'moving within Model asked the spend again');
  assert(S.settingsSection('model/providers') === true, 'model/providers did not move');
  renderSettings();
  assert(showsStale() && asked() === before, 'moving to model/providers asked the spend again');

  assert(S.settingsSection('voice') === true, 'voice did not move'); renderSettings();
  S.spend = stale(); before = asked();
  assert(S.settingsSection('model') === true, 'model did not move'); renderSettings();
  assert(!showsStale() && asked() === before + 1, 'the address kept a stale spend on arriving at Model (asked ' + (asked() - before) + ')');

  S.settingsSection('voice'); renderSettings();
  S.spend = stale(); before = asked();
  S.openSettings('substrate');
  assert(!showsStale() && asked() === before + 1, 'a link elsewhere kept a stale spend on arriving at Model (asked ' + (asked() - before) + ')');
});
</script>`

func TestArrivingAtModelAsksItsSpendAfresh(t *testing.T) {
	runPageInEngines(t, settingsSpendAfreshPage, substrateModules(t))
}
