//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// settingsFieldModules are the modules a Settings card is drawn with in a
// browser, the rest stubbed as the cognition card's test stubs them.
func settingsFieldModules(t *testing.T) map[string][]byte {
	t.Helper()
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? ''); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	return modules
}

// THE LONGEST WAIT BEFORE A REFUSED MODEL IS TRIED AGAIN IS UNDER MODEL'S
// MORE OPTIONS. Its hint states the default as the host sends it (the
// page holds no number of its own: here one no code holds), a blank field
// is the default, and a Save of the card sends the setting only where the
// field was changed, so the default stays the program's and is not
// written into config.json by a save of the model.
func TestTheLongestWaitFieldShowsTheHostsDefaultAndSendsOnlyAChange(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = { llm: { provider: '', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none', timeout_seconds: 0, probe_timeout_seconds: 45,
      refused_retry_max_seconds: 0, refused_retry_max_default: 4321 },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, cognition: {}, restart_required: [] };
  S.providers = []; S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  const field = () => document.getElementById('cfg-refused-retry');
  assert(field(), 'More options draws the longest wait');
  const label = field().previousElementSibling;
  assert(label && label.textContent === 'Try a refused model again after (seconds)', 'the label names the wait: ' + (label && label.textContent));
  const hint = field().nextElementSibling;
  assert(hint && hint.textContent === 'A refused model is tried again by itself, each wait twice the last, up to this long. Blank: 4321 seconds.', 'the hint states the default the host sent: ' + (hint && hint.textContent));
  assert(field().value === '', 'no wait named is a blank field: ' + field().value);

  const save = () => { document.querySelector('[data-save="llm"]').click(); const sent = frames.filter(f => f.type === 'config_set'); frames.length = 0; return sent; };
  let sent = save();
  assert(sent.length === 1 && !('llm.refused_retry_max_seconds' in sent[0].config), 'a save with the field untouched does not send it: ' + JSON.stringify(sent[0] && sent[0].config));
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings(); // the Save is drawn again, no longer waiting

  field().value = '600';
  sent = save();
  assert(sent.length === 1 && sent[0].config['llm.refused_retry_max_seconds'] === 600, 'a wait typed is sent as a number: ' + JSON.stringify(sent[0] && sent[0].config));
  S.config.llm.refused_retry_max_seconds = 600;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings(); // the Save is drawn again, no longer waiting
  assert(field().value === '600', 'the wait the file states is shown: ' + field().value);

  sent = save();
  assert(sent.length === 1 && !('llm.refused_retry_max_seconds' in sent[0].config), 'a save with the stated wait untouched does not send it again: ' + JSON.stringify(sent[0] && sent[0].config));
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings(); // the Save is drawn again, no longer waiting

  field().value = '';
  sent = save();
  assert(sent.length === 1 && sent[0].config['llm.refused_retry_max_seconds'] === null, 'a cleared field sends null, the default again: ' + JSON.stringify(sent[0] && sent[0].config));
  // The host keeps a cleared wait as 0, and the page reads that as what it asked for.
  S.config.llm.refused_retry_max_seconds = 0;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  const said = document.querySelector('[data-said="llm"]');
  assert(said && said.classList.contains('good') && said.textContent === 'Saved.', 'clearing the wait reads as: ' + (said && said.textContent));
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// THE SIZE AT WHICH THE LIVE LOG ROLLS IS ON THE LOGS CARD, beside the
// backup count and the compression age. Its label states the default as
// the host sends it (the card holds no number of its own: here one no
// code holds), a blank field is the default, and a Save of the card sends
// the setting only where the field was changed.
func TestTheRollSizeFieldShowsTheHostsDefaultAndSendsOnlyAChange(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div><div id="technical-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { acceptSettingsConfig } from './views/settings.js';
import { drawLogsForm } from './views/settings/technical/forms.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = { llm: { provider: '', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none' },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, plugins: {}, agency: {}, cognition: {}, restart_required: [],
    logs: { dir: 'log', max_backups: 9, compress_days: 7, max_bytes: 0, max_bytes_default: 3145729 } };
  S.providers = []; S.providersLoaded = true; S.view = 'technical'; S.logsList = [];
  const st = document.getElementById('technical-stack');
  const renderSettings = S.renderTechnical = () => drawLogsForm(st);
  renderSettings();
  const field = () => document.getElementById('cfg-lbytes');
  assert(field(), 'the Logs card draws the roll size');
  assert(field().placeholder === 'Usually 3', 'the field shows the default the host sent: ' + field().placeholder);
  assert(field().value === '', 'no size named is a blank field: ' + field().value);

  const save = () => { document.querySelector('[data-save="technical"]').click(); const sent = frames.filter(f => f.type === 'config_set'); frames.length = 0; return sent; };
  const other = name => { document.getElementById('cfg-ldir').value = name; S.config.logs.dir = name; };
  other('elsewhere');
  let sent = save();
  assert(sent.length === 1 && !('logs.max_bytes' in sent[0].config), 'a save with the field untouched does not send it: ' + JSON.stringify(sent[0] && sent[0].config));
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings(); // the Save is drawn again, no longer waiting

  field().value = '2';
  sent = save();
  assert(sent.length === 1 && sent[0].config['logs.max_bytes'] === 2097152, 'a size typed is sent as a number: ' + JSON.stringify(sent[0] && sent[0].config));
  S.config.logs.max_bytes = 2097152;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings(); // the Save is drawn again, no longer waiting
  assert(field().value === '2', 'the size the file states is shown: ' + field().value);

  other('again');
  sent = save();
  assert(sent.length === 1 && !('logs.max_bytes' in sent[0].config), 'a save with the stated size untouched does not send it again: ' + JSON.stringify(sent[0] && sent[0].config));
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings(); // the Save is drawn again, no longer waiting

  field().value = '';
  sent = save();
  assert(sent.length === 1 && sent[0].config['logs.max_bytes'] === null, 'a cleared field sends null, the default again: ' + JSON.stringify(sent[0] && sent[0].config));
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// THE FOLDER'S BOUND (logs.max_total_bytes) sits on the Logs
// card under the roll size. Its label states the default the host sends
// for the roll size and count in force, a blank field is the default, and
// a Save of the card sends it only where the field was changed. With every
// log kept the host sends no default and the label says there is no bound.
func TestTheFoldersBoundFieldShowsTheHostsDefaultAndSendsOnlyAChange(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div><div id="technical-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { acceptSettingsConfig } from './views/settings.js';
import { drawLogsForm } from './views/settings/technical/forms.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = { llm: { provider: '', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none' },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, plugins: {}, agency: {}, cognition: {}, restart_required: [],
    logs: { dir: 'log', max_backups: 9, compress_days: 7, max_bytes: 0, max_bytes_default: 3145729, max_total_bytes: 0, max_total_bytes_default: 31457290 } };
  S.providers = []; S.providersLoaded = true; S.view = 'technical'; S.logsList = [];
  const st = document.getElementById('technical-stack');
  const renderSettings = S.renderTechnical = () => drawLogsForm(st);
  renderSettings();
  const field = () => document.getElementById('cfg-ltotal');
  assert(field(), 'the Logs card draws the folder bound');
  assert(field().placeholder === 'Usually 30', 'the field shows the default the host sent: ' + field().placeholder);
  assert(field().value === '', 'no bound named is a blank field: ' + field().value);

  const save = () => { document.querySelector('[data-save="technical"]').click(); const sent = frames.filter(f => f.type === 'config_set'); frames.length = 0; return sent; };
  document.getElementById('cfg-ldir').value = 'elsewhere'; S.config.logs.dir = 'elsewhere';
  let sent = save();
  assert(sent.length === 1 && !('logs.max_total_bytes' in sent[0].config), 'a save with the field untouched does not send it: ' + JSON.stringify(sent[0] && sent[0].config));
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();

  field().value = '20';
  sent = save();
  assert(sent.length === 1 && sent[0].config['logs.max_total_bytes'] === 20971520, 'a bound typed is sent as a number: ' + JSON.stringify(sent[0] && sent[0].config));
  S.config.logs.max_total_bytes = 20971520;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  assert(field().value === '20', 'the bound the file states is shown: ' + field().value);

  field().value = '';
  sent = save();
  assert(sent.length === 1 && sent[0].config['logs.max_total_bytes'] === null, 'a cleared field sends null, the default again: ' + JSON.stringify(sent[0] && sent[0].config));
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');

  S.config.logs.max_total_bytes = 0; S.config.logs.max_backups = -1; S.config.logs.max_total_bytes_default = 0;
  renderSettings();
  assert(field().placeholder === '', 'with every log kept no usual bound is shown: ' + field().placeholder);
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}
