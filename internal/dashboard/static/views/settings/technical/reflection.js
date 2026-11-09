import { checkHTML, drawForm, fieldHTML, formHTML, groupHTML, hint, named, say, settingHTML, settingOf } from './forms.js';
import { hinted } from '../parts.js';
import { passAsking, passWords, pressPass } from '../pass-now.js';
import { S } from '../../../state.js';
import { esc, fill } from '../../../util.js';
import W from '../../../strings/settings-technical-forms.js';

const R = W.reflection;
export const REFLECTION_GROUPS = [
  [R.groupWhen, [['agency.rhythm_seconds', 'cadence'], ['cognition.consolidate_spacing_seconds', 'spacing'], ['cognition.self_model_every_minutes', 'portraitEvery'], ['cognition.identity_review_every_minutes', 'reviewEvery']]],
  [R.groupPass, [['cognition.consolidate_batch', 'experiencesPerPass'], ['cognition.consolidate_max_ops', 'changesPerPass'], ['agency.outcome_window', 'outcomeWindow'], ['agency.tool_outcome_min', 'toolPatternAt'], ['agency.tool_outcome_window', 'toolPatternWait'], ['prompt.dream_conversation_max_chars', 'dreamReads'], ['prompt.tensions_max_chars', 'contradictionsShown'], ['prompt.excerpt_chars', 'quotedMax']]],
  [R.groupPortrait, [['cognition.self_model_evidence_rows', 'portraitRecent'], ['cognition.self_model_spoken_rows', 'portraitOthers'], ['prompt.self_model_max_tokens', 'portraitMax']]],
  [R.groupRides, [['prompt.charter_max_tokens', 'charterMax'], ['prompt.ring2_evidence_max_chars', 'evidenceShown'], ['prompt.ring3_max_chars', 'workingTruthMax'], ['prompt.surfacing_max_chars', 'dreamNoteMax'], ['prompt.operator_beliefs_max_chars', 'aboutYouShown'], ['prompt.brief_max_chars', 'briefMax'], ['prompt.ring3_recent_experiences', 'recentInTruth'], ['prompt.work_card_beliefs', 'beliefsOnWork'], ['prompt.alarm_firings', 'alarmsListed']]],
];
function readsSaid(cog) {
  if (!cog.input_budget) return R.readsUnknown;
  const tokens = Number(cog.input_budget).toLocaleString();
  return fill(cog.input_budget_source === 'derived' ? R.readsDerived : cog.input_budget_source === 'fallback' ? R.readsFallback : R.readsCeiling, { tokens });
}
function wire(root) {
  const off = root.querySelector('#cog-brief-off'), at = root.querySelector('#cog-brief-at');
  if (off && at) off.onchange = () => { at.disabled = off.checked; };
  const press = root.querySelector('[data-pass-now]');
  if (press) press.onclick = () => pressPass();
}
export function drawReflection(root) {
  const c = S.config;
  if (!c) { root.dataset.form = ''; root.textContent = W.loading; return; }
  const cog = c.cognition || {}, at = cog.morning_brief_at || '', off = at === 'off';
  const asking = passAsking();
  const fields = rows => rows.map(([key, word]) => {
    const set = settingOf(c, key);
    return set ? settingHTML(set, c, named(R[word]), named(R[word + 'Hint'] || ''), 'applies at the next pass') : '';
  }).join('');
  const last = (cog.conditions || []).map(k =>
    '<li data-cog-condition="' + esc(k.facility) + '" data-kind="' + esc(k.kind) + '"><strong>' + esc(R.facilities[k.facility] || k.facility) + '</strong>: ' + esc(k.said) +
    (k.at ? ' <span class="technical-form__when">' + esc(new Date(k.at).toLocaleString()) + '</span>' : '') + '</li>').join('');
  drawForm(root, 'cognition', formHTML('cognition', say(R.heading),
    '<div class="technical-form__part"><button type="button" class="c-button c-button--secondary" data-pass-now' + (asking || !S.connected ? ' disabled' : '') + '>' + esc(R.runSooner) + '</button>' +
    '<p class="technical-form__said" data-pass-said data-announce>' + esc(passWords(named('{name}'))) + '</p>' + hint(R.runSoonerHint) + '</div>' +
    '<p class="technical-form__fact"><span>' + esc(R.reads) + ':</span> <strong data-cog-budget>' + esc(readsSaid(cog)) + '</strong></p>' +
    (last ? '<h3 class="technical-form__group-title">' + esc(R.lastPasses) + '</h3><ul class="technical-form__facts" data-cog-conditions>' + last + '</ul>' : '') +
    groupHTML(R.groupWhen, fieldHTML({ id: 'cog-brief-at', label: R.briefAt, about: R.briefAtHint + ' ' + fill(R.briefBlank, { time: cog.morning_brief_default || '' }), value: off ? '' : at, type: 'time', short: true,
      attrs: ' placeholder="' + esc(cog.morning_brief_default || '') + '"' + (off ? ' disabled' : '') + hinted(off ? 'off' : at, fill(R.briefBlank, { time: cog.morning_brief_default || '' })) }) +
      checkHTML({ id: 'cog-brief-off', label: R.briefOff, about: R.briefOffHint, checked: off }) + fields(REFLECTION_GROUPS[0][1])) +
    REFLECTION_GROUPS.slice(1).map(([title, rows]) => groupHTML(title, fields(rows))).join(''), R.applies), wire);
}
