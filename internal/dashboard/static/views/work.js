
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
// Edits and the one outstanding request belong to the session, not a DOM
// node: a status push may redraw Home while the request is out.
const grades = new Map();
function gradeHTML(w) {
  if (w.grade) {
    grades.delete(w.id);
    return '<div class="grade-said"><span class="grade-chip is-' + esc(w.grade.grade) + '">' + esc(gradeLabel(w.grade.grade)) + '</span>' +
      (w.grade.comment ? '<span class="grade-line">' + esc(w.grade.comment) + '</span>' : '') + '</div>';
  }
  const d = grades.get(w.id) || {}, disabled = d.requestID || d.uncertain ? ' disabled' : '';
  return '<div class="grade-ask">' + GRADES.map(g =>
    '<button class="grade-chip" data-grade="' + g[0] + '" data-grade-work="' + esc(w.id) + '" aria-pressed="' + (d.grade === g[0]) + '"' + disabled + '>' + g[1] + '</button>').join('') +
    '<span class="grade-note" role="status">' + esc(d.note || '') + '</span>' +
    (d.grade && d.grade !== 'served' ? '<div class="grade-reply"><input type="text" class="grade-input" data-grade-session="' + esc(w.id) + '" maxlength="500" placeholder="What was missing? (optional)" value="' + esc(d.comment || '') + '"' + disabled + '><button class="btn" data-grade-send="1"' + disabled + '>Record</button></div>' : '') + '</div>';
}
function gradeLabel(g) { return GRADES.find(x => x[0] === g)?.[1] || g; }
function gradeBox(session) {
  return document.querySelector('.grade-ask [data-grade-work="' + CSS.escape(session) + '"]')?.closest('.grade-ask');
}
function paintGrade(session) {
  const box = gradeBox(session); if (!box) return;
  const d = grades.get(session) || {};
  box.querySelectorAll('button, input').forEach(el => { el.disabled = !!(d.requestID || d.uncertain); });
  box.querySelectorAll('[data-grade]').forEach(el => el.setAttribute('aria-pressed', String(el.dataset.grade === d.grade)));
  box.querySelector('.grade-note').textContent = d.note || '';
}
function draft(session) {
  if (!grades.has(session)) grades.set(session, { grade: '', comment: '', note: '', requestID: '' });
  return grades.get(session);
}
function wireGradeInput(box, session) {
  const input = box.querySelector('.grade-input'); if (!input) return;
  input.oninput = () => { draft(session).comment = input.value; };
  const fire = () => { const d = draft(session); d.comment = input.value; sendGrade(session, d.grade, input.value.trim()); };
  box.querySelector('[data-grade-send]').onclick = fire;
  input.onkeydown = e => { if (e.isComposing || e.keyCode === 229) return; if (e.key === 'Enter') { e.preventDefault(); fire(); } };
}
export function wireWork(root) {
  if (!root) return;
  root.querySelectorAll('.grade-ask').forEach(box => {
    const session = box.querySelector('[data-grade-work]').dataset.gradeWork;
    wireGradeInput(box, session);
    box.querySelectorAll('[data-grade]').forEach(b => { b.onclick = () => {
      const d = draft(session); if (d.requestID || d.uncertain) return;
      d.grade = b.dataset.grade;
      if (d.grade === 'served') { sendGrade(session, d.grade, ''); return; }
      if (!box.querySelector('.grade-input')) {
        box.insertAdjacentHTML('beforeend', '<div class="grade-reply"><input type="text" class="grade-input" data-grade-session="' + esc(session) + '" maxlength="500" placeholder="What was missing? (optional)"><button class="btn" data-grade-send="1">Record</button></div>');
        box.querySelector('.grade-input').value = d.comment;
        wireGradeInput(box, session);
      }
      paintGrade(session); box.querySelector('.grade-input').focus();
    }; });
  });
}
function sendGrade(session, grade, comment) {
  const d = draft(session); if (d.requestID || d.uncertain) return;
  const id = send({ type: 'grade', grade: { session, grade, comment } });
  d.requestID = id;
  d.note = id ? 'Recording…' : 'Not sent: the dashboard is not connected. Try again once it reconnects.';
  paintGrade(session);
}
S.gradeAnswered = (id, error) => {
  const found = [...grades].find(([, d]) => id && d.requestID === id);
  if (!found) return false;
  const [session, d] = found;
  d.requestID = ''; d.note = error ? 'Not recorded: ' + error : 'recorded'; d.uncertain = !error;
  paintGrade(session); return true;
};
S.gradesLost = () => {
  for (const [session, d] of grades) if (d.requestID) {
    d.requestID = ''; d.uncertain = true;
    d.note = 'The connection closed before the host answered. Checking the recorded result on reconnect.';
    paintGrade(session);
  }
};
// An authoritative work snapshot resolves uncertainty without replaying a save.
S.gradesRead = work => {
  for (const w of work?.delivered || []) {
    const d = grades.get(w.id); if (!d || d.requestID) continue;
    if (w.grade) grades.delete(w.id);
    else if (d.uncertain) { d.uncertain = false; d.note = 'No grade is recorded. You can try again.'; }
  }
};

export function renderWorkPill() {
  const el = $('pill-work');
  const n = S.work ? (S.work.live || []).length + (S.work.queued || 0) : 0;
  if (n > 0) {
    el.style.display = '';
    el.innerHTML = '<span class="work-dot" style="display:inline-block;vertical-align:0"></span> <b>' + n + '</b> working';
  } else el.style.display = 'none';
}
