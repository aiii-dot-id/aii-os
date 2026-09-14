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

  // /, the work tab's half: when the server capped the session
  // listing, the overview count line DECLARES it. A flat count would
  // read as the project's whole record.
  stage.innerHTML = renderProjTab(p, { project: p, work_capped: true,
    work: [{ id: '7', description: 'polish', status: 'delivered', project: 'proj-x', result: 'served: x' }] }, 'overview');
  assert(stage.textContent.includes('capped'), 'work cap not declared');
  assert(stage.textContent.includes('older sessions live in the ledger'), 'capped line missing its pointer');

  // ESCAPING — a project directory holds whatever was put in it. A file
  // named like markup is TEXT.
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

// .
// .
// .
// .
func TestWorkspaceTabsInBrowser(t *testing.T) {
	// .
	// .
	// .
	// .
	// .
	// .
	runPageInEngines(t, workspacePage, overrideModuleStubs(
		"/state.js", []byte("export const S = { view: 'projects', projects: [], workspace: null, workspaceFor: '', projTab: 'overview', activeProject: null };\n"),
		"/app.js", stubModule("go"),
	))
}
