import { S } from '../../../state.js';
import { esc } from '../../../util.js';
import { requestBackups } from '../../backups.js';
import W from '../../../strings/settings-technical-forms.js';

function backupsLines() {
  const v = S.backups;
  if (!v) return '';
  const newest = (v.snapshots || [])[0];
  const last = !v.enabled ? '<span class="bad-text">switched off</span>'
    : !newest ? 'none yet'
    : new Date(newest.at).toLocaleString() + ' · record ' + esc(newest.record) + (newest.encrypted ? ' · encrypted' : ' · <span class="bad-text">not encrypted</span>');
  const failed = v.last_pass && v.last_pass.outcome === 'failed' ? ' <span class="bad-text">(the last pass FAILED)</span>' : '';
  return '<div class="kv"><span>last snapshot</span><b data-id-snapshot>' + last + failed + '</b></div>' +
    '<div class="kv"><span>keys escrowed</span><b data-id-escrow>' + (v.escrow_covers ? 'checked ' + esc(new Date(v.escrow_checked_at).toLocaleDateString()) : '<span class="bad-text">no</span>') +
    ' · <a href="#/settings/backups" data-open-backups>Backups</a></b></div>';
}

function witnessedText(c) {
  if (!c.witness_url) return 'no witness configured';
  if (!c.witnessed_at) return 'never witnessed';
  if (c.unanchored > 0) return 'anchored @' + c.anchored_seq + ' — ' + c.unanchored + ' since';
  return 'fully anchored @' + c.anchored_seq;
}

export function readRecordState() { if (!S.backups) requestBackups(); }

export function recordStateHTML() {
  const c = S.cont;
  let html = '<h3 class="technical-form__group-title">' + esc(W.verification.recordState) + '</h3>';
  if (!c) return html + '<div class="empty">waiting…</div>';
  const modeWhy = c.safe_reason || c.degraded_why;
  html += '<div class="kv"><span>mode</span><b>' + esc(c.mode || 'normal') + (modeWhy ? ' — ' + esc(modeWhy) : '') + '</b></div>';
  if ((c.mode || 'normal') === 'safe' && c.safe_cleared) html += '<div class="dim-note" data-safe-cleared>' + esc(c.safe_cleared) + '</div>';
  html += '<div class="kv"><span>ledger seq</span><b>' + c.ledger_seq + '</b></div>';
  html += '<div class="kv"><span>witnessed</span><b>' + witnessedText(c) + '</b></div>';
  if (c.witnessed_at) html += '<div class="kv"><span>last witness</span><b>' + esc(c.witnessed_at) + '</b></div>';
  html += '<div class="kv"><span>life</span><b>' + c.lifetime_ticks + (c.lifetime_ticks === 1 ? ' tick' : ' ticks') + '</b></div>';
  if (c.review_at) html += '<div class="kv"><span>review</span><b>' + esc(c.review_at) + (c.review_status === 'issues' ? ' — ' + c.review_issues + ' issue' + (c.review_issues === 1 ? '' : 's') : ' — clear') + '</b></div>';
  (c.review_lines || []).forEach(l => { html += '<div class="dim-note" data-review-line>' + esc(l) + '</div>'; });
  html += '<div class="kv"><span>witness</span><b class="kv-code">' + esc(c.witness_url || '—') + '</b></div>';
  html += backupsLines();
  if ((c.mode || 'normal') === 'safe') html += '<div class="dim-note" data-safe-restore>This record is frozen. If it cannot be repaired, a snapshot can be put back: <a href="#/settings/backups" data-open-backups>restore from a backup</a>.</div>';
  return html;
}

S.renderRecordState = () => {
  const el = document.querySelector('[data-record-state]');
  if (!el) return;
  const html = recordStateHTML();
  if (el.innerHTML !== html) el.innerHTML = html;
};
