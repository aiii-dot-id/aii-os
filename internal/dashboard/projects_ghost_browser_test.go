//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// projectsGhostPage pins the ghost deep-link behavior (§8 residual,
// 2026-08-27): a deep link to a project that does not exist must not
// leave a lying address above an honest page. The banner names the
// ghost safely escaped; the empty state stays honest; and the address
// is retracted via replaceState (no history entry — the back button
// must step where the operator actually came from). The injection
// probe is driven through S directly because browsers percent-encode
// angle brackets in hashes — the escaping property under test is the
// banner's, not the URL's.
const projectsGhostPage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="view-projects" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div>
<script type="module">
import { report, assert } from '/__harness.js';
window.__diag = 'imports-done';
const __tell = m => { try { fetch('/__console', { method: 'POST', body: m }); } catch (e) {} };
import { S } from '/state.js';
import { renderProjects } from '/views/projects.js';
import { __sent, __reset } from '/ws.js';
import '/app.js'; // real router: its hashchange handler IS the URL flow under test

const A = { id: 'alpha', name: 'Alpha', state: 'open', description: 'first', dir: '/p/alpha', focus: 'fa', active: true };

function fresh() {
  __reset();
  S.projects = [A];
  S.activeProject = A;
  S.viewedProject = null;
  S.projTab = 'overview';
  S.view = 'projects';
  S.workspace = null; S.workspaceFor = '';
  S.connected = true;
  S.projectsPrimed = true; // the list has landed — ghost detection is armed (state.js:9)
}

let step = 0;
function fail(msg) { report('FAIL: ' + msg); }
function next() {
  step++;
  if (step === 1) {
    // INJECTION PROBE: a ghost id carrying a script tag, driven
    // through S directly (hash-encoding would mangle it). If the
    // banner escaping is broken, the payload EXECUTES.
    fresh();
    // The payload's closing tag is split so THIS page's own script
    // block does not terminate at the literal sequence inside it.
    S.viewedProject = 'ghost-<scr' + 'ipt>window.__pwn=1</scr' + 'ipt>-id';
    renderProjects();
    const banner = document.getElementById('ghost-banner');
    if (!banner) { fail('no ghost banner — the address names no project and nothing says so'); return; }
    if (window.__pwn) { fail('the ghost id EXECUTED — banner escaping is broken'); return; }
    const text = banner.textContent || '';
    if (!text.toLowerCase().includes('names no project')) { fail('banner does not say what happened: ' + text); return; }
    if (!document.getElementById('proj-space').textContent.includes('No project focused')) { fail('empty state missing under the ghost'); return; }
    next();
  } else if (step === 2) {
    // URL FLOW: a real navigation to a ghost id — the router sets
    // S.viewedProject, the banner appears, and the address is
    // retracted to #/projects without a history entry.
    fresh();
    location.hash = '#/projects/nonexistent-id';
    setTimeout(() => {
      const banner = document.getElementById('ghost-banner');
      if (!banner) { fail('no banner after a real ghost navigation (router did not land the ghost)'); return; }
      if (location.hash !== '#/projects') { fail('address not retracted: ' + location.hash); return; }
      next();
    }, 80);
  } else if (step === 3) {
    // A REAL id still works after a ghost: alpha renders, no banner.
    location.hash = '#/projects/alpha';
    setTimeout(() => {
      if (document.getElementById('ghost-banner')) { fail('banner survived a real navigation'); return; }
      if (!document.getElementById('proj-space').textContent.includes('Alpha')) { fail('real project does not render after a ghost'); return; }
      next();
    }, 80);
  } else {
    report('OK');
  }
}
window.addEventListener('error', ev => __tell('page error: ' + ((ev.error && ev.error.message) || ev.message)));
window.__diag = 'armed';
next();
</script>
`

// TestProjectsGhostDeepLinkBrowser pins the ghost banner + replaceState
// retraction in a real engine. Skips honestly when no engine exists.
// The module map is DERIVED (overrideModuleStubs); real modules arrive
// via the rig fall-through.
func TestProjectsGhostDeepLinkBrowser(t *testing.T) {
	runPageInEngines(t, projectsGhostPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
