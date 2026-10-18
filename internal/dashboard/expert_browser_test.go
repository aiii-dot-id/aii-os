//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const expertPage = `<!doctype html><section class="view on" id="view-chat">%BAR%<div id="chat-substrate-wrap"></div><div class="thread" id="thread"></div></section>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderExpert, acceptExpertConfig, rejectExpertConfig, expertStatus, expertConnectionLost, routeLine } from './views/expert.js';
import { assert, run } from './__harness.js';

run(() => {
  const bar = document.getElementById('expert-bar');
  const q = sel => bar.querySelector(sel);
  const shown = el => !!el && !el.hidden && !el.closest('[hidden]');
  const sets = () => frames.filter(f => f.type === 'config_set');
  const status = st => { S.stats = st; expertStatus(); };
  const quinn = (channel, carried) => ({ name: 'Quinn', channel, carried });

  assert(document.getElementById('view-chat').firstElementChild === bar, 'the Expert bar is not the first thing on Chat');
  S.identityExists = false; S.stats = { name: '(not born)' }; renderExpert();
  assert(bar.hidden, 'the bar shows before an identity is born, where it has nothing to reveal');

  S.identityExists = true;
  status({ name: 'Vega', expert: false, notices: { on: false, lines: [] } });
  assert(shown(q('[data-expert]')) && !q('[data-expert]').checked, 'Expert is not an unchecked box at the top');
  assert(!shown(q('[data-expert-panel]')) && q('[data-expert-panel]').children.length === 0, 'Expert off still shows its controls');
  assert(!shown(q('[data-route-line]')), 'a route that is off is said');

  status({ name: 'Vega', expert: false, notices: { on: true, lines: [quinn('telegram', true), quinn('sms', true)] } });
  assert(shown(q('[data-route-line]')) && q('[data-route-line]').textContent === 'Your notices also go to Quinn on telegram, then sms when no page is open.',
    'an active route is not said with Expert off: ' + q('[data-route-line]').textContent);
  assert(!shown(q('[data-expert-panel]')), 'the route being on opened the panel');
  status({ name: 'Vega', expert: false, notices: { on: true, lines: [quinn('telegram', false)], held: 'no installed channel carries a line marked as you' } });
  assert(q('[data-route-line]').textContent.includes('nothing can carry them now: no installed channel carries a line marked as you'), 'a route nothing can carry is not said: ' + q('[data-route-line]').textContent);
  assert(routeLine({ on: true, lines: [], held: 'no contact line is marked as you' }).includes('no contact line is marked as you'), 'a route with no line is not said');

  q('[data-expert]').click();
  assert(sets().length === 1 && JSON.stringify(sets()[0].config) === '{"dashboard.expert":true}', 'Expert sent more than itself: ' + JSON.stringify(sets()));
  assert(q('[data-expert]').checked && q('[data-expert]').disabled, 'the box does not hold the asked value while the host answers');
  assert(acceptExpertConfig(sets()[0].request_id), 'the door’s answer to Expert was not taken');
  assert(q('[data-expert]').checked && !q('[data-expert]').disabled, 'an acknowledged save flickered back before its status');
  status({ name: 'Vega', expert: true, notices: { on: false, lines: [quinn('telegram', true)] } });
  assert(shown(q('[data-expert-panel]')), 'Expert on does not show its controls');
  const route = () => q('[data-notice-route]');
  assert(route() && !route().checked, 'the route control does not read the host’s route');
  assert(q('[data-notice-lines]').textContent.includes('Quinn on telegram'), 'the lines marked as you are not listed');
  assert(q('a[href="#/settings/messages"]'), 'the way to the one contacts editor is missing');
  assert(!q('[data-expert-panel] input[type="text"]'), 'a second contacts editor was built');

  route().click();
  assert(sets().length === 2 && JSON.stringify(sets()[1].config) === '{"dashboard.notices_off_dashboard":true}', 'the route sent more than itself: ' + JSON.stringify(sets()[1]));
  assert(rejectExpertConfig(sets()[1].request_id, 'dashboard.notices_off_dashboard: mark a contact line as you first (Settings → Messages → Contacts, This is me) — your notices go to the lines marked as you, and none is'), 'the refusal was not taken');
  assert(shown(q('[data-notice-said]')) && q('[data-notice-said]').textContent.includes('mark a contact line as you first'), 'the refusal is not beside the control');
  assert(!route().checked, 'a refused route shows as on');
  route().click();
  assert(acceptExpertConfig(sets()[2].request_id), 'the door’s answer to the route was not taken');
  status({ name: 'Vega', expert: true, notices: { on: true, lines: [quinn('telegram', true)] } });
  assert(route().checked && !shown(q('[data-notice-said]')), 'a kept route does not show as on');

  q('[data-expert]').click();
  assert(JSON.stringify(sets().at(-1).config) === '{"dashboard.expert":false}', 'Expert off sent more than itself: ' + JSON.stringify(sets().at(-1)));
  acceptExpertConfig(sets().at(-1).request_id);
  status({ name: 'Vega', expert: false, notices: { on: true, lines: [quinn('telegram', true)] } });
  assert(!shown(q('[data-expert-panel]')) && shown(q('[data-route-line]')), 'Expert off hid the route line or kept the controls');
  assert(frames.every(f => f.type !== 'config_set' || !('dashboard.notices_off_dashboard' in f.config) || f === sets()[1] || f === sets()[2]), 'Expert moved the route');

  status({ name: 'Vega', expert: true, notices: { on: true, lines: [{ name: '<img src=x onerror="window.__owned=1">', channel: 'telegram', carried: true }] } });
  assert(!bar.querySelector('img') && window.__owned === undefined && q('[data-notice-lines]').textContent.includes('<img src=x'), 'a contact name became markup');
  assert(q('[data-route-line]').textContent.includes('<img src=x'), 'the route line lost the name as text');

  q('[data-expert]').click();
  expertConnectionLost();
  assert(q('[data-expert-said]').textContent.includes('Connection lost') && q('[data-expert]').checked && !q('[data-expert]').disabled, 'a lost save is not said, or the box shows the unsaved value');
  let stored = 0; try { stored = localStorage.length; } catch (e) {}
  assert(stored === 0, 'the page kept Expert in this browser’s storage');
});
</script>`

func TestTheTopOfChatShowsExpertAndTheRoute(t *testing.T) {
	frame, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(frame)
	start := strings.Index(html, `<div class="expert-bar" id="expert-bar"`)
	end := strings.Index(html, `<div id="chat-substrate-wrap"`)
	if start < 0 || end < start {
		t.Fatal("the shipped frame carries no Expert bar at the top of Chat")
	}
	modules := map[string][]byte{
		"/state.js": []byte(`export const S = { stats: null, identityExists: false };`),
		"/ws.js": []byte(`let seq = 0;
export const frames = [];
export function send(frame) { frame.request_id ||= String(++seq); frames.push(structuredClone(frame)); return frame.request_id; }`),
	}
	runPageInEngines(t, strings.Replace(expertPage, "%BAR%", html[start:end], 1), modules)
}
