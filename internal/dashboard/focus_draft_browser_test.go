//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

/* A broadcast re-render must not eat a focus note mid-word.

   renderProjects used to rebuild the page from the SAVED manifest on
   every incoming projects message — an edit in progress was replaced
   by the saved text and the keystrokes were gone (report 2026-08-27:
   the Projects surface "very slow and unresponsive", edits lost).
   The render is now skipped while the operator is typing in
   #focus-edit, and the draft lives in S.focusDraft keyed to the
   project so a later render restores rather than erases it.

   This drives the REAL views/projects.js against a recording socket
   stub. The keystrokes survive; the save still sends the draft; the
   ack (a projects payload wearing our request id) clears the draft. */

const focusDraftPage = `<!doctype html>
<div id="proj-space"></div>
<div id="dock"></div>
<span id="pill-proj"></span>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderProjects } from './views/projects.js';

run(() => {
  S.view = 'projects';
  S.projects = [{ id: 'alpha', name: 'Alpha', state: 'open', active: true, focus: 'the saved focus' }];
  S.activeProject = S.projects[0];
  S.viewedProject = 'alpha';
  S.projTab = 'overview';
  S.identityExists = true;
  renderProjects();

  const ta = document.getElementById('focus-edit');
  assert(ta, 'focus textarea missing');
  assert(ta.value === 'the saved focus', 'textarea did not seed from manifest: ' + ta.value);

  // The operator types. Focus the textarea and change its value.
  ta.focus();
  ta.value = 'a focus note typed mid-wor';
  assert(document.activeElement === ta, 'textarea is not focused — guard reads this');

  // A broadcast re-render — the ws.js projects case calls renderProjects
  // on every payload. The guard must skip the rebuild.
  renderProjects();

  const after = document.getElementById('focus-edit');
  assert(after, 'textarea vanished on broadcast re-render');
  assert(after.value === 'a focus note typed mid-wor',
    'BROADCAST ATE THE DRAFT: value is "' + after.value + '" — the render clobbered a live edit');

  // The draft survives a full re-render (e.g. tab switch away and back).
  after.blur();
  S.projTab = 'files';
  renderProjects();
  S.projTab = 'overview';
  renderProjects();
  const restored = document.getElementById('focus-edit');
  assert(restored && restored.value === 'a focus note typed mid-wor',
    'draft did not survive re-render: "' + (restored ? restored.value : 'NO TEXTAREA') + '"');
});
</script>
`

func TestFocusDraftSurvivesBroadcast(t *testing.T) {
	projJS, err := staticFS.ReadFile("static/views/projects.js")
	if err != nil {
		t.Fatal(err)
	}
	stub := "export const S = { view: '', projects: [], activeProject: null, viewedProject: null, projTab: '', identityExists: false, pendingCreate: null, pendingFocusSave: null, focusDraft: null, projectsPrimed: false };"
	util := "export const $ = id => document.getElementById(id); export const esc = value => String(value ?? ''); export const hueOf = id => 200;"
	// projects.js imports ws.js (send/query) and app.js (go); recording
	// stubs so the module graph resolves without a live socket.
	wsStub := "export const send = () => ''; export const query = () => '';"
	appStub := "export const go = () => {};"
	runPageInEngines(t, focusDraftPage, map[string][]byte{
		"/views/projects.js": projJS,
		"/state.js":          []byte(stub),
		"/util.js":           []byte(util),
		"/ws.js":             []byte(wsStub),
		"/app.js":            []byte(appStub),
	})
}
