//go:build !windows

package dashboard

import "testing"

// projectsHeldJS holds the shipped Projects, served beside it, from arriving
// until the page lets it.
const projectsHeldJS = `export * from './projects-real.js';
await new Promise(r => { window.__releaseProjects = r; });
`

func servedProjectsHeld(t *testing.T) map[string][]byte {
	t.Helper()
	real, err := staticFS.ReadFile("static/views/projects.js")
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"/views/projects.js": []byte(projectsHeldJS), "/views/projects-real.js": real}
}

const projectsPageHead = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const fetched = path => performance.getEntriesByType('resource').some(e => new URL(e.name).pathname === path);
const text = id => ((document.getElementById(id) || {}).textContent || '');
const A = { id: 'alpha', name: 'Alpha', state: 'open', description: 'first', dir: '/p/alpha', focus: 'fa', active: true };
const B = { id: 'beta', name: 'Beta', state: 'open', description: 'second', dir: '/p/beta', focus: 'fb', active: false };
const status = { type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } };
`

// PROJECTS IS FETCHED WHEN FIRST OPENED, AND HEARS ITS FRAMES ONCE IT IS IN
// (step 12a). The shipped page, in every engine, with Projects held from
// arriving until the page lets it. Home opens without Projects fetched, and
// the line naming the project worked in is drawn from the first frame: it is
// shell. Opened, Projects is fetched and draws nothing until it is in; a
// frame that arrived while it was on its way is drawn when it is in. Once in,
// a frame reaches it through later() and draws, and the host's answer to a
// project it created reaches it the same way. Opened again, it draws in the
// step that opens it.
const projectsFetchedJS = projectsPageHead + `
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say(status);
  await until(() => S.view === 'chat', 4000);
  sockets[0].say({ type: 'projects', projects: [A, B] });
  // waits by design: an absence is read, after Home has had the time to
  // fetch what it would.
  await new Promise(r => setTimeout(r, 300));
  check(!fetched('/views/projects.js') && !fetched('/views/projects-real.js'), 'Home fetched Projects: ' + performance.getEntriesByType('resource').map(e => new URL(e.name).pathname).filter(p => p.startsWith('/views/')).join(', '));
  check(!(S.later && S.later.renderProjects), 'Projects was in before it was opened');
  check(text('reply-project').includes('Alpha'), 'the line naming the project worked in was not drawn before Projects was in: ' + JSON.stringify(text('reply-project')));
  location.hash = '#/projects';
  await until(() => typeof window.__releaseProjects === 'function', 6000);
  check(fetched('/views/projects.js'), 'Projects was not fetched when it was opened');
  sockets[0].say({ type: 'projects', projects: [{ ...A, active: false }, { ...B, active: true }] });
  check(text('reply-project').includes('Beta'), 'the line did not follow a frame while Projects was on its way: ' + JSON.stringify(text('reply-project')));
  check(text('proj-space') === '', 'Projects drew before it was in: ' + JSON.stringify(text('proj-space').slice(0, 80)));
  window.__releaseProjects();
  await until(() => text('proj-space').includes('/p/beta'), 6000);
  check(text('proj-space').includes('/p/beta'), 'the frame that arrived while Projects was on its way was not drawn once it was in: ' + JSON.stringify(text('proj-space').slice(0, 120)));
  check(S.later && typeof S.later.renderProjects === 'function' && typeof S.later.acceptCreate === 'function', 'what Projects exports was not offered to the socket');
  // Once in, a frame reaches it through later().
  sockets[0].say({ type: 'projects', projects: [A, B] });
  await until(() => text('proj-space').includes('/p/alpha'), 3000);
  check(text('proj-space').includes('/p/alpha'), 'a frame after Projects was in did not reach it: ' + JSON.stringify(text('proj-space').slice(0, 120)));
  // And so does the host's answer to a project it created.
  sockets[0].say({ type: 'projects', projects: [{ ...A, active: false }, B] });
  await until(() => document.getElementById('np-name'), 3000);
  const name = document.getElementById('np-name');
  check(name, 'with no project worked in, Projects offered no new project');
  if (name) {
    name.value = 'Delta';
    document.getElementById('np-create').click();
    const ask = sent.filter(f => f.type === 'project' && f.project && f.project.action === 'create').at(-1);
    check(ask && ask.request_id, 'the new project was not asked for: ' + JSON.stringify(sent.slice(-2)));
    if (ask) {
      const D = { id: 'delta', name: 'Delta', state: 'open', description: '', dir: '/p/delta', focus: '', active: false };
      sockets[0].say({ type: 'projects', request_id: ask.request_id, projects: [{ ...A, active: false }, B, D] });
      await until(() => text('proj-space').includes('/p/delta'), 3000);
      check(S.viewedProject === 'delta' && text('proj-space').includes('/p/delta'), "the host's answer to a created project did not reach Projects: viewed " + S.viewedProject);
    }
  }
  // Once in, it draws at once, in the step that opens it.
  S.go('chat'); await until(() => S.view === 'chat', 2000);
  document.getElementById('proj-space').replaceChildren();
  S.go('projects');
  check(text('proj-space') !== '', 'Projects, once in, did not draw in the step that opened it');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestProjectsIsFetchedWhenFirstOpenedAndHearsItsFramesOnceIn(t *testing.T) {
	runRealPageServing(t, statusFakeSocketJS, projectsFetchedJS, homeStyles, servedProjectsHeld(t))
}

// A PROJECT'S ADDRESS OPENED BEFORE PROJECTS IS IN HOLDS ONLY ITS MOMENT'S
// INTENT (step 12a). The shipped page, with Projects held from arriving.
// #/projects/beta opened on the conversation takes the page to Projects at once, keeping
// the address. Stayed on, the project is shown and selected when Projects
// arrives. Left for the conversation before it arrives, the address is retired: the
// arrival does not take the page back, write its address over the
// operator's, or select the project. Left and opened again before it
// arrives, the project is shown once: only the newer intent is followed.
func projectAddressJS(mode string) string {
	return projectsPageHead + `
const mode = '` + mode + `';
const selectedBeta = () => sent.some(f => f.type === 'project' && f.project && f.project.action === 'select' && f.project.id === 'beta');
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say(status);
  sockets[0].say({ type: 'projects', projects: [A, B] });
  await until(() => S.view === 'chat', 4000);
  location.hash = '#/projects/beta';
  await until(() => typeof window.__releaseProjects === 'function', 6000);
  check(S.view === 'projects' && location.hash === '#/projects/beta', 'a project address opened before Projects was in did not take the page there at once: view ' + S.view + ' at ' + location.hash);
  if (mode === 'leave' || mode === 'return') { location.hash = '#/chat'; await until(() => S.view === 'chat', 4000); }
  if (mode === 'return') { location.hash = '#/projects/beta'; await until(() => S.view === 'projects', 4000); }
  window.__releaseProjects();
  await until(() => S.later && typeof S.later.viewProject === 'function', 4000);
  // what the arrival does, it does in the steps after it
  await until(() => selectedBeta() || text('proj-space').includes('/p/beta'), 500);
  if (mode === 'stay') check(S.view === 'projects' && location.hash === '#/projects/beta' && text('proj-space').includes('/p/beta') && selectedBeta(), 'the project addressed before Projects was in was not shown and selected when it arrived: view ' + S.view + ' at ' + location.hash + ', selected ' + selectedBeta());
  if (mode === 'return') check(S.view === 'projects' && location.hash === '#/projects/beta' && sent.filter(f => f.type === 'project' && f.project && f.project.action === 'select' && f.project.id === 'beta').length === 1, 'an address opened, left and opened again before Projects was in was not shown exactly once: view ' + S.view + ' at ' + location.hash + ', selects ' + sent.filter(f => f.type === 'project' && f.project && f.project.action === 'select').map(f => f.project.id).join());
  if (mode === 'leave') check(S.view === 'chat' && location.hash === '#/chat' && !selectedBeta(), 'a project address left before Projects arrived took the page back or selected it: view ' + S.view + ' at ' + location.hash + ', selected ' + selectedBeta());
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`
}

func TestAProjectAddressOpenedBeforeProjectsIsInHoldsOnlyItsMomentsIntent(t *testing.T) {
	t.Run("stayed on", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, projectAddressJS("stay"), homeStyles, servedProjectsHeld(t))
	})
	t.Run("left for the conversation", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, projectAddressJS("leave"), homeStyles, servedProjectsHeld(t))
	})
	t.Run("left and opened again", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, projectAddressJS("return"), homeStyles, servedProjectsHeld(t))
	})
}

// A PROJECT'S ADDRESS RESTORED ON A COLD LOAD SURVIVES THE SOCKET OPENING
// WHILE PROJECTS IS ON ITS WAY (a review 2319). The page loads on
// #/projects/beta with Projects held; the socket opens while it is held and
// redraws the view shown (ws.js), Alpha is the project worked in, and then
// Projects arrives. The redraw is not a navigation: Beta is shown, selected
// and addressed, not Alpha under Beta's address.
const coldProjectAddressJS = `try { history.replaceState(null, '', location.pathname + location.search + '#/projects/beta'); localStorage.removeItem('aii.view'); } catch (e) {}
`

const projectAddressColdJS = projectsPageHead + `
const selectedBeta = () => sent.some(f => f.type === 'project' && f.project && f.project.action === 'select' && f.project.id === 'beta');
async function run() {
  await until(() => sockets.length > 0 && typeof window.__releaseProjects === 'function', 6000);
  check(S.view === 'projects' && location.hash === '#/projects/beta', 'a cold load on a project address did not open Projects at that address: view ' + S.view + ' at ' + location.hash);
  sockets[0].open();
  sockets[0].say(status);
  sockets[0].say({ type: 'projects', projects: [A, B] });
  check(S.view === 'projects' && location.hash === '#/projects/beta', 'the socket opening moved the page or its address: view ' + S.view + ' at ' + location.hash);
  window.__releaseProjects();
  await until(() => S.later && typeof S.later.viewProject === 'function', 4000);
  await until(() => selectedBeta() && text('proj-space').includes('/p/beta'), 3000).catch(() => {});
  check(S.view === 'projects' && location.hash === '#/projects/beta' && S.viewedProject === 'beta' && text('proj-space').includes('/p/beta') && selectedBeta(), 'the address restored before Projects was in was retired by the socket opening: view ' + S.view + ' at ' + location.hash + ', viewed ' + S.viewedProject + ', selected ' + selectedBeta() + ', drawn ' + JSON.stringify(text('proj-space').slice(0, 80)));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestAProjectAddressRestoredColdSurvivesTheSocketOpeningWhileProjectsIsHeld(t *testing.T) {
	runRealPageServing(t, coldProjectAddressJS+statusFakeSocketJS, projectAddressColdJS, homeStyles, servedProjectsHeld(t))
}
