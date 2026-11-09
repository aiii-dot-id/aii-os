//go:build !windows

package dashboard

import "testing"

// THE ITEM IS OFFERED ONLY WHERE THE HOST SAYS SO (L1c, C6): no part at
// all unless the status offers it, and its one control sends the fixed
// verb and nothing else — the app's dialogs on the Mac do the rest. It is
// Settings → App's last part since step 18 (views/settings/app.js).
func TestTheUninstallItemIsOfferedOnlyWhereTheHostSaysAndSendsTheFixedVerb(t *testing.T) {
	page := `<!doctype html><html><body><div id="root"></div>
<script type="module">
import { run } from '/__harness.js';
import { sent } from '/ws.js';
import { removeHTML, wireApp } from '/views/settings/app.js';
run(async (assert) => {
  assert(removeHTML(false) === '', 'not offered, there is no part');
  const root = document.getElementById('root');
  root.innerHTML = removeHTML(true);
  const btns = [...root.querySelectorAll('button')];
  assert(btns.length === 1 && btns[0].textContent.trim() === 'Uninstall AII OS…', 'offered, the one control is Uninstall AII OS…: ' + JSON.stringify(btns.map(b => b.textContent)));
  assert(root.textContent.includes('Their record, keys and files stay where they are.'), 'the part says what stays: ' + root.textContent);
  wireApp(root);
  btns[0].click();
  assert(sent.length === 1 && sent[0].type === 'uninstall' && Object.keys(sent[0]).length === 1, 'the click sends the fixed verb with no argument');
  assert(btns[0].disabled === true, 'the button disarms once asked');
  assert(root.querySelector('[data-uninstall-note]').textContent === 'AII OS opens on this Mac and asks you there.', 'the note says the Mac asks');
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
	for _, path := range []string{"views/settings.js", "views/settings/app.js"} {
		real, err := staticFS.ReadFile("static/" + path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+path] = real
	}
	runPageInEngines(t, page, modules)
}
