//go:build !windows

package dashboard

import "testing"

const speechEngineSettingSavePage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const id = 'id.example.voice';
  S.identityExists = true; S.providersLoaded = true;
  S.providers = [];
  S.config = {
    llm: {},
    plugins: { installed: [{ id, settings: [] }] },
    speech: {
      stt: { provider: id, plugin: true, default: true },
      tts: { provider: id, plugin: true, default: true },
      services: [
        { name: id, title: 'Example Voice', plugin: true, added: true, speech: { stt: {}, tts: {} },
          settings: [
            { key: 'punctuate', type: 'boolean', title: 'Punctuate', scope: 'hearing', value: false },
            { key: 'beam', type: 'integer', title: 'Beam width', scope: 'hearing', value: 4 },
            { key: 'lexicon', type: 'string', title: 'Lexicon', scope: 'hearing', value: 'base' },
            { key: 'old_rate', undeclared: true, scope: 'hearing', value: 9, description: 'no longer read' },
            { key: 'pitch', type: 'number', title: 'Pitch', scope: 'speaking', value: 1 },
          ] },
      ],
    },
  };
  renderSettings();
  document.querySelector('[data-sec="speech"]').click();
  const field = key => document.querySelector('#speech-form [data-pset-key="' + key + '"]');
  const edit = (key, f) => { const el = field(key); f(el); el.dispatchEvent(new Event('change', { bubbles: true })); };
  assert(field('punctuate') && field('beam') && field('lexicon') && field('old_rate') && field('pitch'), 'the Speech card did not draw every tunable');
  edit('punctuate', el => { el.checked = true; });
  edit('beam', el => { el.value = '6.5'; });
  edit('lexicon', el => { el.value = ''; });
  edit('pitch', el => { el.value = ''; });
  document.querySelector('[data-save="speech"]').click();
  const set = frames.filter(f => f.type === 'config_set').at(-1);
  assert(set, 'the Speech Save sent no configuration');
  const c = set.config, k = key => 'plugins.settings.' + id + '.' + key;
  assert(c[k('punctuate')] === true, 'a ticked box is not sent as true: ' + JSON.stringify(c));
  assert(c[k('beam')] === 6.5, 'a number is not sent as the number typed: ' + JSON.stringify(c));
  assert(k('lexicon') in c && c[k('lexicon')] === null, 'an emptied field is not sent as a clear: ' + JSON.stringify(c));
  assert(k('pitch') in c && c[k('pitch')] === null, 'an emptied number is not sent as a clear: ' + JSON.stringify(c));
  assert(!(k('old_rate') in c), 'an untouched forget box discarded the kept value: ' + JSON.stringify(c));
  assert(acceptSettingsConfig(set.request_id), 'the Save reply was not claimed');
  renderSettings();

  edit('old_rate', el => { el.checked = true; });
  edit('punctuate', el => { el.checked = false; });
  document.querySelector('[data-save="speech"]').click();
  const second = frames.filter(f => f.type === 'config_set').at(-1);
  assert(second !== set, 'the second Save sent no configuration');
  const again = second.config;
  assert(k('old_rate') in again && again[k('old_rate')] === null, 'a ticked forget box is not sent as a clear: ' + JSON.stringify(again));
  assert(again[k('punctuate')] === false, 'an unticked box is not sent as false: ' + JSON.stringify(again));
});
</script>`

func TestTheSpeechCardSavesAnEngineSettingAsItsPluginCardDoes(t *testing.T) {
	runPageInEngines(t, speechEngineSettingSavePage, substrateModules(t))
}
