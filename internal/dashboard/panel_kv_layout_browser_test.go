//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const panelKVLayoutPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<main class="stage"></main>
<aside class="slot panel-col occupied" id="slot-panel"></aside>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPanel } from './panel.js';

run(() => {
  const WARNING = 'the claude-code credential is no longer usable — refresh it with its own tool; the identity cannot think until you do';

  S.stats = { ledger_seq: 121, lifetime_ticks: 4242, belief_count: 3, intention_count: 2,
              experience_count: 9, reflection_count: 1, malformed_calls: 0, suspicious_paths: 0,
              credential_warning: WARNING };
  S.cont = { witness_url: 'https://witness.test', witnessed_at: '2026-08-23T16:38:26Z',
             anchored_seq: 106, unanchored: 15, mode: 'normal' };
  S.work = { live: [], queued: 0, delivered: [] };
  S.config = { llm: { resolved_provider: 'prov', resolved_model: 'mdl' } };
  S.providers = [];
  renderPanel();

  let row = null;
  document.querySelectorAll('.sp-kv').forEach(r => {
    const name = r.querySelector('span');
    if (name && name.textContent.trim() === 'credential') row = r;
  });
  assert(row !== null, 'no .sp-kv row carries the credential label — panel.js did not render it');

  const label = row.querySelector('span');
  const value = row.querySelector('b');
  assert(value !== null, 'credential row has no <b> reading');
  assert(value.textContent.includes('no longer usable'), 'the warning text did not reach the row');

  const lr = label.getBoundingClientRect();
  const vr = value.getBoundingClientRect();
  const rr = row.getBoundingClientRect();

  assert(rr.width > 0 && lr.width > 0 && vr.width > 0,
    'zero-size geometry — the panel did not render visibly (row ' + rr.width +
    ', label ' + lr.width + ', value ' + vr.width + ')');

  assert(lr.right <= vr.left + 0.5,
    'the credential reading overlaps its field name: label ends at ' + lr.right +
    ' but the reading starts at ' + vr.left);

  assert(vr.right <= rr.right + 0.5,
    'the reading overflows the panel column: reading ends at ' + vr.right +
    ' but the row ends at ' + rr.right);

  assert(label.scrollWidth <= label.clientWidth + 1,
    'the field name is clipped: needs ' + label.scrollWidth +
    'px but has ' + label.clientWidth + 'px');

  assert(rr.height > lr.height + 1,
    'the row did not grow to fit a wrapped sentence (row ' + rr.height +
    ', label ' + lr.height + ') — the reading is being clipped, not wrapped');
});
</script>`

func TestPanelKVRowHoldsALongReadingInBrowser(t *testing.T) {
	panelJS, err := staticFS.ReadFile("static/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	layoutCSS, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Replace(panelKVLayoutPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	if page == panelKVLayoutPage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}

	runPageInEnginesAtSize(t, page, map[string][]byte{
		"/panel.js": panelJS,
		"/state.js": []byte(`export const S = { stats: null, cont: null, work: null, config: null, providers: [] };`),
		"/util.js":  utilStub(`export const $ = id => document.getElementById(id); export const esc = value => String(value ?? '');`),
	}, 1280, 800)
}
