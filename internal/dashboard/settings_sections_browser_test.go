//go:build !windows

package dashboard

import "testing"

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
    backups: 'text:loading backups', witness: '#cfg-wurl', prompt: '#cfg-ptokens', agency: '#cfg-plr', identity: '[data-cognition]',
    logs: '#cfg-ldir', sandbox: 'text:loading sandbox', tools: 'text:TOOLS — RING 5 REACH', storage: '#cfg-db-format',
    messages: '#contact-channels', 'held-mail': 'text:HELD MAIL', updates: 'text:this build carries no release version',
  };
  const st = () => document.getElementById('settings-stack');
  const has = mark => mark.startsWith('text:') ? st().textContent.includes(mark.slice(5)) : !!st().querySelector(mark);
  renderSettings();
  const ids = [...document.querySelectorAll('[data-sec]')].map(b => b.dataset.sec);
  assert(ids.join() === Object.keys(marks).join(), 'the sections offered: ' + ids.join());
  for (const id of ids) {
    document.querySelector('[data-sec="' + id + '"]').click();
    assert(st().dataset.section === id, id + ' did not open: ' + st().dataset.section);
    assert(has(marks[id]), id + ' does not draw its own body (' + marks[id] + ')');
    for (const other of ids) if (other !== id && marks[other] !== marks[id]) assert(!has(marks[other]), id + ' draws ' + other + '\'s body (' + marks[other] + ')');
  }
});
</script>`

func TestEverySettingsSectionDrawsItsOwnBody(t *testing.T) {
	runPageInEngines(t, settingsSectionsPage, substrateModules(t))
}
