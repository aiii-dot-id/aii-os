//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

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

  assert(root.querySelectorAll('[data-grade-work="ws_1"]').length === 3, 'three chips on an ungraded result');
  assert(!/ungraded|please grade|rate this/i.test(text), 'nothing asks for a grade: ' + text.slice(0, 200));

  assert(root.querySelector('[data-grade-work="ws_3"]') === null, 'a graded result offers no chips');
  assert(text.includes('the second section is thin'), 'the operator\'s line is shown back');
  assert(root.querySelector('.grade-chip.is-partial') !== null, 'the grade is shown as its own chip');

  root.querySelector('[data-grade-work="ws_1"][data-grade="served"]').click();
  const served = frames.find(f => f.type === 'grade' && f.grade.session === 'ws_1');
  assert(served && served.grade.grade === 'served' && served.grade.comment === '', 'served is recorded at once: ' + JSON.stringify(served));
  assert(root.querySelectorAll('[data-grade-work="ws_1"]:not([disabled])').length === 0, 'the chips settle once pressed');

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
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; }`)
	runPageInEngines(t, page, modules)
}

func TestGradeAnswerStandsBesideTheChips(t *testing.T) {
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
    { id: 'ws_2', description: 'the migration', status: 'delivered', result: 'partial: done' },
  ] };
  const root = document.getElementById('home-inner');
  const draw = () => { root.innerHTML = workSectionHTML(); wireWork(root); };
  draw();
  const chip = (s, g) => root.querySelector('[data-grade-work="' + s + '"][data-grade="' + g + '"]');
  const note = s => chip(s, 'served').closest('.grade-ask').querySelector('.grade-note').textContent;
  const usable = s => !chip(s, 'served').disabled;

  window.__connected = false;
  chip('ws_1', 'served').click();
  assert(frames.length === 0 && /Not sent/.test(note('ws_1')) && usable('ws_1'), 'a closed socket claimed a grade: ' + note('ws_1'));

  window.__connected = true;
  chip('ws_1', 'served').click();
  assert(frames.length === 1 && note('ws_1') === 'Recording…' && !usable('ws_1'), 'a sent grade claimed a record before the answer: ' + note('ws_1'));
  assert(S.gradeAnswered('someone-else', null) === false, 'an unrelated reply was claimed');

  assert(S.gradeAnswered('g-1', 'grading is not available') === true, 'the refusal was not claimed');
  assert(note('ws_1') === 'Not recorded: grading is not available' && usable('ws_1'), 'a refusal left the chips dead: ' + note('ws_1'));
  chip('ws_1', 'served').click();
  assert(S.gradeAnswered('g-2', null) === true && note('ws_1') === 'recorded' && !usable('ws_1'), 'the host\'s yes did not say recorded: ' + note('ws_1'));

  chip('ws_2', 'partial').click();
  chip('ws_2', 'unserved').click();
  assert(chip('ws_2', 'unserved').getAttribute('aria-pressed') === 'true' && chip('ws_2', 'partial').getAttribute('aria-pressed') === 'false', 'the choice is not shown');
  root.querySelector('.grade-input').value = 'it never ran';
  root.querySelector('[data-grade-send]').click();
  const sent = frames.at(-1);
  assert(sent.grade.session === 'ws_2' && sent.grade.grade === 'unserved' && sent.grade.comment === 'it never ran', 'Record sent the chip that opened the line: ' + JSON.stringify(sent));

  draw();
  assert(note('ws_2') === 'Recording…' && !usable('ws_2'), 'redraw lost pending state');
  assert(root.querySelector('.grade-input').value === 'it never ran', 'redraw lost the explanation');
  const n = frames.length;
  chip('ws_2', 'served').click();
  assert(frames.length === n, 'redraw permitted a second pending grade');
  assert(S.gradeAnswered('g-3', 'the session is not delivered') === true && note('ws_2') === 'Not recorded: the session is not delivered' && usable('ws_2'), 'the answer was lost with the old box: ' + note('ws_2'));

  chip('ws_2', 'served').click();
  S.gradesLost();
  assert(/closed before the host answered/.test(note('ws_2')) && !/recorded$/.test(note('ws_2')), 'a lost connection claimed an outcome: ' + note('ws_2'));
  assert(S.gradeAnswered('g-4', null) === false, 'a reply to a lost request was still claimed');
  draw(); assert(!usable('ws_2'), 'redraw lost uncertain submission');
  S.gradesRead(S.work); draw();
  assert(usable('ws_2') && /No grade is recorded/.test(note('ws_2')), 'readback did not resolve uncertainty');
  assert(frames.length === n + 1, 'readback replayed a grade');
});
</script>`
	modules := map[string][]byte{}
	data, err := staticFS.ReadFile("static/views/work.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/work.js"] = data
	modules["/state.js"] = []byte(`export const S = { work: null };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; let n = 0; export function send(f) { if (!window.__connected) return ''; frames.push(f); return 'g-' + (++n); }`)
	runPageInEngines(t, page, modules)
}
