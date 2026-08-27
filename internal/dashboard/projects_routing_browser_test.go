//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// projectsRoutingPage pins addressability (§8.7): a project has a URL.
// The URL is not decoration beside the router — the address IS the
// router. Back/forward and a pasted link land in the same handler a
// click lands in, performing the same act. Before this, the dashboard
// had no pushState or hash routing anywhere: a project could not be
// linked, bookmarked, or returned to; reload dropped you on your last
// remembered tab, never on the project you were reading.
//
// The pin, precisely: viewProject writes the address; a pasted link
// performs the same select a click performs; back/forward re-perform
// navigation; and a hash echo of the state we just set does not
// re-fire the act (the guard is state equality, not a flag).
const projectsRoutingPage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="view-projects" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div>
<script type="module">
import { report, assert } from '/__harness.js';
window.__diag = 'imports-done';
const __tell = m => { try { fetch('/__console', { method: 'POST', body: m }); } catch (e) {} };
import { S } from '/state.js';
import { viewProject } from '/views/projects.js';
import { __sent, __reset } from '/ws.js';
import { go, parseHash } from '/app.js';

const A = { id: 'alpha', name: 'Alpha', state: 'open', description: 'first', dir: '/p/alpha', focus: 'fa', active: true };
const B = { id: 'beta', name: 'Beta', state: 'open', description: 'second', dir: '/p/beta', focus: 'fb', active: false };

function fresh() {
  __reset();
  S.projects = [A, B];
  S.activeProject = A;
  S.viewedProject = null;
  S.projTab = 'overview';
  S.view = 'projects';
  S.workspace = null; S.workspaceFor = '';
  S.connected = true;
}

// run() is synchronous-only; this surface is evented (hashchange fires
// async). Reuse its result channel manually: perform async steps, then
// report once.
let step = 0;
const steps = [];
window.__diag = 'vars';
function fail(msg) { report('FAIL: ' + msg); }
function next() {
  step++;
  window.__diag = 'step' + step;
  __tell('next step ' + step);
  try { (steps[step - 1] || (() => report('OK')))(); }
  catch (e) { fail((e && e.message) || String(e)); }
}
window.__diagReady = new Promise(res => {
  window.addEventListener('error', () => res());
  res();
});

steps[0] = () => {
  window.__diag = 'S0-entered';
  fresh();
  // CLICK WRITES THE ADDRESS — a project is where you are, so it has one.
  viewProject('beta');
  assert(location.hash === '#/projects/beta', 'clicking a project did not address it: ' + location.hash);

  // THE ADDRESS IS SHARED VOCABULARY — parseHash reads what the bar shows.
  const r = parseHash('#/projects/beta');
  assert(r.view === 'projects' && r.project === 'beta', 'parseHash misread the address: ' + JSON.stringify(r));
  assert(parseHash('#/chat').project === null, 'a plain view must have no project');

  // go() DOES NOT FLATTEN A DEEP ADDRESS — re-entering the projects
  // view keeps the project address; entering another view replaces it.
  go('projects');
  assert(location.hash === '#/projects/beta', 'go(projects) flattened the project address: ' + location.hash);
  go('chat');
  assert(location.hash === '#/chat', 'go(chat) did not write its own address: ' + location.hash);
  next();
};

steps[1] = () => setTimeout(() => {
  window.__diag = 'S1-timer';
  // THE ECHO GUARD — go's own hash writes fired hashchange; neither may
  // re-fire the act. The identity never left alpha's list, and no stray
  // select leaked: exactly the one from viewProject('beta').
  assert(__sent.filter(m => m.project && m.project.action === 'select').length === 1,
    'hash echoes re-fired the act: ' + JSON.stringify(__sent));

  // A PASTED LINK IS THE GESTURE — the same select a click performs.
  fresh();
  location.hash = '#/projects/beta';
  setTimeout(() => {
    const selects = __sent.filter(m => m.project && m.project.action === 'select');
    try {
      assert(S.view === 'projects', 'a pasted project link did not route to the projects view: ' + S.view);
      assert(selects.length === 1 && selects[0].project.id === 'beta',
        'a pasted link did not perform the select a click performs: ' + JSON.stringify(__sent));
    } catch (e) { return fail(e.message); }
    next();
  }, 60);
}, 60);

steps[2] = () => {
  // BACK/FOREWORD — history re-performs navigation. Back returns to
  // the chat address (the stack: chat, projects/beta, chat, then the
  // pasted link); forward must re-select beta — one act per address.
  const selectsBefore = __sent.filter(m => m.project && m.project.action === 'select').length;
  history.back();
  setTimeout(() => {
    const r = parseHash(location.hash);
    try {
      assert(r.view === 'chat', 'history.back() did not return to the chat address: ' + location.hash);
      assert(S.view === 'chat', 'returning to the chat address did not route the view: ' + S.view);
      history.forward();
      setTimeout(() => {
        const r2 = parseHash(location.hash);
        const selectsAfter = __sent.filter(m => m.project && m.project.action === 'select').length;
        try {
          assert(r2.project === 'beta', 'history.forward() did not restore the project address: ' + location.hash);
          assert(S.view === 'projects', 'forward to the project address did not route the view: ' + S.view);
          assert(selectsAfter === selectsBefore + 1,
            'forward re-fired the act ' + (selectsAfter - selectsBefore) + ' times, expected exactly 1');
        } catch (e) { return fail(e.message); }
        next();
      }, 60);
    } catch (e) { return fail(e.message); }
  }, 60);
};

window.addEventListener('error', ev => __tell('page error: ' + ((ev.error && ev.error.message) || ev.message)));

next();
</script>`

// TestProjectsRoutingInBrowser pins the URL router in a real engine.
// Skips honestly when no engine is installed.
// TestProjectsRoutingInBrowser pins the URL router in a real engine.
// Skips honestly when no engine is installed. The module map is DERIVED
// (overrideModuleStubs); real modules arrive via the rig fall-through.
func TestProjectsRoutingInBrowser(t *testing.T) {
	runPageInEngines(t, projectsRoutingPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}

// overrideModuleStubs layers caller entries over the shared base so the
// map is one derivation site, not N hand-copied forks (§8 fork pattern).
func overrideModuleStubs(entries ...any) map[string][]byte {
	m := browserModuleStubs()
	for i := 0; i+1 < len(entries); i += 2 {
		m[entries[i].(string)] = entries[i+1].([]byte)
	}
	return m
}
