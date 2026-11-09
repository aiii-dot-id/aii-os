//go:build !windows

package dashboard

import "testing"

// VERIFICATION SAYS WHAT THE REVIEW FOUND, NOT ONLY HOW MUCH. It read
// "review 09:00 Tue Oct 13 — 2 issues" and the findings themselves were in
// the log alone. Each is a line under that row, whole and as text: a finding
// quotes a note or a belief, and a quote is never markup. (It was on About;
// the state of their record is Technical → Verification's now.)
func TestVerificationShowsEachReviewFindingWhole(t *testing.T) {
	page := `<!doctype html>
<div data-record-state></div>
<script type="module">
import { S } from './state.js';
import { recordStateHTML } from './views/settings/technical/record-state.js';
import { assert, run } from './__harness.js';
run(() => {
  const st = document.querySelector('[data-record-state]');
  const first = 'standing contradiction: [exp_1] your own note: "QUILLFEATHER the harbour ledger was balanced by lamplight" vs [b_1] — consider resolving (edge.archive) or superseding';
  const second = '12 active intentions — may need pruning <img src=x onerror="document.title=1">';
  S.cont = { mode: 'normal', ledger_seq: 9, lifetime_ticks: 3, review_at: '09:00 Tue Oct 13', review_status: 'issues', review_issues: 2, review_lines: [first, second] };
  st.innerHTML = recordStateHTML();
  const lines = [...st.querySelectorAll('[data-review-line]')].map(e => e.textContent);
  assert(lines.length === 2, 'each finding is a line of its own: ' + JSON.stringify(lines));
  assert(lines[0] === first, 'the first finding is shown whole: ' + lines[0]);
  assert(lines[1] === second && !st.querySelector('[data-review-line] img'), 'a finding is text, never an element of the page: ' + lines[1]);
  assert(/2 issues/.test(st.textContent), 'the count stays beside its words');
  S.cont = { mode: 'normal', ledger_seq: 9, lifetime_ticks: 3, review_at: '09:05 Tue Oct 13', review_status: 'clear' };
  st.innerHTML = recordStateHTML();
  assert(st.querySelectorAll('[data-review-line]').length === 0 && /clear/.test(st.textContent), 'a clear review lists nothing');
});
</script>`
	runPageInEngines(t, page, recordStateModules(t))
}
