//go:build !windows

package dashboard

import "testing"

func TestTheUpdatesCardSaysTheCheckCadenceInForce(t *testing.T) {
	page := `<!doctype html><html><body><div id="root"></div>
<script type="module">
import { run } from '/__harness.js';
import { updateCardHTML } from '/views/settings.js';
run(async (assert) => {
  const card = (u, c) => { const d = document.createElement('div'); d.innerHTML = updateCardHTML(u, c); return d; };
  const said = (u) => card(u).querySelector('[data-update-cadence]').textContent;
  assert(said({enabled:true}).startsWith('Checks run once an hour. '), 'with no cadence sent the card says what it always said: ' + said({enabled:true}));
  assert(said({enabled:true, check_interval_minutes:60}).startsWith('Checks run once an hour. '), 'at the default 60 the card says what it always said: ' + said({enabled:true, check_interval_minutes:60}));
  assert(said({enabled:true, check_interval_minutes:15}).startsWith('Checks run every 15 minutes. '), 'at 15 the card says 15 minutes: ' + said({enabled:true, check_interval_minutes:15}));
  assert(said({enabled:true, check_interval_minutes:1440}).startsWith('Checks run every 1440 minutes. '), 'at 1440 the card says 1440 minutes: ' + said({enabled:true, check_interval_minutes:1440}));
  const c = {updates:{automatic:true, check_interval_minutes:90}, cognition:{settings:[
    {key:'updates.check_interval_minutes', min:15, max:1440, default:60, unit:'minutes', card:'updates'},
    {key:'prompt.alarm_firings', min:1, max:100000, default:20, unit:'firings', card:''}]}};
  const field = card({enabled:true}, c).querySelector('[data-cog-key="updates.check_interval_minutes"]');
  assert(field !== null && field.value === '90' && field.min === '15' && field.max === '1440', 'the setting is a field on this card, with the value configured and its bounds');
  assert(card({enabled:true}, c).querySelector('[data-cog-key="prompt.alarm_firings"]') === null, 'another card\'s row is not drawn here');
  assert(card({enabled:true}).querySelector('[data-cog-key]') === null, 'with no configuration yet no field is drawn');
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
