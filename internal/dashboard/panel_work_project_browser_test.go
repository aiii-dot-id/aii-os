//go:build !windows

package dashboard

import (
	"testing"
)

const panelWorkProjectPage = `<!doctype html>
<aside class="slot panel-col" id="slot-panel"></aside>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPanel } from './panel.js';

const panel = () => document.getElementById('slot-panel');
run(() => {
  S.stats = { ledger_seq: 1, lifetime_ticks: 1, belief_count: 0, intention_count: 0,
              experience_count: 0, reflection_count: 0 };
  S.work = { live: [], queued: 0, delivered: [] };
  S.activeProject = { id: 'project-menu', name: 'project-menu' };
  renderPanel();

  let text = panel().textContent;
  assert(text.includes('working in'), 'WORK card does not say where the identity works: ' + text);
  assert(text.includes('project-menu'), 'active project name missing from WORK card');
  assert(text.includes('no active session'), 'session-empty line missing');
  assert(!text.includes('no active work'), 'old ambiguous wording still rendered');

  let workCard = null;
  panel().querySelectorAll('.sp-card').forEach(c => {
    if (c.querySelector('.sp-h') && c.querySelector('.sp-h').textContent === 'WORK') workCard = c;
  });
  assert(workCard !== null, 'no WORK card rendered');
  assert(workCard.textContent.includes('working in'), 'project line is outside the WORK card');
  assert(workCard.textContent.includes('no active session'), 'session line is outside the WORK card');

  S.work = { live: [{ id: 'w1', description: 'fix the dock' }], queued: 0, delivered: [] };
  renderPanel();
  text = panel().textContent;
  assert(text.includes('fix the dock'), 'live session missing');
  assert(text.includes('working in'), 'project line vanished when a session started');
  assert(!text.includes('no active session'), 'empty line rendered next to a live session');

  S.activeProject = null;
  S.work = { live: [], queued: 0, delivered: [] };
  renderPanel();
  text = panel().textContent;
  assert(!text.includes('working in'), 'project line rendered with no active project');
  assert(text.includes('no active session'), 'session-empty line missing without a project');

  S.activeProject = { id: 'p2', name: 'voice-plugin' };
  S.work = null;
  renderPanel();
  text = panel().textContent;
  assert(text.includes('working in'), 'WORK card absent when only the projects payload has arrived');
  assert(text.includes('voice-plugin'), 'project name missing in the projects-first ordering');
});
</script>`

func TestPanelShowsActiveProject(t *testing.T) {
	panelJS, err := staticFS.ReadFile("static/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, panelWorkProjectPage, map[string][]byte{
		"/panel.js": panelJS,
		"/state.js": []byte(`export const S = { stats: null, cont: null, work: null, config: null, providers: [], activeProject: null };`),
		"/util.js":  utilStub(`export const $ = id => document.getElementById(id); export const esc = value => String(value ?? '');`),
	})
}
