//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// recordedScript is the shared half of both pages: the items, and the
// checks on what is drawn under a row.
const recordedScript = `
const full = { id: 'ws_full', description: 'count the rows', status: 'delivered', result: 'served: they match',
  next_move: 'read the <b>second</b> log', expected_evidence: 'counts <i>match</i>', falsifier: 'a row in one log only',
  evidence_class: 'locally_verified', evidence_readback: 'wc -l: <b>4120</b> and 4120' };
const live = { id: 'ws_live', description: 'compare the logs', status: 'active', next_move: 'open <script>x<\/script> next' };
const bare = { id: 'ws_bare', description: 'say hello', status: 'delivered', result: 'served: hello' };
function checkFull(holder, where) {
  assert(holder, where + ': nothing under the full session');
  assert(!holder.querySelector('b, i, script'), where + ': a value became markup: ' + holder.innerHTML);
  const rows = [...holder.querySelectorAll('.work-rec-row')].map(r => r.textContent);
  const want = ['Next move read the <b>second</b> log', 'Expected evidence counts <i>match</i>', 'Would show it wrong a row in one log only',
    'Evidence (locally_verified)', 'Read back wc -l: <b>4120</b> and 4120'];
  assert(JSON.stringify(rows) === JSON.stringify(want), where + ': rows ' + JSON.stringify(rows));
}
`

func TestWhatASessionRecordsIsDrawnAsTextOnHome(t *testing.T) {
	page := `<!doctype html>
<div id="home-inner"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { deliveredHTML, wireDelivered } from './views/delivered.js';
` + recordedScript + `
run(() => {
  const root = document.getElementById('home-inner');
  // Today's drawing of the bare session, before any session records anything.
  S.work = { live: [], queued: 0, delivered: [bare] };
  root.innerHTML = deliveredHTML();
  wireDelivered(root);
  const today = root.querySelector('[data-work="ws_bare"]').outerHTML;

  S.work = { live: [live], queued: 0, delivered: [full, bare] };
  root.innerHTML = deliveredHTML();
  wireDelivered(root);
  checkFull(root.querySelector('[data-work="ws_full"] .work-rec'), 'home delivered');
  const b = root.querySelector('[data-work="ws_bare"]');
  assert(!b.querySelector('.work-rec'), 'a session with none got rows under it');
  assert(b.outerHTML === today, 'a session with none is drawn otherwise than today:\n' + b.outerHTML + '\n' + today);
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/work-recorded.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { work: null };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export function send() { return 'req-1'; }`)
	runPageInEngines(t, page, modules)
}

func TestWhatASessionRecordsIsDrawnAsTextOnAProjectsWorkTab(t *testing.T) {
	page := `<!doctype html>
<div id="proj-space"></div>
<div class="dock-wrap"><div class="dock-tools"><input type="search" id="dock-filter"></div><div class="dock" id="dock"></div></div>
<span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderProjects } from './views/projects.js';
` + recordedScript + `
run(() => {
  const p = { id: 'proj-x', name: 'Proj X', state: 'open', dir: '/p/proj-x' };
  S.view = 'projects'; S.projects = [p]; S.viewedProject = 'proj-x'; S.projTab = 'work'; S.identityExists = true;
  S.workspaceFor = 'proj-x';
  S.workspace = { project: p, files: [], work: [bare] };
  renderProjects();
  const today = document.querySelector('#proj-space .ws-item').outerHTML;

  S.workspace = { project: p, files: [], work: [full, live, bare] };
  renderProjects();
  const items = [...document.querySelectorAll('#proj-space .ws-item')];
  checkFull(items[0].querySelector('.work-rec'), 'project work');
  const l = items[1].querySelector('.work-rec');
  assert(l && l.textContent === 'Next move open <script>x<\/script> next' && !l.querySelector('script'), 'project live: ' + (l && l.innerHTML));
  assert(!items[2].querySelector('.work-rec'), 'a session with none got rows under it');
  assert(items[2].outerHTML === today, 'a session with none is drawn otherwise than today:\n' + items[2].outerHTML + '\n' + today);
});
</script>`
	runPageInEngines(t, page, overrideModuleStubs(
		"/state.js", []byte("export const S = { view: 'projects', projects: [], workspace: null, workspaceFor: '', projTab: 'overview', activeProject: null, dockFilter: '', showArchived: false };\n"),
		"/app.js", stubModule("go"),
		"/ws.js", []byte("export function send() { return 'req-1'; }\nexport function query() {}\n"),
	))
}
