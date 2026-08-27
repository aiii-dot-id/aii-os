//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// workspacePage drives renderProjTab — the three-surface workspace
// renderer (docs/DESIGN-PROJECT-WORKSPACE.md §7) — in a real engine.
// A syntax check proves a module parses; it says nothing about what
// the operator sees, and the two defects this test pins were both
// invisible to parsing: a verdict the projection carried and the view
// dropped, and a hostile file name reaching innerHTML.
const workspacePage = `<!doctype html>
<div id="stage"></div>
<script type="module">
import { run, assert } from '/__harness.js';
import { renderProjTab } from '/views/projects.js';
run(() => {
  const p = { id: 'proj-x', name: 'Proj X', state: 'open', focus: 'shipping',
              description: 'a workroom', dir: '/tmp/proj-x' };
  const ws = {
    project: p,
    files: [{ name: 'notes.md', dir: false, size: 2048 }, { name: 'data', dir: true, size: 0 }],
    work: [{ id: '7', description: 'polish', status: 'delivered', project: 'proj-x',
             result: 'served: widget polished' },
           { id: '8', description: 'in flight', status: 'active', project: 'proj-x', result: '' }]
  };
  const stage = document.getElementById('stage');

  // FILES — the directory is the truth: names listed, sizes formatted,
  // a directory sized with a dash rather than a fictional byte count.
  stage.innerHTML = renderProjTab(p, ws, 'files');
  assert(stage.textContent.includes('notes.md'), 'file name not rendered');
  assert(stage.textContent.includes('2.0 KB'), 'file size not formatted');
  assert(stage.textContent.includes('\u2014'), 'directory size not dashed');

  // WORK — the verdict is the reason this tab exists. A status chip
  // alone is not the outcome; a session without one must SAY it has
  // none rather than render an empty slot.
  stage.innerHTML = renderProjTab(p, ws, 'work');
  assert(stage.textContent.includes('served: widget polished'), 'verdict not rendered');
  assert(stage.textContent.includes('delivered'), 'status not rendered');
  assert(stage.textContent.includes('no outcome recorded yet'), 'absent verdict not declared');

  // NULL SNAPSHOT — an explicit loading state, never invented rows.
  stage.innerHTML = renderProjTab(p, null, 'files');
  assert(!stage.querySelector('.ws-table'), 'table rendered without data');
  assert(stage.textContent.includes('Loading'), 'loading state not declared');
  stage.innerHTML = renderProjTab(p, null, 'work');
  assert(!stage.querySelector('.ws-item'), 'work rows rendered without data');

  // EMPTY, not ABSENT — an empty directory says so.
  stage.innerHTML = renderProjTab(p, { project: p, files: [], work: [] }, 'files');
  assert(stage.textContent.includes('empty'), 'empty directory not declared');

  // ESCAPING — a project directory holds whatever was put in it. A file
  // named like markup is TEXT (DESIGN-PROJECT-WORKSPACE.md §6).
  stage.innerHTML = renderProjTab(p, {
    project: p,
    files: [{ name: '<img src=x onerror="window.__pwn=1">', dir: false, size: 1 }],
    work: [{ id: '9', description: '<b>bold</b>', status: 'active',
             project: 'proj-x', result: '<script>window.__pwn=1<\/script>' }]
  }, 'files');
  assert(!stage.querySelector('img'), 'hostile file name became markup');
  assert(stage.textContent.includes('<img src=x'), 'file name not rendered as text');
  stage.innerHTML = renderProjTab(p, {
    project: p,
    work: [{ id: '9', description: '<b>bold</b>', status: 'active',
             project: 'proj-x', result: '<i>verdict</i>' }]
  }, 'work');
  assert(!stage.querySelector('b') && !stage.querySelector('i'), 'work strings became markup');
  assert(!window.__pwn, 'injected script executed');
});
</script>`

// TestWorkspaceTabsInBrowser is the functional-UI half of the
// workspace proof: the seam test (TestWorkspaceQuerySeam) pins what
// crosses the wire, this pins what the operator is shown when it
// arrives. Skips honestly when no engine is installed.
func TestWorkspaceTabsInBrowser(t *testing.T) {
	// The module map is DERIVED (browserModuleStubs + overrides); the
	// real projects.js and util.js arrive via the rig fall-through.
	// This test drives renderProjTab only — no router, no socket — so
	// app.js is overridden inert (the cluster-test precedent: real
	// app.js boots the router and needs DOM this page doesn't carry)
	// and state.js carries just the fields renderProjTab reads.
	runPageInEngines(t, workspacePage, overrideModuleStubs(
		"/state.js", []byte("export const S = { view: 'projects', projects: [], workspace: null, workspaceFor: '', projTab: 'overview', activeProject: null };\n"),
		"/app.js", stubModule("go"),
	))
}
