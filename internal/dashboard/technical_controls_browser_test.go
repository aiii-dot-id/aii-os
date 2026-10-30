//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheTechnicalControlsAreShownFromWhereTheyAre(t *testing.T) {
	frame, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	shipped := string(frame)
	start := strings.Index(shipped, `<div class="expert-bar" id="expert-bar"`)
	end := strings.Index(shipped, `<div id="chat-substrate-wrap"`)
	if start < 0 || end < start {
		t.Fatal("the shipped frame carries no bar at the top of Chat")
	}
	places, err := json.Marshal(technicalPlaces)
	if err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><html><body><section id="view-chat">` + shipped[start:end] + `</section><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { frames } from '/ws.js';
import { renderSettings } from '/views/settings.js';
import { expertStatus, acceptExpertConfig, rejectExpertConfig } from '/views/expert.js';
import { run } from '/__harness.js';
const places = ` + string(places) + `;
run(async (assert) => {
  const shown = el => !!el && !el.hidden && !el.closest('[hidden]');
  const chat = () => document.querySelector('#expert-bar [data-expert]');
  const here = () => document.querySelector('#settings-stack [data-expert-here]');
  const chooser = () => document.getElementById('cfg-route');
  const sets = () => frames.filter(f => f.type === 'config_set');
  const status = expert => { S.stats = { name: 'Vega', expert, notices: { on: false, lines: [] } }; expertStatus(); };
  // Every place the box governs, as the page now draws it.
  const governed = on => {
    for (const place of Object.keys(places)) {
      const el = document.querySelector('[data-technical="' + place + '"]');
      assert(on ? shown(el) : !shown(el), place + (on ? ' is not shown with the technical controls on' : ' is shown with the technical controls off'));
    }
    assert([...document.querySelectorAll('[data-technical]')].every(el => el.dataset.technical in places), 'a place the box governs is not one its sentence names');
  };

  S.view = 'settings'; S.connected = true; S.identityExists = true; S.providersLoaded = true; S.providers = [];
  S.config = {
    llm: {}, speech: { stt: {}, tts: {}, services: [] }, dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: true },
    secrets_ok: true, witness: {}, prompt: {}, agency: {}, logs: {}, contacts: [],
    public_name: { name: 'vega', status: 'issued', origin: 'https://vega.example', route_mode: 'direct', relays: [], machine_addresses: ['192.0.2.7'] },
  };
  status(false);
  renderSettings();
  document.querySelector('[data-sec="dashboard"]').click();
  assert(here() && chat(), 'the box is not on both Chat and Settings → Dashboard');
  const label = el => el.closest('label');
  assert(label(here()).textContent.trim() === 'Show technical controls' && label(chat()).textContent.trim() === label(here()).textContent.trim(),
    'the two copies of the box read differently: ' + label(here()).textContent + ' / ' + label(chat()).textContent);
  assert(label(here()).title === label(chat()).title && label(here()).title.includes('route'), 'the two copies of the box say different things when the pointer rests on them');
  assert(!here().checked && !chat().checked, 'a box is ticked while the host says the technical controls are off');
  assert(chooser() && !shown(chooser()), 'the route chooser is shown with the technical controls off');
  governed(false);

  // From Settings alone: the box beside the route shows it, saving only itself.
  here().click();
  assert(sets().length === 1 && JSON.stringify(sets()[0].config) === '{"dashboard.expert":true}', 'the box in Settings sent more or less than itself: ' + JSON.stringify(sets()));
  assert(here().checked && here().disabled && shown(chooser()), 'the route chooser is not shown while the host answers, or the box takes a second press');
  assert(chat().checked && chat().disabled, 'the box on Chat does not show what Settings asked for');
  assert(acceptExpertConfig(sets()[0].request_id), 'the door’s answer to the box in Settings was not taken');
  status(true);
  assert(here().checked && !here().disabled && chat().checked && !chat().disabled, 'the host’s word is not shown by both copies');
  governed(true);
  here().click();
  acceptExpertConfig(sets().at(-1).request_id);
  status(false);
  assert(!here().checked && !shown(chooser()) && !chat().checked, 'the box in Settings did not hide the route chooser again');
  governed(false);

  // From Chat: the box there shows the route chooser in Settings.
  chat().click();
  assert(JSON.stringify(sets().at(-1).config) === '{"dashboard.expert":true}', 'the box on Chat sent more or less than itself');
  acceptExpertConfig(sets().at(-1).request_id);
  status(true);
  assert(here().checked && shown(chooser()), 'a press on Chat is not shown in Settings');
  chat().click();
  acceptExpertConfig(sets().at(-1).request_id);
  status(false);
  assert(!here().checked && !shown(chooser()), 'a press on Chat did not hide the route chooser in Settings');

  // A refusal is said beside the box that was pressed, and nothing is shown.
  here().click();
  rejectExpertConfig(sets().at(-1).request_id, 'dashboard.expert: the host refused it');
  const said = document.querySelector('#settings-stack [data-expert-here-said]');
  assert(said && said.textContent.includes('the host refused it') && !here().checked && !shown(chooser()), 'a refused save from Settings is not said beside its box, or shows the controls');
});
</script></body></html>`
	modules := map[string][]byte{
		"/state.js": []byte(`export const S = { providers: [], config: null, providersLoaded: false, stats: null };`),
		"/ws.js": []byte(`let n = 0; export const frames = [];
export function send(frame) { frame.request_id ||= 'req-' + (++n); frames.push(structuredClone(frame)); return frame.request_id; }
export function query(name, extra) { return send(Object.assign({ type: 'query', query: name }, extra || {})); }`),
	}
	runPageInEngines(t, page, modules)
}
