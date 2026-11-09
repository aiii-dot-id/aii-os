//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// CONTINUE WITH {NAME} TAKES HOME THE PROJECT THE HOST SELECTED (spec 9c,
// "Resume a project"; step 12b), over the real ws.js and a fake socket. A
// press on a project not being worked in asks the host to select it and
// waits, saying so; an answer to another request leaves the page where it
// is; its own answer, with the project active, goes to the conversation. A refusal frees the
// button and goes nowhere; an answer that arrives after the reader left the
// project goes nowhere; a press with no socket sends nothing and says so;
// and a project already being worked in goes to the conversation at once.
func TestContinueWithThemGoesToTheConversationOnlyOnTheHostsSelection(t *testing.T) {
	page := substratePageMarkup + `<section id="view-projects"><div id="proj-space"></div><input id="dock-filter"><div id="dock"></div></section><span id="pill-proj"></span>
<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { went } from './app.js';
import { assert, run } from './__harness.js';
const projects = await import('./views/projects.js');
{ const { offer } = await import('/shell/later.js'); offer(projects); }
run(() => {
 const sockets = [];
 window.setTimeout = () => 1; window.clearTimeout = () => {};
 window.WebSocket = class {
   constructor() { this.readyState = 0; this.frames = []; sockets.push(this); }
   send(bytes) { this.frames.push(JSON.parse(bytes)); }
   recv(frame) { this.onmessage({ data: JSON.stringify(frame) }); }
 };
 S.view = 'projects'; S.identityExists = true; S.stats = { name: 'Taylor' }; S.projects = []; S.projectsPrimed = true;
 connect(); const socket = sockets.at(-1); socket.readyState = 1; socket.onopen();
 const list = active => [{ id: 'alpha', name: 'Alpha', state: 'open', active: active === 'alpha', dir: '/p/alpha', contract: {} },
   { id: 'beta', name: 'Beta', state: 'open', active: active === 'beta', dir: '/p/beta', contract: {} }];
 const button = () => document.getElementById('proj-work'), said = () => document.getElementById('proj-continue-said').textContent;
 const view = id => { S.view = 'projects'; S.viewedProject = id; projects.renderProjects(); };
 const homes = () => went.filter(v => v === 'chat').length;
 const acts = () => socket.frames.filter(f => f.type === 'project'), lastAct = () => acts().at(-1);
 socket.recv({ type: 'projects', projects: list('beta') });
 view('alpha');
 assert(button().textContent === 'Continue with Taylor', 'the button: ' + button().textContent);
 button().click(); const ask = lastAct();
 assert(ask.type === 'project' && ask.project.action === 'select' && ask.project.id === 'alpha', 'Continue did not ask the host to select it: ' + JSON.stringify(ask));
 assert(said() === 'Waiting for AII OS to confirm…' && homes() === 0, 'Continue did not wait for the host: "' + said() + '", went ' + went.join());
 socket.recv({ type: 'projects', request_id: 'foreign', projects: list('beta') });
 assert(homes() === 0, 'an answer to another request took the page to the conversation');
 socket.recv({ type: 'projects', request_id: ask.request_id, projects: list('alpha') });
 assert(homes() === 1, 'its own answer, with the project selected, did not go to the conversation: ' + went.join());

 // A refusal frees the button and goes nowhere.
 view('beta'); button().click(); const refused = lastAct();
 socket.recv({ type: 'error', request_id: refused.request_id, message: 'fixture: not selected' });
 assert(homes() === 1 && said() === '' && !button().disabled, 'a refusal went to the conversation or held the button: "' + said() + '"');
 // An answer that arrives after the reader left the project goes nowhere.
 button().click(); const left = lastAct();
 view('alpha');
 socket.recv({ type: 'projects', request_id: left.request_id, projects: list('beta') });
 assert(homes() === 1, 'AN ANSWER THAT CAME AFTER THE READER LEFT TOOK THE PAGE HOME');
 // Already being worked in: the conversation at once, nothing asked.
 view('beta'); const asked = acts().length; button().click();
 assert(homes() === 2 && acts().length === asked, 'a project already being worked in did not go to the conversation at once, or asked again');
 // No socket: nothing sent, and said.
 socket.readyState = 3; socket.onclose();
 view('alpha'); const before = acts().length; button().click();
 assert(acts().length === before && said() === 'Not connected. Nothing was sent.' && homes() === 2, 'a press with no socket: "' + said() + '"');
});
</script>`
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(ws, []byte(`export const S={providers:[],projects:[]};`))
	for _, file := range []string{"views/projects.js", "util.js"} {
		data, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = data
	}
	app := string(modules["/app.js"])
	if !strings.Contains(app, "export const go = () => {};") {
		t.Fatal("rig: the socket rig's app.js has no go stub to record")
	}
	modules["/app.js"] = []byte(strings.Replace(app, "export const go = () => {};", "export const went = []; export const go = v => { went.push(v); };", 1))
	runPageInEngines(t, page, modules)
}

// A WORK SESSION'S ID IS NEVER SAID AS EVIDENCE (step 12b; a review 2026-11-01
// 17:04Z point 1). A claim whose evidence is a work session says it as that
// session, by its own description where this page holds it, and as "a work
// session" where it does not; any other reference is said as it is.
func TestAWorkSessionIsSaidAsTheSessionNotItsID(t *testing.T) {
	page := `<!doctype html>
<div id="stage"></div>
<script type="module">
import { run, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderProjTab } from '/views/projects.js';
run(() => {
  S.workspace = { work: [{ id: 'ws_0f3a', description: 'Draft sections 1, 2 and 4' }] }; S.workspaceFor = 'p';
  const p = { id: 'p', name: 'P', state: 'open', dir: '/p', contract: {}, progress: { has_criteria: true, counts: { supported: 3 }, next_index: -1, items: [
    { index: 0, text: 'one', state: 'supported', class: 'completed_locally', ref: 'ws_0f3a' },
    { index: 1, text: 'two', state: 'supported', class: 'completed_locally', ref: 'ws_ffff' },
    { index: 2, text: 'three', state: 'supported', class: 'completed_locally', ref: 'draft.md' } ] } };
  const stage = document.getElementById('stage');
  stage.innerHTML = renderProjTab(p, S.workspace, 'overview');
  const why = [...stage.querySelectorAll('.prog-why')].map(e => e.textContent);
  assert(why[0].startsWith('Evidence: the work session “Draft sections 1, 2 and 4”') && why[1].startsWith('Evidence: a work session') && why[2].startsWith('Evidence: draft.md'), 'the evidence lines: ' + JSON.stringify(why));
  assert(!stage.textContent.includes('ws_'), 'A WORK SESSION\'S ID WAS SAID AS EVIDENCE: ' + stage.textContent);
});
</script>`
	runPageInEngines(t, page, overrideModuleStubs(
		"/state.js", []byte("export const S = { view: 'projects', projects: [], workspace: null, workspaceFor: '', activeProject: null, identityExists: true, stats: { name: 'Taylor' } };\n"),
		"/app.js", stubModule("go"),
	))
}
