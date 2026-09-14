
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { query } from '../ws.js';

let memFilter = '';
export function renderMemory() {
  const st = $('memory-stack');
  const id = S.identity;
  if (!id) { st.innerHTML = '<div class="empty">listening for memory…</div>'; return; }
  const f = memFilter.toLowerCase();
  const hit = t => !f || String(t || '').toLowerCase().indexOf(f) !== -1;
  const list = (items, render) => {
    if (!items.length) return '<div class="empty">none yet</div>';
    const shown = items.filter(render.hit);
    if (!shown.length) return '<div class="empty">no matches among what is loaded here — press Enter to recall the whole record</div>';
    return shown.map(render.row).join('');
  };
  let html = '<div class="card" style="padding:12px 16px"><input type="text" id="mem-search" placeholder="Filter what is loaded · Enter recalls the whole record" value="' + esc(memFilter) + '"></div>';
  if (S.recall) {
    html += '<div class="card"><h3>RECALL — the whole record, for “' + esc(S.recall.query) + '”</h3><pre class="quote" id="mem-recall" style="white-space:pre-wrap">' + esc(S.recall.text) + '</pre></div>';
  }
  if (id.synthesis) html += '<div class="card"><h3>SELF-MODEL SYNTHESIS</h3><div class="quote">' + esc(id.synthesis) + '</div></div>';
  html += '<div class="card"><h3>BELIEFS (' + (id.beliefs || []).length + ')</h3>';
  html += list(id.beliefs || [], { hit: b => hit(b.statement), row: b =>
    '<div class="item"><span class="chip ' + esc(b.status || '') + '">' + esc(b.status || 'held') + '</span>' +
    esc(b.statement) + ' <span class="id">' + esc(b.id) + ' · ring ' + b.ring + '</span></div>' });
  html += '</div>';
  html += '<div class="card"><h3>INTENTIONS (' + (id.intentions || []).length + ')</h3>';
  html += list(id.intentions || [], { hit: i => hit(i.statement), row: i =>
    '<div class="item"><span class="chip ' + esc(i.state || '') + '">' + esc(i.state || '') + '</span>' + esc(i.statement) + '</div>' });
  html += '</div>';
  html += '<div class="card"><h3>EXPERIENCES (' + (id.experiences || []).length + ' recent' +
    (id.private_count ? ' · ' + id.private_count + ' private' : '') + ')</h3>';
  html += list(id.experiences || [], { hit: x => hit(x.content), row: x =>
    '<div class="item">' + esc(x.content) + ' <span class="id">' + esc(x.provenance || '') + '</span></div>' });
  html += '</div>';
  st.innerHTML = html;
  const ms = $('mem-search');
  if (ms) {
    ms.oninput = () => { memFilter = ms.value; const pos = ms.selectionStart; renderMemory(); const ms2 = $('mem-search'); ms2.focus(); ms2.setSelectionRange(pos, pos); };
    ms.onkeydown = (e) => {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      const q = ms.value.trim();
      if (q) query('recall', { q });
    };
  }
}
