import { sec } from '../settings.js';
import { LIMIT_FIELDS, LIMIT_WORD, limitDrafted } from './parts.js';
import { configValue, limitDraft } from './save.js';
import { S } from '../../state.js';
import { esc } from '../../util.js';
export function limitSet(block, key, provider, model, value) {
  const section = key.slice(0, key.indexOf('.')), name = key.slice(key.indexOf('.') + 1);
  const p = block[provider] = block[provider] || {};
  const at = model ? ((p.models = p.models || {})[model] = p.models[model] || {}) : p;
  const sec = at[section] = at[section] || {};
  if (value === undefined) delete sec[name]; else sec[name] = value;
  if (!Object.keys(sec).length) delete at[section];
  if (model && !Object.keys(at).length) delete p.models[model];
  if (p.models && !Object.keys(p.models).length) delete p.models;
  if (!Object.keys(p).length) delete block[provider];
}
export function collectLimits(ch, root = document.getElementById('settings-stack')) {
  let bad = '';
  root.querySelectorAll('[data-cog-key]').forEach(el => {
    const key = el.dataset.cogKey, stated = configValue(S.config, key), had = stated === 0 && S.config?.cognition?.settings?.find(s => s.key === key)?.zero_is_default ? null : stated;
    const box = root.querySelector('[data-cog-unl="' + key + '"]');
    const lifted = had === LIMIT_WORD || (had == null && S.config?.cognition?.settings?.find(s => s.key === key)?.default_unlimited);
    if (box && box.checked) { if (!lifted) ch[key] = LIMIT_WORD; return; }
    const none = root.querySelector('[data-cog-none="' + key + '"]');
    if (none && none.checked) { if (had !== -1) ch[key] = -1; return; }
    const raw = el.value.trim();
    if (raw === '') { if (had != null && had !== LIMIT_WORD) ch[key] = null; else if (had === LIMIT_WORD) ch[key] = null; return; }
    if (raw === el.dataset.cogShown) return;
    const per = Number(el.dataset.cogPer) || 1, n = per > 1 ? Math.round(Number(raw) * per) : Number(raw);
    if (!Number.isInteger(n)) { bad = bad || (root.querySelector('label[for="' + el.id + '"]')?.textContent || (LIMIT_FIELDS[key] || [key])[0]) + ' is a whole number, or blank for the default.'; return; }
    if (n !== had) ch[key] = n;
  });
  if (limitDrafted(S.config)) {
    ch['overrides'] = Object.keys(limitDraft).length ? limitDraft : null;
    ch['overrides_digest'] = S.config.cognition.overrides_digest;
  }
  return bad;
}
export function pluginToolNote(t) {
  return t.plugin ? '<br><span class="store-hint">An operation of plugin ' + esc(t.plugin) + ', shown as the identity is given it. ' +
    'Its author\'s summary is on that plugin\'s card on the Plugins page, whole.</span>' : '';
}
