import { S } from '../state.js';
import { $, esc, fill, loadingCard } from '../util.js';
import W from '../strings/settings-permissions.js';
import { send } from '../ws.js';
import { saveConfigSection, configFeedbackHTML, savebarHTML, captureForm, restoreForm } from './settings.js';
import { keyTyping, wireSettingControls } from './plugin-setting.js';
import { storeHTML, pendingHTML, wireStore, catalogNote, tierWords } from './plugins/catalog.js';
import { installedHTML, assetCardHTML, framePagesHTML, wireCard } from './plugins/card.js';
import { storedHTML, wireStored } from './plugins/stored.js';
import { profilesHTML, wireProfiles, dropRetiredProfileForm, takeConnectRequest } from './plugins/accounts.js';
export { rejectPluginAction } from './plugins/catalog.js';
export { applyPluginStored } from './plugins/stored.js';
export { connectPrefill } from './plugins/accounts.js';

function machineHTML(m) {
  if (!m) return '';
  const line = (cls, text) => '<div class="store-hint ' + cls + '">' + text + '</div>';
  let body = '';
  if (m.state === 'linked') {
    body = line('machine-state', 'machine service: running, build ' + esc(m.build || 'unknown') + ' · ' + (m.linked || 1) + ' identit' + ((m.linked || 1) === 1 ? 'y' : 'ies') + ' linked');
    if (m.update) {
      body += '<div class="machine-update"><span class="store-hint">the machine service\'s update waits for idle (' + esc(m.update) + ')</span>' +
        (m.can_force ? ' <button class="btn sm" data-machine-update>Update now</button><span class="savenote" data-machine-note></span>' : '') + '</div>';
    }
    (m.missing || []).forEach(t => { body += line('machine-missing', esc(t)); });
  } else if (m.state === 'refused') {
    body = line('machine-state', 'machine service: running, and refused this identity — ' + esc(m.reason || 'no reason given'));
  } else if (m.state === 'not running') {
    body = line('machine-state' + (m.failing ? ' machine-failing' : ''), 'machine service: not running' + (m.reason ? ' — ' + esc(m.reason) : ''));
  } else {
    body = line('machine-state', 'machine service: linking…');
  }
  if (m.store) body += line('machine-store', 'plugins\' models and runtimes: the account\'s plugin store at ' + esc(m.store));
  if (m.remedy) body += line('machine-remedy', esc(m.remedy));
  if (m.warning) body += line('machine-warning', esc(m.warning));
  return '<div class="card machine" data-machine="' + esc(m.state) + '"><h3>MACHINE SERVICE</h3>' + body + '</div>';
}
function wireMachine(st) {
  const btn = st.querySelector('[data-machine-update]');
  if (!btn) return;
  btn.onclick = () => {
    btn.disabled = true; btn.textContent = 'Updating…';
    const note = st.querySelector('[data-machine-note]');
    const request = send({ type: 'plugin', plugin: { action: 'machine_update', id: '' } });
    if (!request) { btn.disabled = false; btn.textContent = 'Update now'; if (note) note.textContent = 'not connected — try again'; return; }
    if (note) note.textContent = 'asked: the service hands over now';
  };
}

function settingsHTML(c) {
  if (!c) return loadingCard('configuration');
  const lvl = c.plugins.autoload || 'T1';
  const opts = [
    ['T3', 'Platform-signed only'],
    ['T2', 'Reviewed or better'],
    ['T1', 'Signed or better (default)'],
    ['T0', 'Any, even unsigned'],
    ['none', 'None'],
  ];
  const url = (c.plugins && c.plugins.catalog_url) || '';
  return '<div class="card"><h3>PLUGIN SETTINGS</h3>' +
    '<label class="f">LOAD PLUGINS SIGNED</label><select id="cfg-plevel">' +
    opts.map(o => '<option value="' + o[0] + '"' + (o[0] === lvl ? ' selected' : '') + '>' + o[1] + '</option>').join('') + '</select>' +
    '<div class="dim-note">' + esc(W.signatureChecked) + '</div>' +
    savebarHTML('plugins', 'Applies at once.') +
    skipsHTML(c.plugins.skips) +
    '<label class="f">CATALOG ADDRESS</label>' +
    '<input type="text" id="cfg-caturl" value="' + esc(url) + '" placeholder="https://…/aiios-plugins.md">' +
    '<label class="f sub">Check every (minutes)' +
    '<input type="number" id="cfg-catevery" min="15" max="1440" value="' + ((c.plugins && c.plugins.catalog_refresh_minutes) || 60) + '"></label>' +
    savebarHTML('catalog', 'Applies at once.') +
    runtimeLimitsHTML(c.plugins.runtime) +
    '</div>';
}

function runtimeLimitsHTML(r) {
  if (!r) return '';
  const mib = n => n ? String(Math.round(n / 1048576)) : '';
  const bytesBox = (id, n) => '<input type="number" id="' + id + '" min="1" placeholder="Not set" value="' + mib(n) + '">';
  return '<label class="f">LIMITS FOR A PLUGIN\'S OWN PROGRAM</label>' +
    '<label class="f sub">Installed size (MiB)' + bytesBox('cfg-rt-installed', r.max_installed_bytes) + '</label>' +
    '<label class="f sub">Files<input type="number" id="cfg-rt-files" min="1" value="' + (r.max_files || 0) + '"></label>' +
    '<label class="f sub">Largest file (MiB)' + bytesBox('cfg-rt-file', r.max_file_bytes) + '</label>' +
    '<label class="f sub">Download size (MiB)' + bytesBox('cfg-rt-archive', r.max_compressed_bytes) + '</label>' +
    '<label class="f sub">Folder depth<input type="number" id="cfg-rt-depth" min="1" value="' + (r.max_depth || 0) + '"></label>' +
    '<label class="f sub">Old versions kept<input type="number" id="cfg-rt-kept" min="1" value="' + (r.roots_kept || 0) + '"></label>' +
    savebarHTML('plugin_runtime', 'Applies at once.');
}

function skipsHTML(skips) {
  if (!skips || !skips.length) return '';
  const kept = skips.filter(s => !s.kind || s.kind === 'policy');
  const refused = skips.filter(s => s.kind && s.kind !== 'policy');
  return (kept.length ? '<div class="store-skips"><label class="f">PRESENT, VERIFIED — NOT LOADED</label>' +
    kept.map(s => '<div class="store-skip"><b>' + esc(s.id) + '</b> (' + esc(s.tier) + ') — ' + esc(s.reason) + '</div>').join('') + '</div>' : '') +
    (refused.length ? '<div class="store-skips refused"><label class="f">PRESENT — REFUSED</label>' +
    refused.map(s => '<div class="store-skip"><b>' + esc(s.package || s.dir) + '</b>' + (s.id ? ' (' + esc(s.id) + ')' : '') + ' — ' + esc(s.reason) + '</div>').join('') + '</div>' : '');
}

const nameOf = p => p.title || p.id;
const managed = new Set();
function pluginsPartHTML(pl) {
  const installed = ((pl && pl.installed) || []).slice().sort((a, b) => nameOf(a).localeCompare(nameOf(b), undefined, { sensitivity: 'base' }));
  const offered = new Map(((pl && pl.catalog) || []).map(e => [e.id, e]));
  const updates = (pl && pl.catalog_updates) || 0;
  const count = fill(updates === 0 ? W.pluginsCountNone : updates === 1 ? W.pluginsCountOne : W.pluginsCount, { installed: installed.length, updates: updates });
  const rows = installed.map((p, i) => {
    const e = offered.get(p.id), update = !!(e && e.update_available), open = managed.has(p.id);
    const said = fill(W.pluginSignedBy, { tier: tierWords(p.tier), version: p.version }) +
      (e ? ' · ' + (update ? fill(W.updateOut, { version: e.version }) : W.upToDate) : '');
    return '<li class="permissions__row" data-plugin-row="' + esc(p.id) + '"><span class="permissions__words"><b>' + esc(nameOf(p)) + '</b>' +
      '<span class="permissions__soft">' + esc(said) + '</span></span><div class="permissions__buttons">' +
      (update ? '<button class="c-button" data-plugin="install:' + esc(p.id) + '">' + esc(fill(W.updateTo, { version: e.version })) + '</button>' : '') +
      framePagesHTML(p) + '<button class="c-button c-button--quiet" data-manage="' + esc(p.id) + '" aria-expanded="' + open + '" aria-controls="manage-' + i + '">' + esc(W.manage) + '</button></div>' +
      '<div class="permissions__manage" id="manage-' + i + '" data-manage-of="' + esc(p.id) + '"' + (open ? '' : ' hidden') + '>' +
      (p.kind === 'asset' ? assetCardHTML(p) : installedHTML([p])) +
      '<div class="savebar"><button class="btn sm ghost" data-plugin="uninstall:' + esc(p.id) + '">Uninstall</button></div></div></li>';
  }).join('');
  const refresh = pl && (pl.catalog_url || pl.catalog_dir) ? ' <button class="permissions__act" data-catalog-refresh>' + esc(W.catalogRefresh) + '</button>' : '';
  return '<section class="permissions__part" data-plugins-part aria-labelledby="plugins-h" aria-describedby="plugins-hint">' +
    '<h3 id="plugins-h" class="permissions__h3">' + esc(W.plugins) + '<span class="permissions__soft"> · ' + esc(count) + '</span></h3>' +
    '<p id="plugins-hint" class="permissions__hint">' + esc(W.pluginsHint) + '</p>' +
    '<p class="permissions__line">' + catalogNote(pl) + refresh + '</p>' +
    (rows ? '<ul class="permissions__rows permissions__rows--plugins">' + rows + '</ul>' : '<p class="permissions__line">' + esc(W.noPlugins) + '</p>') + '</section>';
}

export function renderPlugins() {
  const st = $('plugins-stack');
  if (st.formComposing) { st.formRefreshDue = true; return; }
  dropRetiredProfileForm(st);
  const activeForm = captureForm(st);
  const c = S.config;
  takeConnectRequest(c);
  const typing = keyTyping();
  const pl = c && c.plugins;
  st.innerHTML = configFeedbackHTML() + pluginsPartHTML(pl) + pendingHTML(pl && pl.pending) + storeHTML(pl) + machineHTML(pl && pl.machine) + storedHTML(pl) + profilesHTML(pl) + settingsHTML(c);
  restoreForm(st, activeForm);
  if (!st.composeHeard) {
    st.composeHeard = true;
    st.addEventListener('compositionstart', () => { st.formComposing = true; });
    st.addEventListener('compositionend', () => { st.formComposing = false; if (st.formRefreshDue) { st.formRefreshDue = false; renderPlugins(); } });
  }
  wireSettingControls(st, { send, rerender: renderPlugins, typing, connect: () => {
    renderPlugins();
    const form = st.querySelector('.profile-form');
    if (form && form.scrollIntoView) form.scrollIntoView({ block: 'nearest' });
  } });
  wireProfiles(st);
  st.querySelectorAll('[data-manage]').forEach(b => { b.onclick = () => {
    const id = b.dataset.manage, region = st.querySelector('[data-manage-of="' + CSS.escape(id) + '"]');
    if (managed.has(id)) managed.delete(id); else managed.add(id);
    b.setAttribute('aria-expanded', String(managed.has(id)));
    if (region) region.hidden = !managed.has(id);
  }; });
  st.querySelectorAll('[data-save]').forEach(btn => { btn.onclick = () => { saveConfigSection(btn.dataset.save); renderPlugins(); }; });
  st.querySelectorAll('[data-open-section]').forEach(a => {
    a.onclick = e => { e.preventDefault(); if (S.openSettings) S.openSettings(a.dataset.openSection, 'sp-provider-stt'); };
  });
  st.querySelectorAll('[data-open-frame]').forEach(b => { b.onclick = () => S.go('section:' + b.dataset.openFrame); });
  wireCard(st);
  wireStore(st);
  wireMachine(st);
  wireStored(st);
}
