import { configResult, configWaitingFor, sendConfigChanges } from './save.js';
import { renderSettings } from './parts.js';
import { S } from '../../state.js';
import { esc, fill } from '../../util.js';
import { send } from '../../ws.js';
import L from '../../strings/settings-local.js';
import { localCheckbox } from './local-rule.js';

let look = null;
let found = null;
let refusal = '';
let adding = null;
let models;
let reportAsked = false;
const picked = new Map();

const choiceFor = f => {
  const ms = f.models || [];
  const p = picked.get(f.port);
  return ms.includes(p) ? p : (ms[0] || '');
};

const rerender = () => { if (S.view === 'settings' || S.view === 'phone') renderSettings(); };

const foundLine = (port, words, rest) => '<div class="settings-access__acts" data-local-found="' + port + '">' +
  '<span class="settings-access__hint">' + esc(words) + '</span>' + (rest || '') + '</div>';
function foundHTML(f) {
  const vars = { usual: f.usual, port: f.port, entry: f.entry || '' };
  if (f.added) return foundLine(f.port, fill(L.foundAdded, vars));
  if (f.entry) return foundLine(f.port, fill(L.foundEntry, vars));
  if (f.needs_key) return foundLine(f.port, fill(L.foundKey, vars));
  const models = f.models || [];
  if (!models.length) return foundLine(f.port, fill(L.foundNoModels, vars));
  const choice = choiceFor(f);
  const busy = adding && adding.port === f.port;
  return foundLine(f.port, fill(L.foundModels, vars),
    '<label class="sr-only" for="local-model-' + f.port + '">' + esc(L.modelLabel) + '</label>' +
    '<select class="settings-access__select" id="local-model-' + f.port + '" data-local-pick="' + f.port + '" data-immediate>' +
    models.map(m => '<option value="' + esc(m) + '"' + (m === choice ? ' selected' : '') + '>' + esc(m) + '</option>').join('') + '</select>' +
    '<button type="button" class="c-button" data-local-add="' + f.port + '"' + (adding ? ' disabled' : '') + '>' + esc(busy ? L.adding : L.add) + '</button>');
}

export function localModelHTML(c) {
  const on = !!(c && c.agency && c.agency.prefer_local_for_roles);
  const roles = ((c && c.agency && c.agency.roles) || []).map(r => r.name);
  if (models === undefined && !reportAsked) { reportAsked = true; send({ type: 'query', query: 'providers' }); }
  const box = localCheckbox(models, on, roles);
  let html = '<div class="settings-access__part settings-model__local" data-local>' +
    '<h4 class="settings-access__h4">' + esc(L.heading) + '</h4>' +
    '<p class="settings-access__hint">' + esc(L.meaning) + '</p>' +
    '<div class="settings-access__acts"><button type="button" class="c-button" data-local-look' + (look ? ' disabled' : '') + '>' + esc(look ? L.looking : L.look) + '</button></div>' +
    '<p class="settings-access__hint">' + esc(L.lookHint) + '</p>';
  if (refusal) html += '<div class="config-result bad" data-announce>' + esc(refusal) + '</div>';
  if (found) {
    html += found.length ? '<div data-local-found-list data-announce>' + found.map(foundHTML).join('') + '</div>'
      : '<p class="settings-access__hint" data-announce>' + esc(L.foundNone) + '</p>';
    html += '<p class="settings-access__hint">' + esc(L.otherPort) + '</p>';
  }
  if (box.show) {
    html += '<label class="settings-access__check"><input type="checkbox" id="local-roles-box" data-immediate' + (on ? ' checked' : '') + (configWaitingFor('local') ? ' disabled' : '') + '> ' + esc(L.checkbox) + '</label>' +
      '<div class="settings-access__' + (box.warn ? 'warn' : 'hint') + '" data-local-line data-announce>' + esc(configWaitingFor('local') ? L.saving : box.line) + '</div>';
  }
  if (configResult && configResult.section === 'local' && configResult.kind === 'bad') html += '<div class="config-result bad" data-announce>' + esc(configResult.text) + '</div>';
  return html + '</div>';
}

export function localMarkField(tag, p) {
  const id = 'pv-local-' + tag;
  return '<label class="f" style="display:flex;gap:6px;align-items:center"><input type="checkbox" id="' + id + '" aria-describedby="' + id + '-hint"' +
    (p && p.local ? ' checked' : '') + '> ' + esc(L.mark) + '</label>' +
    '<div class="settings-access__hint" id="' + id + '-hint" style="margin:-4px 0 8px">' + esc(L.markHint) + '</div>';
}

const markAdded = (rows, providers) => rows.map(f => !f.added && (providers || []).some(p => p.local && p.endpoint === f.address) ? { ...f, added: true } : f);

export function localFoundAnswered(msg) {
  if (!look || msg.request_id !== look.requestID) return false;
  look = null;
  found = markAdded(msg.local_found || [], S.providers);
  refusal = '';
  rerender();
  return true;
}
export function localRefused(requestID, text) {
  if (look && requestID === look.requestID) look = null;
  else if (adding && requestID === adding.requestID) adding = null;
  else return false;
  refusal = text;
  rerender();
  return true;
}
export function localProvidersAnswered(requestID, providers) {
  if (found && found.length) found = markAdded(found, providers);
  if (!adding || requestID !== adding.requestID) return;
  const port = adding.port;
  found = (found || []).map(f => f.port === port ? { ...f, added: true } : f);
  adding = null;
  refusal = '';
}

export function localConnectionLost() {
  if (!look && !adding) return false;
  look = null;
  adding = null;
  return true;
}

export function localPart(msg) {
  if (msg.type === 'local_found') return localFoundAnswered(msg);
  if (msg.type === 'error') return localRefused(msg.request_id, msg.message || '');
  if (msg.type === 'providers') { models = msg.local_models || null; localProvidersAnswered(msg.request_id, msg.providers); }
  return false;
}

document.addEventListener('click', e => {
  const el = e.target && e.target.closest ? e.target.closest('[data-local-look], [data-local-add]') : null;
  if (!el || el.disabled) return;
  if (el.hasAttribute('data-local-look')) {
    if (look) return;
    const id = send({ type: 'local_look' });
    if (!id) return;
    look = { requestID: id };
    refusal = '';
  } else {
    if (adding) return;
    const port = Number(el.dataset.localAdd);
    const f = (found || []).find(x => x.port === port);
    if (!f) return;
    const model = choiceFor(f);
    const id = send({ type: 'local_add', local_server: { port, model } });
    if (!id) return;
    adding = { requestID: id, port };
    refusal = '';
  }
  rerender();
});

document.addEventListener('change', e => {
  const el = e.target;
  if (!el || !el.closest || !el.closest('[data-local]')) return;
  if (el.id === 'local-roles-box') {
    sendConfigChanges('local', { 'agency.prefer_local_for_roles': !!el.checked });
    rerender();
  } else if (el.dataset.localPick) {
    picked.set(Number(el.dataset.localPick), el.value);
  }
});
