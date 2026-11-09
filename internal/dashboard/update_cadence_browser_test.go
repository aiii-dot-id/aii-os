//go:build !windows

package dashboard

import "testing"

func TestTheUpdatesCardSaysTheCheckCadenceInForce(t *testing.T) {
	page := `<!doctype html><html><body><div id="root"></div>
<script type="module">
import { run } from '/__harness.js';
import { updatesHTML } from '/views/settings/app.js';
run(async (assert) => {
  const card = (u, c) => { const d = document.createElement('div'); d.innerHTML = updatesHTML(u, c); return d; };
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
  // The switch's hint says the cadence in force: the annex's sentence at the
  // default, the card's sentence for another number. The switch reads what
  // is configured once the configuration has come, so a save shows at once.
  const hint = (u) => card(u).querySelector('#app-updates-hint').textContent;
  assert(hint({enabled:true}) === 'Checks once an hour. An update that fails to start goes back to this version by itself, and tells you.', 'the hint at the default: ' + hint({enabled:true}));
  assert(hint({enabled:true, check_interval_minutes:15}).startsWith('Checks run every 15 minutes. An update that fails'), 'the hint at 15: ' + hint({enabled:true, check_interval_minutes:15}));
  assert(card({enabled:true, automatic:false}, c).querySelector('#cfg-uauto').checked === true, 'the configured value is the switch once the configuration has come');
  assert(card({enabled:true, automatic:true}).querySelector('#cfg-uauto').checked === true && card({enabled:true, automatic:false}).querySelector('#cfg-uauto').checked === false, 'before it, the switch is what the update state says');
  const both = card({enabled:true}, c);
  assert(both.querySelector('details [data-cog-key="updates.check_interval_minutes"]') !== null && both.querySelector('details [data-save="updates"]') !== null, 'the cadence field and its Save are under Details');
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
	for _, path := range []string{"views/settings.js", "views/settings/app.js"} {
		real, err := staticFS.ReadFile("static/" + path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+path] = real
	}
	runPageInEngines(t, page, modules)
}
