//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestGradeChipsInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="home-inner"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { workSectionHTML, wireWork } from './views/work.js';
import { frames } from './ws.js';
run(() => {
  S.work = { live: [], queued: 0, delivered: [
    { id: 'ws_1', description: 'book the flights', status: 'delivered', result: 'served: both legs booked' },
    { id: 'ws_2', description: 'the migration', status: 'delivered', result: 'served: done', project: 'proj-a' },
    { id: 'ws_3', description: 'the report', status: 'delivered', result: 'partial: two sections',
      grade: { grade: 'partial', comment: 'the second section is thin', turn: 41 } },
  ] };
  const root = document.getElementById('home-inner');
  root.innerHTML = workSectionHTML();
  wireWork(root);
  const text = root.textContent;

  // Offered, never asked: chips, no card, no prompt, no count.
  assert(root.querySelectorAll('[data-grade-work="ws_1"]').length === 3, 'three chips on an ungraded result');
  assert(!/ungraded|please grade|rate this/i.test(text), 'nothing asks for a grade: ' + text.slice(0, 200));

  // A grade already given shows the operator's own words, not chips.
  assert(root.querySelector('[data-grade-work="ws_3"]') === null, 'a graded result offers no chips');
  assert(text.includes('the second section is thin'), 'the operator\'s line is shown back');
  assert(root.querySelector('.grade-chip.is-partial') !== null, 'the grade is shown as its own chip');

  // Served sends at once, with no line.
  root.querySelector('[data-grade-work="ws_1"][data-grade="served"]').click();
  const served = frames.find(f => f.type === 'grade' && f.grade.session === 'ws_1');
  assert(served && served.grade.grade === 'served' && served.grade.comment === '', 'served is recorded at once: ' + JSON.stringify(served));
  assert(root.querySelectorAll('[data-grade-work="ws_1"]:not([disabled])').length === 0, 'the chips settle once pressed');

  // Not served opens the line first and sends nothing yet.
  const before = frames.length;
  root.querySelector('[data-grade-work="ws_2"][data-grade="unserved"]').click();
  assert(frames.length === before, 'the second and third chips ask for the reason before recording');
  const input = root.querySelector('.grade-input');
  assert(input !== null, 'the line opened');
  input.value = 'it never ran';
  root.querySelector('[data-grade-send]').click();
  const unserved = frames.find(f => f.type === 'grade' && f.grade.session === 'ws_2');
  assert(unserved && unserved.grade.grade === 'unserved' && unserved.grade.comment === 'it never ran', 'the line rides the grade: ' + JSON.stringify(unserved));
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/work.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { work: null };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; }`)
	runPageInEngines(t, page, modules)
}
