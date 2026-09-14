//go:build !windows

// .
// .

package dashboard

import (
	"strings"
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

const projectFilesOpenPage = `<!doctype html>
<div id="proj-space"></div>
<div class="dock-wrap"><div class="dock-tools"><input type="search" id="dock-filter"></div><div class="dock" id="dock"></div></div>
<span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderProjects } from './views/projects.js';

run(() => {
  S.view = 'projects';
  S.projects = [
    { id: 'proj-x', name: 'Proj X', state: 'open', dir: '/tmp/proj-x',
      description: 'a workroom', focus: 'shipping' }
  ];
  S.activeProject = S.projects[0];
  S.viewedProject = 'proj-x';
  S.projTab = 'files';
  S.identityExists = true;
  S.dockFilter = '';
  S.workspace = {
    project: S.projects[0],
    files: [
      { name: 'notes.md', dir: false, size: 2048 },
      { name: 'shot.png', dir: false, size: 40960 },
      { name: 'data', dir: true, size: 0 }
    ],
    work: []
  };
  S.workspaceFor = 'proj-x';
  renderProjects();

  // THE CLICK TARGET — the HITL's whole complaint. A row without
  // affordances is a listing; this pins the viewer surface.
  const rows = document.querySelectorAll('#proj-space .fl-file');
  assert(rows.length === 2, 'clickable file rows not rendered (got ' + rows.length + ')');
  const row = rows[0];
  assert(row.dataset.name === 'notes.md', 'row carries no file name');
  assert(typeof row.onclick === 'function', 'file row has no click handler');

  // THE VIEWER — click opens the overlay. The fetch stub returns a
  // SYNCHRONOUS thenable (then() invokes inline), so the real
  // aiiOpenFile chain completes during onclick() and the whole
  // round-trip is observable without timers — the rig's run() is
  // synchronous-only and reports at body return.
  // A SYNCHRONOUS thenable with real-promise semantics where they
  // matter here: then(cb) runs cb inline AND FLATTENS — if cb returns
  // a thenable (aiiOpenFile returns r.text().then(...) from its then-callback), that
  // thenable becomes the chain head, so its own callback ran inline
  // too. Without flattening the nested callback never executes and
  // the body never lands.
  const syncThen = (v) => ({
    then: (cb) => {
      if (!cb) return syncThen(v);
      const out = cb(v);
      return (out && typeof out.then === 'function') ? out : syncThen(out);
    },
    catch: () => syncThen(v)
  });
  let asked = [];
  const realFetch = window.fetch;
  window.fetch = function (url, init) {
    const u = String(url);
    if (u.indexOf('/__result') === 0 || u.indexOf('/__harness.js') === 0) {
      return realFetch(url, init);
    }
    asked.push(u);
    const png = u.endsWith('.png');
    return syncThen({
      ok: true,
      headers: { get: () => png ? 'image/png' : 'text/plain; charset=utf-8' },
      text: () => syncThen('hello-body')
    });
  };
  row.onclick();
  const viewer = document.getElementById('file-viewer');
  assert(viewer, 'viewer overlay not in the document after click');
  assert(document.getElementById('fv-title').textContent === 'notes.md', 'viewer title wrong');
  assert(asked.length === 1 && asked[0] === '/p/proj-x/notes.md',
    'fetch URL wrong or unencoded: ' + asked.join(','));
  assert(document.getElementById('fv-body').textContent === 'hello-body',
    'viewer body missing fetched text');

  // IMAGE BRANCH — image types render an img, never innerHTML'd text.
  const rows2 = document.querySelectorAll('#proj-space .fl-file');
  rows2[1].onclick();
  assert(document.querySelector('#file-viewer img'), 'image type must render an img element');

  // ESCAPE CLOSES — the keydown listener aiiOpenFile installs runs
  // synchronously under a dispatched event; no timers needed.
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  assert(!document.getElementById('file-viewer'), 'Escape did not close the viewer');
});
</script>
`

func TestProjectFilesClickToOpen(t *testing.T) {
	// .
	// .
	// .
	// .
	runPageInEngines(t, projectFilesOpenPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
		"/state.js", []byte("export const S = { view:'projects', projects:[], activeProject:null, viewedProject:null, dockFilter:'', identityExists:true, stats:{}, workspace:null, workspaceFor:'', projTab:'overview', focusDraft:null };"),
		"/util.js", []byte("export const $ = id => document.getElementById(id); export const esc = value => String(value ?? ''); export const hueOf = id => 200;"),
	))

	// .
	// .
	// .
	// .
	// .
	cssBytes, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal("layout.css missing from staticFS:", err)
	}
	if !strings.Contains(string(cssBytes), ".ws-table.fl-open .fl-file { cursor:pointer") {
		t.Fatal("layout.css lost the .fl-file cursor:pointer affordance rule")
	}
}

const projectFilesViewerRacePage = `<!doctype html>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { report, assert } from '/__harness.js';
import { aiiOpenFile } from '/views/projects.js';

const pending = {};
window.fetch = url => new Promise(resolve => { pending[String(url)] = resolve; });
aiiOpenFile('p', 'old.md');
aiiOpenFile('p', 'new.md');

pending['/p/p/new.md']({ ok:true, headers:{get:()=> 'text/plain'}, text:()=>Promise.resolve('NEW') });
setTimeout(() => {
  pending['/p/p/old.md']({ ok:true, headers:{get:()=> 'text/plain'}, text:()=>Promise.resolve('OLD') });
  setTimeout(() => {
    try {
      assert(document.getElementById('fv-title').textContent === 'new.md', 'new viewer was replaced');
      assert(document.getElementById('fv-body').textContent === 'NEW', 'late old fetch overwrote the new viewer');
      report('OK');
    } catch (e) { report('FAIL: ' + e.message); }
  }, 20);
}, 20);
</script>`

func TestProjectFileLateFetchCannotOverwriteNewViewer(t *testing.T) {
	runPageInEngines(t, projectFilesViewerRacePage, overrideModuleStubs(
		"/util.js", []byte("export const $ = id => document.getElementById(id); export const esc = value => String(value ?? ''); export const hueOf = id => 200;"),
	))
}
