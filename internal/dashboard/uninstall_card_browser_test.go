//go:build !windows

package dashboard

import "testing"

func TestTheUninstallItemIsOfferedOnlyWhereTheHostSaysAndSendsTheFixedVerb(t *testing.T) {
	page := `<!doctype html><html><body><div id="root"></div>
<script type="module">
import { run } from '/__harness.js';
import { sent } from '/ws.js';
import { uninstallCardHTML, wireUpdateCheck } from '/views/settings.js';
run(async (assert) => {
  assert(uninstallCardHTML(false) === '', 'not offered, there is no card');
  const root = document.getElementById('root');
  root.innerHTML = uninstallCardHTML(true);
  const btns = [...root.querySelectorAll('button')];
  assert(btns.length === 1 && btns[0].textContent.trim() === 'Uninstall AII OS…', 'offered, the one control is Uninstall AII OS…: ' + JSON.stringify(btns.map(b => b.textContent)));
  assert(root.textContent.includes('data stays'), 'the card says every identity\'s data stays');
  wireUpdateCheck(root);
  btns[0].click();
  assert(sent.length === 1 && sent[0].type === 'uninstall' && Object.keys(sent[0]).length === 1, 'the click sends the fixed verb with no argument');
  assert(btns[0].disabled === true, 'the button disarms once asked');
  assert(root.querySelector('[data-uninstall-note]').textContent.includes('on this Mac'), 'the note says the Mac asks');
});
</script></body></html>`
	modules := map[string][]byte{
		"/state.js":              []byte(`export const S = {};`),
		"/util.js":               utilStub(`export const $ = (id) => document.getElementById(id); export const esc = (s) => String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c])); export const copyText = async () => true;`),
		"/ws.js":                 []byte(`export const sent = []; export function send(m) { sent.push(m); return 'r' + sent.length; } export function query() {}`),
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
