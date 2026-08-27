//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

/* §8.9: the dock listed every project, unsorted and unfiltered — a
   navigation surface that stops working past a handful of bubbles.
   This pins the filter: typing narrows the row (case-insensitive,
   over BOTH id and name), an unmatched filter says so honestly
   rather than rendering an empty strip, the filter input itself is
   static HTML that no broadcast re-render can clobber (the exact
   class that ate focus drafts), and the new-project escape stays
   reachable under a filter that hides every bubble. */

const dockFilterPage = `<!doctype html>
<div id="proj-space"></div>
<div class="dock-wrap"><div class="dock-tools"><input type="search" id="dock-filter"></div><div class="dock" id="dock"></div></div>
<span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderProjects, renderDock, dockFilterOf } from './views/projects.js';

run(() => {
  S.view = 'projects';
  S.projects = [
    { id: 'project-menu', name: 'Project menu', state: 'open', active: true },
    { id: 'beta', name: 'Beta Tracker', state: 'open' },
    { id: 'legacy', name: 'Legacy Stuff', state: 'closed' }
  ];
  S.activeProject = S.projects[0];
  S.viewedProject = 'project-menu';
  S.projTab = 'overview';
  S.identityExists = true;
  S.dockFilter = '';
  renderProjects();

  const bubbles = () => Array.from(document.querySelectorAll('#dock .dock-item[data-id]')).map(el => el.dataset.id);
  assert(bubbles().join(',') === 'project-menu,beta,legacy', 'unfiltered dock wrong: ' + bubbles().join(','));
  assert(document.getElementById('dock-new'), 'new-project escape missing from dock');

  // The predicate, by name: with a filter set, matching is a
  // case-insensitive substring over BOTH id and name.
  S.dockFilter = 'project-menu';
  assert(dockFilterOf({ id: 'project-menu', name: 'Whatever' }) === true, 'id substring must match');
  assert(dockFilterOf({ id: 'legacy', name: 'Legacy Stuff' }) === false, 'non-matching project must be filtered out');
  S.dockFilter = 'track';
  assert(dockFilterOf({ id: 'x1', name: 'Beta Tracker' }) === true, 'name substring must match');
  assert(dockFilterOf({ id: 'legacy', name: 'Legacy Stuff' }) === false, 'name-only match must exclude others');
  S.dockFilter = 'TRACK';
  assert(dockFilterOf({ id: 'x1', name: 'Beta Tracker' }) === true, 'matching must be case-insensitive');
  S.dockFilter = '';

  // Typing narrows the row — the input drives renderDock only.
  const input = document.getElementById('dock-filter');
  input.value = 'proj';
  input.dispatchEvent(new Event('input'));
  assert(bubbles().join(',') === 'project-menu', 'filter "proj" did not narrow: ' + bubbles().join(','));

  // The active project stays visible under a filter that matches it.
  input.value = 'BETA';
  input.dispatchEvent(new Event('input'));
  assert(bubbles().join(',') === 'beta', 'case-insensitive id/name match failed: ' + bubbles().join(','));

  // A filter that hides everything says so honestly, and the
  // new-project escape stays reachable.
  input.value = 'zzz-nothing';
  input.dispatchEvent(new Event('input'));
  assert(bubbles().length === 0, 'unmatched filter still showed bubbles');
  const empty = document.querySelector('#dock .dock-empty');
  assert(empty && /no projects match/.test(empty.textContent), 'unmatched filter must say so, not render an empty strip');
  assert(document.getElementById('dock-new'), 'new-project escape vanished under unmatched filter');

  // A broadcast re-render (the ws.js projects case) must NOT clobber
  // the filter mid-word: the input is static, the state survives, and
  // the row re-renders under the surviving filter.
  input.value = 'leg';
  input.dispatchEvent(new Event('input'));
  S.projects.push({ id: 'legal', name: 'Legal Review', state: 'open' });
  renderProjects(); // full re-render, as the projects message does
  const inputAfter = document.getElementById('dock-filter');
  assert(inputAfter === input, 'the filter input was replaced — it must be static HTML');
  assert(input.value === 'leg', 'BROADCAST ATE THE FILTER: value is "' + input.value + '"');
  assert(bubbles().join(',') === 'legacy,legal', 're-render did not re-apply the surviving filter: ' + bubbles().join(','));
});
</script>
`

func TestDockFilterBrowser(t *testing.T) {
	// Map DERIVED from the shared base. The ws entry is the recording
	// stub: viewProject legitimately sends the select act, and a stub
	// that throws would misattribute that send to the filter logic.
	runPageInEngines(t, dockFilterPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
		"/state.js", []byte("export const S = { view:'projects', projects:[], activeProject:null, viewedProject:null, dockFilter:'', identityExists:true, stats:{}, workspace:null, workspaceFor:'', projTab:'overview', focusDraft:null };"),
		"/util.js", []byte("export const $ = id => document.getElementById(id); export const esc = value => String(value ?? ''); export const hueOf = id => 200;"),
	))
}
