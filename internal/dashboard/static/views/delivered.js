import { S } from '../state.js';
import { esc, composing } from '../util.js';
import { send } from '../ws.js';
import { holdReloadWhile } from '../overlay.js';
import { recordedHTML } from './work-recorded.js';

const OUTSIDE = '\u0000outside';

const GRADES = [['served', 'Served'], ['partial', 'Partly'], ['unserved', 'Not served']];

const grades = new Map();

function gradeLabel(g) { return (GRADES.find((x) => x[0] === g) || [])[1] || g; }

function gradeHTML(w) {
  if (w.grade) {
    grades.delete(w.id);
    return '<div class="grade-said"><span class="grade-chip is-' + esc(w.grade.grade) + '">' +
      esc(gradeLabel(w.grade.grade)) + '</span>' +
      (w.grade.comment ? '<span class="grade-line">' + esc(w.grade.comment) + '</span>' : '') + '</div>';
  }
  const d = grades.get(w.id) || {};
  const disabled = d.requestID || d.uncertain ? ' disabled' : '';
  return '<div class="grade-ask">' + GRADES.map((g) =>
    '<button class="grade-chip" data-grade="' + g[0] + '" data-grade-work="' + esc(w.id) +
    '" aria-pressed="' + (d.grade === g[0]) + '"' + disabled + '>' + g[1] + '</button>').join('') +
    '<span class="grade-note" role="status">' + esc(d.note || '') + '</span>' +
    (d.grade && d.grade !== 'served'
      ? '<div class="grade-reply"><textarea rows="2" class="grade-input" data-grade-session="' + esc(w.id) +
        '" maxlength="500" placeholder="What was missing? (optional)"' + disabled + '>' + esc(d.comment || '') +
        '</textarea><button class="btn" data-grade-send="1"' + disabled + '>Record</button></div>'
      : '') + '</div>';
}

function gradeBox(root, session) {
  if (!root) return null;
  return root.querySelector('.grade-ask [data-grade-work="' + CSS.escape(session) + '"]')?.closest('.grade-ask') || null;
}

function paintGrade(root, session) {
  const box = gradeBox(root, session);
  if (!box) return;
  const d = grades.get(session) || {};
  box.querySelectorAll('button, textarea').forEach((el) => { el.disabled = !!(d.requestID || d.uncertain); });
  box.querySelectorAll('[data-grade]').forEach((el) => el.setAttribute('aria-pressed', String(el.dataset.grade === d.grade)));
  const note = box.querySelector('.grade-note');
  if (note) note.textContent = d.note || '';
}

function draft(session) {
  if (!grades.has(session)) grades.set(session, { grade: '', comment: '', note: '', requestID: '' });
  return grades.get(session);
}

function wireGradeInput(root, box, session) {
  const input = box.querySelector('.grade-input');
  if (!input) return;
  input.oninput = () => { draft(session).comment = input.value; };
  const fire = () => {
    const d = draft(session);
    d.comment = input.value;
    sendGrade(root, session, d.grade, input.value.trim());
  };
  const sendBtn = box.querySelector('[data-grade-send]');
  if (sendBtn) sendBtn.onclick = fire;
  input.onkeydown = (e) => {
    if (composing(e)) return;
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); fire(); }
  };
}

const unpress = d => { if (d.grade === 'served') d.grade = ''; };

function sendGrade(root, session, grade, comment) {
  const d = draft(session);
  if (d.requestID || d.uncertain) return;
  const id = send({ type: 'grade', grade: { session, grade, comment } });
  d.requestID = id;
  d.note = id ? 'Recording\u2026' : 'Not sent — not connected. Try again when the connection is back.';
  if (!id) unpress(d);
  paintGrade(root, session);
}

export function wireDelivered(root) {
  if (!root) return;
  root.querySelectorAll('.grade-ask').forEach((box) => {
    const btn = box.querySelector('[data-grade-work]');
    if (!btn) return;
    const session = btn.dataset.gradeWork;
    wireGradeInput(root, box, session);
    box.querySelectorAll('[data-grade]').forEach((b) => {
      b.onclick = () => {
        const d = draft(session);
        if (d.requestID || d.uncertain) return;
        d.grade = b.dataset.grade;
        if (d.grade === 'served') { sendGrade(root, session, d.grade, ''); return; }
        if (!box.querySelector('.grade-input')) {
          box.insertAdjacentHTML('beforeend',
            '<div class="grade-reply"><textarea rows="2" class="grade-input" data-grade-session="' + esc(session) +
            '" maxlength="500" placeholder="What was missing? (optional)"></textarea><button class="btn" data-grade-send="1">Record</button></div>');
          box.querySelector('.grade-input').value = d.comment;
          wireGradeInput(root, box, session);
        }
        paintGrade(root, session);
        box.querySelector('.grade-input').focus();
      };
    });
  });
}

holdReloadWhile(() => [...grades.values()].some((d) => d.requestID || (d.comment.trim() && !d.uncertain)));

S.gradeAnswered = (id, error) => {
  const found = [...grades].find(([, d]) => id && d.requestID === id);
  if (!found) return false;
  const [session, d] = found;
  d.requestID = '';
  d.note = error ? 'Not recorded: ' + error : 'recorded';
  d.uncertain = !error;
  if (error) unpress(d);
  paintGrade(activeRoot, session);
  return true;
};

S.gradesLost = () => {
  for (const [session, d] of grades) {
    if (!d.requestID) continue;
    d.requestID = '';
    d.uncertain = true;
    d.note = 'The connection closed before the host answered. Checking the recorded result on reconnect.';
    paintGrade(activeRoot, session);
  }
};

S.gradesRead = (work) => {
  for (const w of (work && work.delivered) || []) {
    const d = grades.get(w.id);
    if (!d || d.requestID) continue;
    if (w.grade) grades.delete(w.id);
    else if (d.uncertain) { d.uncertain = false; d.note = 'No grade is recorded. You can try again.'; unpress(d); }
  }
};

let activeRoot = null;

export function deliveredHTML(showHeading) {
  const all = (S.work && S.work.delivered) || [];
  if (!all.length) return '';

  const byProject = new Map();
  for (const w of all) {
    const key = w.project ? String(w.project) : OUTSIDE;
    if (!byProject.has(key)) byProject.set(key, []);
    byProject.get(key).push(w);
  }

  const names = new Map();
  for (const p of S.projects || []) names.set(String(p.id), p.name);

  const row = (w) =>
    '<div class="work-row done" data-work="' + esc(w.id) + '"><span class="work-dot done"></span><b>' +
    esc(w.description) + '</b><span class="work-st">delivered</span>' +
    (w.result ? '<div class="work-res">' + esc(w.result) + '</div>' : '') +
    recordedHTML(w) + gradeHTML(w) + '</div>';

  const keys = [...byProject.keys()].sort((a, b) => (a === OUTSIDE ? -1 : b === OUTSIDE ? 1 : 0));

  let html = showHeading === false ? '' : '<h3 class="projects__h3 delivered-title">Delivered</h3>';
  for (const key of keys) {
    const rows = byProject.get(key);
    const title = key === OUTSIDE
      ? 'Outside any project'
      : (names.get(key) || key);
    html += '<div class="delivered-group" data-delivered-group="' + esc(key === OUTSIDE ? '' : key) + '">' +
      '<div class="delivered-h">' + esc(title) + '</div>' +
      rows.map(row).join('') + '</div>';
  }
  return html;
}

export function renderDelivered(host, showHeading, keep) {
  if (!host) return;
  const html = deliveredHTML(showHeading);
  host.innerHTML = html;
  activeRoot = host;
  if (html) wireDelivered(host);
  restoreGradeFocus(host, keep);
}

export function gradeFocusOf() {
  const el = document.activeElement;
  if (!el || !el.classList || !el.classList.contains('grade-input')) return null;
  return { session: el.dataset.gradeSession, start: el.selectionStart, end: el.selectionEnd };
}

function restoreGradeFocus(root, keep) {
  if (!keep || !root) return;
  const el = root.querySelector('.grade-input[data-grade-session="' + CSS.escape(keep.session) + '"]');
  if (!el || el.disabled) return;
  el.focus();
  el.setSelectionRange(keep.start, keep.end);
}
