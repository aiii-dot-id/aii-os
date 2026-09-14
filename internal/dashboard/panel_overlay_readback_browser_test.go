//go:build !windows

// .
// .

package dashboard

import (
	"fmt"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const panelOverlayReadbackPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<main class="stage"></main>
<aside class="slot panel-col occupied" id="slot-panel"></aside>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPanel } from './panel.js';

run(() => {
  // Both sentences are substituted from acceptedOutcome() in Go — the
  // longest real verdicts the server emits, not hand-copied prose.
  S.overlays = [
    { path: '/theme.css', outcome: '__FORK_OUTCOME__', decided_at: '2026-08-24T13:29:34Z' },
    { path: '/custom.css', outcome: '__ADDITIVE_OUTCOME__', decided_at: '2026-08-24T13:29:34Z' }
  ];
  S.stats = { ledger_seq: 121, lifetime_ticks: 4242 };
  S.cont = { mode: 'normal' };
  S.work = { live: [], queued: 0, delivered: [] };
  S.config = { llm: { resolved_provider: 'prov', resolved_model: 'mdl' } };
  S.providers = [];
  renderPanel();

  const heads = [...document.querySelectorAll('.sp-h')].map(h => h.textContent);
  assert(heads.includes('UI Adapters'), 'no UI Adapters card rendered — panel.js did not draw the overlay readback (heads: ' + heads.join(',') + ')');

  // The verb row: label 'accepted', reading the path.
  let verbRow = null;
  document.querySelectorAll('.sp-kv').forEach(r => {
    const b = r.querySelector('b');
    if (b && b.textContent === '/theme.css') verbRow = r;
  });
  assert(verbRow !== null, 'no .sp-kv row carries /theme.css');
  const label = verbRow.querySelector('span');
  assert(label.textContent === 'accepted', 'verb label = ' + label.textContent + ', want accepted');

  // The sentence row: the FORK warning, in a row whose label is empty.
  let sentRow = null;
  document.querySelectorAll('.sp-kv').forEach(r => {
    const b = r.querySelector('b');
    if (b && b.textContent === S.overlays[0].outcome.split(': ')[1]) sentRow = r;
  });
  assert(sentRow !== null, 'the outcome sentence did not render as a row');

  const vr = sentRow.querySelector('b').getBoundingClientRect();
  const rr = sentRow.getBoundingClientRect();

  // 0. Guard: real geometry, or every assertion below is vacuous.
  assert(rr.width > 0 && vr.width > 0,
    'zero-size geometry — the panel did not render visibly (row ' + rr.width + ', reading ' + vr.width + ')');

  // 1. The sentence must wrap inside the 232px column, not overflow it.
  assert(vr.right <= rr.right + 0.5,
    'the outcome sentence overflows the panel column: reading ends at ' + vr.right +
    ' but the row ends at ' + rr.right);

  // 2. A sentence this long must actually wrap — proof the row grew in
  //    height rather than the text being cut off or scrolled away.
  assert(vr.height > 18,
    'the FORK sentence renders as one line (' + vr.height + 'px) — it cannot fit 232px; it is being clipped, not wrapped');

  // 3. Negative half: no outcomes, no card. Silence is the shipped
  //    behavior and this pins it against regressions toward noise.
  S.overlays = [];
  renderPanel();
  const heads2 = [...document.querySelectorAll('.sp-h')].map(h => h.textContent);
  assert(!heads2.includes('UI Adapters'), 'an empty readback still renders a UI Adapters card — noise, not silence (heads: ' + heads2.join(',') + ')');
});
</script>`

func TestPanelOverlayReadbackInBrowser(t *testing.T) {
	panelJS, err := staticFS.ReadFile("static/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	layoutCSS, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	stateJS, err := staticFS.ReadFile("static/state.js")
	if err != nil {
		t.Fatal(err)
	}
	utilJS, err := staticFS.ReadFile("static/util.js")
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Replace(panelOverlayReadbackPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	// .
	// .
	// .
	// .
	forkData := []byte("/* replaces shipped frame */\n:root{}\n")
	shippedTheme, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	fork := acceptedOutcome("/theme.css", forkData, "e2e01a9")
	if !strings.Contains(fork, fmt.Sprintf("(%d bytes replacing %d", len(forkData), len(shippedTheme))) {
		t.Fatalf("fork verdict does not describe the real divergence: %s", fork)
	}
	page = strings.Replace(page, "__FORK_OUTCOME__", fork, 1)
	page = strings.Replace(page, "__ADDITIVE_OUTCOME__", acceptedOutcome("/custom.css", make([]byte, 972), "e2e01a9"), 1)
	if strings.Contains(page, "__FORK_OUTCOME__") || strings.Contains(page, "__ADDITIVE_OUTCOME__") {
		t.Fatal("verdict placeholders not substituted — the page would assert against empty strings")
	}
	if page == panelOverlayReadbackPage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	runPageInEnginesAtSize(t, page, map[string][]byte{
		"/panel.js": panelJS,
		"/state.js": stateJS,
		"/util.js":  utilJS,
	}, 1280, 800)
}
