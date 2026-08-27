//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

/* The panel must be QUIET when the facts it renders are unchanged.

   Every status/work/continuity message used to rebuild the whole
   panel's innerHTML — a chatty conversation meant dozens of full DOM
   rebuilds per minute, each one layout+GC the operator feels as a
   sluggish UI (report, 2026-08-27: "very slow and unresponsive").
   renderPanel now computes a signature over every fact it reads and
   skips the DOM write entirely when the signature is unchanged.

   This drives the REAL panel.js. The trap: replace the panel
   element's innerHTML setter with a counter, then call renderPanel
   again with identical facts — the write count must not move. */

const panelQuietPage = `<!doctype html>
<aside class="slot panel-col" id="slot-panel"></aside>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPanel } from './panel.js';

const panel = () => document.getElementById('slot-panel');
run(() => {
  S.stats = { ledger_seq: 121, lifetime_ticks: 4242, belief_count: 3, intention_count: 2,
              experience_count: 9, reflection_count: 1, malformed_calls: 0, suspicious_paths: 0 };
  S.cont = { witness_url: 'https://witness.test', witnessed_at: '2026-08-23T16:38:26Z',
             anchored_seq: 106, unanchored: 15, mode: 'normal' };
  S.work = { live: [{ id: 'w1', description: 'probe', status: 'live' }], queued: 1, delivered: [{ id: 'd1' }] };
  S.config = { llm: { resolved_provider: 'prov', resolved_model: 'mdl' } };
  S.providers = [];
  S.activeProject = { id: 'proj-1', name: 'Proj', active: true };
  S.overlays = [];

  // Trap innerHTML writes on the panel element.
  let writes = 0;
  const d = Object.getOwnPropertyDescriptor(Element.prototype, 'innerHTML');
  Object.defineProperty(panel(), 'innerHTML', {
    get() { return d.get.call(this); },
    set(v) { writes++; d.set.call(this, v); },
    configurable: true
  });

  renderPanel();
  assert(writes === 1, 'first render must write exactly once: ' + writes);
  assert(panel().querySelector('.sys-panel'), 'panel content missing after first render');

  // SAME facts again — a chatty status message that changes nothing
  // readable. The DOM must not be touched.
  renderPanel();
  assert(writes === 1, 'unchanged facts caused a second DOM write: ' + writes + ' — render churn the operator feels as lag');

  // A fact the panel reads DID change (live work rotated) — must render.
  S.work = { live: [{ id: 'w2', description: 'second', status: 'live' }], queued: 0, delivered: [] };
  renderPanel();
  assert(writes === 2, 'changed facts must re-render: ' + writes);

  // And quiet again for the same state.
  renderPanel();
  assert(writes === 2, 'second unchanged state re-rendered: ' + writes);
});
</script>
`

func TestPanelQuietWhenFactsUnchanged(t *testing.T) {
	panelJS, err := staticFS.ReadFile("static/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	stub := "export const S = { stats: null, cont: null, work: null, config: null, providers: [], activeProject: null, overlays: [] };"
	util := "export const $ = id => document.getElementById(id); export const esc = value => String(value ?? '');"
	runPageInEngines(t, panelQuietPage, map[string][]byte{
		"/panel.js": panelJS,
		"/state.js": []byte(stub),
		"/util.js":  []byte(util),
	})
}
