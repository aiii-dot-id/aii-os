
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { go } from '../app.js';
import { requestBackups } from './backups.js';
import { openBackups } from './settings.js';

let askedBackups = false;
S.renderIdentityView = () => { if (S.view === 'identity') renderIdentity(); };

// Two lines and a way there: whether there is a way back, and whether the
// keys are escrowed. The page that does something about either is Settings.
function backupsLines() {
  const v = S.backups;
  if (!v) {
    if (!askedBackups) { askedBackups = true; requestBackups(); }
    return '';
  }
  const newest = (v.snapshots || [])[0];
  const last = !v.enabled ? '<span class="bad-text">switched off</span>'
    : !newest ? 'none yet'
    : new Date(newest.at).toLocaleString() + ' · record ' + esc(newest.record) + (newest.encrypted ? ' · encrypted' : ' · <span class="bad-text">not encrypted</span>');
  const failed = v.last_pass && v.last_pass.outcome === 'failed' ? ' <span class="bad-text">(the last pass FAILED)</span>' : '';
  return '<div class="kv"><span>last snapshot</span><b data-id-snapshot>' + last + failed + '</b></div>' +
    '<div class="kv"><span>keys escrowed</span><b data-id-escrow>' + (v.escrow_covers ? 'checked ' + esc(new Date(v.escrow_checked_at).toLocaleDateString()) : '<span class="bad-text">no</span>') +
    ' · <a href="#" data-open-backups>Backups &amp; Keys</a></b></div>';
}

export function renderIdentity() {
  const st = $('identity-stack');
  const id = S.identity, c = S.cont;
  let html = '';
  if (id && id.brief) html += '<div class="card"><h3>THEIR MORNING BRIEF</h3><div class="quote">' + esc(id.brief) + '</div></div>';
  if (id && id.charter) html += '<div class="card"><h3>CHARTER — RING 1</h3>' + approvalHTML(id.approval) + '<div style="white-space:pre-wrap;font-size:13.5px;line-height:1.6;color:var(--dim)">' + esc(id.charter) + '</div></div>';
  html += '<div class="card"><h3>CONTINUITY</h3>';
  if (c) {
    html += '<div class="kv"><span>mode</span><b>' + esc(c.mode || 'normal') + (c.safe_reason ? ' — ' + esc(c.safe_reason) : '') + '</b></div>';
    html += '<div class="kv"><span>ledger seq</span><b>' + c.ledger_seq + '</b></div>';
    html += '<div class="kv"><span>witnessed</span><b>' + witnessedText(c) + '</b></div>';
    if (c.witnessed_at) html += '<div class="kv"><span>last witness</span><b>' + esc(c.witnessed_at) + '</b></div>';
    html += '<div class="kv"><span>life</span><b>' + c.lifetime_ticks + ' ticks</b></div>';
    if (c.review_at) html += '<div class="kv"><span>review</span><b>' + esc(c.review_at) + (c.review_status === 'issues' ? ' — ' + c.review_issues + ' issue' + (c.review_issues === 1 ? '' : 's') : ' — clear') + '</b></div>';
    html += '<div class="kv"><span>witness</span><b style="font-family:var(--mono);font-size:11px">' + esc(c.witness_url || '—') + '</b></div>';
    html += backupsLines();
    if ((c.mode || 'normal') === 'safe') html += '<div class="dim-note" data-safe-restore style="margin-top:8px">This record is frozen. If it cannot be repaired, a snapshot can be put back: <a href="#" data-open-backups>restore from a backup</a>.</div>';
  } else html += '<div class="empty">waiting…</div>';
  html += '</div>';
  if (id) html += '<div class="card"><h3>RELATIONSHIP</h3>' +
    '<div class="kv"><span>trust</span><b>' + esc(id.trust_level || '—') + '</b></div>' +
    '<div class="kv"><span>autonomy</span><b>' + esc(id.autonomy_level || '—') + '</b></div></div>';
  st.innerHTML = html || '<div class="empty">listening…</div>';
  st.querySelectorAll('[data-open-backups]').forEach(a => { a.onclick = e => { e.preventDefault(); openBackups(); go('settings'); }; });
}

// THE OPERATOR'S OWN WORDS, FIRST IN THE CHARTER'S CARD. The record cites
// them as the approval of the charter below, and the operator reading them
// here is the check, so every state is said in words: words that are cut,
// that the conversation no longer holds or holds otherwise, that were never
// cited, or that could not be read — never an empty place.
function approvalHTML(a) {
  a = a || { state: 'unreadable', error: 'the runtime sent no account of them' };
  const note = (words, bad) => '<div class="dim-note' + (bad ? ' bad-text' : '') + '" data-approval-note>' + words + '</div>';
  const block = inner => '<div data-charter-approval data-state="' + esc(a.state) + '" style="margin-bottom:12px">' + inner + '</div>';
  if (a.state === 'unreadable') return block(note('The words the identity cites as your approval of this charter could not be read: ' + esc(a.error), true));
  const record = 'record ' + esc(a.record);
  if (!a.excerpt) return block(note('No approval of this charter is recorded: the signed entry that wrote it, ' + record + ', cites no words of yours.', true));
  const turn = 'turn ' + esc(a.turn);
  const head = 'The words the identity cites as your approval — ' + (a.turn ? turn + (a.said_at ? ', ' + esc(when(a.said_at)) : '') : 'no turn cited');
  let html = '<div class="dim-note" data-approval-head>' + head + '</div>' +
    '<div class="quote" data-approval-words>' + esc(a.excerpt) + (a.chars ? '…' : '') + '</div>';
  const kept = 'they are as the signed record keeps them, ' + record + '.';
  if (a.state === 'gone') html += note('Turn ' + esc(a.turn) + ' is no longer in the conversation on this machine, so these words cannot be checked against it here; ' + kept, true);
  if (a.state === 'differs') html += note('Turn ' + esc(a.turn) + ' in the conversation does not hold these words of yours now; ' + kept, true);
  if (a.state === 'uncited') html += note('The signed entry that wrote this charter, ' + record + ', cites no turn for these words.', true);
  if (a.chars) html += note('Cut here: ' + esc(a.chars) + ' characters in all. The whole is ' + (a.state === 'said' ? turn + ' in the conversation.' : record + ' of the signed record.'));
  return block(html);
}

function when(t) {
  const d = new Date(t);
  return isNaN(d.getTime()) ? t : d.toLocaleString();
}

function witnessedText(c) {
  if (!c.witness_url) return 'no witness configured';
  if (!c.witnessed_at) return 'never witnessed';
  if (c.unanchored > 0) return 'anchored @' + c.anchored_seq + ' — ' + c.unanchored + ' since';
  return 'fully anchored @' + c.anchored_seq;
}
