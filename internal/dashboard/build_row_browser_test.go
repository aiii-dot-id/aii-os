//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"strings"
	"testing"
)

// The build identity has to arrive on a SCREEN, not only on the wire.
//
// BuildIdentity was correct and unreachable: computed at boot, stated in
// one log line, and nowhere else. Putting it in StatsResponse without
// rendering it would move the defect one layer up rather than end it —
// a field no screen shows is as unaskable as a log nobody kept.
const buildRowPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<main class="stage"></main>
<aside class="slot panel-col occupied" id="slot-panel"></aside>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPanel } from './panel.js';

run(() => {
  S.stats = { ledger_seq: 121, lifetime_ticks: 4242, belief_count: 0, intention_count: 0,
              experience_count: 0, reflection_count: 0, malformed_calls: 0, suspicious_paths: 0,
              version: '0.1.0', build: 'ee418ce4ff77 (dirty)' };
  S.cont = null; S.work = null; S.config = null; S.providers = [];
  renderPanel();

  let row = null;
  document.querySelectorAll('.sp-kv').forEach(r => {
    const name = r.querySelector('span');
    if (name && name.textContent.trim() === 'build') row = r;
  });
  assert(row !== null, 'the operator cannot see which binary produced these numbers — no build row');

  const reading = row.querySelector('b').textContent;
  assert(reading.includes('0.1.0'), 'the version is missing from the build row: ' + reading);
  assert(reading.includes('ee418ce4ff77'), 'the commit is missing from the build row: ' + reading);
  assert(reading.includes('dirty'), 'the dirty flag was dropped — a modified build would read as clean');

  const rr = row.getBoundingClientRect();
  assert(rr.width > 0 && rr.height > 0, 'the build row rendered with no geometry');

  // It frames the numbers beneath it, so it comes first.
  let ledger = null;
  document.querySelectorAll('.sp-kv').forEach(r => {
    const n = r.querySelector('span');
    if (n && n.textContent.trim() === 'ledger') ledger = r;
  });
  assert(ledger !== null, 'the continuity rows vanished');
  assert(rr.top <= ledger.getBoundingClientRect().top,
    'the build sits below the counts it produced');
});
</script>`

func TestBuildIdentityReachesTheOperatorsScreen(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	page := strings.Replace(buildRowPage, "__LAYOUT_CSS__", string(read("layout.css")), 1)
	if page == buildRowPage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}
	runPageInEngines(t, page, map[string][]byte{
		"/panel.js": read("panel.js"),
		"/state.js": []byte(`export const S = { stats: null, cont: null, work: null, config: null, providers: [] };`),
		"/util.js":  []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`),
	})
}
