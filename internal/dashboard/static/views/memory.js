/* views/memory.js — the memory surface: beliefs, intentions,
   experiences with client-side filter; private items surface as a
   COUNT, never content. R66: half of the identity-inner-life default
   section (UI_FRAME.md §6), fed read-only from identity queries. */
import { S } from '../state.js';
import { $, esc } from '../util.js';

/* --- memory ------------------------------------------------- */
let memFilter = '';
export function renderMemory() {
  const st = $('memory-stack');
  const id = S.identity;
  if (!id) { st.innerHTML = '<div class="empty">listening for memory…</div>'; return; }
  const f = memFilter.toLowerCase();
  const hit = t => !f || String(t || '').toLowerCase().indexOf(f) !== -1;
  let html = '<div class="card" style="padding:12px 16px"><input type="text" id="mem-search" placeholder="Search memory — beliefs, intentions, experiences…" value="' + esc(memFilter) + '"></div>';
  if (id.synthesis) html += '<div class="card"><h3>SELF-MODEL SYNTHESIS</h3><div class="quote">' + esc(id.synthesis) + '</div></div>';
  html += '<div class="card"><h3>BELIEFS (' + (id.beliefs || []).length + ')</h3>';
  html += (id.beliefs || []).length ? (id.beliefs || []).filter(b => hit(b.statement)).map(b =>
    '<div class="item"><span class="chip ' + esc(b.status || '') + '">' + esc(b.status || 'held') + '</span>' +
    esc(b.statement) + ' <span class="id">' + esc(b.id) + ' · ring ' + b.ring + '</span></div>').join('') :
    '<div class="empty">none yet</div>';
  html += '</div>';
  html += '<div class="card"><h3>INTENTIONS (' + (id.intentions || []).length + ')</h3>';
  html += (id.intentions || []).length ? (id.intentions || []).filter(i => hit(i.statement)).map(i =>
    '<div class="item"><span class="chip ' + esc(i.state || '') + '">' + esc(i.state || '') + '</span>' + esc(i.statement) + '</div>').join('') :
    '<div class="empty">none yet</div>';
  html += '</div>';
  html += '<div class="card"><h3>EXPERIENCES (' + (id.experiences || []).length + ' recent' +
    (id.private_count ? ' · ' + id.private_count + ' private' : '') + ')</h3>';
  html += (id.experiences || []).length ? (id.experiences || []).filter(x => hit(x.content)).map(x =>
    '<div class="item">' + esc(x.content) + ' <span class="id">' + esc(x.provenance || '') + '</span></div>').join('') :
    '<div class="empty">none yet</div>';
  html += '</div>';
  st.innerHTML = html;
  const ms = $('mem-search');
  if (ms) {
    ms.oninput = () => { memFilter = ms.value; const pos = ms.selectionStart; renderMemory(); const ms2 = $('mem-search'); ms2.focus(); ms2.setSelectionRange(pos, pos); };
  }
}
