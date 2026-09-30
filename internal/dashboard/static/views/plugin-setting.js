// ONE RENDERER FOR A PLUGIN'S DECLARED SETTING, wherever it is shown.
// A package declares each setting's SCOPE — hearing, speaking, or
// neither — and the host puts it where that says: the ones about
// hearing and speaking beside those halves of Settings -> Speech, the
// rest on the plugin's own card. Two renderers would have meant the
// same declaration drawn two ways, and an operator learning the control
// twice.
import { S } from '../state.js';
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
    field = secretHTML(id, s, current);
  } else if (s.type === 'number' || s.type === 'integer') {
    field = '<label class="f">' + esc(s.title) + (s.type === 'integer' ? ' <span class="store-hint">— a whole number</span>' : '') + '</label><input type="number"' + attrs +
      (s.type === 'integer' ? ' step="1"' : ' step="any"') +
      (s.minimum !== undefined && s.minimum !== null ? ' min="' + esc(s.minimum) + '"' : '') +
      (s.maximum !== undefined && s.maximum !== null ? ' max="' + esc(s.maximum) + '"' : '') +
      ' value="' + (current !== undefined && current !== null ? esc(current) : '') + '">';
  } else if (s.choices_from) {
    field = lookedUpHTML(id, s, current, attrs);
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
// A SECRET IS PASTED HERE AND NEVER SHOWN AGAIN. The page says whether a
// key is kept and the one host it may ride to; the field is always empty
// when drawn. What the operator has pasted but not yet saved lives only in
// keyDrafts, so a re-render does not lose it, and is dropped the moment it
// is sent. A connection that is not private is not offered the field at
// all — the server refuses a key from one anyway.
export const keyDrafts = new Map();
export function keyDraftKey(id, key) { return id + '\u0000' + key; }
function secretHTML(id, s, current) {
  if (s.oauth) return accountHTML(id, s, current);
  const kept = !!s.key_kept;
  const configured = !s.invalid && typeof current === 'string' && (s.handles || []).includes(current);
  const secretsOK = !!(S.config && S.config.secrets_ok);
  const ids = ' data-pkey-plugin="' + esc(id) + '" data-pkey-key="' + esc(s.key) + '"';
  let html = '<label class="f">' + esc(s.title) + '</label>';
  if (s.key_host) {
    html += '<div class="store-hint" data-pkey-state' + ids + '>' + (kept ? 'a key is kept for this setting' : configured ? 'a key is configured for this setting' : 'no key yet') +
      ' — it rides only to ' + esc(s.key_host) + ', is kept when you save, and is never shown again</div>';
    if (secretsOK) {
      html += '<div class="pkey-row"><input type="password" autocomplete="off" spellcheck="false" data-pkey-input' + ids +
        ' placeholder="' + (kept || configured ? 'paste a new key to replace it, then save' : 'paste the key, then save') + '" aria-label="' + esc(s.title) + '">' +
        (kept ? '<button class="btn ghost" data-pkey-remove' + ids + '>Remove</button>' : '') + '</div>';
    } else {
      html += '<div class="bad-text" data-nosecrets>This connection is not private, so a key pasted here would cross the network in the clear. Open the dashboard on the machine itself, or switch on TLS in Settings → Dashboard, and come back.</div>' +
        (kept ? '<div class="pkey-row"><button class="btn ghost" data-pkey-remove' + ids + '>Remove the kept key</button></div>' : '');
    }
  } else if (s.key_not) {
    html += '<div class="store-hint">a key cannot be pasted for this plugin: ' + esc(s.key_not) + '</div>';
  }
  return html;
}
// AN ACCOUNT, CONNECTED ON THE CARD. A secret whose package asks for
// an OAuth account names one the operator connected; the host lists the
// accounts of that authority, says which can be chosen and why not, and
// whether the stored choice is granted. The choice rides the card's own
// Save; saving grants the plugin its handle and the one host its signed
// package declares, and the card says exactly that before Save. Remove
// forgets the choice and takes the handle back. There is no key field: the
// package says the credential comes from an authority.
function accountHTML(id, s, current) {
  const a = s.account || {};
  const host = (a.hosts || []).join(', '); // every host the signed package declares
  const saved = typeof current === 'string' ? current : '';
  const ids = ' data-pkey-plugin="' + esc(id) + '" data-pkey-key="' + esc(s.key) + '"';
  const remove = saved || s.key_kept ? '<button class="btn ghost" data-pkey-remove' + ids + ' data-pkey-account="' + esc(saved) + '" data-account-host="' + esc(host) + '">Remove</button>' : '';
  let html = '<label class="f">' + esc(s.title) + '</label>';
  // Nothing can be connected here; what was stored before can still go.
  if (!host) return html + '<div class="store-hint" data-pacct-not>an account cannot be connected here: ' + esc(a.not || 'the host did not say why') +
    (saved ? ' — ' + esc(saved) + ' is stored for it' : '') + '</div>' + (remove ? '<div class="pkey-row">' + remove + '</div>' : '');
  const offers = a.offers || [];
  const mine = offers.find(o => o.name === saved);
  html += '<div class="store-hint" data-pacct-state>' + esc(accountState(saved, mine, !!a.granted)) +
    (s.key_kept ? '; a key pasted for this setting earlier is kept too, and Remove forgets it with the choice' : '') + '</div>';
  if (offers.length || saved || s.key_kept) {
    html += '<div class="pkey-row"><select data-pset-plugin="' + esc(id) + '" data-pset-key="' + esc(s.key) + '" data-pset-type="secret"' +
      ' data-pset-saved="' + esc(saved) + '" data-pset-granted="' + (a.granted ? 'true' : 'false') + '" data-account-host="' + esc(host) + '" aria-label="' + esc(s.title) + '">' +
      (saved ? '' : '<option value="">choose an account…</option>') +
      (saved && !mine ? '<option value="' + esc(saved) + '" selected disabled>' + esc(saved) + ' — no longer exists</option>' : '') +
      offers.map(o => '<option value="' + esc(o.name) + '"' + (o.name === saved ? ' selected' : '') + (o.not ? ' disabled' : '') + '>' +
        esc(o.name + ' — ' + (o.not ? 'cannot be used here' : o.state)) + '</option>').join('') + '</select>' + remove + '</div>' +
      offers.filter(o => o.not).map(o => '<div class="store-hint">' + esc(o.name + ': ' + o.not) + '</div>').join('') +
      '<div class="store-hint" data-pacct-grants>' + esc(accountDisclosure(id, host, mine && !mine.not ? saved : '', saved, !!a.granted)) + '</div>';
  }
  if (!offers.some(o => !o.not)) {
    html += '<div class="pkey-row"><button class="btn ghost" data-pacct-connect data-pacct-plugin="' + esc(id) + '" data-pacct-key="' + esc(s.key) + '">Connect a ' + esc(s.oauth.provider) + ' account…</button></div>';
  }
  return html;
}
// What the stored choice is now, in the card's words.
function accountState(saved, mine, granted) {
  if (!saved) return 'no account chosen yet';
  if (!mine) return saved + ' no longer exists on this identity: choose another account, or Remove';
  if (mine.not) return saved + ' cannot be used here any more: ' + mine.not;
  if (!granted) return saved + ' is chosen but not granted to this plugin: Save grants it';
  if (mine.state === 'disconnected') return saved + ' is not connected: every call is refused until you connect it under Connected accounts below';
  if (mine.state === 'expired') return saved + '\'s sign-in has expired: connect it again under Connected accounts below';
  return saved + ' — ' + mine.state;
}
// THE DISCLOSURE: exactly what saving the chosen account grants, before
// Save — which plugin, which handle, which host — or that saving it again
// changes nothing. chosen is "" when no account that can be used is picked.
export function accountDisclosure(id, host, chosen, saved, granted) {
  // One host, or several joined by ", ": the package signs for every one, and
  // saving grants every one.
  const many = host.includes(', ');
  const hosts = (many ? 'hosts ' : 'host ') + host, ones = many ? 'the ones' : 'the one';
  if (!chosen) return 'Saving grants ' + id + ' the handle of the account you choose and the ' + hosts + ', ' + ones + ' its signed package declares — nothing else.';
  if (chosen === saved && granted) return 'Granted: the handle ' + chosen + ' and the ' + hosts + ' — ' + id + ' calls ' + host + ' as ' + chosen + '. Saving it again changes nothing.';
  return 'Saving grants ' + id + ' the handle ' + chosen + ' and the ' + hosts + ', ' + ones + ' its signed package declares: ' + id + ' will call ' + host + ' as ' + chosen +
    (saved && saved !== chosen ? ', in place of ' + saved : '') + '.';
}
// CHOICES THE PLUGIN LOOKS UP. The card asks the plugin once when it is
// first drawn and again on Refresh; the answer is kept here, per setting.
// Until it arrives, and when the lookup fails, the field is the plain text
// box with the value as saved, so a Save can never blank it. The list is
// an offer: a saved value it no longer holds stays visible as saved.
export const choiceCache = new Map();
export function choiceKey(id, key) { return id + '\u0000' + key; }
export function applySettingChoices(reply) {
  if (!reply || !reply.plugin || !reply.key) return;
  choiceCache.set(choiceKey(reply.plugin, reply.key), { choices: reply.choices || null, error: reply.error || '' });
}
function lookedUpHTML(id, s, current, attrs) {
  const k = choiceKey(id, s.key);
  const entry = choiceCache.get(k);
  const value = current !== undefined && current !== null ? String(current) : '';
  const ids = ' data-choices-plugin="' + esc(id) + '" data-choices-key="' + esc(s.key) + '"';
  const refresh = '<button class="btn ghost" data-choices-refresh' + ids + '>Refresh</button>';
  if (!entry || entry.pending || !entry.choices) {
    const why = !entry || entry.pending ? 'looking up the choices…' : 'the choices could not be looked up: ' + entry.error;
    return '<label class="f">' + esc(s.title) + '</label><div class="pkey-row"><input type="text"' + attrs + ' value="' + esc(value) + '">' + (entry && !entry.pending ? refresh : '') + '</div>' +
      '<div class="store-hint" data-choices-state' + ids + '>' + esc(why) + '</div>';
  }
  const offered = entry.choices.map(c => c.value);
  const stale = value !== '' && !offered.includes(value);
  return '<label class="f">' + esc(s.title) + ' <span class="store-hint">— ' + entry.choices.length + ' offered by the plugin</span></label>' +
    '<div class="pkey-row"><select' + attrs + '>' +
    (value === '' ? '<option value=""' + (value === '' ? ' selected' : '') + '>' + (s.default ? 'default (' + esc(s.default) + ')' : 'none') + '</option>' : '') +
    (stale ? '<option value="' + esc(value) + '" selected>' + esc(value) + ' — saved, not offered now</option>' : '') +
    entry.choices.map(c => '<option value="' + esc(c.value) + '" title="' + esc(c.value) + '"' + (c.value === value ? ' selected' : '') + '>' + esc(c.label || c.value) + '</option>').join('') +
    '</select>' + refresh + '</div>';
}
// shown renders a value the way the page names it: an enum value under
// its label when the declaration gives one.
export function shown(s, v) {
  if (s.type === 'enum' && s.labels && s.labels[v]) return s.labels[v];
  return v;
}
