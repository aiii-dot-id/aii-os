//go:build !windows

package dashboard

import "testing"

const speechEngineSettingControlsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
import { applySettingChoices } from './views/plugin-setting.js';
import { assert, run } from './__harness.js';
run(() => {
  const id = 'id.aiii.voice';
  const went = [];
  S.go = view => went.push(view);
  S.identityExists = true; S.providersLoaded = true;
  S.providers = [];
  S.config = {
    secrets_ok: true,
    llm: { provider: 'OpenAI', model: 'gpt', resolved_provider: 'OpenAI', resolved_model: 'gpt', timeout_seconds: 120 },
    plugins: { installed: [{ id, settings: [{ key: 'api_key', type: 'secret', key_kept: false }] }] },
    speech: {
      stt: { provider: id, plugin: true, default: true },
      tts: { provider: id, plugin: true, default: true },
      services: [
        { name: id, title: 'AII Voice', plugin: true, added: true, speech: { stt: {}, tts: {} },
          settings: [
            { key: 'model', type: 'string', title: 'Recognizer model', scope: 'hearing', choices_from: true, value: 'big-v1', effective: 'big-v1' },
            { key: 'api_key', type: 'secret', title: 'Vendor key', scope: 'hearing', key_host: 'api.voice.test:443' },
            { key: 'account', type: 'secret', title: 'Voice account', scope: 'speaking', oauth: { provider: 'google', services: ['speech'] },
              key_host: 'api.voice.test:443', account: { hosts: ['api.voice.test:443'], offers: [] } },
          ] },
      ],
    },
  };
  renderSettings();
  document.querySelector('[data-sec="speech"]').click();
  const asks = () => frames.filter(f => f.type === 'plugin_setting_choices');
  const box = dir => document.getElementById('sp-engine-settings-' + dir);
  const field = key => document.querySelector('#speech-form [data-pset-key="' + key + '"]');

  assert(field('model') && field('model').tagName === 'INPUT' && field('model').value === 'big-v1', 'while the plugin is asked, the field is the saved value');
  assert(box('stt').textContent.includes('looking up the choices'), 'the Speech card does not say it is asking: ' + box('stt').textContent);
  assert(asks().length === 1 && asks()[0].plugin_key.plugin === id && asks()[0].plugin_key.key === 'model', 'the Speech card did not ask the plugin once: ' + JSON.stringify(asks()));
  renderSettings();
  assert(asks().length === 1, 'a re-render asked again');

  applySettingChoices({ plugin: id, key: 'model', choices: [{ value: 'big-v1', label: 'Big, first' }, { value: 'big-v2' }] });
  renderSettings();
  const sel = field('model');
  assert(sel && sel.tagName === 'SELECT', 'the answer is not a drop-down on the Speech card');
  const opts = [...sel.options].map(o => o.value + '=' + o.textContent);
  assert(opts.includes('big-v1=Big, first') && opts.includes('big-v2=big-v2') && sel.value === 'big-v1', 'the plugin\'s choices under their names: ' + opts);
  document.querySelector('#speech-form [data-choices-refresh]').click();
  assert(asks().length === 2 && box('stt').textContent.includes('looking up the choices'), 'Refresh on the Speech card does not ask again');

  const paste = document.querySelector('#sp-engine-settings-stt [data-pkey-input][data-pkey-key="api_key"]');
  assert(paste && paste.type === 'password' && box('stt').textContent.includes('rides only to api.voice.test:443'), 'the Speech card offers no place to paste the key: ' + box('stt').textContent);
  paste.value = 'sk-voice-PASTED';
  paste.dispatchEvent(new Event('input'));
  renderSettings();
  const again = document.querySelector('#sp-engine-settings-stt [data-pkey-input][data-pkey-key="api_key"]');
  assert(again && again.value === 'sk-voice-PASTED', 'a re-render lost the pasted key');
  const before = frames.length;
  document.querySelector('[data-save="speech"]').click();
  const keyed = frames.slice(before).filter(f => f.type !== 'query');
  assert(keyed.length === 1 && keyed[0].type === 'plugin_key_set' && keyed[0].plugin_key.plugin === id && keyed[0].plugin_key.key === 'api_key' && keyed[0].plugin_key.secret === 'sk-voice-PASTED',
    'the Speech Save did not send the pasted key first: ' + JSON.stringify(keyed));
  S.config.plugins.installed[0].settings[0].key_kept = true;
  assert(acceptSettingsConfig(keyed[0].request_id), 'the key reply was not claimed');
  const set = frames.filter(f => f.type === 'config_set').at(-1);
  assert(set && set.config['speech.stt.provider'] === id && !JSON.stringify(set).includes('sk-voice-PASTED'), 'the settings did not follow the key, or carried it: ' + JSON.stringify(set));

  const connect = document.querySelector('#sp-engine-settings-tts [data-pacct-connect]');
  assert(connect, 'the Speech card offers no way to connect the account: ' + box('tts').textContent);
  connect.click();
  assert(went.join() === 'plugins' && S.connectRequest && S.connectRequest.connector === id && S.connectRequest.setting === 'account' && S.connectRequest.fromCard,
    'Connect did not land on Plugins with the request: ' + went + ' ' + JSON.stringify(S.connectRequest));
});
</script>`

func TestTheSpeechCardWiresAnEngineSettingAsItsPluginCardDoes(t *testing.T) {
	runPageInEngines(t, speechEngineSettingControlsPage, substrateModules(t))
}
