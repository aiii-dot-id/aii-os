//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// projectsDeliveredPage drives the "Outside any project" list in a real
// engine.
//
// The defect this pins (host disposition 12.1, a review item 2): the Delivered
// list lived only inside Home's interim work section, which retires. A
// project-only list loses every delivered result that recorded no project —
// and loses its grading controls with it, so the operator can neither see
// nor grade an unassigned result. The fix moves the list to the Projects
// page, with "Outside any project" as a first-class heading that appears
// even when there are no projects at all.
//
// Two further hazards are pinned because they look right while being wrong:
// a result's project is the one IT RECORDED, never whichever project is
// active or viewed; and grade state must have exactly ONE owner, so a
// second surface cannot hold a draft the operator can no longer see.
const projectsDeliveredPage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { run, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderProjects } from '/views/projects.js';
import { __reset } from '/ws.js';

function fresh(work, projects) {
  __reset();
  S.projects = projects || [];
  S.activeProject = null;
  S.viewedProject = '';
  S.projTab = 'overview';
  S.view = 'projects';
  S.contractDraft = null;
  S.workspace = null; S.workspaceFor = '';
  S.work = work;
}

// The list's real host: projects.js renders the no-project branch with
// <div id="projects-delivered"> and calls renderDelivered into it.
const list = () => document.getElementById('projects-delivered');

run(() => {
  // ---- NO PROJECTS AT ALL, ONE UNASSIGNED RESULT ----
  // The case the disposition names: with zero projects the page must still
  // show the result under "Outside any project", with grade controls.
  fresh({ live: [], queued: 0, delivered: [
    { id: 'w1', description: 'an unassigned result', project: '', at: '2026-10-31T20:00:00Z' },
  ] }, []);
  renderProjects();
  const l1 = list();
  assert(l1, 'the delivered list is not reachable with zero projects');
  const t1 = l1.textContent;
  assert(/Outside any project/i.test(t1), 'the "Outside any project" heading is missing');
  assert(t1.includes('an unassigned result'), 'the unassigned result is not shown');
  const chips1 = l1.querySelectorAll('[data-grade-work="w1"]');
  assert(chips1.length === 3,
    'an ungraded result must offer three grade chips (served/partly/not served), found ' + chips1.length);

  // ---- A RESULT BELONGS TO THE PROJECT IT RECORDED ----
  // S.viewedProject is alpha; the result recorded beta. It must group under
  // beta, and must NOT appear under Outside.
  fresh({ live: [], queued: 0, delivered: [
    { id: 'w2', description: 'a beta result', project: 'beta', at: '2026-10-31T20:01:00Z' },
  ] }, [
    { id: 'alpha', name: 'Alpha', state: 'open', dir: '/p/alpha', active: true },
    { id: 'beta', name: 'Beta', state: 'open', dir: '/p/beta', active: false },
  ]);
  S.viewedProject = 'alpha';
  renderProjects();
  const l2 = list();
  assert(l2, 'the delivered list vanished when projects exist');
  const t2 = l2.textContent;
  assert(t2.includes('a beta result'), 'a delivered result is missing');
  assert(!/Outside any project/i.test(t2),
    'a result that recorded a project was filed under Outside any project');
  const betaGroup = l2.querySelector('[data-delivered-group="beta"]');
  assert(betaGroup && betaGroup.textContent.includes('a beta result'),
    'a result was not filed under the project it recorded');
  const alphaGroup = l2.querySelector('[data-delivered-group="alpha"]');
  assert(!alphaGroup || !alphaGroup.textContent.includes('a beta result'),
    'a result was filed under the viewed project instead of the one it recorded');

  // ---- ONE OWNER OF GRADE STATE ----
  // Exactly one grade control surface for a session across the whole page.
  // A second surface holding a copy is the hazard the disposition names.
  fresh({ live: [], queued: 0, delivered: [
    { id: 'w3', description: 'a graded result', project: '', at: '2026-10-31T20:02:00Z' },
  ] }, []);
  renderProjects();
  const controls = document.querySelectorAll('[data-grade-work="w3"]');
  assert(controls.length === 3,
    'expected exactly three grade chips for one result across the page, found ' + controls.length);

  // ---- A RECORDED GRADE IS SHOWN, NOT ASKED FOR AGAIN ----
  fresh({ live: [], queued: 0, delivered: [
    { id: 'w4', description: 'a served result', project: '', at: '2026-10-31T20:03:00Z',
      grade: { grade: 'served', comment: '' } },
  ] }, []);
  renderProjects();
  const l4 = list();
  assert(l4, 'the delivered list is missing');
  assert(/Served/i.test(l4.textContent), 'a recorded grade is not shown');
  assert(!l4.querySelector('[data-grade-work="w4"]'),
    'a result already graded is still asking to be graded');

  // ---- ABSENT WORK DOES NOT CRASH AND INVENTS NO ROWS ----
  fresh(null, []);
  renderProjects();
  const l5 = list();
  assert(!l5 || !l5.querySelector('[data-work]'),
    'an absent work state invented delivered rows');
});
</script>`

// TestDeliveredListIsReachableOutsideAnyProject pins the unassigned delivered
// list, its grade controls, recorded-project grouping and single ownership
// against a real engine.
func TestDeliveredListIsReachableOutsideAnyProject(t *testing.T) {
	runPageInEngines(t, projectsDeliveredPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
