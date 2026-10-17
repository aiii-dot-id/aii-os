//go:build !windows

package dashboard

import (
	"testing"
)

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
    fresh();
    S.viewedProject = 'ghost-<scr' + 'ipt>window.__pwn=1</scr' + 'ipt>-id';
    renderProjects();
    const banner = document.getElementById('ghost-banner');
    if (!banner) { fail('no ghost banner — the address names no project and nothing says so'); return; }
    if (window.__pwn) { fail('the ghost id EXECUTED — banner escaping is broken'); return; }
    const text = banner.textContent || '';
    if (!text.toLowerCase().includes('names no project')) { fail('banner does not say what happened: ' + text); return; }
    if (!document.getElementById('proj-space').textContent.includes('No project focused')) { fail('empty state missing under the ghost'); return; }
    if (document.title.indexOf('ghost') >= 0) { fail('ghost id reached the tab title: ' + document.title); return; }
    next();
  } else if (step === 2) {
    fresh();
    location.hash = '#/projects/nonexistent-id';
    (function poll(waited) {
      const landed = document.getElementById('ghost-banner') && location.hash === '#/projects';
      if (!landed && waited < 3000) { setTimeout(() => poll(waited + 20), 20); return; }
      const banner = document.getElementById('ghost-banner');
      if (!banner) { fail('no banner after a real ghost navigation (router did not land the ghost)'); return; }
      if (location.hash !== '#/projects') { fail('address not retracted: ' + location.hash); return; }
      next();
    })(0);
  } else if (step === 3) {
    location.hash = '#/projects/alpha';
    (function poll(waited) {
      const landed = !document.getElementById('ghost-banner') &&
        document.getElementById('proj-space').textContent.includes('Alpha') && document.title.indexOf('Alpha') >= 0;
      if (!landed && waited < 3000) { setTimeout(() => poll(waited + 20), 20); return; }
      if (document.getElementById('ghost-banner')) { fail('banner survived a real navigation'); return; }
      if (!document.getElementById('proj-space').textContent.includes('Alpha')) { fail('real project does not render after a ghost'); return; }
      if (document.title.indexOf('Alpha') < 0) { fail('focused project did not title the tab: ' + document.title); return; }
      next();
    })(0);
  } else {
    report('OK');
  }
}
window.addEventListener('error', ev => __tell('page error: ' + ((ev.error && ev.error.message) || ev.message)));
window.__diag = 'armed';
next();
</script>
`

func TestProjectsGhostDeepLinkBrowser(t *testing.T) {
	runPageInEngines(t, projectsGhostPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}

const projectsLeftRoutePage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="view-projects" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div>
<script type="module">
import { report } from '/__harness.js';
import { S } from '/state.js';
import { __sent, __reset } from '/ws.js';
import '/app.js';

const A = { id: 'alpha', name: 'Alpha', state: 'open', description: 'first', dir: '/p/alpha', focus: 'fa', active: true };
function fail(msg) { report('FAIL: ' + msg); }

__reset();
S.projects = [A];
S.activeProject = A;
S.viewedProject = null;
S.projTab = 'overview';
S.view = 'projects';
S.workspace = null; S.workspaceFor = '';
S.connected = true;
S.projectsPrimed = true;

location.hash = '#/projects/nonexistent-id';
window.dispatchEvent(new HashChangeEvent('hashchange'));
import('/views/projects.js').then(() => { location.hash = '#/projects/alpha'; });
window.dispatchEvent(new HashChangeEvent('hashchange'));

(function poll(waited) {
  const landed = !document.getElementById('ghost-banner') && location.hash === '#/projects/alpha' &&
    document.getElementById('proj-space').textContent.includes('Alpha');
  if (!landed && waited < 3000) { setTimeout(() => poll(waited + 20), 20); return; }
  if (location.hash !== '#/projects/alpha') { fail('a route the address had left put its own address back: ' + location.hash); return; }
  if (document.getElementById('ghost-banner')) { fail('a route the address had left put its banner back'); return; }
  if (!document.getElementById('proj-space').textContent.includes('Alpha')) { fail('the project the address names is not shown'); return; }
  const selects = __sent.filter(m => m.type === 'project' && m.project.action === 'select').map(m => m.project.id);
  if (selects.filter(id => id === 'nonexistent-id').length > 1) { fail('one ghost address was applied more than once: ' + selects.join(',')); return; }
  if (selects[selects.length - 1] !== 'alpha') { fail('the host was last told to select ' + selects[selects.length - 1] + ': ' + selects.join(',')); return; }
  report('OK');
})(0);
</script>
`

func TestAProjectRouteTheAddressHasLeftIsNotApplied(t *testing.T) {
	runPageInEngines(t, projectsLeftRoutePage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
