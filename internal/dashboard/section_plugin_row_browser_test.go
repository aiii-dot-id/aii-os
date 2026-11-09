//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

/*
A PLUGIN'S PAGE OPENS FROM ITS PLUGIN'S ROW, AND IS NEVER LEFT WITH NO WAY IN.

	A page a plugin places among the main tabs used to add a button to the
	bar's end for itself. Its way in is its plugin's card on the Permissions
	page, matched by the package's id and never by a title. While no card
	opens it, the bar keeps its button: a page whose card is missing, or goes
	away, still has a way in. This drives the real sections, frames and
	Permissions modules together, so the card's button reaches the page
	through the same lists the bar reads.
*/
const sectionPluginRowPage = `<!doctype html>
<div id="nav"></div><div id="views"></div><div id="slot-rail"></div><div id="slot-panel"></div>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { onSections, onLayout, publish } from '/sections.js';
import { S } from '/state.js';
import { run, until } from '/__harness.js';
import { renderPlugins } from '/views/plugins.js';

run(async assert => {
  const inBar = id => !!document.querySelector('#nav [data-view="section:' + id + '"]');
  const card = (id, extra) => Object.assign({ id, version: '1.0.0', tier: 'T1', mode: 'wasm', variant: 'wasm', settings: [],
    grants: { listed: true, read_only: false, auto_confirm: [] } }, extra || {});
  const config = installed => ({ plugins: { autoload: 'T2', skips: [], catalog: [], pending: [], installed } });
  const openButtons = () => Array.from(document.querySelectorAll('#plugins-stack [data-open-frame]'));
  const slots = { 'main-tabs': ['owned', 'free'] };
  onLayout({ profiles: { desktop: slots, mobile: slots, phone: slots, tablet: slots } });
  const list = [
    { id: 'owned', title: 'Owned page', slot: 'main-tabs', entry: 'index.html', plugin: 'org.example.pages' },
    { id: 'free', title: 'Free page', slot: 'main-tabs', entry: 'index.html' },
  ];
  // A card titled like the free page: a title must never stand for the id.
  const rows = config([card('org.example.pages'), card('org.example.titled', { title: 'Free page' })]);

  // No card opens either page yet: both keep their bar button.
  onSections(list);
  await until(() => inBar('owned') && inBar('free'));

  // The plugin's card arrives: its page leaves the bar; the other stays.
  publish({ type: 'config', config: rows });
  assert(!inBar('owned'), 'a page its plugin\'s card opens is still in the bar');
  assert(inBar('free'), 'a page no plugin owns lost its bar button, or was matched by a title');
  // The page its card opens is a page under Settings (step 16b); one no card opens is not.
  assert(S.framePlace('section:owned') === 'settings' && S.framePlace('section:free') === '' && S.framePlace('plugins') === '',
    'the frames do not say which page is under Settings');

  // The card offers exactly its own page, and opens it.
  S.view = 'plugins'; S.config = rows; renderPlugins();
  const open = openButtons();
  assert(open.length === 1 && open[0].dataset.openFrame === 'owned',
    'the cards do not offer exactly the owned page: ' + open.map(b => b.dataset.openFrame).join(','));
  const own = open[0].closest('[data-plugin-row]');
  assert(own && own.dataset.pluginRow === 'org.example.pages',
    'the page is offered on a row that is not its plugin\'s');
  assert(open[0].textContent === 'Open Owned page', 'the button does not name its page: ' + open[0].textContent);
  open[0].click();
  assert(S.view === 'section:owned', 'the card did not open its page: ' + S.view);

  // The card goes away: the page has its bar button again.
  publish({ type: 'config', config: config([]) });
  assert(inBar('owned'), 'a page whose card went away has no way in');
  assert(S.framePlace('section:owned') === '', 'a page whose card went away is still under Settings');

  // Mounted after its card arrived: never in the bar, and offered at once
  // on a Permissions page that is already open.
  onSections([]);
  await until(() => !inBar('owned') && !inBar('free'));
  S.config = rows; S.view = 'plugins'; renderPlugins();
  assert(openButtons().length === 0, 'a page that is not mounted is offered');
  onSections(list);
  await until(() => inBar('free') && openButtons().length === 1);
  assert(!inBar('owned'), 'a page mounted after its card arrived went into the bar');
});
</script>`

func TestAPluginsPageOpensFromItsCardAndKeepsAWayIn(t *testing.T) {
	runPageInEngines(t, sectionPluginRowPage, map[string][]byte{
		"/app.js": []byte(`import { S } from '/state.js'; export function go(v) { S.view = v; } export function toast() {} S.go = go;`),
		"/ws.js":  []byte(`export function send() { return ''; } export function query() { return ''; }`),
		"/views/settings.js": []byte(`export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection(){}
export function savebarHTML() { return ''; } export function sendConfigChanges() {} export function configFeedbackHTML() { return ''; }`),
	})
}
