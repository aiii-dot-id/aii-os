import { esc } from '../util.js';

export const RECORDED = [
  ['next_move', 'Next move', w => w.next_move],
  ['expected_evidence', 'Expected evidence', w => w.expected_evidence],
  ['falsifier', 'Would show it wrong', w => w.falsifier],
  ['evidence_class', 'Evidence', w => '(' + w.evidence_class + ')'],
  ['evidence_readback', 'Read back', w => w.evidence_readback],
  ['check', 'Check', w => w.check],
  ['check_run', 'Check ran', w => w.check_run],
];
export function recordedHTML(w) {
  const rows = RECORDED.filter(([key]) => w && w[key]);
  if (!rows.length) return '';
  return '<div class="work-rec">' + rows.map(([, label, value]) =>
    '<div class="work-rec-row"><span class="work-rec-k">' + esc(label) + '</span> <span class="work-rec-v">' + esc(value(w)) + '</span></div>').join('') + '</div>';
}
