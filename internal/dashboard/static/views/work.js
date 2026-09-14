
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send } from '../ws.js';

export function workSectionHTML() {
  if (!(S.work && ((S.work.live || []).length || S.work.queued > 0 || (S.work.delivered || []).length))) return '';
  let html = '';
  html += '<div class="home-h">LIVE WORK — RING 4</div>';
  (S.work.live || []).forEach(w => {
    html += '<div class="work-row live"><span class="work-dot"></span><b>' + esc(w.description) + '</b>' + (w.project ? '<span class="work-proj">' + esc(w.project) + '</span>' : '') + '<span class="work-st">running</span></div>';
  });
  if (S.work.queued > 0) html += '<div class="work-row"><span class="work-dot queued"></span>' + S.work.queued + ' queued</div>';
  (S.work.delivered || []).forEach(w => {
    html += '<div class="work-row done" data-work="' + esc(w.id) + '"><span class="work-dot done"></span><b>' + esc(w.description) + '</b>' + (w.project ? '<span class="work-proj">' + esc(w.project) + '</span>' : '') + '<span class="work-st">delivered</span>' +
      (w.result ? '<div class="work-res">' + esc(w.result) + '</div>' : '') + gradeHTML(w) + '</div>';
  });
  return html;
}

// THE OPERATOR'S WORD ON A RESULT (seam 2).
// Three chips and a line, offered beside every delivered result and
// never asked for: no card, no prompt, no count of ungraded results.
// A grade already given is shown in its place — the operator's own
// words back, not a tick.
const GRADES = [['served', 'Served'], ['partial', 'Partly'], ['unserved', 'Not served']];
function gradeHTML(w) {
  if (w.grade) {
    return '<div class="grade-said"><span class="grade-chip is-' + esc(w.grade.grade) + '">' + esc(gradeLabel(w.grade.grade)) + '</span>' +
      (w.grade.comment ? '<span class="grade-line">' + esc(w.grade.comment) + '</span>' : '') + '</div>';
  }
  return '<div class="grade-ask">' + GRADES.map(g =>
    '<button class="grade-chip" data-grade="' + g[0] + '" data-grade-work="' + esc(w.id) + '">' + g[1] + '</button>').join('') +
    '<span class="grade-note"></span></div>';
}
function gradeLabel(g) {
  const found = GRADES.find(x => x[0] === g);
  return found ? found[1] : g;
}

// wireWork attaches the chips. The first chip sends at once — a served
// result needs no explanation; the other two open the line first,
// because on those the reason IS the signal.
export function wireWork(root) {
  if (!root) return;
  root.querySelectorAll('[data-grade]').forEach(b => { b.onclick = () => {
    const session = b.dataset.gradeWork, grade = b.dataset.grade;
    const box = b.closest('.grade-ask');
    if (grade === 'served') { sendGrade(box, session, grade, ''); return; }
    if (box.querySelector('.grade-input')) return;
    box.insertAdjacentHTML('beforeend', '<div class="grade-reply"><input type="text" class="grade-input" maxlength="500" placeholder="What was missing? (optional)"><button class="btn" data-grade-send="1">Record</button></div>');
    const input = box.querySelector('.grade-input');
    const fire = () => sendGrade(box, session, grade, input.value.trim());
    box.querySelector('[data-grade-send]').onclick = fire;
    input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); fire(); } });
    input.focus();
  }; });
}
function sendGrade(box, session, grade, comment) {
  box.querySelectorAll('button, input').forEach(el => { el.disabled = true; });
  const note = box.querySelector('.grade-note');
  if (note) note.textContent = 'recorded';
  send({ type: 'grade', grade: { session: session, grade: grade, comment: comment } });
}

export function renderWorkPill() {
  const el = $('pill-work');
  const n = S.work ? (S.work.live || []).length + (S.work.queued || 0) : 0;
  if (n > 0) {
    el.style.display = '';
    el.innerHTML = '<span class="work-dot" style="display:inline-block;vertical-align:0"></span> <b>' + n + '</b> working';
  } else el.style.display = 'none';
}
