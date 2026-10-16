
import { S } from '../state.js';
import { $, esc, composing } from '../util.js';
import { query, send } from '../ws.js';

function sourceChoice(m) {
  const chosen = m.choice || '';
  const options = (chosen ? [] : [['', 'not chosen yet']]).concat([['none', 'nothing: the layer is off'],
    ['provider', 'the embeddings model of the provider the identity thinks with' + (m.provider_model ? '' : ' (none is named)')]]);
  const plugins = (m.plugins || []).slice();
  if (chosen.startsWith('plugin:') && !plugins.includes(chosen.slice(7))) plugins.push(chosen.slice(7));
  for (const id of plugins) {
    options.push(['plugin:' + id, 'the plugin ' + id + ', on this machine' + ((m.plugins || []).includes(id) ? '' : ' (not running)')]);
  }
  return '<div class="item"><label for="mem-source">The vectors are made by </label><select id="mem-source">' +
    options.map(([value, label]) => '<option value="' + esc(value) + '"' + (value === chosen ? ' selected' : '') + '>' + esc(label) + '</option>').join('') +
    '</select></div>';
}

export function meaningCard(m) {
  if (!m) return '';
  let html = '<div class="card" id="mem-meaning"><h3>MEANING LAYER</h3>';
  if (m.question) html += '<div class="item" id="mem-source-question">' + esc(m.question) + '</div>';
  html += sourceChoice(m);
  if (!m.source) {
    html += '<div class="item">Off: ' + esc(m.unavailable || 'no source of vectors is set up') + '.</div>' +
      '<div class="item">Recall answers by exact words and fuzzy matches. Two things can make the vectors. A plugin that computes them on this machine: install one in Plugins. ' +
      'Or the provider the identity thinks with: name an embeddings model on it in Settings → Providers → Advanced. Each memory\'s text is then sent to that provider once to be embedded.</div>';
    return html + '</div>';
  }
  const scale = '<span class="id">basis ' + esc(m.basis) + ' · floor ' + Number(m.floor).toFixed(3) + '</span>';
  if (m.source === 'plugin') {
    html += '<div class="item">Source: the plugin ' + esc(m.plugin) + ', on this machine ' + scale + '</div>' +
      '<div class="item">Each memory\'s text is handed to that plugin once to be embedded, and again if its model changes. ' +
      'It declares no permission of any kind, and the host asks it only while the operator has granted it none. The floor is the plugin\'s own, signed with it.</div>';
  } else {
    html += '<div class="item">Source: the embeddings model of the provider the identity thinks with ' + scale + '</div>' +
      '<div class="item">Each memory\'s text is sent to that provider once to be embedded, and again if the model changes. ' +
      'The floor is the host\'s own for a provider\'s model; it was measured for none.</div>';
  }
  let rows = 0, embedded = 0;
  for (const c of m.coverage || []) {
    rows += c.rows; embedded += c.embedded;
    const kept = c.beyond_embedded || 0;
    html += '<div class="item">' + esc(c.store) + ' <span class="id">' + c.embedded + ' of ' + c.rows + ' hold a vector</span>' +
      (c.beyond > 0 ? ' <span class="id">' + c.beyond + ' older are outside the provider\'s window (memory.conversation_vector_window) and get no new vector: ' +
        (kept > 0 ? kept + ' keep the one they have and are read by meaning, ' : '') + (c.beyond - kept) + ' are read by words alone</span>' : '') + '</div>';
  }
  const waiting = rows - embedded;
  html += '<div class="item">' + (waiting <= 0 ? 'Every row in scope holds a vector'
    : m.waiting ? waiting + ' waiting for voice: the backfill holds off while a session is listening or a reply is being spoken'
    : waiting + ' waiting for the backfill\'s next pass') + '.</div>';
  if (m.set_aside > 0) {
    html += '<div class="item">' + m.set_aside + ' set aside: the source could not embed ' + (m.set_aside === 1 ? 'it' : 'them') +
      (m.set_aside_why ? ' (' + esc(m.set_aside_why) + ')' : '') + '. Asked again when the source next starts.</div>';
  }
  const p = m.last_pass;
  if (p) {
    html += '<div class="item">Last pass ' + esc(new Date(p.at).toLocaleString()) + ': ' + p.embedded + ' embedded' +
      (p.stopped ? ', then stopped: ' + esc(p.stopped) : '') + '.</div>';
  }
  return html + '</div>';
}

let memFilter = '';
export function renderMemory() {
  const st = $('memory-stack');
  if (st.memoryComposing) return;
  const previous = $('mem-search');
  const focused = previous && document.activeElement === previous;
  const selection = focused ? [previous.selectionStart, previous.selectionEnd, previous.selectionDirection] : null;
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
  html += meaningCard(id.meaning);
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
  html += '<div class="card"><h3>EXPERIENCES (' + (id.experiences || []).length + ' recent)</h3>';
  html += list(id.experiences || [], { hit: x => hit(x.content), row: x =>
    '<div class="item">' + (x.unmetabolized ? '<span class="chip" title="The identity kept this note out of DREAM and CONSOLIDATE. It is recorded, recalled and shown like any other.">kept out of DREAM and CONSOLIDATE</span>' : '') +
    esc(x.content) + ' <span class="id">' + esc(x.provenance || '') + '</span></div>' });
  html += '</div>';
  st.innerHTML = html;
  let ms = $('mem-search');
  if (ms && previous) { ms.replaceWith(previous); ms = previous; }
  const source = $('mem-source');
  if (source) source.onchange = () => { send({ type: 'config_set', config: { 'memory.meaning_source': source.value } }); };
  if (ms) {
    ms.value = memFilter;
    if (focused) { ms.focus({ preventScroll: true }); ms.setSelectionRange(...selection); }
    ms.oninput = () => { memFilter = ms.value; renderMemory(); };
    ms.oncompositionstart = () => { st.memoryComposing = true; };
    ms.oncompositionend = () => { st.memoryComposing = false; memFilter = ms.value; renderMemory(); };
    ms.onkeydown = (e) => {
      if (composing(e) || st.memoryComposing || e.key !== 'Enter') return;
      e.preventDefault();
      const q = ms.value.trim();
      if (q) query('recall', { q });
    };
  }
}
