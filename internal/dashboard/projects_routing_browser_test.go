//go:build !windows

// .
// .

package dashboard

import (
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
  // BACK/FORWARD — history re-performs navigation. Back returns to
  // the chat address (the stack: chat, projects/beta, chat, then the
  // pasted link — a stack that holds only because the steps start after
  // load; see the start below); forward must re-select beta — one act
  // per address.
  // History traversal is asynchronous and the fixed 60ms wait this
  // step once used was a timing assumption: the public runner's
  // Chrome needed longer under load and the assertion fired before
  // the traversal landed.
  // The poll waits for the STATE with a deadline — the assertion
  // stays exact; only the assumption about WHEN leaves.
  const when = (cond, then) => {
    const deadline = Date.now() + 3000;
    const tick = () => {
      if (cond()) return then();
      if (Date.now() > deadline) return then(); // deadline: assert with what is actually there
      setTimeout(tick, 20);
    };
    tick();
  };
  const selectsBefore = __sent.filter(m => m.project && m.project.action === 'select').length;
  history.back();
  /* Wait for the ROUTED state, not just the address. history.back()
     and history.forward() update location.hash BEFORE dispatching
     hashchange, so a waiter that polls only the hash can proceed while
     S.view still holds the previous view — which is the Gecko failure
     this test kept producing under load ("did not route the view:
     chat"). The deadline branch still calls then() and asserts against
     whatever is really there, so a genuine routing failure produces
     the same message it always did; only the false one is gone. */
  when(() => parseHash(location.hash).view === 'chat' && S.view === 'chat', () => {
    const r = parseHash(location.hash);
    try {
      assert(r.view === 'chat', 'history.back() did not return to the chat address: ' + location.hash);
      assert(S.view === 'chat', 'returning to the chat address did not route the view: ' + S.view);
      history.forward();
      when(() => { const p = parseHash(location.hash); return p.project === 'beta' && S.view === 'projects'; }, () => {
        const r2 = parseHash(location.hash);
        const selectsAfter = __sent.filter(m => m.project && m.project.action === 'select').length;
        try {
          assert(r2.project === 'beta', 'history.forward() did not restore the project address: ' + location.hash);
          assert(S.view === 'projects', 'forward to the project address did not route the view: ' + S.view);
          assert(selectsAfter === selectsBefore + 1,
            'forward re-fired the act ' + (selectsAfter - selectsBefore) + ' times, expected exactly 1');
        } catch (e) { return fail(e.message); }
        next();
      });
    } catch (e) { return fail(e.message); }
  });
};

window.addEventListener('error', ev => __tell('page error: ' + ((ev.error && ev.error.message) || ev.message)));

/* THE STACK STEP 2 WALKS EXISTS ONLY ONCE THE DOCUMENT HAS LOADED. A
   script navigation made while the document is not yet completely
   loaded, without user activation, REPLACES the current history entry
   instead of pushing one (HTML, "Location-object navigate"). This module
   runs before the load event, so starting at once raced it: when load
   was still pending at step 1's pasted link, that write replaced the
   chat entry and back() had nowhere to go, which is "history.back() did
   not return to the chat address: #/projects/beta" after the whole
   deadline, on the public runner's Firefox 155. No longer
   wait could fix that; no traversal was coming. Measured on the build
   host: Chrome 153 replaces before load, and holding its load event
   behind a slow image fails this page with exactly that message; Firefox
   140 ESR pushes regardless, which is why that host never saw it.
   Starting after load gives every engine the same pushed stack — in a
   task AFTER the load event, because a write inside its listener still
   replaced in both engines. */
const start = () => setTimeout(next, 0);
if (document.readyState === 'complete') start();
else window.addEventListener('load', start, { once: true });
</script>`

// .
// .
// .
// .
// .
func TestProjectsRoutingInBrowser(t *testing.T) {
	runPageInEngines(t, projectsRoutingPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}

// .
// .
func overrideModuleStubs(entries ...any) map[string][]byte {
	m := browserModuleStubs()
	for i := 0; i+1 < len(entries); i += 2 {
		m[entries[i].(string)] = entries[i+1].([]byte)
	}
	return m
}
