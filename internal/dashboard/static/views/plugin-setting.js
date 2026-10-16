import { S } from '../state.js';
import { bytes, esc } from '../util.js';

const CHOICE_FILTER_FROM = 12;

export function settingHTML(id, s) {
  const attrs = ' data-pset-plugin="' + esc(id) + '" data-pset-key="' + esc(s.key) + '" data-pset-type="' + esc(s.type) + '"';
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
    const stale = s.invalid && s.value !== undefined && s.value !== null && !values.includes(String(s.value));
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
  const stale = s.invalid ? '<div class="store-hint setting-stale">your saved value ' + esc(JSON.stringify(s.value)) + ' ' + esc(s.invalid) + ' — in effect: ' +
    (s.effective !== undefined && s.effective !== null ? esc(shown(s, s.effective)) : 'nothing') + ' until you choose again</div>' : '';
  return '<div class="store-setting">' + field +
    (s.description || meta.length ? '<div class="store-hint">' + esc(s.description || '') + (meta.length ? ' (' + meta.join(', ') + ')' : '') + '</div>' : '') + stale + modelsLine(s, id) + '</div>';
}
function modelsLine(s, id) {
  const m = s.models;
  if (!m || !m.files_total || m.state === 'ready') return '';
  const have = m.files_present + ' of ' + m.files_total + (m.files_total === 1 ? ' file' : ' files') + ', ' + bytes(m.bytes_present) + ' of ' + bytes(m.bytes_total);
  let words;
  if (m.state === 'fetching') words = 'Fetching the files this choice needs: ' + esc(have) + '. It works once they are here; no restart is needed.';
  else if (m.state === 'waiting') words = 'The files this choice needs are not all here yet (' + esc(have) + '). The last attempt stopped: ' + esc(String(m.last_error || 'no reason was given').replace(/[.\s]+$/, '')) + '. It is tried again on its own.';
  else if (m.state === 'stopped') words = 'The files this choice needs could not be fetched (' + esc(have) + '): ' + esc(String(m.last_error || '').replace(/[.\s]+$/, '')) + '. ' + esc(m.remedy || '') +
    ' <button class="btn sm" data-models-retry="' + esc(id) + '">Try again</button>';
  else words = 'The files this choice needs are not installed yet (' + esc(have) + ').';
  return '<div class="store-hint setting-models" data-setting-models="' + esc(m.state) + '" data-announce>' + words + '</div>';
}
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
function accountHTML(id, s, current) {
  const a = s.account || {};
  const host = (a.hosts || []).join(', ');
  const saved = typeof current === 'string' ? current : '';
  const ids = ' data-pkey-plugin="' + esc(id) + '" data-pkey-key="' + esc(s.key) + '"';
  const remove = saved || s.key_kept ? '<button class="btn ghost" data-pkey-remove' + ids + ' data-pkey-account="' + esc(saved) + '" data-account-host="' + esc(host) + '">Remove</button>' : '';
  let html = '<label class="f">' + esc(s.title) + '</label>';
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
function accountState(saved, mine, granted) {
  if (!saved) return 'no account chosen yet';
  if (!mine) return saved + ' no longer exists on this identity: choose another account, or Remove';
  if (mine.not) return saved + ' cannot be used here any more: ' + mine.not;
  if (!granted) return saved + ' is chosen but not granted to this plugin: Save grants it';
  if (mine.state === 'disconnected') return saved + ' is not connected: every call is refused until you connect it under Connected accounts below';
  if (mine.state === 'expired') return saved + '\'s sign-in has expired: connect it again under Connected accounts below';
  return saved + ' — ' + mine.state;
}
export function accountDisclosure(id, host, chosen, saved, granted) {
  const many = host.includes(', ');
  const hosts = (many ? 'hosts ' : 'host ') + host, ones = many ? 'the ones' : 'the one';
  if (!chosen) return 'Saving grants ' + id + ' the handle of the account you choose and the ' + hosts + ', ' + ones + ' its signed package declares — nothing else.';
  if (chosen === saved && granted) return 'Granted: the handle ' + chosen + ' and the ' + hosts + ' — ' + id + ' calls ' + host + ' as ' + chosen + '. Saving it again changes nothing.';
  return 'Saving grants ' + id + ' the handle ' + chosen + ' and the ' + hosts + ', ' + ones + ' its signed package declares: ' + id + ' will call ' + host + ' as ' + chosen +
    (saved && saved !== chosen ? ', in place of ' + saved : '') + '.';
}
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
export function keyTyping() {
  const active = document.activeElement;
  return active && active.dataset && active.hasAttribute('data-pkey-input') ? { k: keyDraftKey(active.dataset.pkeyPlugin, active.dataset.pkeyKey), at: active.selectionStart } : null;
}
export function wireSettingControls(root, { send, rerender, connect, typing }) {
  const ask = (plugin, key) => {
    choiceCache.set(choiceKey(plugin, key), { pending: true });
    send({ type: 'plugin_setting_choices', plugin_key: { plugin: plugin, key: key } });
  };
  root.querySelectorAll('[data-pset-plugin]').forEach(el => {
    const card = el.closest('.store-setting');
    const marker = card && card.querySelector('[data-choices-plugin]');
    if (!marker) return;
    if (!choiceCache.has(choiceKey(marker.dataset.choicesPlugin, marker.dataset.choicesKey))) ask(marker.dataset.choicesPlugin, marker.dataset.choicesKey);
  });
  root.querySelectorAll('[data-choices-refresh]').forEach(b => { b.onclick = () => { ask(b.dataset.choicesPlugin, b.dataset.choicesKey); rerender(); }; });
  root.querySelectorAll('[data-models-retry]').forEach(b => { b.onclick = () => {
    b.disabled = true; b.textContent = 'Retrying…';
    send({ type: 'plugin', plugin: { action: 'retry', id: b.dataset.modelsRetry } });
  }; });
  root.querySelectorAll('[data-pkey-input]').forEach(inp => {
    const k = keyDraftKey(inp.dataset.pkeyPlugin, inp.dataset.pkeyKey);
    if (keyDrafts.has(k)) inp.value = keyDrafts.get(k);
    inp.oninput = () => { if (inp.value) keyDrafts.set(k, inp.value); else keyDrafts.delete(k); };
    if (typing && typing.k === k) { inp.focus(); try { inp.setSelectionRange(typing.at, typing.at); } catch (e) { } }
  });
  root.querySelectorAll('[data-pkey-remove]').forEach(b => { b.onclick = () => {
    const account = b.dataset.pkeyAccount;
    const ask = account !== undefined
      ? 'Remove ' + (account || 'the kept key') + ' from ' + b.dataset.pkeyPlugin + '? It can no longer call ' + (b.dataset.accountHost || 'its service') + ' as ' + (account || 'that key') + '; the grant of the host stays, and the account stays connected for anything else.'
      : 'Forget the key kept for ' + b.dataset.pkeyKey + '? The plugin cannot reach its service until you paste one again.';
    if (window.confirm && !window.confirm(ask)) return;
    b.disabled = true;
    keyDrafts.delete(keyDraftKey(b.dataset.pkeyPlugin, b.dataset.pkeyKey));
    send({ type: 'plugin_key_clear', plugin_key: { plugin: b.dataset.pkeyPlugin, key: b.dataset.pkeyKey } });
  }; });
  root.querySelectorAll('select[data-pset-type="secret"]').forEach(sel => {
    const said = sel.closest('.store-setting')?.querySelector('[data-pacct-grants]');
    const show = () => {
      const picked = sel.selectedOptions[0];
      if (said) said.textContent = accountDisclosure(sel.dataset.psetPlugin, sel.dataset.accountHost, picked && !picked.disabled ? sel.value : '', sel.dataset.psetSaved || '', sel.dataset.psetGranted === 'true');
    };
    sel.onchange = show;
    show();
  });
  root.querySelectorAll('[data-pacct-connect]').forEach(b => { b.onclick = () => {
    S.connectRequest = { connector: b.dataset.pacctPlugin, setting: b.dataset.pacctKey, scope: 'read', fromCard: true };
    connect();
  }; });
}
export function settingValue(el) {
  const type = el.dataset.psetType;
  if (type === 'forget') return el.checked ? null : undefined;
  if (type === 'boolean') return !!el.checked;
  if (type === 'number' || type === 'integer') {
    const n = el.value.trim() === '' ? NaN : Number(el.value);
    return isNaN(n) ? null : n;
  }
  if (type === 'secret') return accountChoice(el);
  return el.value === '' ? null : el.value;
}
function accountChoice(el) {
  const picked = el.selectedOptions && el.selectedOptions[0];
  if (!el.value || !picked || picked.disabled) return undefined;
  return el.value !== (el.dataset.psetSaved || '') || el.dataset.psetGranted !== 'true' ? el.value : undefined;
}
export function shown(s, v) {
  if (s.type === 'enum' && s.labels && s.labels[v]) return s.labels[v];
  return v;
}
