
import { S } from './state.js';
import { $, esc } from './util.js';

export function renderPanel() {
  const el = $('slot-panel');
  if (!el) return;

  if (el.querySelector('.section-box')) return;

  const html = panelHTML();
  if (html === lastPanelHTML) return;

  el.classList.add('occupied');
  lastPanelHTML = html;
  el.innerHTML = html;
}

let lastPanelHTML = '';

function panelHTML() {
  const c = S.cont, st = S.stats, w = S.work;
  let html = '<div class="sys-panel">';

  const asks = S.asks || [];
  if (asks.length) {
    html += '<div class="sp-card sp-asks"><h3 class="sp-h">WAITING ON YOU</h3>';
    asks.forEach(a => {
      const label = a.kind === 'confirm' ? 'confirm ' + (a.operation || '') : a.kind;
      html += '<div class="sp-kv"><span>' + esc(label) + '</span><b>' + esc((a.text || '').slice(0, 80)) + '</b></div>';
    });
    html += '<div class="store-hint">answer in the thread</div></div>';
  }

  const l = S.config && S.config.llm;
  if (l) {
    const prov = l.resolved_provider || l.provider || '—';
    const mdl = l.resolved_model || l.model || '—';
    html += '<div class="sp-card"><h3 class="sp-h">SUBSTRATE</h3>';
    html += '<div class="sp-kv"><span>provider</span><b>' + esc(prov) + '</b></div>';
    html += '<div class="sp-kv"><span>model</span><b>' + esc(mdl) + '</b></div>';

    const p = (S.providers || []).find(x => x.name === prov);
    if (p && p.context_length) {
      html += '<div class="sp-kv"><span>context</span><b>' + p.context_length.toLocaleString() + '</b></div>';
    }
    html += '</div>';
  }

  if (st) {
    html += '<div class="sp-card"><h3 class="sp-h">MEMORY</h3>';
    html += '<div class="sp-counts">';
    html += '<div class="sp-count"><b>' + st.belief_count + '</b><span>beliefs</span></div>';
    html += '<div class="sp-count"><b>' + st.intention_count + '</b><span>intents</span></div>';
    html += '<div class="sp-count"><b>' + st.experience_count + '</b><span>experiences</span></div>';
    html += '<div class="sp-count"><b>' + st.reflection_count + '</b><span>reflections</span></div>';
    html += '</div>';
    html += '</div>';
  }

  if (w || S.activeProject) {
    html += '<div class="sp-card"><h3 class="sp-h">WORK</h3>';

    if (S.activeProject) {
      html += '<div class="sp-kv"><span>working in</span><b>' + esc(S.activeProject.name || S.activeProject.id) + '</b></div>';
    }
    if (w && w.live && w.live.length > 0) {
      w.live.forEach(function (item) {
        html += '<div class="sp-work-row live"><span class="sp-work-dot"></span><b>' + esc(item.description || item.id) + '</b></div>';
      });
    } else if (w && w.queued > 0) {
      html += '<div class="sp-work-row"><span class="sp-work-dot queued"></span>' + w.queued + ' queued</div>';
    } else {
      html += '<div class="sp-empty">no active session</div>';
    }
    if (w && w.delivered && w.delivered.length > 0) {
      html += '<div class="sp-delivered">' + w.delivered.length + ' recently delivered</div>';
    }
    html += '</div>';
  }

  if (st || c) {
    html += '<div class="sp-card"><h3 class="sp-h">CONTINUITY</h3>';
    if (st) {
      if (st.build) html += '<div class="sp-kv"><span>build</span><b>' + esc(st.version || 'dev') + ' &middot; ' + esc(st.build) + '</b></div>';
      html += '<div class="sp-kv"><span>ledger</span><b>seq ' + esc(String(st.ledger_seq)) + '</b></div>';
      html += '<div class="sp-kv"><span>life</span><b>' + esc(String(st.lifetime_ticks)) + ' ticks</b></div>';
    }
    if (c) {

      const anchor = !c.witness_url ? 'no witness'
        : !c.witnessed_at ? 'never witnessed'
        : c.unanchored > 0 ? 'anchored @' + c.anchored_seq + ' +' + c.unanchored
        : 'anchored';
      html += '<div class="sp-kv"><span>witnessed</span><b>' + esc(anchor) + '</b></div>';
    }
    if (st) {

      const mal = (st.malformed_calls || 0) + (st.suspicious_paths || 0) + (st.duplicate_arg_keys || 0);
      if (mal > 0) {
        html += '<div class="sp-kv"><span>channel</span><b>' + esc(String(mal)) + ' odd calls</b></div>';
      }

      const cw = st.credential_warning || '';
      if (cw) {
        html += '<div class="sp-kv"><span>credential</span><b>' + esc(String(cw)) + '</b></div>';
      }

      const lt = st.last_turn || '';
      if (lt) {
        html += '<div class="sp-kv"><span>last turn</span><b>' + esc(String(lt)) + '</b></div>';
      }
    }
    html += '</div>';
  }

  const ov = S.overlays || [];
  if (ov.length > 0) {
    html += '<div class="sp-card"><h3 class="sp-h">UI Adapters</h3>';
    ov.forEach(function (e) {

      const i = e.outcome.indexOf(':');
      const verb = i > 0 ? e.outcome.slice(0, i) : 'overlay';
      const rest = i > 0 ? e.outcome.slice(i + 2) : e.outcome;
      html += '<div class="sp-kv"><span>' + esc(verb) + '</span><b>' + esc(e.path) + '</b></div>';
      if (rest) html += '<div class="sp-kv"><span></span><b>' + esc(rest) + '</b></div>';
    });
    html += '</div>';
  }

  html += '</div>';
  return html;
}
