/* views/work.js — the WORK surface: Ring-4 agency rendered
   honestly (live sub-agents, queue depth, delivered results) as the
   home rows and the stagebar pill. R66: the work-sessions default
   section (UI_FRAME.md §6) — this module is the seed that ships as
   an ASSET-kind section package in UP3. */
import { S } from '../state.js';
import { $, esc } from '../util.js';

// The home rows: '' when there is nothing to show (the caller
// concatenates unconditionally — same rendered bytes as the
// pre-split inline block).
export function workSectionHTML() {
  if (!(S.work && ((S.work.live || []).length || S.work.queued > 0 || (S.work.delivered || []).length))) return '';
  let html = '';
  html += '<div class="home-h">LIVE WORK — RING 4</div>';
  (S.work.live || []).forEach(w => {
    html += '<div class="work-row live"><span class="work-dot"></span><b>' + esc(w.description) + '</b>' + (w.project ? '<span class="work-proj">' + esc(w.project) + '</span>' : '') + '<span class="work-st">running</span></div>';
  });
  if (S.work.queued > 0) html += '<div class="work-row"><span class="work-dot queued"></span>' + S.work.queued + ' queued</div>';
  (S.work.delivered || []).forEach(w => {
    html += '<div class="work-row done"><span class="work-dot done"></span><b>' + esc(w.description) + '</b>' + (w.project ? '<span class="work-proj">' + esc(w.project) + '</span>' : '') + '<span class="work-st">delivered</span>' +
      (w.result ? '<div class="work-res">' + esc(w.result) + '</div>' : '') + '</div>';
  });
  return html;
}

export function renderWorkPill() {
  const el = $('pill-work');
  const n = S.work ? (S.work.live || []).length + (S.work.queued || 0) : 0;
  if (n > 0) {
    el.style.display = '';
    el.innerHTML = '<span class="work-dot" style="display:inline-block;vertical-align:0"></span> <b>' + n + '</b> working';
  } else el.style.display = 'none';
}
