//go:build !windows

package dashboard

import (
	"testing"
)

const projectsFocusPage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { run, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderProjects, renderProjPill, viewProject, newlyCreatedID } from '/views/projects.js';
import { __sent, __queried, __reset, wake } from '/ws.js';

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
}

run(() => {
  const space = document.getElementById('proj-space');

  fresh();
  renderProjects();
  assert(space.textContent.includes('Alpha'), 'default view is not the active project');
  assert(space.textContent.includes('working here'), 'active project does not say the identity is working here');
  const leave = document.getElementById('proj-leave');
  assert(leave, 'active project offers no way to work outside projects');
  leave.onclick();
  assert(__sent.length === 1 && __sent[0].project.action === 'deselect',
    'leave-project control did not send deselect: ' + JSON.stringify(__sent));

  fresh();
  viewProject('beta');
  assert(__sent.length === 1 && __sent[0].project.action === 'select' && __sent[0].project.id === 'beta',
    'viewing a project did not select it: ' + JSON.stringify(__sent));
  assert(S.activeProject.id === 'alpha', 'client state moved the identity before the server answered');

  assert(space.textContent.includes('Beta'), 'viewed project is not on screen');
  assert(space.textContent.includes('/p/beta'), 'page shows another project\'s directory');
  assert(!space.textContent.includes('/p/alpha'), 'active project\'s directory leaked onto the viewed page');

  const ta = document.getElementById('focus-edit');
  assert(ta && ta.value === 'fb', 'focus editor holds the wrong project\'s text: ' + (ta && ta.value));
  __reset(); // the click-commits select has already been asserted; measure the save alone
  document.getElementById('focus-save').onclick();
  assert(__sent.length === 1, 'focus save did not send exactly one message');
  assert(__sent[0].project.action === 'update', 'focus save is not an update');
  assert(__sent[0].project.id === 'beta', 'focus edit aimed at the viewed project landed on ' + __sent[0].project.id);

  fresh();
  viewProject('beta');
  __reset();
  const wb = document.getElementById('proj-work');
  assert(wb, 'a non-active project offers no way to work in it');
  wb.onclick();
  assert(__sent.length === 1 && __sent[0].project.action === 'select' && __sent[0].project.id === 'beta',
    'the work button did not select the viewed project: ' + JSON.stringify(__sent));

  fresh();
  renderProjects();
  document.getElementById('dock-new').onclick();
  renderProjPill(); // ws.js repaints the pill on every payload; do the same here
  assert(S.activeProject && S.activeProject.id === 'alpha', 'opening the new-project page dropped the active project');
  assert(document.getElementById('pill-proj').textContent.includes('Alpha'), 'the pill stopped naming the working project');
  assert(space.textContent.includes('NEW PROJECT'), 'the new-project page did not render');

  fresh();
  viewProject('beta');
  const active = document.querySelectorAll('#dock .bubble.active');
  const viewing = document.querySelectorAll('#dock .bubble.viewing');
  assert(active.length === 1 && viewing.length === 1, 'dock does not mark exactly one active and one viewed bubble');
  assert(!active[0].classList.contains('viewing'), 'the active and viewed marks landed on the same bubble');

  /* WAKE-ON-RETURN (§8.10 residual, closed): a tab at the cap is no
     longer dead to the user. The online and visibilitychange events
     fire an immediate reconnect with backoff state preserved. */
  assert(typeof wake === 'function', 'ws.js does not export wake');

  assert(newlyCreatedID(['alpha'], [A, B]) === 'beta', 'the one new project was not identified');
  assert(newlyCreatedID(['alpha', 'beta'], [A, B]) === '', 'no new project should yield no move');
  assert(newlyCreatedID([], [A, B]) === '', 'two new projects is ambiguous and must not move the view');
});
</script>`

func TestProjectsFocusInBrowser(t *testing.T) {
	runPageInEngines(t, projectsFocusPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
