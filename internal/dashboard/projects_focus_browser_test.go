//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// projectsFocusPage drives renderProjects — the whole projects surface,
// not one tab — in a real engine, against the defect the operator hit:
// a new project was created and a focus edit aimed at it landed on the
// PREVIOUS project instead, one second apart in the two manifests.
//
// The cause was one variable answering two questions. S.activeProject
// meant both "what is on my screen" and "where is the identity
// working", so the page was ALWAYS the active project and every control
// on it carried the active project's id — while the operator believed
// they were looking at the one they had just made. Splitting viewed from
// active is what these assertions pin: looking is free and silent,
// working somewhere is one explicit act, and the two are never inferred
// from each other.
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

  // DEFAULT — nothing chosen yet follows the active project, so the
  // first paint is what it always was.
  fresh();
  renderProjects();
  assert(space.textContent.includes('Alpha'), 'default view is not the active project');
  assert(space.textContent.includes('working here'), 'active project does not say the identity is working here');

  // CLICK IS THE COMMITMENT — the operator's gesture selects. Viewing
  // beta (a bubble or home-card click) sends select; the server answers
  // with a fresh payload. Three reports (2026-08-27) read
  // click-as-choose; the browse-only page sent nothing, felt dead, and
  // left WORK empty because nothing had moved.
  fresh();
  viewProject('beta');
  assert(__sent.length === 1 && __sent[0].project.action === 'select' && __sent[0].project.id === 'beta',
    'viewing a project did not select it: ' + JSON.stringify(__sent));
  assert(S.activeProject.id === 'alpha', 'client state moved the identity before the server answered');

  // AND IT ACTUALLY MOVES THE PAGE — the other half of the same claim.
  assert(space.textContent.includes('Beta'), 'viewed project is not on screen');
  assert(space.textContent.includes('/p/beta'), 'page shows another project\'s directory');
  assert(!space.textContent.includes('/p/alpha'), 'active project\'s directory leaked onto the viewed page');

  // THE CONTROLS BELONG TO THE PAGE YOU SEE. The exact defect: while
  // viewing Beta, the focus editor must carry BETA's text and its save
  // must name BETA. Previously both were Alpha's.
  const ta = document.getElementById('focus-edit');
  assert(ta && ta.value === 'fb', 'focus editor holds the wrong project\'s text: ' + (ta && ta.value));
  __reset(); // the click-commits select has already been asserted; measure the save alone
  document.getElementById('focus-save').onclick();
  assert(__sent.length === 1, 'focus save did not send exactly one message');
  assert(__sent[0].project.action === 'update', 'focus save is not an update');
  assert(__sent[0].project.id === 'beta', 'focus edit aimed at the viewed project landed on ' + __sent[0].project.id);

  // WORKING IS STILL ONE ACT — viewProject performs it on the click
  // path; the button covers the page you landed on without selecting
  // (a restored viewedProject after restart, or a payload adoption).
  fresh();
  viewProject('beta');
  __reset();
  const wb = document.getElementById('proj-work');
  assert(wb, 'a non-active project offers no way to work in it');
  wb.onclick();
  assert(__sent.length === 1 && __sent[0].project.action === 'select' && __sent[0].project.id === 'beta',
    'the work button did not select the viewed project: ' + JSON.stringify(__sent));

  // THE NEW-PROJECT PAGE IS A VIEW, NOT A CLAIM. It used to null out
  // activeProject client-side — asserting locally that the identity had
  // left a project it was still in, until the next payload contradicted
  // it. The pill is server truth and must not move.
  fresh();
  renderProjects();
  document.getElementById('dock-new').onclick();
  renderProjPill(); // ws.js repaints the pill on every payload; do the same here
  assert(S.activeProject && S.activeProject.id === 'alpha', 'opening the new-project page dropped the active project');
  assert(document.getElementById('pill-proj').textContent.includes('Alpha'), 'the pill stopped naming the working project');
  assert(space.textContent.includes('NEW PROJECT'), 'the new-project page did not render');

  // THE DOCK SHOWS BOTH FACTS AT ONCE. Viewing Beta while working in
  // Alpha: two different bubbles, two different marks. When these were
  // one variable this state could not be drawn at all.
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

  // ADOPTION RULE — where a create lands the operator. Exactly one new
  // id is the answer; anything else is a guess and must decline.
  assert(newlyCreatedID(['alpha'], [A, B]) === 'beta', 'the one new project was not identified');
  assert(newlyCreatedID(['alpha', 'beta'], [A, B]) === '', 'no new project should yield no move');
  assert(newlyCreatedID([], [A, B]) === '', 'two new projects is ambiguous and must not move the view');
});
</script>`

// TestProjectsFocusInBrowser pins the viewed/active split in a real
// engine. Skips honestly when no engine is installed.
func TestProjectsFocusInBrowser(t *testing.T) {
	// Map DERIVED from the shared base (fork removed); the recording
	// socket is the assertion channel. Real state.js/util.js/projects.js
	// arrive via the rig fall-through.
	runPageInEngines(t, projectsFocusPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
