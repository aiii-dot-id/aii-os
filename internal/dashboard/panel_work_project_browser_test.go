//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// panelWorkProjectPage drives the REAL panel.js against the operator's
// report of 2026-08-27: the project pill said "now working in
// 'project-menu'" while the WORK card said "no active work". Both were
// true — the card rendered work SESSIONS and never said WHERE the
// identity works — but the operator cannot be required to know that two
// different facts share one word. The WORK card now carries both facts:
// the active project, and whether a session is running.
const panelWorkProjectPage = `<!doctype html>
<aside class="slot panel-col" id="slot-panel"></aside>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPanel } from './panel.js';

const panel = () => document.getElementById('slot-panel');
run(() => {
  // The operator's exact state: a project is active, no session runs.
  S.stats = { ledger_seq: 1, lifetime_ticks: 1, belief_count: 0, intention_count: 0,
              experience_count: 0, reflection_count: 0 };
  S.work = { live: [], queued: 0, delivered: [] };
  S.activeProject = { id: 'project-menu', name: 'project-menu' };
  renderPanel();

  let text = panel().textContent;
  // 1. The card names where the identity is working.
  assert(text.includes('working in'), 'WORK card does not say where the identity works: ' + text);
  assert(text.includes('project-menu'), 'active project name missing from WORK card');
  // 2. The empty line speaks about sessions, so it cannot be read as
  //    denying the project focus shown one line above it.
  assert(text.includes('no active session'), 'session-empty line missing');
  assert(!text.includes('no active work'), 'old ambiguous wording still rendered');

  // 3. Both facts live inside the same WORK card, not in two corners.
  let workCard = null;
  panel().querySelectorAll('.sp-card').forEach(c => {
    if (c.querySelector('.sp-h') && c.querySelector('.sp-h').textContent === 'WORK') workCard = c;
  });
  assert(workCard !== null, 'no WORK card rendered');
  assert(workCard.textContent.includes('working in'), 'project line is outside the WORK card');
  assert(workCard.textContent.includes('no active session'), 'session line is outside the WORK card');

  // 4. A live session and the project render together.
  S.work = { live: [{ id: 'w1', description: 'fix the dock' }], queued: 0, delivered: [] };
  renderPanel();
  text = panel().textContent;
  assert(text.includes('fix the dock'), 'live session missing');
  assert(text.includes('working in'), 'project line vanished when a session started');
  assert(!text.includes('no active session'), 'empty line rendered next to a live session');

  // 5. No active project: the card must not invent one — sessions only.
  S.activeProject = null;
  S.work = { live: [], queued: 0, delivered: [] };
  renderPanel();
  text = panel().textContent;
  assert(!text.includes('working in'), 'project line rendered with no active project');
  assert(text.includes('no active session'), 'session-empty line missing without a project');

  // 6. Project but no work message yet (page just loaded, projects
  //    payload arrived first): the card still says where work happens.
  S.activeProject = { id: 'p2', name: 'voice-plugin' };
  S.work = null;
  renderPanel();
  text = panel().textContent;
  assert(text.includes('working in'), 'WORK card absent when only the projects payload has arrived');
  assert(text.includes('voice-plugin'), 'project name missing in the projects-first ordering');
});
</script>`

// TestPanelShowsActiveProject pins the fix for the operator's report:
// the WORK card must state the active project alongside session state,
// in both message orderings (work-first and projects-first).
func TestPanelShowsActiveProject(t *testing.T) {
	panelJS, err := staticFS.ReadFile("static/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, panelWorkProjectPage, map[string][]byte{
		"/panel.js": panelJS,
		"/state.js": []byte(`export const S = { stats: null, cont: null, work: null, config: null, providers: [], activeProject: null };`),
		"/util.js":  []byte(`export const $ = id => document.getElementById(id); export const esc = value => String(value ?? '');`),
	})
}
