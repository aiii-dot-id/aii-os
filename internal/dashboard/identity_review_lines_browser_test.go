//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestTheIdentityPageShowsEachReviewFindingWhole(t *testing.T) {
	page := `<!doctype html>
<div id="identity-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderIdentity } from './views/identity.js';
import { assert, run } from './__harness.js';
run(() => {
  const st = document.getElementById('identity-stack');
  const first = 'standing contradiction: [exp_1] your own note: "QUILLFEATHER the harbour ledger was balanced by lamplight" vs [b_1] — consider resolving (edge.archive) or superseding';
  const second = '12 active intentions — may need pruning <img src=x onerror="document.title=1">';
  S.view = 'identity'; S.identity = { brief: 'b' };
  S.cont = { mode: 'normal', ledger_seq: 9, lifetime_ticks: 3, review_at: '09:00 Tue Oct 13', review_status: 'issues', review_issues: 2, review_lines: [first, second] };
  renderIdentity();
  const lines = [...st.querySelectorAll('[data-review-line]')].map(e => e.textContent);
  assert(lines.length === 2, 'each finding is a line of its own: ' + JSON.stringify(lines));
  assert(lines[0] === first, 'the first finding is shown whole: ' + lines[0]);
  assert(lines[1] === second && !st.querySelector('[data-review-line] img'), 'a finding is text, never an element of the page: ' + lines[1]);
  assert(/2 issues/.test(st.textContent), 'the count stays beside its words');
  S.cont = { mode: 'normal', ledger_seq: 9, lifetime_ticks: 3, review_at: '09:05 Tue Oct 13', review_status: 'clear' };
  renderIdentity();
  assert(st.querySelectorAll('[data-review-line]').length === 0 && /clear/.test(st.textContent), 'a clear review lists nothing');
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/backups.js", "static/views/identity.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = {};`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'r'; }`)
	modules["/app.js"] = []byte(`export const went = []; export function go(v) { went.push(v); }`)
	modules["/views/settings.js"] = []byte(`export const opened = []; export function openBackups() { opened.push(1); }`)
	runPageInEngines(t, page, modules)
}
