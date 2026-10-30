//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const pluginHeldPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
import { frames } from './ws.js';

run(() => {
  const words = 'Runs as a native program; signed for fs.private, net.outbound:api.example.org:443. Not started yet: press Start when you have read this.';
  const config = () => ({ plugins: { autoload: 'T1', catalog_url: 'https://plugins.example.test/aiios-plugins.md',
    catalog: [
      { id: 'org.example.engine', title: 'Example Engine', version: '1.0.0', tier: 'T3', available: true, installed: false, build: 'system', pending: 'unstarted', pending_text: words },
      { id: 'org.example.module', version: '1.0.0', tier: 'T1', available: true, installed: false, build: 'portable' },
    ],
    pending: [{ id: 'org.example.engine', version: '1.0.0', phase: 'unstarted', summary: words }],
    installed: [] } });
  S.config = config();
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  const row = () => st.querySelector('[data-entry="org.example.engine"]');
  const card = () => st.querySelector('[data-pending="org.example.engine"]');
  const starts = () => Array.from(st.querySelectorAll('[data-plugin-start="org.example.engine"]'));

  // 1. The row and the card say what the signed package says, and offer
  //    Start and Uninstall. Nothing reads as a failure or as waiting on
  //    its own; a module's row is as it was.
  assert(row() !== null && card() !== null, 'the held plugin has no row or no card');
  assert(row().textContent.includes(words) && card().textContent.includes(words), 'the sentence is missing: ' + row().textContent + ' | ' + card().textContent);
  assert(row().textContent.includes('not started') && card().textContent.includes('not started'), 'not named as not started');
  assert(starts().length === 2 && starts().every(b => b.textContent === 'Start' && !b.disabled), 'Start is not offered on the row and the card: ' + starts().map(b => b.textContent));
  assert(row().querySelector('[data-plugin="uninstall:org.example.engine"]') !== null && card().querySelector('[data-plugin="uninstall:org.example.engine"]') !== null, 'Uninstall is not offered');
  assert(row().querySelector('[data-plugin^="install:"]') === null && st.querySelector('[data-plugin="retry:org.example.engine"]') === null, 'a held plugin is offered Install or Try again');
  assert(!card().textContent.includes('Nothing to click') && !card().textContent.includes('refused') && !card().textContent.includes('downloading'), 'the card reads as a failure or as waiting on its own: ' + card().textContent);
  assert(st.querySelector('[data-plugin="install:org.example.module"]').textContent.trim() === 'Install' && st.querySelector('[data-plugin-start="org.example.module"]') === null, 'a module is no longer simply installed');

  // 2. One press sends one start; while its answer is owed a second press
  //    sends nothing.
  starts()[1].click();
  assert(frames.length === 1 && frames[0].type === 'plugin' && frames[0].plugin.action === 'start' && frames[0].plugin.id === 'org.example.engine', 'Start did not send the start action: ' + JSON.stringify(frames));
  assert(starts().length === 2 && starts().every(b => b.disabled && b.textContent === 'Starting…'), 'the press is not shown as in hand');
  starts()[0].click();
  assert(frames.length === 1, 'a second press sent a second start');

  // 3. A refusal is said where the press was made, and only its own answer
  //    is claimed. The plugin is still not started, and Start is offered.
  assert(S.pluginStartRefused('another-request', 'not this one') === false, 'an answer that was not its own was claimed');
  assert(S.pluginStartRefused(frames[0].request_id, 'plugins: org.example.engine is not started: the mark that holds it could not be removed') === true, 'its own refusal was not claimed');
  assert(row().querySelector('[data-start-said]') !== null && card().querySelector('[data-start-said]').textContent.includes('could not be removed'), 'the refusal is not said on the row and the card');
  assert(starts().every(b => !b.disabled && b.textContent === 'Start'), 'Start is not offered again after a refusal');

  // 4. Pressed again and taken: the next configuration no longer lists it
  //    as held, and nothing of the earlier refusal stays.
  starts()[0].click();
  assert(frames.length === 2 && row().querySelector('[data-start-said]') === null, 'a new press did not clear what the last one was told');
  S.pluginStartAnswered(frames[1].request_id);
  S.config = config();
  S.config.plugins.pending = [{ id: 'org.example.engine', version: '1.0.0', phase: 'starting', summary: 'verifying its files and starting it' }];
  S.config.plugins.catalog[0].pending = 'starting';
  S.config.plugins.catalog[0].pending_text = 'verifying its files and starting it';
  renderPlugins();
  assert(st.querySelector('[data-plugin-start]') === null && st.querySelector('[data-start-said]') === null && st.querySelector('[data-unstarted]') === null, 'a plugin that is starting is still drawn as held');
});
</script>`

func TestAHeldNativePluginIsStartedFromItsRowOrItsCard(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	page := strings.Replace(pluginHeldPage, "__LAYOUT_CSS__", string(read("layout.css")), 1)
	if page == pluginHeldPage {
		t.Fatal("stylesheet placeholder not substituted")
	}
	runPageInEngines(t, page, map[string][]byte{
		"/views/plugins.js": read("views/plugins.js"),
		"/state.js":         read("state.js"),
		"/util.js":          read("util.js"),
		"/views/settings.js": []byte(`export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection() {}
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; }
export function sendConfigChanges() {}
export function configFeedbackHTML() { return ''; }`),
		// The recording socket: what the page sends is the assertion, and each
		// send has its own request id, as the real one does.
		"/ws.js": []byte(`export const frames = []; export function send(f) { f.request_id = 'req-' + (frames.length + 1); frames.push(f); return f.request_id; }`),
	})
}
