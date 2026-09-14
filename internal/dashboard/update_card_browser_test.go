//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
func TestUpdateCardRendersTheStatesAndCheckNow(t *testing.T) {
	page := `<!doctype html><html><body><div id="root"></div>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { sent } from '/ws.js';
import { updateCardHTML, wireUpdateCheck } from '/views/settings.js';
run(async (assert) => {
  const text = (u) => { const d = document.createElement('div'); d.innerHTML = updateCardHTML(u); return d; };
  assert(text(null).textContent.includes('Unavailable'), 'no checker renders as unavailable');
  assert(text({enabled:false, current_version:'dev'}).textContent.includes('Unavailable'), 'an unarmed build renders as unavailable');
  assert(text({enabled:true, checking:true}).textContent.includes('Checking'), 'in flight renders as checking');
  assert(text({enabled:true, checked_at:'2026-09-02T10:00:00Z'}).textContent.includes('Up to date'), 'current renders as up to date');
  const avail = text({enabled:true, current_version:'1.0.0', available_version:'1.1.0'});
  assert(avail.textContent.includes('Update available: 1.1.0'), 'an available version is named');
  assert(avail.textContent.includes('automatic apply is off'), 'with automatic off the card says so');
  assert(avail.querySelector('#cfg-uauto') !== null && !avail.querySelector('#cfg-uauto').checked, 'automatic apply is a switch on the card, off here');
  const managed = text({enabled:true, available_version:'1.1.0', stage_refusal:'/usr/bin is not writable', release_url:'https://github.com/aiii-dot-id/aii-os/releases/tag/v1.1.0'});
  assert(managed.textContent.includes('package manager') && managed.textContent.includes('sudo dpkg -i aii-os_1.1.0_amd64.deb'), 'a package-managed install is given the exact package command');
  const rel = managed.querySelector('a[href="https://github.com/aiii-dot-id/aii-os/releases/tag/v1.1.0"]');
  assert(rel !== null && rel.getAttribute('rel') === 'noopener', 'the release is a safe link');
  const ready = text({enabled:true, installed_version:'1.1.0', needs_restart:true});
  assert(ready.textContent.includes('relaunch'), 'an installed update asks for a relaunch');
  assert([...ready.querySelectorAll('button')].some(b => b.textContent.trim() === 'Relaunch now'), 'an installed update offers Relaunch now');
  assert(text({enabled:true, error:'release source unreachable'}).textContent.includes('Last check failed'), 'a failure is shown as one');
  const root = document.getElementById('root');
  root.innerHTML = updateCardHTML({enabled:true, current_version:'1.0.0'});
  const btns = [...root.querySelectorAll('button')].map(b => b.textContent.trim());
  assert(btns.length === 2 && btns[0] === 'Save' && btns[1] === 'Check now', 'up to date, the controls are Save and Check now: ' + JSON.stringify(btns));
  assert(!/\bApply\b/.test(root.textContent), 'the card never offers an apply');
  wireUpdateCheck(root);
  const check = [...root.querySelectorAll('button')].find(b => b.textContent.trim() === 'Check now');
  check.click();
  assert(sent.length === 1 && sent[0].type === 'update_check' && Object.keys(sent[0]).length === 1, 'the click sends the fixed verb with no argument');
  assert(check.disabled === true, 'the button disarms until the state comes back');
  const checking = [...text({enabled:true, checking:true}).querySelectorAll('button')].find(b => b.textContent.trim() === 'Check now');
  assert(checking.disabled === true, 'while checking the control is disabled');
  root.innerHTML = updateCardHTML({enabled:true, installed_version:'1.1.0', needs_restart:true});
  wireUpdateCheck(root);
  const relaunch = [...root.querySelectorAll('button')].find(b => b.textContent.trim() === 'Relaunch now');
  relaunch.click();
  assert(sent.length === 2 && sent[1].type === 'restart' && Object.keys(sent[1]).length === 1, 'Relaunch sends the fixed restart verb');
  assert(relaunch.disabled === true, 'Relaunch disarms once asked');
});
</script></body></html>`
	modules := map[string][]byte{
		"/state.js":              []byte(`export const S = {};`),
		"/util.js":               []byte(`export const $ = (id) => document.getElementById(id); export const esc = (s) => String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));`),
		"/ws.js":                 []byte(`export const sent = []; export function send(m) { sent.push(m); } export function query() {}`),
		"/sandbox.js":            stubModule("sandboxCardHTML", "wireSandboxCard"),
		"/pending.js":            stubModule("pendingSlot"),
		"/views/model-picker.js": stubModule("providerModels"),
	}
	real, err := staticFS.ReadFile("static/views/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/settings.js"] = real
	runPageInEngines(t, page, modules)
}
