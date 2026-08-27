/* views/identity.js — the identity surface: who the resident is
   right now (brief), the charter, continuity/anchoring, the
   relationship levels. R66: the other half of the identity-inner-
   life default section (UI_FRAME.md §6); read-only by design. */
import { S } from '../state.js';
import { $, esc } from '../util.js';

/* --- identity ----------------------------------------------- */
export function renderIdentity() {
  const st = $('identity-stack');
  const id = S.identity, c = S.cont;
  let html = '';
  if (id && id.brief) html += '<div class="card"><h3>WHO THEY ARE RIGHT NOW</h3><div class="quote">' + esc(id.brief) + '</div></div>';
  if (id && id.charter) html += '<div class="card"><h3>CHARTER — RING 1</h3><div style="white-space:pre-wrap;font-size:13.5px;line-height:1.6;color:var(--dim)">' + esc(id.charter) + '</div></div>';
  html += '<div class="card"><h3>CONTINUITY</h3>';
  if (c) {
    html += '<div class="kv"><span>mode</span><b>' + esc(c.mode || 'normal') + (c.safe_reason ? ' — ' + esc(c.safe_reason) : '') + '</b></div>';
    html += '<div class="kv"><span>ledger seq</span><b>' + c.ledger_seq + '</b></div>';
    html += '<div class="kv"><span>witnessed</span><b>' + witnessedText(c) + '</b></div>';
    if (c.witnessed_at) html += '<div class="kv"><span>last witness</span><b>' + esc(c.witnessed_at) + '</b></div>';
    html += '<div class="kv"><span>life</span><b>' + c.lifetime_ticks + ' ticks</b></div>';
    if (c.review_at) html += '<div class="kv"><span>review</span><b>' + esc(c.review_at) + (c.review_status === 'issues' ? ' — ' + c.review_issues + ' issue' + (c.review_issues === 1 ? '' : 's') : ' — clear') + '</b></div>';
    html += '<div class="kv"><span>witness</span><b style="font-family:var(--mono);font-size:11px">' + esc(c.witness_url || '—') + '</b></div>';
  } else html += '<div class="empty">waiting…</div>';
  html += '</div>';
  if (id) html += '<div class="card"><h3>RELATIONSHIP</h3>' +
    '<div class="kv"><span>trust</span><b>' + esc(id.trust_level || '—') + '</b></div>' +
    '<div class="kv"><span>autonomy</span><b>' + esc(id.autonomy_level || '—') + '</b></div></div>';
  st.innerHTML = html || '<div class="empty">listening…</div>';
}


// Three separate facts, not one number: whether a witness is configured
// at all, whether this identity has EVER been anchored, and how many
// events have accrued since the last anchor. Collapsing them into a raw
// pending count made a healthy identity look permanently broken —
// interval_events is 100 by default, so a live resident is "unanchored"
// almost all the time by design. Reported live 2026-08-23 ("witness
// always says unanchored") against an identity whose ledger was in fact
// anchored at ordinal 106 with a valid receipt.
function witnessedText(c) {
  if (!c.witness_url) return 'no witness configured';
  if (!c.witnessed_at) return 'never witnessed';
  if (c.unanchored > 0) return 'anchored @' + c.anchored_seq + ' — ' + c.unanchored + ' since';
  return 'fully anchored @' + c.anchored_seq;
}
