// ONE RENDERER FOR A PLUGIN'S DECLARED SETTING, wherever it is shown.
// A package declares each setting's SCOPE — hearing, speaking, or
// neither — and the host puts it where that says: the ones about
// hearing and speaking beside those halves of Settings -> Speech, the
// rest on the plugin's own card. Two renderers would have meant the
// same declaration drawn two ways, and an operator learning the control
// twice.
import { esc } from '../util.js';

const CHOICE_FILTER_FROM = 12;

export function settingHTML(id, s) {
  const attrs = ' data-pset-plugin="' + esc(id) + '" data-pset-key="' + esc(s.key) + '" data-pset-type="' + esc(s.type) + '"';
  // A VALUE THIS RELEASE NO LONGER OFFERS. Kept from an earlier one and
  // read by nothing: there is nothing to edit, so the only control is
  // forgetting it. Unticked it is left exactly as it is — an upgrade
  // that drops a setting must not quietly discard what was chosen.
  if (s.undeclared) {
    return '<div class="store-setting setting-orphan">' +
      '<label class="f"><input type="checkbox" data-pset-plugin="' + esc(id) + '" data-pset-key="' + esc(s.key) + '" data-pset-type="forget"> forget ' + esc(s.key) + '</label>' +
      '<div class="store-hint">saved value ' + esc(JSON.stringify(s.value)) + ' — ' + esc(s.description) + '</div></div>';
  }
  const current = s.value !== undefined && s.value !== null ? s.value : s.default;
  let field;
  if (s.type === 'boolean') {
    field = '<label class="f"><input type="checkbox"' + attrs + (current ? ' checked' : '') + '> ' + esc(s.title) + '</label>';
  } else if (s.type === 'enum') {
    const labels = s.labels || {};
    const values = (s.values || []).slice();
    // A stored choice this release no longer offers stays visible AS
    // the stored choice, named as such, so the operator sees what they
    // chose and chooses again; saving it unchanged is refused by the
    // host by name, never silently replaced.
    const stale = s.invalid && s.value !== undefined && s.value !== null && !values.includes(String(s.value));
    // The name is what the person chooses by; the stable value is what
    // is stored, shown on hover and never appended to the name (the
    // voice platform: a label may change, the selection
    // must not).
    const name = v => labels[v] ? labels[v] : v;
    field = '<label class="f">' + esc(s.title) + (values.length > CHOICE_FILTER_FROM ? ' <span class="store-hint">— ' + values.length + ' choices</span>' : '') + '</label>' +
      (values.length > CHOICE_FILTER_FROM ? '<input type="search" class="choice-filter" data-choice-filter-for="' + esc(s.key) + '" placeholder="filter the ' + values.length + ' choices…" aria-label="filter ' + esc(s.title) + '">' : '') +
      '<select' + attrs + '>' +
      (stale ? '<option value="' + esc(s.value) + '" selected>' + esc(s.value) + ' — saved, not offered by this release</option>' : '') +
      values.map(v => '<option value="' + esc(v) + '" title="' + esc(v) + '"' + (v === current ? ' selected' : '') + '>' + esc(name(v)) + '</option>').join('') + '</select>';
  } else if (s.type === 'secret') {
    const handles = s.handles || [];
    field = '<label class="f">' + esc(s.title) + ' — a credential handle</label><select' + attrs + '><option value="">none</option>' +
      handles.map(h => '<option value="' + esc(h) + '"' + (h === current ? ' selected' : '') + '>' + esc(h) + '</option>').join('') + '</select>' +
      (handles.length ? '' : '<div class="store-hint">grant this plugin a credential handle first (plugins.grants.' + esc(id) + '.credential_handles)</div>');
  } else if (s.type === 'number' || s.type === 'integer') {
    field = '<label class="f">' + esc(s.title) + (s.type === 'integer' ? ' <span class="store-hint">— a whole number</span>' : '') + '</label><input type="number"' + attrs +
      (s.type === 'integer' ? ' step="1"' : ' step="any"') +
      (s.minimum !== undefined && s.minimum !== null ? ' min="' + esc(s.minimum) + '"' : '') +
      (s.maximum !== undefined && s.maximum !== null ? ' max="' + esc(s.maximum) + '"' : '') +
      ' value="' + (current !== undefined && current !== null ? esc(current) : '') + '">';
  } else {
    field = '<label class="f">' + esc(s.title) + '</label><input type="text"' + attrs + ' value="' + (current !== undefined && current !== null ? esc(current) : '') + '">';
  }
  const meta = [];
  if (s.required) meta.push('required');
  if (s.default !== undefined && s.default !== null && s.type !== 'boolean') meta.push('default ' + esc(shown(s, s.default)));
  // The stored value against what the plugin reads: when the stored
  // value no longer holds to this release's declaration the page says
  // so and names what is in effect instead — never a quiet default.
  const stale = s.invalid ? '<div class="store-hint setting-stale">your saved value ' + esc(JSON.stringify(s.value)) + ' ' + esc(s.invalid) + ' — in effect: ' +
    (s.effective !== undefined && s.effective !== null ? esc(shown(s, s.effective)) : 'nothing') + ' until you choose again</div>' : '';
  return '<div class="store-setting">' + field +
    (s.description || meta.length ? '<div class="store-hint">' + esc(s.description || '') + (meta.length ? ' (' + meta.join(', ') + ')' : '') + '</div>' : '') + stale + '</div>';
}
// shown renders a value the way the page names it: an enum value under
// its label when the declaration gives one.
export function shown(s, v) {
  if (s.type === 'enum' && s.labels && s.labels[v]) return s.labels[v];
  return v;
}
