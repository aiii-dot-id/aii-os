import { esc } from '../../util.js';

export function charterText(t) {
  return '<div class="who__relationship-text">' + esc(t) + '</div>';
}

export function relationshipHTML(r) {
  const seq = esc(String(r.record));
  const html = '<div class="who__relationship" data-relationship="' + esc(r.id) + '">' +
    '<div class="kv"><span>with</span><b>' + (r.name ? esc(r.name) : 'no name given') + '</b></div>' +
    '<div class="kv"><span>role</span><b>' + esc(r.role) + '</b></div>' +
    '<div class="kv"><span>entry</span><b><a href="#/identity/record/' + seq + '">record ' + seq + '</a></b></div>';
  if (!r.charter) return html + '<div class="dim-note">No text is recorded for this relationship.</div></div>';
  return html + approvalHTML(r.approval) + charterText(r.charter) + '</div>';
}

export function approvalHTML(a) {
  a = a || { state: 'unreadable', error: 'the runtime sent no account of them' };
  const note = (words, bad) => '<div class="dim-note' + (bad ? ' bad-text' : '') + '" data-approval-note>' + words + '</div>';
  const block = inner => '<div class="who__approval" data-charter-approval data-state="' + esc(a.state) + '">' + inner + '</div>';
  if (a.state === 'unreadable') return block(note('The words the identity cites as your approval of this charter could not be read: ' + esc(a.error), true));
  const record = 'record ' + esc(a.record);
  if (!a.excerpt) return block(note('No approval of this charter is recorded: the signed entry that wrote it, ' + record + ', cites no words of yours.', true));
  const turn = 'turn ' + esc(a.turn);
  const head = 'The words the identity cites as your approval — ' + (a.turn ? turn + (a.said_at ? ', ' + esc(when(a.said_at)) : '') : 'no turn cited');
  let html = '<div class="dim-note" data-approval-head>' + head + '</div>' +
    '<div class="quote" data-approval-words>' + esc(a.excerpt) + (a.chars ? '…' : '') + '</div>';
  const kept = 'they are as the signed record keeps them, ' + record + '.';
  if (a.state === 'gone') html += note('That part of the conversation is no longer on this computer, so these words can\'t be checked here; ' + kept, true);
  if (a.state === 'differs') html += note('Turn ' + esc(a.turn) + ' in the conversation does not hold these words of yours now; ' + kept, true);
  if (a.state === 'uncited') html += note('The signed entry that wrote this charter, ' + record + ', cites no turn for these words.', true);
  if (a.chars) html += note('Cut here: ' + esc(a.chars) + ' characters in all. The whole is ' + (a.state === 'said' ? turn + ' in the conversation.' : record + ' of the signed record.'));
  return block(html);
}

function when(t) {
  const d = new Date(t);
  return isNaN(d.getTime()) ? t : d.toLocaleString();
}
