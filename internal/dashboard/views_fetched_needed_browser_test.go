//go:build !windows

package dashboard

import (
	"bytes"
	"regexp"
	"testing"
)

// ABOUT, STATUS AND FIRST RUN ARE FETCHED WHEN FIRST NEEDED, WITH THEIR
// STYLESHEETS (step 22). The shipped page, in every engine. Home opens
// without About's modules, Status's or first run's, and without their
// sheets. Opened, About and Status are each fetched with their sheets,
// placed before custom.css so an identity's own styles still come last,
// and drawn styled, and what they export is offered to the socket. A page
// that finds no identity fetches first run, which draws the services the
// page already held before it arrived; and a first run or a Search that
// does not arrive says so.
const viewsNeededHeadJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const fetched = path => performance.getEntriesByType('resource').some(e => new URL(e.name).pathname === path);
const before = href => { const l = document.querySelector('link[href="./' + href + '"]'), c = document.querySelector('link[href="./custom.css"]'); return !!(l && c && (l.compareDocumentPosition(c) & Node.DOCUMENT_POSITION_FOLLOWING)); };
// What About draws from: an identity with nothing in it yet.
const IDENTITY = { beliefs: [], intentions: [], experiences: [], synthesis: '', brief: '', charter: '' };
const stats = name => ({ name, version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 });
`

const viewsNeededOpenJS = viewsNeededHeadJS + `async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: stats('Mira') }); sockets[0].say({ type: 'identity', identity: IDENTITY });
  await until(() => S.view === 'home', 4000);
  // waits by design: an absence is read, after Home has had the time to
  // fetch what it would.
  await new Promise(r => setTimeout(r, 300));
  const early = ['/views/identity.js', '/views/memory.js', '/views/settings/status.js', '/views/firstrun/start.js', '/views/identity/who.css', '/views/settings/status.css', '/views/firstrun/start.css'].filter(fetched);
  check(!early.length, 'Home fetched what it does not draw: ' + early.join(', '));

  location.hash = '#/identity';
  // About is offered once all its modules and sheets have loaded, so the
  // wait is for each thing read below, not for the first of them.
  const aboutOffered = () => typeof S.later?.renderIdentity === 'function' && typeof S.later?.renderMemory === 'function';
  await until(() => fetched('/views/identity.js') && fetched('/views/identity/who.css') && before('views/identity/who.css') && aboutOffered(), 6000).catch(() => {});
  check(fetched('/views/identity.js') && fetched('/views/identity/who.css'), 'About was not fetched with its sheet when it was opened');
  check(before('views/identity/who.css'), "About's sheet is not placed before custom.css");
  check(aboutOffered(), 'what About exports was not offered to the socket');
  await until(() => document.querySelector('.who__part'), 3000).catch(() => {});
  const part = document.querySelector('.who__part');
  check(part && getComputedStyle(part).display === 'flex', 'About was not drawn styled');

  location.hash = '#/settings/status';
  await until(() => document.getElementById('status-heading'), 6000).catch(() => {});
  check(fetched('/views/settings/status.js') && fetched('/views/settings/status.css') && before('views/settings/status.css'), 'Status was not fetched with its sheet, before custom.css, when it was opened');
  check(typeof S.later.renderStatus === 'function', 'what Status exports was not offered to the socket');
  const heading = document.getElementById('status-heading');
  check(heading && getComputedStyle(heading).fontWeight === '600', 'Status was not drawn styled');
  check(!fetched('/views/firstrun/start.js'), 'a page with an identity fetched first run');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

const viewsNeededFirstRunJS = viewsNeededHeadJS + `async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'providers', providers: [{ name: 'Alpha Cloud', chat: true, models: ['a-1'] }, { name: 'Beta Local', chat: true, models: ['b-1'] }] });
  sockets[0].say({ type: 'status', stats: stats('(not born)') });
  await until(() => document.getElementById('fb-provider') && document.getElementById('fb-provider').options.length > 1, 6000).catch(() => {});
  check(fetched('/views/firstrun/start.js') && fetched('/views/firstrun/start.css') && before('views/firstrun/start.css'), 'first run was not fetched with its sheet, before custom.css, when no identity was found');
  const names = [...((document.getElementById('fb-provider') || {}).options || [])].map(o => o.textContent);
  check(names.includes('Alpha Cloud') && names.includes('Beta Local'), 'first run did not draw the services the page held before it arrived: ' + names.join(', '));
  check(!fetched('/views/identity.js') && !fetched('/views/settings/status.js'), 'first run fetched About or Status');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

// A part that does not arrive says so (recovery.js): first run, the whole
// page until an identity is born, in the boot alert's own words; Search, as
// any part does. Each module is served as one that throws as it starts, as
// a stale copy of it would.
const viewsNeededBrokenJS = viewsNeededHeadJS + `async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: stats('(not born)') });
  await until(() => document.getElementById('boot-failure'), 6000).catch(() => {});
  const boot = document.getElementById('boot-failure');
  check(boot && boot.textContent.includes('This page could not load.'), 'a first run that did not arrive was not said: ' + (boot && boot.textContent));
  sockets[0].say({ type: 'status', stats: stats('(not born)') });
  // waits by design: an absence is read, after the next frame has had the
  // time to ask for first run again and be refused again.
  await new Promise(r => setTimeout(r, 300));
  check(document.querySelectorAll('#boot-failure').length === 1, 'the first run that did not arrive was said more than once');
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true }));
  await until(() => document.getElementById('part-failure'), 6000).catch(() => {});
  const part = document.getElementById('part-failure');
  check(part && part.textContent.includes('Search did not open.'), 'a Search that did not arrive was not said: ' + (part && part.textContent));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestAboutStatusAndFirstRunAreFetchedWhenFirstNeeded(t *testing.T) {
	t.Run("About and Status, opened", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsNeededOpenJS, homeStyles)
	})
	t.Run("first run, with no identity", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsNeededFirstRunJS, homeStyles)
	})
	t.Run("first run and Search, when they do not arrive", func(t *testing.T) {
		broken := []byte("throw new Error('a copy from another build');\n")
		runRealPageServing(t, statusFakeSocketJS, viewsNeededBrokenJS, homeStyles, map[string][]byte{"/views/firstrun/start.js": broken, "/shell/menu.js": broken})
	})
}

// A VIEW'S SHEET IS READY ONLY ONCE IT HAS LOADED (step 22, round one). On
// the real page: a sheet the Record and About share is still loading when
// the operator leaves the Record for About, and About waits for that load,
// not for the link being there; a sheet that does not load is said as a
// part that does not arrive is, leaves no link behind, and is fetched again
// when the view is opened again. The page's own load and error events are
// what is held or sent, so the case is the page's readiness and not the
// network's timing.
const viewsSheetSharedJS = viewsNeededHeadJS + `let release = null;
new MutationObserver(ms => { for (const m of ms) for (const n of m.addedNodes) {
  if (n.tagName === 'LINK' && n.getAttribute('href') === './components/filter/filter.css' && n.onload) {
    const loaded = n.onload; n.onload = () => { release = () => loaded.call(n); };
  }
} }).observe(document.head, { childList: true });
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: stats('Mira') }); sockets[0].say({ type: 'identity', identity: IDENTITY });
  await until(() => S.view === 'home', 4000);
  location.hash = '#/record';
  await until(() => document.querySelector('link[href="./components/filter/filter.css"]'), 6000).catch(() => {});
  location.hash = '#/identity';
  await until(() => release && fetched('/views/identity.js') && fetched('/views/identity/who.css') && fetched('/views/identity/beliefs.css'), 6000).catch(() => {});
  // waits by design: an absence is read, after About's own modules and
  // sheets have arrived and only the shared sheet's load is held.
  await new Promise(r => setTimeout(r, 300));
  check(release, 'the shared sheet was not loading when About was opened');
  check(!document.querySelector('.who__part') && typeof S.later?.renderIdentity !== 'function', 'About was drawn before a sheet it shares with the Record had loaded');
  if (release) release();
  await until(() => document.querySelector('.who__part'), 6000).catch(() => {});
  const part = document.querySelector('.who__part');
  check(part && getComputedStyle(part).display === 'flex' && typeof S.later?.renderIdentity === 'function', 'About was not drawn once the shared sheet had loaded');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

const viewsSheetFailedJS = viewsNeededHeadJS + `let asked = 0;
new MutationObserver(ms => { for (const m of ms) for (const n of m.addedNodes) {
  if (n.tagName === 'LINK' && n.getAttribute('href') === './views/identity/who.css' && !asked++) n.dispatchEvent(new Event('error'));
} }).observe(document.head, { childList: true });
const linked = () => document.querySelectorAll('link[href="./views/identity/who.css"]').length;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: stats('Mira') }); sockets[0].say({ type: 'identity', identity: IDENTITY });
  await until(() => S.view === 'home', 4000);
  location.hash = '#/identity';
  await until(() => document.getElementById('part-failure'), 6000).catch(() => {});
  const bar = document.getElementById('part-failure');
  check(bar && bar.textContent.includes('did not open.'), 'a sheet that did not load was not said: ' + (bar && bar.textContent));
  check(!document.querySelector('.who__part'), 'About was drawn without a sheet that did not load');
  check(linked() === 0, 'the sheet that did not load was left in the page');
  location.hash = '#/home';
  await until(() => S.view === 'home', 4000);
  location.hash = '#/identity';
  await until(() => document.querySelector('.who__part'), 6000).catch(() => {});
  const part = document.querySelector('.who__part');
  check(asked === 2, 'the sheet was not asked for again when About was opened again: asked ' + asked + ' times');
  check(part && getComputedStyle(part).display === 'flex', 'About was not drawn styled when it was opened again');
  check(linked() === 1, 'the sheet is in the page ' + linked() + ' times, not once');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

// A VIEW'S REDRAWS WAIT FOR ITS SHEETS, frames from the real socket through the
// real dispatcher. A module installs its own redraw as it runs, before its sheets
// are in, and the socket calls that directly: while a sheet is held nothing is
// drawn, and the latest frame is what is drawn when it is in; after a sheet fails
// nothing is drawn, and opening the view again fetches it and draws.
const viewsRedrawHeadJS = viewsNeededHeadJS + `let release = null, failing = '', asked = 0;
const holding = href => new MutationObserver(ms => { for (const m of ms) for (const n of m.addedNodes) {
  if (n.tagName !== 'LINK' || n.getAttribute('href') !== './' + href) continue;
  if (failing && !asked++) { n.dispatchEvent(new Event('error')); continue; }
  if (!failing && n.onload) { const loaded = n.onload; n.onload = () => { release = () => loaded.call(n); }; }
} }).observe(document.head, { childList: true });
const experience = words => ({ type: 'identity', identity: { beliefs: [], intentions: [], retired: [], experiences: [{ id: 'x1', content: words, category: 'work', created_at: '2026-11-01T07:00:00Z' }] } });
const start = async () => {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: stats('Mira') }); sockets[0].say({ type: 'identity', identity: IDENTITY });
  await until(() => S.view === 'home', 4000);
};
// waits by design: an absence is read, after the frame has been dispatched.
const settle = () => new Promise(r => setTimeout(r, 300));
const finish = () => report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
const failed = e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : ''));
`

const viewsRedrawHeldJS = viewsRedrawHeadJS + `holding('views/identity/experiences.css');
async function run() {
  await start();
  location.hash = '#/identity/experiences';
  await until(() => release && S.renderExperiences, 6000).catch(() => {});
  check(release && S.renderExperiences, 'rig: the module has not run with its sheet held');
  const stack = document.getElementById('experiences-stack');
  sockets[0].say(experience('The first words.'));
  await settle();
  check(!stack.children.length, 'a frame drew Experiences before its sheet had loaded: ' + stack.textContent);
  sockets[0].say(experience('The latest words.'));
  await settle();
  check(!stack.children.length, 'a second frame drew Experiences before its sheet had loaded: ' + stack.textContent);
  if (release) release();
  await until(() => /The latest words/.test(stack.textContent), 6000).catch(() => {});
  check(/The latest words/.test(stack.textContent) && !/The first words/.test(stack.textContent), 'the latest frame was not drawn once the sheet had loaded: ' + stack.textContent);
  sockets[0].say(experience('Words after it.'));
  await until(() => /Words after it/.test(stack.textContent), 6000).catch(() => {});
  check(/Words after it/.test(stack.textContent), 'a frame after the sheet had loaded did not draw: ' + stack.textContent);
  finish();
}
run().catch(failed);
`

const viewsTechnicalHeldJS = viewsRedrawHeadJS + `holding('views/settings/technical/technical.css');
async function run() {
  await start();
  location.hash = '#/settings/technical/verification';
  await until(() => release && S.renderTechnical, 6000).catch(() => {});
  check(release && S.renderTechnical, 'rig: Technical has not run with its sheet held');
  const stack = document.getElementById('technical-stack');
  sockets[0].say({ type: 'config', config: { witness: { url: 'https://witness.test', interval_events: 100 }, logs: { dir: 'log' }, restart_required: [] } });
  await settle();
  check(!stack.children.length, 'a frame drew Technical before its sheet had loaded: ' + stack.textContent);
  location.hash = '#/settings/technical/logs';
  await settle();
  check(!stack.children.length, 'an address within it drew Technical before its sheet had loaded: ' + stack.textContent);
  if (release) release();
  const title = () => (document.querySelector('.technical__title') || {}).textContent;
  await until(() => title() === 'Logs', 6000).catch(() => {});
  check(title() === 'Logs', 'the panel last asked for was not drawn once the sheet had loaded: ' + title());
  finish();
}
run().catch(failed);
`

const viewsStatusHeldJS = viewsRedrawHeadJS + `holding('views/settings/status.css');
async function run() {
  await start();
  location.hash = '#/settings/status';
  await until(() => release && S.renderStatus, 6000).catch(() => {});
  check(release && S.renderStatus, 'rig: Status has not run with its sheet held');
  const stack = document.getElementById('status-stack');
  sockets[0].say({ type: 'status', stats: stats('Mira') }); sockets[0].say({ type: 'identity', identity: IDENTITY });
  await settle();
  check(!stack.children.length, 'a frame drew Status before its sheet had loaded: ' + stack.textContent);
  if (release) release();
  await until(() => stack.children.length, 6000).catch(() => {});
  check(stack.children.length, 'Status was not drawn once its sheet had loaded');
  finish();
}
run().catch(failed);
`

const viewsRedrawFailedJS = viewsRedrawHeadJS + `failing = 'yes'; holding('views/identity/experiences.css');
async function run() {
  await start();
  location.hash = '#/identity/experiences';
  await until(() => document.getElementById('part-failure'), 6000).catch(() => {});
  check(document.getElementById('part-failure'), 'a sheet that did not load was not said');
  // A sheet's failure can come before the modules have run: the redraw must be
  // installed, or the frame below would have nothing to go through.
  await until(() => S.renderExperiences, 6000).catch(() => {});
  check(S.renderExperiences, 'rig: the module has not run');
  const stack = document.getElementById('experiences-stack');
  sockets[0].say(experience('Words too soon.'));
  await settle();
  check(!stack.children.length, 'a frame drew Experiences after its sheet had failed: ' + stack.textContent);
  location.hash = '#/home';
  await until(() => S.view === 'home', 4000);
  location.hash = '#/identity/experiences';
  await until(() => /Words too soon/.test(stack.textContent), 6000).catch(() => {});
  check(asked === 2, 'the sheet was not asked for again: asked ' + asked + ' times');
  check(/Words too soon/.test(stack.textContent), 'opened again, Experiences did not draw what the page holds: ' + stack.textContent);
  sockets[0].say(experience('Words after it.'));
  await until(() => /Words after it/.test(stack.textContent), 6000).catch(() => {});
  check(/Words after it/.test(stack.textContent), 'a frame after the sheet came did not draw: ' + stack.textContent);
  finish();
}
run().catch(failed);
`

const viewsRedrawBackupsJS = viewsRedrawHeadJS + `holding('views/identity/who.css');
async function run() {
  await start();
  location.hash = '#/settings/all';
  await until(() => S.later?.applyBackups, 6000).catch(() => {});
  check(S.later?.applyBackups, 'rig: Settings has not offered Backups');
  location.hash = '#/identity';
  await until(() => release && S.renderIdentityView, 6000).catch(() => {});
  check(release && S.renderIdentityView, 'rig: About has not run with its sheet held');
  const stack = document.getElementById('identity-stack');
  sockets[0].say({ type: 'backups', backups: { action: 'status', view: { enabled: true, snapshots: [] } } });
  await settle();
  check(!stack.children.length, 'a Backups frame drew About before its sheet had loaded: ' + stack.textContent);
  if (release) release();
  await until(() => stack.children.length, 6000).catch(() => {});
  check(stack.children.length, 'About was not drawn once its sheet had loaded');
  finish();
}
run().catch(failed);
`

// PROJECTS COMES WITH ITS SHEET (step 12b), as About does: a frame while the
// sheet is held draws nothing and offers nothing, and the page draws what it
// then holds once the sheet is in; a sheet that failed is said, and the next
// opening asks for it again.
const viewsProjectsHeadJS = viewsRedrawHeadJS + `holding('views/projects.css');
const projectsFrame = name => ({ type: 'projects', projects: [{ id: 'p1', name, state: 'open', dir: '/p/one' }] });
const projectsView = () => document.getElementById('view-projects');
`

const viewsProjectsHeldJS = viewsProjectsHeadJS + `async function run() {
  await start();
  location.hash = '#/projects';
  await until(() => release && S.gradeAnswered, 6000).catch(() => {});
  check(release && S.gradeAnswered, 'rig: Projects has not run with its sheet held');
  sockets[0].say(projectsFrame('The first name'));
  await settle();
  check(!/The first name/.test(projectsView().textContent), 'a frame drew Projects before its sheet had loaded: ' + projectsView().textContent);
  check(!S.later?.renderProjects, 'Projects was offered to the socket before its sheet had loaded');
  if (release) release();
  await until(() => /The first name/.test(projectsView().textContent), 6000).catch(() => {});
  check(/The first name/.test(projectsView().textContent), 'Projects was not drawn once its sheet had loaded: ' + projectsView().textContent);
  sockets[0].say(projectsFrame('A later name'));
  await until(() => /A later name/.test(projectsView().textContent), 6000).catch(() => {});
  check(/A later name/.test(projectsView().textContent), 'a frame after the sheet had loaded did not draw: ' + projectsView().textContent);
  finish();
}
run().catch(failed);
`

const viewsProjectsFailedJS = viewsProjectsHeadJS + `failing = 'yes';
async function run() {
  await start();
  location.hash = '#/projects';
  await until(() => document.getElementById('part-failure'), 6000).catch(() => {});
  check(document.getElementById('part-failure'), 'a sheet that did not load was not said');
  await until(() => S.gradeAnswered, 6000).catch(() => {});
  check(S.gradeAnswered, 'rig: Projects has not run');
  sockets[0].say(projectsFrame('Words too soon'));
  await settle();
  check(!/Words too soon/.test(projectsView().textContent), 'a frame drew Projects after its sheet had failed: ' + projectsView().textContent);
  location.hash = '#/home';
  await until(() => S.view === 'home', 4000);
  location.hash = '#/projects';
  await until(() => /Words too soon/.test(projectsView().textContent), 6000).catch(() => {});
  check(asked === 2, 'the sheet was not asked for again: asked ' + asked + ' times');
  check(/Words too soon/.test(projectsView().textContent), 'opened again, Projects did not draw what the page holds: ' + projectsView().textContent);
  finish();
}
run().catch(failed);
`

// SETTINGS COMES WITH ACCESS'S AND PEOPLE'S SHEETS (step 17b): held, nothing of it
// is drawn or offered, whatever frame arrives, even when Search has already
// fetched its module and its redraws stand; once they are in it draws what the
// page then holds, each sheet before custom.css. A sheet that failed is said, and
// the next opening asks for it again.
const viewsSettingsHeadJS = viewsRedrawHeadJS + `const settingsConfig = { type: 'config', config: { llm: {}, speech: { stt: {}, tts: {}, services: [] }, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, identity: {}, public_name: {}, contacts: [], channels: [], restart_required: [], dashboard: { host: '127.0.0.1', port: 1, tls: false } } };
const settingsStack = () => document.getElementById('settings-stack');
`

const viewsSettingsHeldJS = viewsSettingsHeadJS + `holding(HELD);
async function run() {
  await start();
  sockets[0].say(settingsConfig);
  location.hash = '#/settings/messages';
  await until(() => release, 6000).catch(() => {});
  check(release, 'rig: the sheet was not asked for with Settings');
  await until(() => S.settingsSection, 6000).catch(() => {});
  check(S.settingsSection, 'rig: Settings has not run with its sheet held');
  sockets[0].say(settingsConfig);
  S.renderMessages?.();
  await settle();
  check(!settingsStack().children.length, 'a frame drew Settings before its sheet had loaded: ' + settingsStack().textContent);
  check(!S.later?.renderSettings, 'Settings was offered to the socket before its sheet had loaded');
  if (release) release();
  await until(() => settingsStack().querySelector('.settings-people'), 6000).catch(() => {});
  const part = settingsStack().querySelector('.settings-people');
  check(part, 'Settings was not drawn once its sheet had loaded: ' + settingsStack().textContent);
  check(part && getComputedStyle(part).display === 'flex', 'Settings was drawn without People\'s sheet');
  const link = document.querySelector('link[href="./' + HELD + '"]'), last = document.querySelector('link[href="./custom.css"]');
  check(link && last && (link.compareDocumentPosition(last) & Node.DOCUMENT_POSITION_FOLLOWING), 'the sheet does not stand before custom.css');
  finish();
}
run().catch(failed);
`

const viewsSettingsSearchFirstJS = viewsSettingsHeadJS + `holding('views/settings/people.css');
async function run() {
  await start();
  sockets[0].say(settingsConfig);
  await import('/views/settings.js');
  check(S.renderMessages && S.openSettings, 'rig: Search\'s fetch did not install the redraws');
  check(!settingsStack().children.length, 'rig: fetching the module drew Settings');
  location.hash = '#/settings/messages';
  await until(() => release, 6000).catch(() => {});
  check(release, 'rig: the sheet was not asked for with Settings');
  await settle();
  S.renderMessages();
  S.renderNoticeRoute();
  await settle();
  check(!settingsStack().children.length, 'a redraw the module installed drew Settings before its sheet had loaded: ' + settingsStack().textContent);
  if (release) release();
  await until(() => settingsStack().querySelector('.settings-people'), 6000).catch(() => {});
  check(settingsStack().querySelector('.settings-people'), 'Settings was not drawn once its sheet had loaded');
  finish();
}
run().catch(failed);
`

const viewsSettingsFailedJS = viewsSettingsHeadJS + `failing = 'yes';
holding('views/settings/people.css');
const linked = () => document.querySelectorAll('link[href="./views/settings/people.css"]').length;
async function run() {
  await start();
  sockets[0].say(settingsConfig);
  location.hash = '#/settings/messages';
  await until(() => document.getElementById('part-failure'), 6000).catch(() => {});
  const bar = document.getElementById('part-failure');
  check(bar && bar.textContent.includes('did not open.'), 'a sheet that did not load was not said: ' + (bar && bar.textContent));
  sockets[0].say(settingsConfig);
  await settle();
  check(!settingsStack().children.length, 'a frame drew Settings after its sheet had failed: ' + settingsStack().textContent);
  check(linked() === 0, 'the sheet that did not load was left in the page');
  location.hash = '#/home';
  await until(() => S.view === 'home', 4000);
  location.hash = '#/settings/messages';
  await until(() => settingsStack().querySelector('.settings-people'), 6000).catch(() => {});
  check(asked === 2, 'the sheet was not asked for again: asked ' + asked + ' times');
  const part = settingsStack().querySelector('.settings-people');
  check(part && getComputedStyle(part).display === 'flex', 'opened again, Settings was not drawn with its sheet: ' + settingsStack().textContent);
  check(linked() === 1, 'the sheet is in the page ' + linked() + ' times, not once');
  finish();
}
run().catch(failed);
`

// PLUGINS AND PERMISSIONS COMES WITH ITS PARTS' SHEET (step 16b), as Projects
// does: a configuration while the sheet is held draws nothing and offers
// nothing; a sheet that failed is said, and the next opening asks again.
const viewsPluginsHeadJS = viewsRedrawHeadJS + `holding('views/settings/permissions.css');
const pluginsFrame = title => ({ type: 'config', config: { plugins: { autoload: 'T1', catalog: [], pending: [], skips: [], stored: [],
  installed: [{ id: 'org.example.one', title, version: '1.0.0', tier: 'T1', kind: 'plugin', mode: 'wasm', variant: 'wasm', settings: [], grants: {} }] } } });
const pluginsView = () => document.getElementById('view-plugins');
// An actual change of the pages: the plugin's own page placed among the main
// tabs, through the socket's layout and sections frames (sections-frames.js
// then tells Plugins through S.framePagesChanged).
const slots = { 'main-tabs': ['one-page'] };
const pagesChange = () => {
  sockets[0].say({ type: 'layout', layout: { profiles: { desktop: slots, mobile: slots, phone: slots, tablet: slots } } });
  sockets[0].say({ type: 'sections', sections: [{ id: 'one-page', title: 'One page', slot: 'main-tabs', entry: 'index.html', plugin: 'org.example.one' }] });
};
const mounted = () => S.framePagesOf && S.framePagesOf('org.example.one').length > 0;
`

const viewsPluginsHeldJS = viewsPluginsHeadJS + `async function run() {
  await start();
  location.hash = '#/settings/permissions';
  await until(() => release && S.revokeAnswered, 6000).catch(() => {});
  check(release && S.revokeAnswered, 'rig: Plugins has not run with its sheet held');
  sockets[0].say(pluginsFrame('The first title'));
  await settle();
  check(!/The first title/.test(pluginsView().textContent), 'a configuration drew Plugins before its sheet had loaded: ' + pluginsView().textContent);
  check(!S.later?.renderPlugins, 'Plugins was offered to the socket before its sheet had loaded');
  pagesChange();
  await until(mounted, 6000).catch(() => {});
  check(mounted(), 'rig: the plugin\'s page was not mounted');
  await settle();
  check(!/The first title/.test(pluginsView().textContent), 'a change of the pages drew Plugins before its sheet had loaded: ' + pluginsView().textContent);
  if (release) release();
  await until(() => /The first title/.test(pluginsView().textContent), 6000).catch(() => {});
  check(/The first title/.test(pluginsView().textContent), 'Plugins was not drawn once its sheet had loaded: ' + pluginsView().textContent);
  check(pluginsView().querySelector('[data-open-frame="one-page"]'), 'the pages as they then stood were not drawn once the sheet had loaded');
  sockets[0].say(pluginsFrame('A later title'));
  await until(() => /A later title/.test(pluginsView().textContent), 6000).catch(() => {});
  check(/A later title/.test(pluginsView().textContent), 'a configuration after the sheet had loaded did not draw: ' + pluginsView().textContent);
  finish();
}
run().catch(failed);
`

const viewsPluginsFailedJS = viewsPluginsHeadJS + `failing = 'yes';
async function run() {
  await start();
  location.hash = '#/settings/permissions';
  await until(() => document.getElementById('part-failure'), 6000).catch(() => {});
  check(document.getElementById('part-failure'), 'a sheet that did not load was not said');
  await until(() => S.revokeAnswered, 6000).catch(() => {});
  check(S.revokeAnswered, 'rig: Plugins has not run');
  sockets[0].say(pluginsFrame('Words too soon'));
  await settle();
  check(!/Words too soon/.test(pluginsView().textContent), 'a configuration drew Plugins after its sheet had failed: ' + pluginsView().textContent);
  pagesChange();
  await until(mounted, 6000).catch(() => {});
  check(mounted(), 'rig: the plugin\'s page was not mounted');
  await settle();
  check(!/Words too soon/.test(pluginsView().textContent), 'a change of the pages drew Plugins after its sheet had failed: ' + pluginsView().textContent);
  location.hash = '#/home';
  await until(() => S.view === 'home', 4000);
  location.hash = '#/settings/permissions';
  await until(() => /Words too soon/.test(pluginsView().textContent), 6000).catch(() => {});
  check(asked === 2, 'the sheet was not asked for again: asked ' + asked + ' times');
  check(/Words too soon/.test(pluginsView().textContent), 'opened again, Plugins did not draw what the page holds: ' + pluginsView().textContent);
  check(pluginsView().querySelector('[data-open-frame="one-page"]'), 'opened again, Plugins did not draw the pages as they stand');
  finish();
}
run().catch(failed);
`

func TestAViewsRedrawsWaitForItsSheets(t *testing.T) {
	for _, held := range []struct{ name, sheet string }{{"Access's", "views/settings/access.css"}, {"People's", "views/settings/people.css"}} {
		t.Run("Settings, a frame while "+held.name+" sheet is held", func(t *testing.T) {
			runRealPage(t, statusFakeSocketJS, "const HELD = '"+held.sheet+"';\n"+viewsSettingsHeldJS, homeStyles)
		})
	}
	t.Run("Settings, a redraw while its sheet is held after Search fetched its module", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsSettingsSearchFirstJS, homeStyles)
	})
	t.Run("Settings, a frame after its sheet failed, then opened again", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsSettingsFailedJS, homeStyles)
	})
	t.Run("Experiences, a frame while its sheet is held", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsRedrawHeldJS, homeStyles)
	})
	t.Run("Experiences, a frame after its sheet failed, then opened again", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsRedrawFailedJS, homeStyles)
	})
	t.Run("About after Settings, a Backups frame while its sheet is held", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsRedrawBackupsJS, homeStyles)
	})
	t.Run("Technical, a frame and an address while its sheet is held", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsTechnicalHeldJS, homeStyles)
	})
	t.Run("Status, a frame while its sheet is held", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsStatusHeldJS, homeStyles)
	})
	t.Run("Projects, a frame while its sheet is held", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsProjectsHeldJS, homeStyles)
	})
	t.Run("Projects, a frame after its sheet failed, then opened again", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsProjectsFailedJS, homeStyles)
	})
	t.Run("Plugins, a configuration while its sheet is held", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsPluginsHeldJS, homeStyles)
	})
	t.Run("Plugins, a configuration after its sheet failed, then opened again", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsPluginsFailedJS, homeStyles)
	})
}

// EVERY REDRAW A LAZY VIEW INSTALLS IS NAMED FOR ITS BUNDLE, so it waits for the
// sheets. One added to such a module and not named would draw before them.
func TestEveryRedrawALazyViewInstallsWaitsForItsSheets(t *testing.T) {
	views, err := staticFS.ReadFile("static/views.js")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, file := range []string{"views/identity.js", "views/memory.js", "views/identity/who.js", "views/identity/between.js", "views/identity/experiences.js", "views/identity/beliefs.js", "views/identity/beliefs-show.js", "views/identity/trail.js", "views/settings/status.js", "views/settings/technical/technical.js", "views/projects.js", "views/delivered.js", "views/plugins.js", "views/plugins/card.js", "views/plugins/catalog.js", "views/plugins/stored.js", "views/plugins/accounts.js"} {
		src, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		// A redraw is a render* hook, or any one-line hook that draws a view
		// (S.framePagesChanged, step 16b).
		for _, m := range regexp.MustCompile(`(?m)^S\.(render\w+) = |^S\.(\w+) = .*\brender\w*\(`).FindAllSubmatch(src, -1) {
			if len(m[1]) == 0 {
				m[1] = m[2]
			}
			found++
			if !bytes.Contains(views, []byte("'"+string(m[1])+"'")) {
				t.Errorf("%s installs S.%s, which no bundle in views.js holds until its sheets are in", file, m[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("rig: no redraw found")
	}
}

func TestAViewsSheetIsReadyOnlyOnceItHasLoaded(t *testing.T) {
	t.Run("shared with a view still loading it", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsSheetSharedJS, homeStyles)
	})
	t.Run("that did not load, then opened again", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsSheetFailedJS, homeStyles)
	})
}
