//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// panelStatusPage drives the REAL panel.js against a minimal DOM that mirrors
// the shipping frame: a left rail and the right-hand #slot-panel column. The
// assertions pin the consolidation of status readings — ledger / life /
// witnessed / channel render inside the right panel, and nothing renders them
// in the left rail any more. The witness wording is checked in all three of
// its distinct states, because collapsing them is exactly the display defect
// that made a healthy cadence look like an alarm (d6a0605).
const panelStatusPage = `<!doctype html>
<aside class="rail" id="rail">
  <div class="presence"><div id="orb"><div class="orb"></div></div>
  <div class="p-name" id="p-name"></div><div class="p-state" id="p-state"></div></div>
  <div class="slot" id="slot-rail"></div>
</aside>
<main class="stage"><span class="pill" id="pill-mode"></span></main>
<aside class="slot panel-col" id="slot-panel"></aside>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPanel } from './panel.js';

const panel = () => document.getElementById('slot-panel');
const rail = () => document.getElementById('rail');
run(() => {
  S.stats = { ledger_seq: 121, lifetime_ticks: 4242, belief_count: 3, intention_count: 2,
              experience_count: 9, reflection_count: 1, malformed_calls: 0, suspicious_paths: 0 };
  S.cont = { witness_url: 'https://witness.test', witnessed_at: '2026-08-23T16:38:26Z',
             anchored_seq: 106, unanchored: 15, mode: 'normal' };
  S.work = { live: [], queued: 0, delivered: [] };
  S.config = { llm: { resolved_provider: 'prov', resolved_model: 'mdl' } };
  S.providers = [];
  renderPanel();

  // 1. The three readings live in the right-hand panel.
  const panelText = panel().textContent;
  assert(panelText.includes('ledger'), 'ledger label missing from right panel');
  assert(panelText.includes('seq 121'), 'ledger seq missing from right panel');
  assert(panelText.includes('life'), 'life label missing from right panel');
  assert(panelText.includes('4242 ticks'), 'lifetime ticks missing from right panel');
  assert(panelText.includes('witnessed'), 'witness label missing from right panel');
  assert(panelText.includes('anchored @106 +15'), 'witness backlog wording missing: ' + panelText);
  assert(panelText.includes('CONTINUITY'), 'continuity card heading missing');

  // 2. They are structurally inside the panel column, not merely present.
  const cards = panel().querySelectorAll('.sp-card');
  let continuityCard = null;
  cards.forEach(c => { if (c.textContent.includes('ledger')) continuityCard = c; });
  assert(continuityCard !== null, 'no .sp-card contains the ledger reading');
  assert(continuityCard.closest('#slot-panel') !== null, 'continuity card is not inside #slot-panel');

  // 3. The left rail no longer carries any status reading.
  const railText = rail().textContent;
  assert(!railText.includes('ledger'), 'ledger reading still in the left rail');
  assert(!railText.includes('ticks'), 'life reading still in the left rail');
  assert(!railText.includes('witnessed'), 'witness reading still in the left rail');
  assert(rail().querySelector('.rail-foot') === null, 'rail-foot still exists in the frame');

  // 4. Continuity sits at the BOTTOM of the panel — after SUBSTRATE/MEMORY/WORK.
  const headings = [...panel().querySelectorAll('.sp-h')].map(h => h.textContent);
  assert(headings.at(-1) === 'CONTINUITY', 'CONTINUITY is not the last card: ' + headings.join(','));

  // 5. A clean channel stays invisible; a dirty channel becomes visible.
  assert(!panel().textContent.includes('odd calls'), 'clean channel rendered noise');
  S.stats.malformed_calls = 2; S.stats.suspicious_paths = 1;
  renderPanel();
  assert(panel().textContent.includes('3 odd calls'), 'dirty channel not surfaced');
  // Duplicate argument keys join the same tally: valid JSON that dispatched
  // on its last copy is channel corruption too, and the operator reads one
  // number, not three (forensics 2026-08-24).
  S.stats.duplicate_arg_keys = 2;
  renderPanel();
  assert(panel().textContent.includes('5 odd calls'), 'duplicate arg keys not counted in the channel row');
  delete S.stats.duplicate_arg_keys;
  S.stats.malformed_calls = 0; S.stats.suspicious_paths = 0;
  renderPanel();
  assert(!panel().textContent.includes('odd calls'), 'channel row did not go quiet again');

  // 5b. Credential warning: silent until the operator has something to do
  // about it, then named verbatim (1f35fa3, carried through the rebase).
  assert(!panel().textContent.includes('credential'), 'clean credential rendered noise');
  S.stats.credential_warning = 'expires in 3 days';
  renderPanel();
  assert(panel().textContent.includes('credential'), 'credential warning not surfaced');
  assert(panel().textContent.includes('expires in 3 days'), 'credential warning text not rendered verbatim');
  delete S.stats.credential_warning;
  renderPanel();

  // 6. The three witness states stay distinguishable.
  S.cont = { witness_url: '', witnessed_at: '', unanchored: 0, mode: 'normal' };
  renderPanel();
  assert(panel().textContent.includes('no witness'), 'unconfigured witness not named');
  S.cont = { witness_url: 'https://witness.test', witnessed_at: '', unanchored: 0, mode: 'normal' };
  renderPanel();
  assert(panel().textContent.includes('never witnessed'), 'never-anchored witness not named');
  S.cont = { witness_url: 'https://witness.test', witnessed_at: '2026-08-23T16:38:26Z', anchored_seq: 106, unanchored: 0, mode: 'normal' };
  renderPanel();
  assert(panel().textContent.includes('anchored'), 'healthy witness not named');
  assert(!panel().textContent.includes('+'), 'healthy witness rendered a backlog');

  // 7. A mounted R66 section still wins the slot — panel.js must defer.
  panel().innerHTML = '<div class="section-box">SECTION</div>';
  renderPanel();
  assert(panel().textContent === 'SECTION', 'panel.js clobbered a mounted section');
});
</script>`

func TestPanelStatusInBrowser(t *testing.T) {
	panelJS, err := staticFS.ReadFile("static/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, panelStatusPage, map[string][]byte{
		"/panel.js": panelJS,
		"/state.js": []byte(`export const S = { stats: null, cont: null, work: null, config: null, providers: [] };`),
		"/util.js":  []byte(`export const $ = id => document.getElementById(id); export const esc = value => String(value ?? '');`),
	})
}
