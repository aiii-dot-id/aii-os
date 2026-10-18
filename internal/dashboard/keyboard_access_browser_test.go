//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const fileViewerFocusPage = `<!doctype html>
<div id="proj-space"></div>
<div class="dock-wrap"><div class="dock-tools"><input type="search" id="dock-filter"></div><div class="dock" id="dock"></div></div>
<span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderProjects, closeFileViewer } from './views/projects.js';

run(() => {
  S.view = 'projects'; S.identityExists = true; S.dockFilter = '';
  S.projects = [{ id: 'proj-x', name: 'Proj X', state: 'open', dir: '/tmp/proj-x', description: '', focus: '' }];
  S.activeProject = S.projects[0]; S.viewedProject = 'proj-x'; S.projTab = 'files';
  S.workspace = { project: S.projects[0], work: [], files: [
    { name: 'notes.md', dir: false, size: 2048 }, { name: 'data', dir: true, size: 0 }] };
  S.workspaceFor = 'proj-x';
  window.fetch = () => new Promise(() => {});
  renderProjects();
  const opener = () => [...document.querySelectorAll('#proj-space .fl-file')].find(r => r.dataset.name === 'notes.md')?.querySelector('.fl-name');

  assert(opener() && opener().tagName === 'BUTTON' && opener().textContent === 'notes.md', 'a file row has no native button named by its file');
  assert(!document.querySelector('#proj-space .fl-dir button'), 'a directory row offers an action it does not have');

  opener().focus(); opener().click();
  const v = document.getElementById('file-viewer');
  assert(v && v.tagName === 'DIALOG' && v.open && v.matches(':modal'), 'the viewer is not a modal dialog');
  assert(v.getAttribute('aria-labelledby') === 'fv-title' && document.getElementById('fv-title').textContent === 'notes.md', 'the viewer is not named by its title');
  assert(v.contains(document.activeElement), 'focus did not move into the viewer: ' + document.activeElement.outerHTML.slice(0, 60));
  const behind = document.getElementById('fl-refresh'); behind.focus();
  assert(v.contains(document.activeElement), 'the page behind the viewer took focus');
  document.getElementById('file-viewer-close').click();
  assert(!document.getElementById('file-viewer'), 'Close did not close the viewer');
  assert(document.activeElement === opener(), 'focus did not return to the file that opened the viewer');

  const first = opener(); first.focus(); first.click(); renderProjects();
  assert(!first.isConnected && opener(), 'the refresh did not replace the row');
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  assert(!document.getElementById('file-viewer'), 'Escape did not close the viewer');
  assert(document.activeElement === opener(), 'focus did not reach the same file in the refreshed list');

  opener().focus(); opener().click();
  S.workspace.files = S.workspace.files.filter(f => f.name !== 'notes.md'); renderProjects();
  closeFileViewer();
  assert(document.activeElement === document.getElementById('fl-refresh'), 'focus was lost with the file');
});
</script>`

func TestTheFileViewerIsAModalThatGivesFocusBack(t *testing.T) {
	runPageInEngines(t, fileViewerFocusPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
		"/state.js", []byte("export const S = { view:'projects', projects:[], activeProject:null, viewedProject:null, dockFilter:'', identityExists:true, stats:{}, workspace:null, workspaceFor:'', projTab:'overview', focusDraft:null };"),
		"/util.js", utilStub("export const $ = id => document.getElementById(id); export const esc = value => String(value ?? ''); export const hueOf = id => 200;"),
	))
}

func TestAProjectCardIsANativeButton(t *testing.T) {
	page := `<!doctype html><div id="home-inner"></div>
<script type="module">
import { assert, run } from '/__harness.js';
import { S } from '/state.js';
import { renderHome } from '/views/home.js';
import { opened } from '/views/projects.js';
run(() => {
  S.connected = true; S.identityExists = true; S.stats = { name: 'Fixture' };
  S.projects = [{ id: 'p1', name: 'Product strategy', description: 'Q3', state: 'open', active: true, focus: 'positioning' }];
  renderHome();
  const card = document.querySelector('#home-inner .proj-card[data-id="p1"]');
  const open = card && card.querySelector('.proj-open');
  assert(open && open.tagName === 'BUTTON' && open.textContent === 'Product strategy', 'the card has no native button named by its project');
  open.focus();
  assert(document.activeElement === open, 'the card cannot take focus');
  open.click();
  assert(opened.length === 1 && opened[0] === 'p1', 'pressing the card did not open its project once: ' + opened.join(','));
});
</script>`
	modules := browserModuleStubs()
	for _, path := range []string{"static/views/home.js", "static/state.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/views/projects.js"] = []byte(`export const opened = []; export function viewProject(id) { opened.push(id); }`)
	modules["/views/work.js"] = []byte(`export function workSectionHTML() { return ''; } export function wireWork() {}`)
	modules["/views/identity.js"] = []byte(`export function renderIdentity() {}`)
	modules["/app.js"] = []byte(`export function go() {}`)
	modules["/views/backups.js"] = []byte(`export function requestBackups() {}`)
	modules["/views/settings.js"] = []byte(`export function openBackups() {}`)
	runPageInEngines(t, page, modules)
}
