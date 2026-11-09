import { startSignIn, signInProgress, wireSignInCompletion } from '../../signin.js';
import { S } from '../../state.js';
import W from '../../strings/settings-permissions.js';
import { $, esc } from '../../util.js';
import { send } from '../../ws.js';
import { forgetForm } from '../settings.js';
import { renderPlugins } from '../plugins.js';

export function connectPrefill(req, providers, installed) {
  if (!req) return null;
  const word = String(req.connector || '').toLowerCase();
  const mode = req.scope === 'modify' ? 'modify' : 'read';
  const plugin = (installed || []).find(p => p.id === req.connector);
  const hinted = plugin && (plugin.settings || []).find(s => s.type === 'secret' && s.oauth && (!req.setting || s.key === req.setting));
  if (hinted) {
    const services = {};
    (hinted.oauth.services || []).forEach(svc => { services[svc] = mode; });
    const prov = (providers || []).some(p => p.name === hinted.oauth.provider) ? hinted.oauth.provider : 'custom';
    return { name: (hinted.oauth.provider + '-' + hinted.key).replace(/[^a-z0-9._-]+/g, '-').slice(0, 40), provider: prov, services: services, connector: req.connector, scope: mode, handle: hinted.key, title: hinted.title || hinted.key, plugin: plugin.id, fromCard: !!req.fromCard };
  }
  const aliases = { gmail: ['google', 'gmail'], calendar: ['google', 'calendar'], drive: ['google', 'drive'], contacts: ['google', 'contacts'], google: ['google', ''],
    outlook: ['microsoft', 'mail'], microsoft: ['microsoft', ''], github: ['github', 'issues'], slack: ['slack', 'chat'] };
  let provider = 'custom', service = '';
  for (const key of Object.keys(aliases)) { if (word.includes(key)) { provider = aliases[key][0]; service = aliases[key][1] || service; } }
  if (!(providers || []).some(p => p.name === provider)) provider = 'custom';
  const services = {};
  if (service) services[service] = mode;
  return { name: word.replace(/[^a-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40), provider: provider, services: services, connector: req.connector, scope: mode };
}

let profileDraft = null;
const STATE = { connected: 'Connected', expired: 'Expired. Connect it again.', disconnected: 'Not connected' };
const button = (cls, attr, words) => '<button class="c-button' + cls + '" ' + attr + '>' + words + '</button>';
export function profilesHTML(pl) {
  const profiles = (pl && pl.auth_profiles) || [];
  const providers = (pl && pl.providers) || [];
  const titles = new Map(((pl && pl.installed) || []).map(p => [p.id, p.title || p.id]));
  const rows = profiles.map(p => {
    const name = esc(p.name);
    const used = p.handles && p.handles.length ? 'Used by ' + p.handles.map(h => titles.get(h) || h).join(', ') : 'No plugin uses it yet';
    if (p.scheme !== 'oauth2') {
      const where = [p.scheme, 'sent only to ' + p.host + ':' + p.port, 'secret from ' + (p.secret_source || 'nowhere')];
      return '<li class="permissions__row" data-profile="' + name + '"><span class="permissions__words"><b>' + name + '</b>' +
        '<span class="permissions__soft">' + esc('Set in the config file · ' + used) + '</span>' +
        '<details class="permissions__hint"><summary>Details</summary>' + esc(where.join(' · ')) + '</details></span></li>';
    }
    const scopes = (p.scopes || []).map(sc => sc.replace(/^https?:\/\/[^/]+\/auth\//, '')).join(', ');
    const facts = [p.provider || 'custom', p.client_id ? 'client ' + p.client_id : '', p.has_client_secret ? 'client secret kept' : 'no client secret',
      p.expires_at ? 'signed in until ' + p.expires_at : '', scopes ? 'scopes: ' + scopes : '', (p.hosts || []).length ? 'sent only to ' + p.hosts.join(', ') : ''].filter(Boolean);
    const pending = p.signin && p.signin.status === 'pending';
    return '<li class="permissions__row" data-profile="' + name + '"><span class="permissions__words"><b>' + name + '</b>' +
      '<span class="permissions__soft">' + esc((STATE[p.state] || p.state) + ' · ' + used) + '</span>' +
      (p.revocation ? '<span class="permissions__soft" data-profile-revocation="' + name + '">' + esc(p.revocation) + '</span>' : '') +
      (p.borrowed ? '<span class="permissions__soft" data-profile-borrowed="' + name + '">' + esc(p.borrowed) + '</span>' : '') +
      '<details class="permissions__hint"><summary>Details</summary>' + esc(facts.join(' · ')) + '</details></span>' +
      '<div class="permissions__buttons">' +
      (pending ? button(' c-button--quiet', 'data-profile-cancel-signin="' + name + '"', esc(W.accountCancelSignIn)) : '') +
      (p.borrowed ? '' : button('', 'data-profile-connect="' + name + '"', p.state === 'connected' ? 'Reconnect' : 'Connect')) +
      (p.can_device && !p.borrowed ? button(' c-button--quiet', 'data-profile-device="' + name + '"', 'Connect with a code') : '') +
      (p.state !== 'disconnected' || p.borrowed ? button(' c-button--quiet', 'data-profile-disconnect="' + name + '"', 'Disconnect') : '') +
      button(' c-button--quiet', 'data-profile-delete="' + name + '"' + (p.borrowed ? ' data-profile-keeps-tokens' : ''), 'Delete') + '</div>' +
      '<div class="permissions__manage">' + signInProgress(p.signin, 'profile', p.name) + '</div></li>';
  }).join('');
  return '<section class="permissions__part" data-accounts-part aria-labelledby="accounts-h" aria-describedby="accounts-hint">' +
    '<h3 id="accounts-h" class="permissions__h3">Connected accounts</h3>' +
    '<p id="accounts-hint" class="permissions__hint">Accounts your plugins use. You connect each one here.</p>' +
    (rows ? '<ul class="permissions__rows">' + rows + '</ul>' : '<p class="permissions__line">No accounts yet.</p>') +
    newProfileHTML(providers) + '</section>';
}
function newProfileHTML(providers) {
  const d = profileDraft;
  if (!d) return '<div class="permissions__buttons"><button class="c-button" data-profile-new>Connect an account</button></div>';
  const prov = providers.find(x => x.name === d.provider);
  const manual = prov && prov.sign_in === 'manual';
  const returnURL = manual ? prov.redirect_uri : location.origin + '/oauth/callback';
  const services = prov ? prov.services : [];
  const provOpts = providers.map(x => '<option value="' + esc(x.name) + '"' + (x.name === d.provider ? ' selected' : '') + '>' + esc(x.name) + '</option>').join('') + '<option value="custom"' + (d.provider === 'custom' ? ' selected' : '') + '>custom</option>';
  const svcRows = services.map(sv => '<div class="svc-row"><span>' + esc(sv.name) + '</span>' +
    [['', 'no'], ['read', 'read'], ['modify', 'read and change']].map(([v, words]) => '<label><input type="radio" name="svc-' + esc(sv.name) + '" value="' + v + '"' + ((d.services[sv.name] || '') === v ? ' checked' : '') + '> ' + words + '</label>').join('') + '</div>').join('');
  const field = (label, id, value, hint, attrs) => '<label class="f" for="' + id + '">' + label + '</label><input type="text" id="' + id + '" value="' + esc(value || '') + '"' + (attrs || '') + '>' + (hint ? '<div class="store-hint">' + hint + '</div>' : '');
  const custom = d.provider === 'custom' ? field('Sign-in address', 'pf-auth', d.authorize_url) + field('Token address', 'pf-token', d.token_url) +
    field('Device address (optional)', 'pf-device', d.device_url) + field('Revoke address (optional)', 'pf-revoke', d.revoke_url) : '';
  const hosts = d.hosts != null ? d.hosts : (prov ? prov.hosts.join(', ') : '');
  const asker = S.stats && S.stats.name && S.stats.name !== 'Unnamed' ? S.stats.name : 'They';
  const choose = d.handle ? ', then choose it for ' + esc(d.title || d.handle) + ' on the plugin\'s card' : '';
  const why = d.fromCard ? '<p class="permissions__line">An account for ' + esc(d.plugin) + '. Save it and connect it' + choose + '.</p>'
    : d.connector ? '<p class="permissions__line">' + esc(asker) + ' asked to connect <b>' + esc(d.connector) + '</b>, ' + (d.scope === 'modify' ? 'to read and change' : 'to read only') + '. Save it and connect it' + choose + '.</p>' : '';
  return '<div class="profile-form"><p class="permissions__line"><b>Connect an account</b></p>' + why +
    field('Name', 'pf-name', d.name, '', ' placeholder="google-work"') +
    '<label class="f" for="pf-provider">Provider</label><select id="pf-provider">' + provOpts + '</select>' +
    field('Return address', 'pf-redirect', d.redirect_uri || returnURL, manual ? 'The provider shows a code at this address. Paste that code here to finish.' : 'Register this address with the provider for sign-in from a browser.') +
    field('Client id', 'pf-client', d.client_id) +
    '<label class="f" for="pf-secret">Client secret</label><input id="pf-secret" type="password" autocomplete="off" value=""><div class="store-hint">Typed once, written to a private file, never shown again.</div>' +
    (services.length ? '<label class="f">Services</label>' + svcRows : '') +
    (d.provider === 'custom' || !services.length ? field('Scopes', 'pf-scopes', (d.scopes || []).join(' '), 'Separated by spaces.') : '') +
    field('Hosts the token may be sent to', 'pf-hosts', hosts) + custom +
    '<div class="savebar"><button class="btn" data-profile-save>Save</button><button class="btn ghost" data-profile-cancel>Cancel</button><span class="savenote">Saved. Connect it next.</span></div></div>';
}
function readDraft() {
  const d = profileDraft || { services: {} };
  const val = id => { const el = $(id); return el ? el.value : ''; };
  d.redirect_uri = val('pf-redirect').trim(); d.name = val('pf-name').trim(); d.client_id = val('pf-client').trim();
  const sel = $('pf-provider'); if (sel) d.provider = sel.value;
  d.services = {};
  document.querySelectorAll('.profile-form input[type=radio]:checked').forEach(r => { if (r.value) d.services[r.name.replace(/^svc-/, '')] = r.value; });
  d.hosts = val('pf-hosts');
  d.scopes = val('pf-scopes').split(/\s+/).filter(Boolean);
  d.authorize_url = val('pf-auth').trim(); d.token_url = val('pf-token').trim(); d.device_url = val('pf-device').trim(); d.revoke_url = val('pf-revoke').trim();
  return d;
}
export function wireProfiles(st) {
  wireSignInCompletion(st);
  st.querySelectorAll('[data-profile-cancel-signin]').forEach(b => { b.onclick = () => send({type:'profile_signin_cancel',profile:b.dataset.profileCancelSignin}); });
  st.querySelectorAll('[data-profile-connect]').forEach(b => { b.onclick = () => { startSignIn({ type: 'profile_signin', profile: b.dataset.profileConnect }); }; });
  st.querySelectorAll('[data-profile-device]').forEach(b => { b.onclick = () => { b.disabled = true; b.textContent = 'Asking for a code…'; send({ type: 'profile_device', profile: b.dataset.profileDevice }); }; });
  st.querySelectorAll('[data-profile-disconnect]').forEach(b => { b.onclick = () => { b.disabled = true; send({ type: 'profile_disconnect', profile: b.dataset.profileDisconnect }); }; });
  st.querySelectorAll('[data-profile-delete]').forEach(b => { b.onclick = () => { if (!window.confirm || window.confirm('Delete ' + b.dataset.profileDelete + '? ' + ('profileKeepsTokens' in b.dataset ? 'Its client secret is removed; the borrowed sign-in stays where it is.' : 'Its sign-in and client secret are removed.'))) { b.disabled = true; send({ type: 'auth_profile_delete', profile: b.dataset.profileDelete }); } }; });
  const nb = st.querySelector('[data-profile-new]');
  if (nb) nb.onclick = () => { profileDraft = { provider: 'google', services: {} }; renderPlugins(); };
  const sel = st.querySelector('#pf-provider');
  if (sel) sel.onchange = () => { const d = readDraft(); const form = st.querySelector('.profile-form'); forgetForm(form); form.remove(); d.hosts = null; d.redirect_uri = ''; d.services = {}; profileDraft = d; renderPlugins(); };
  const cancel = st.querySelector('[data-profile-cancel]');
  if (cancel) cancel.onclick = () => { const form = st.querySelector('.profile-form'); forgetForm(form); form.remove(); profileDraft = null; renderPlugins(); };
  const save = st.querySelector('[data-profile-save]');
  if (save) save.onclick = () => {
    const d = readDraft();
    const secret = ($('pf-secret') && $('pf-secret').value) || '';
    const edit = { redirect_uri: d.redirect_uri, name: d.name, provider: d.provider, client_id: d.client_id, client_secret: secret, services: d.services,
      scopes: d.scopes, hosts: (d.hosts || '').split(/[,\s]+/).filter(Boolean),
      authorize_url: d.authorize_url, token_url: d.token_url, device_url: d.device_url, revoke_url: d.revoke_url };
    save.disabled = true; save.textContent = 'Saving…';
    const request = send({ type: 'auth_profile_set', profile_edit: edit });
    if (!request) { save.disabled = false; save.textContent = 'Not connected. Try again.'; return; }
    if ($('pf-secret')) $('pf-secret').value = '';
    forgetForm(st.querySelector('.profile-form'));
    profileDraft = null;
  };
}
export function dropRetiredProfileForm(st) {
  const retiredForm = st.querySelector('.profile-form');
  if (!profileDraft && retiredForm) { forgetForm(retiredForm); retiredForm.remove(); }
}
export function takeConnectRequest(c) {
  if (S.connectRequest && c && c.plugins) {
    profileDraft = connectPrefill(S.connectRequest, c.plugins.providers, c.plugins.installed);
    S.connectRequest = null;
  }
}
