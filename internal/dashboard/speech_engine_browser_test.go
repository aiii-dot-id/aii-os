//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
// .
// .
// .
const speechEnginePage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true; S.providersLoaded = true;
  S.providers = [{ name: 'Deepgram', chat: false, endpoint: 'https://api.deepgram.com', models: [], speech: { stt: {} } }];
  S.config = {
    llm: { provider: 'OpenAI', model: 'gpt', resolved_provider: 'OpenAI', resolved_model: 'gpt', timeout_seconds: 120 },
    speech: {
      stt: { provider: 'id.aiii.voice', plugin: true, default: true },
      tts: { provider: 'id.aiii.voice', plugin: true, default: true },
      services: [
        { name: 'id.aiii.voice', title: 'AII Voice', plugin: true, added: true,
          speech: { stt: {}, tts: {} },
          settings: [
            { key: 'stt_language', type: 'string', title: 'Spoken language', scope: 'hearing' },
            { key: 'turn_pause_ms', type: 'integer', title: 'Turn pause', scope: 'hearing' },
            { key: 'tts_voice', type: 'enum', title: 'Voice', values: ['ana', 'bo'], scope: 'speaking' },
            { key: 'log_sessions', type: 'boolean', title: 'Log sessions', scope: 'session' },
            { key: 'endpoint', type: 'string', title: 'Endpoint' },
          ] },
        { name: 'Deepgram', added: true, endpoint: 'https://api.deepgram.com', speech: { stt: {} } },
      ],
    },
  };
  renderSettings();
  document.querySelector('[data-sec="speech"]').click();

  // It is offered in both halves, read by its title and held by its id.
  ['stt', 'tts'].forEach(dir => {
    const sel = document.getElementById('sp-provider-' + dir);
    assert(sel, dir + ': no engine picker');
    const opt = [...sel.options].find(o => o.value === 'id.aiii.voice');
    assert(opt, dir + ': the installed engine is not offered: ' + [...sel.options].map(o => o.value).join(','));
    assert(opt.textContent === 'AII Voice', dir + ': the operator must read a name, not an id: ' + opt.textContent);
    assert(sel.value === 'id.aiii.voice', dir + ': it is not selected though it serves');
  });

  // Nothing to reach, nothing to pay, nothing to key.
  assert(document.getElementById('sp-key-row-stt').hidden, 'an engine on this machine asked for an API key');
  assert(!document.getElementById('sp-ceiling-stt'), 'an engine on this machine offered a monthly bill');
  assert(!document.getElementById('sp-model-stt'), 'it was asked for a model it never reads');
  assert(!document.getElementById('sp-voice-tts'), 'it was asked for a voice from the page, not from its own settings');

  // What it declared as hearing is beside Hearing; speaking beside
  // Speaking; and what it scoped as neither is on neither.
  const inBox = (dir, key) => !!document.querySelector('#sp-engine-settings-' + dir + ' [data-pset-key="' + key + '"]');
  assert(inBox('stt', 'stt_language') && inBox('stt', 'turn_pause_ms'), 'the hearing settings are not beside Hearing');
  assert(inBox('tts', 'tts_voice'), 'the speaking setting is not beside Speaking');
  assert(!inBox('stt', 'tts_voice') && !inBox('tts', 'stt_language'), 'a setting landed beside the wrong half');
  ['log_sessions', 'endpoint'].forEach(k => {
    assert(!inBox('stt', k) && !inBox('tts', k), k + ' is not about hearing or speaking and must stay on the plugin card');
  });

  // And it says what is in force, and why.
  const back = document.getElementById('sp-readback-stt').textContent;
  assert(back.includes('AII Voice') && back.includes('installed on this machine'), 'the readback does not say where it runs: ' + back);
  assert(back.includes('because it is installed'), 'the readback does not say it is the default: ' + back);

  // A service chosen beside it is still choosable.
  assert([...document.getElementById('sp-provider-stt').options].some(o => o.value === 'Deepgram'),
    'choosing a service instead of the engine is not offered');
});
</script>`

func TestSettingsSpeechOffersAndTunesAnInstalledEngine(t *testing.T) {
	runPageInEngines(t, speechEnginePage, substrateModules(t))
}

// .
// .
// .
// .
const pluginCardScopePage = `<div id="plugins-stack"></div>` + substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true;
  let opened = null;
  S.openSettings = (sec, focus) => { opened = sec + '/' + focus; };
  S.config = { llm: {}, dashboard: {}, prompt: {}, witness: {}, logs: {}, agency: {}, restart_required: [],
    plugins: { autoload: 'T1', installed: [{
      id: 'id.aiii.voice', title: 'AII Voice', version: '0.1.0-beta.3', tier: 'T3',
      mode: 'supervised', variant: 'linux-x86_64-native', tools: [], family: 'voice_interface',
      settings: [
        { key: 'stt_language', type: 'string', title: 'Spoken language', scope: 'hearing' },
        { key: 'tts_voice', type: 'enum', title: 'Voice', values: ['ana'], scope: 'speaking' },
        { key: 'log_sessions', type: 'boolean', title: 'Log sessions', scope: 'session' },
        { key: 'endpoint', type: 'string', title: 'Endpoint' },
      ],
    }] } };
  renderPlugins();
  const here = k => !!document.querySelector('[data-pset-key="' + k + '"]');
  assert(here('log_sessions') && here('endpoint'), 'the plugin card lost the settings that are about the plugin');
  assert(!here('stt_language') && !here('tts_voice'), 'the hearing and speaking settings are still duplicated here');
  const link = document.querySelector('[data-open-section="speech"]');
  assert(link, 'the card does not say where the speech settings went');
  const note = link.parentElement.textContent;
  assert(note.includes('Spoken language') && note.includes('Voice'), 'it does not name what moved: ' + note);
  link.click();
  assert(opened === 'speech/sp-provider-stt', 'the pointer does not open Speech on the engine: ' + opened);
});
</script>`

func TestThePluginCardKeepsWhatIsAboutThePlugin(t *testing.T) {
	runPageInEngines(t, pluginCardScopePage, substrateModules(t))
}
