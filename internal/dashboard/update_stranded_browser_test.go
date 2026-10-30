//go:build !windows

package dashboard

import "testing"

func TestTheUpdatesCardNamesThePluginsAnOfferedVersionWouldStrand(t *testing.T) {
	page := `<!doctype html><html><body><div id="root"></div>
<script type="module">
import { run } from '/__harness.js';
import { updateCardHTML } from '/views/settings.js';
run(async (assert) => {
  const card = (u) => { const d = document.createElement('div'); d.innerHTML = updateCardHTML(u); return d; };
  const two = card({enabled:true, current_version:'0.2.4', available_version:'0.3.0', stranded:[
    {id:'org.example.gone', version:'1.4.0', reason:'the AII OS 0.3 catalog does not carry it'},
    {id:'org.example.<b>', version:'1.0.0', reason:"the AII OS 0.3 catalog's 2.0.0 needs AII OS 0.3.1 or newer"}]});
  const block = two.querySelector('[data-update-stranded]');
  assert(block !== null && block.textContent.startsWith('Updating to 0.3.0 leaves 2 installed plugins without a version for it:'), 'the offer names how many it strands: ' + (block && block.textContent));
  const rows = [...block.querySelectorAll('div')].map(d => d.textContent);
  assert(rows.length === 2 && rows[0] === 'org.example.gone 1.4.0: the AII OS 0.3 catalog does not carry it' && rows[1] === "org.example.<b> 1.0.0: the AII OS 0.3 catalog's 2.0.0 needs AII OS 0.3.1 or newer", 'each is named with its version and why: ' + JSON.stringify(rows));
  assert(block.querySelector('b') === null, 'a plugin id is shown as written, never as markup');
  const one = card({enabled:true, available_version:'0.3.0', stranded:[{id:'a', version:'1', reason:'r'}]});
  assert(one.querySelector('[data-update-stranded]').textContent.startsWith('Updating to 0.3.0 leaves 1 installed plugin without a version for it:'), 'one plugin is one plugin');
  const note = card({enabled:true, available_version:'0.3.0', stranded_note:'The AII OS 0.3 catalog is not published yet: updating to 0.3.0 would leave every installed plugin (3) without a version listed for it.'});
  assert(note.querySelector('[data-update-stranded-note]').textContent.startsWith('The AII OS 0.3 catalog is not published yet') && note.querySelector('[data-update-stranded]') === null, "what cannot be listed is said in the host's words");
  const stale = card({enabled:true, checked_at:'2026-10-22T10:00:00Z', stranded:[{id:'a', version:'1', reason:'r'}], stranded_note:'n'});
  assert(stale.querySelector('[data-update-stranded]') === null && stale.querySelector('[data-update-stranded-note]') === null, 'with nothing offered nothing is said');
  assert(card({enabled:true, available_version:'0.2.5', stranded:[]}).querySelector('[data-update-stranded]') === null, 'nothing stranded, nothing said');
});
</script></body></html>`
	modules := map[string][]byte{
		"/state.js":              []byte(`export const S = {};`),
		"/util.js":               utilStub(`export const $ = (id) => document.getElementById(id); export const esc = (s) => String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c])); export const copyText = async () => true;`),
		"/ws.js":                 []byte(`export const sent = []; export function send(m) { sent.push(m); } export function query() {}`),
		"/sandbox.js":            stubModule("sandboxCardHTML", "wireSandboxCard"),
		"/pending.js":            []byte(`export function pendingSlot() { return { arm(){ return true; }, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`),
		"/views/model-picker.js": stubModule("providerModels"),
	}
	real, err := staticFS.ReadFile("static/views/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/settings.js"] = real
	runPageInEngines(t, page, modules)
}
