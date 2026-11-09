import { startSignIn, signInProgress, signInWanted, signInUnmetRow, wireSignInCompletion } from '../../signin.js';
import { S } from '../../state.js';
import { $, h, fill } from '../../util.js';
import { renderInto } from '../../announce.js';
import { send, query } from '../../ws.js';
import { renderRestoreDoor } from '../../firstboot-restore.js';
import W from '../../strings/firstrun.js';

let discoverRequestID = '';
let selectedProvider = '';
let userSelected = false;
let connectedSignIn = '';
let askedURL = '';
let verifiedURL = '';
let notesSave = '';
let pendingBirth = null, birthSent = false;
let row = '';
let drawnRows = '';
let modelsOpen = false;
let modelChoice = '', choiceGone = false;

function drawScreen() {
  const host = $('firstboot');
  if (!host || $('fb-birth')) return;
  const head = h('div', { class: 'first-run__head' },
    h('h2', { id: 'fb-title', class: 'first-run__title' }, W.title), h('p', { class: 'first-run__pitch' }, W.theyRemember));
  const what = h('section', { class: 'first-run__what', 'aria-labelledby': 'fb-title' },
    h('p', { class: 'first-run__lede' }, W.what),
    h('ul', { class: 'first-run__lines' }, W.lines.map(l => h('li', null, h('strong', null, l.title), h('span', null, l.text)))),
    h('div', { class: 'first-run__restore' },
      h('button', { type: 'button', class: 'first-run__link', id: 'fb-restore-open' }, W.restoreInstead),
      h('div', { id: 'fb-restore-body', hidden: true })));
  const detail = h('div', { class: 'first-run__detail', id: 'fb-detail' },
    h('div', { class: 'first-run__why', id: 'fb-cred-why' }),
    h('div', { class: 'first-run__field', id: 'fb-other-row', hidden: true },
      h('label', { for: 'fb-provider' }, W.whichService), h('select', { id: 'fb-provider' })),
    h('div', { class: 'first-run__field', id: 'fb-url-row', hidden: true },
      h('label', { for: 'fb-url' }, W.serverAddress),
      h('input', { type: 'text', id: 'fb-url', inputmode: 'url', autocomplete: 'off', spellcheck: 'false', placeholder: W.serverHint })),
    h('div', { class: 'first-run__field', id: 'fb-key-row' },
      h('label', { for: 'fb-apikey' }, W.key),
      h('input', { type: 'password', id: 'fb-apikey', autocomplete: 'off', placeholder: W.keyPlaceholder }),
      h('a', { class: 'first-run__sub', id: 'fb-subscribe', target: '_blank', rel: 'noopener', hidden: true })),
    h('div', { class: 'first-run__field', id: 'fb-model-row' },
      h('label', { for: 'fb-model' }, W.model),
      h('div', { class: 'first-run__model', id: 'fb-model-chosen', hidden: true }, h('span', { class: 'first-run__model-name' }),
        h('span', { class: 'first-run__recommended' }, W.recommended),
        h('button', { type: 'button', class: 'first-run__link', id: 'fb-model-more', onclick: () => drawModel(true) }, W.otherModels)),
      h('select', { id: 'fb-model' })),
    h('div', { class: 'first-run__hint', id: 'fb-hint', role: 'status' }));
  const card = h('section', { class: 'first-run__card', 'aria-labelledby': 'fb-model-title' },
    h('h3', { id: 'fb-model-title', class: 'first-run__card-title' }, W.modelCard),
    h('fieldset', { class: 'first-run__choices', id: 'fb-choices' }, h('legend', { class: 'sr-only' }, W.model)),
    detail,
    h('div', { class: 'first-run__begin' },
      h('h4', null, W.whenBegin),
      h('ol', null, h('li', null, W.step1), h('li', { id: 'fb-notes-step', hidden: true }, W.beginStep2), h('li', null, W.beginStep3)),
      h('label', { class: 'first-run__notes', id: 'fb-notes-row', hidden: true }, h('input', { type: 'checkbox', id: 'fb-notes', checked: true }), h('span', null, W.copyNotes))),
    h('button', { type: 'button', class: 'first-run__go', id: 'fb-birth' }, W.begin),
    h('div', { class: 'first-run__result', id: 'fb-result', role: 'status' }));
  host.replaceChildren(h('div', { class: 'first-run' }, head, card, what));
}
drawScreen();

const subscription = p => !!p && (p.credential === 'claude-code' || p.credential === 'codex');
const unsigned = p => subscription(p) && ['no_credential', 'credential_expired', 'auth_required'].includes(p.status);
function rowsFor(providers) {
  const chat = providers.map((p, i) => ({ p, i })).filter(e => e.p.chat !== false);
  const rec = chat.find(e => e.p.preselect);
  const named = n => chat.find(e => e.p.name === n && e !== rec);
  const rows = [];
  if (rec) {
    const p = rec.p;
    rows.push({ id: 'rec', index: rec.i, recommended: true,
      label: p.local ? fill(W.onThisComputer, { model: p.preselect_model || p.default_model || (p.models || [])[0] || p.name }) : p.name,
      sub: p.local ? W.foundHere : p.preselect_why || '' });
  }
  for (const e of chat) if (e !== rec && subscription(e.p)) rows.push({ id: e.p.name, index: e.i, label: e.p.name, line: unsigned(e.p) ? e.p.status_reason || '' : '' });
  for (const [n, sub] of [['OpenAI', null], ['Anthropic', W.pasteKey]]) {
    const e = named(n);
    if (e) rows.push({ id: n, index: e.i, label: n, sub: sub || (e.p.can_sign_in ? W.signInOrKey : W.pasteKey) });
  }
  rows.push({ id: 'other', index: -1, label: W.providerOther, sub: W.giveAddress });
  return rows;
}

function drawChoices() {
  const box = $('fb-choices');
  if (!box) return;
  const detail = $('fb-detail');
  const rows = rowsFor(S.providers || []);
  if (!row && rows[0] && rows[0].recommended) row = 'rec';
  const drawn = JSON.stringify(rows);
  const focused = box.contains(document.activeElement) ? document.activeElement : null;
  const focusedRow = focused && focused.name === 'fb-model-choice' ? focused.value : '';
  if (drawn !== drawnRows || !box.querySelector('[data-row]')) {
    if (detail) box.after(detail);
    drawnRows = drawn;
    box.replaceChildren(box.querySelector('legend'), ...rows.map(r => h('div', { class: 'first-run__choice', 'data-row': r.id },
      h('label', { class: 'first-run__choice-label' },
        h('input', { type: 'radio', name: 'fb-model-choice', value: r.id, onchange: () => choose(r) }),
        h('span', { class: 'first-run__choice-words' },
          h('span', { class: 'first-run__choice-name' }, r.label, r.recommended ? h('span', { class: 'first-run__recommended' }, ' ' + W.recommended) : null,
            r.recommended || !r.sub ? null : h('span', { class: 'first-run__choice-how' }, ' · ' + r.sub)),
          r.recommended && r.sub ? h('span', { class: 'first-run__choice-sub' }, r.sub) : null,
          r.line ? h('span', { class: 'first-run__choice-sub' }, r.line) : null)))));
  }
  let on = null;
  for (const div of box.querySelectorAll('[data-row]')) {
    const mine = div.dataset.row === row;
    div.classList.toggle('is-on', mine);
    div.querySelector('input').checked = mine;
    if (mine) on = div;
  }
  if (detail) { if (on && detail.parentElement !== on) on.append(detail); detail.hidden = !on; }
  $('fb-other-row').hidden = row !== 'other';
  const again = focusedRow ? box.querySelector('input[value="' + CSS.escape(focusedRow) + '"]') : focused;
  if (again && again.isConnected && again !== document.activeElement) again.focus();
}

function choose(r) {
  row = r.id;
  userSelected = true; freshChoice();
  const sel = $('fb-provider');
  if (r.index >= 0) sel.value = String(r.index);
  else if (rowsFor(S.providers || []).some(x => x.index === parseInt(sel.value, 10))) sel.value = '';
  $('fb-apikey').value = '';
  $('fb-url').value = '';
  const p = S.providers[parseInt(sel.value, 10)];
  selectedProvider = p ? p.name : ''; connectedSignIn = '';
  drawChoices();
  renderSignIn(p);
  onProviderChange();
  beginReady();
}

export function renderProviderOptions() {
  drawScreen();
  renderRestoreDoor();
  const sel = $('fb-provider');
  if (!sel || S.identityExists) return;
  sel.replaceChildren(h('option', { value: '' }, W.whichService),
    ...S.providers.map((p, i) => p.chat === false ? null :
      h('option', { value: String(i), selected: (selectedProvider ? p.name === selectedProvider : p.preselect) || null }, p.name)));

  const pre = S.providers.find(p => p.preselect);
  const current = S.providers[parseInt(sel.value, 10)];
  showWhy(current);
  drawChoices();
  renderSignIn(current);
  notesFromConfig();
  if (!selectedProvider && pre) { selectedProvider = pre.name; onProviderChange(); }
  if (current && userSelected && current.signin && current.signin.status === 'connected' && connectedSignIn !== current.name) {
    connectedSignIn = current.name; onProviderChange();
  }
  sel.onchange = () => {
    $('fb-apikey').value = '';
    $('fb-url').value = '';
    const p = S.providers[parseInt(sel.value, 10)];
    selectedProvider = p ? p.name : ''; userSelected = true; connectedSignIn = ''; freshChoice();
    const own = rowsFor(S.providers || []).find(r => r.index === parseInt(sel.value, 10));
    row = own ? own.id : 'other';
    drawChoices();
    renderSignIn(p);
    onProviderChange();
    beginReady();
  };
  beginReady();
}
function showWhy(p) {
  const why = $('fb-cred-why');
  if (!why) return;
  why.textContent = !p ? ''
    : row === 'rec' && p.preselect && $('fb-choices') ? ''
    : p.preselect && p.preselect_why ? p.preselect_why
    : p.status_reason && !p.local && !p.can_sign_in && !subscription(p) ? p.status_reason : '';
}
function onProviderChange() {
  const sel = $('fb-provider');
  const p = S.providers[parseInt(sel.value, 10)];
  const sub = $('fb-subscribe');
  const url = $('fb-url');
  $('fb-url-row').hidden = !(p && p.local);
  const keyRow = $('fb-key-row');
  if (keyRow) keyRow.hidden = !!(p && (p.credential || (p.has_key && !p.local)));
  askedURL = verifiedURL = '';
  showWhy(p);
  if (!p) { setModelOptions([]); sub.hidden = true; return; }

  if (p.local && !url.value.trim()) url.value = p.endpoint || '';
  offerModels(p.local ? [] : p.models || [], p.default_model);
  if (p.subscribe_url) {
    sub.hidden = false;
    sub.href = p.subscribe_url;
    sub.textContent = fill(W.subscribe, { provider: p.name });
  } else sub.hidden = true;
  renderSignIn(p);
  const ask = { provider: p.name, api_key: $('fb-apikey').value.trim() };
  if (p.local) {
    askedURL = ask.base_url = url.value.trim();
    fbHint(W.localChecking);
  }
  discoverRequestID = query('discover', ask);
  if (!discoverRequestID) fbHint(W.beginOffline);
}

function renderSignIn(p) {
  let line = $('fb-signin');
  if (!line) { line = document.createElement('div'); line.id = 'fb-signin'; line.className = 'first-run__signin'; $('fb-hint').after(line); }
  if (!p || !userSelected) { line.innerHTML = ''; return; }
  if (!p.can_sign_in) { renderInto(line, signInUnmetRow(p)); return; }
  renderInto(line, signInProgress(p.signin, 'provider', p.name) +
    (signInWanted(p, S.skipSignInWithValidToken !== false) ? '<button type="button" class="btn" id="fb-signin-start"></button>' : '') +
    (p.signin && p.signin.status === 'pending' ? '<button type="button" class="btn ghost" id="fb-signin-cancel"></button>' : ''));
  const start = $('fb-signin-start'), cancel = $('fb-signin-cancel');
  if (start) start.textContent = fill(W.signIn, { provider: p.name });
  if (cancel) cancel.textContent = W.signInCancel;
  wireSignInCompletion(line);
  if (cancel) cancel.onclick = () => send({ type: 'provider_signin_cancel', provider: p.name });
  if (start) start.onclick = () => { connectedSignIn = ''; startSignIn({ type: 'provider_signin', provider: p.name }); };
}

export function acceptDiscoveryResponse(requestID, provider) {
  const sel = $('fb-provider');
  const current = S.providers[parseInt(sel && sel.value, 10)];
  if (!requestID || requestID !== discoverRequestID || !current || current.name !== provider) return false;
  discoverRequestID = '';
  return true;
}
export function discoveryAnswered(models) {
  offerModels(models, recommended(), true);
  verifiedURL = models.length ? askedURL : '';
  fbHint('');
}
export function discoveryFailed(text) {
  const p = S.providers[parseInt($('fb-provider').value, 10)];
  if (!p || !p.local) return false;
  fbHint(fill(W.modelWaits, { url: askedURL }) + ' (' + text + ')');
  return true;
}
function freshChoice() { modelsOpen = false; modelChoice = ''; choiceGone = false; }
function recommended() { const p = S.providers[parseInt($('fb-provider').value, 10)]; return p && (p.preselect_model || p.default_model); }
export function setModelOptions(models) {
  const sel = $('fb-model'), rec = recommended();
  sel.replaceChildren(...(models.length ? models.map(m => h('option', { value: m }, m === rec ? m + ' · ' + W.recommended : m)) : [h('option', { value: '' }, '—')]));
  drawModel();
  beginReady();
}
function offerModels(models, rec, answered) {
  const sel = $('fb-model'), before = modelChoice && !answered && !models.includes(modelChoice);
  setModelOptions(before ? [modelChoice, ...models] : models);
  if (modelChoice && (before || models.includes(modelChoice))) sel.value = modelChoice;
  else if (modelChoice && models.length) { modelChoice = ''; choiceGone = true; }
  else if (!choiceGone && !modelChoice && rec && models.includes(rec)) sel.value = rec;
  if (choiceGone) sel.selectedIndex = -1;
  drawModel();
  beginReady();
}
function drawModel(open) {
  const sel = $('fb-model'), chosen = $('fb-model-chosen');
  if (!sel || !chosen) return;
  if (open) modelsOpen = true;
  const rec = recommended();
  const behind = !modelsOpen && sel.options.length > 1 && !!rec && sel.value === rec;
  sel.hidden = behind; chosen.hidden = !behind;
  chosen.firstChild.textContent = behind ? rec : '';
  if (open) sel.focus();
}
function beginReady() {
  const b = $('fb-birth');
  if (!b || !$('fb-choices') || notesSave || b.textContent === W.beginning) return;
  const service = !!$('fb-provider').value;
  b.disabled = !(service && $('fb-model').value) || choiceGone || unsigned(S.providers[parseInt($('fb-provider').value, 10)]);
  if (!service || choiceGone) fbResult('ok', W.chooseModel);
  else if ($('fb-result').textContent === W.chooseModel) fbResult('ok', '');
}
export function fbHint(t) { $('fb-hint').textContent = t || ''; }
export function fbResult(kind, text) {
  const out = $('fb-result');
  out.className = 'first-run__result' + (kind === 'ok' ? '' : ' is-bad');
  out.textContent = text || '';
}

let notesTouched = false;
export function firstRunConfigArrived() { notesFromConfig(); }
function notesFromConfig() {
  const box = $('fb-notes');
  if (!box) return;
  const g = S.config && S.config.genesis;
  for (const id of ['fb-notes-step', 'fb-notes-row']) if ($(id)) $(id).hidden = !(g && g.agent_memory_found);
  if (notesTouched) return;
  if (g) box.checked = !g.agent_memory_off;
  else if (!S.identityExists) query('config');
}

function wire() {
  if (!$('fb-apikey') || !$('fb-birth')) return;
  $('fb-apikey').addEventListener('change', onProviderChange);
  $('fb-url').addEventListener('change', onProviderChange);
  if ($('fb-notes')) $('fb-notes').addEventListener('change', () => { notesTouched = true; });
  $('fb-model').addEventListener('change', () => { modelChoice = $('fb-model').value; choiceGone = false; beginReady(); });
  $('fb-birth').onclick = begin;
  beginReady();
}
wire();

function begin() {
  const p = S.providers[parseInt($('fb-provider').value, 10)];
  const url = $('fb-url').value.trim();
  if (p && p.local && (!url || url !== verifiedURL)) {
    fbResult('err', fill(W.modelWaits, { url }));
    return;
  }
  const g = {
    provider: p ? p.name : '',
    model: $('fb-model').value,
    api_key: $('fb-apikey').value.trim(),
    endpoint: p ? (p.local ? url : p.endpoint) : '',
  };
  const saved = !!(S.config && S.config.genesis && S.config.genesis.agent_memory_off);
  const box = $('fb-notes');
  const off = box ? !box.checked : saved;
  $('fb-birth').disabled = true;
  if (box && (off !== saved || !(S.config && S.config.genesis))) {
    fbResult('ok', W.savingNotes);
    pendingBirth = g;
    notesSave = send({ type: 'config_set', config: { 'genesis.agent_memory.off': off } });
    if (!notesSave) { pendingBirth = null; beginAgain(W.beginOffline); }
    return;
  }
  askForBirth(g);
}
function askForBirth(g) {
  fbResult('ok', W.beginning);
  $('fb-birth').textContent = W.beginning;
  birthSent = !!send({ type: 'genesis', genesis: g });
  if (!birthSent) beginAgain(W.beginOffline);
}
export function beginAgain(why) {
  const b = $('fb-birth');
  birthSent = false;
  if (!b) return;
  b.textContent = W.begin; b.disabled = false;
  beginReady();
  if (why) fbResult('err', why);
}
export function acceptFirstRunConfig(requestID) {
  if (!requestID || requestID !== notesSave) return false;
  notesSave = '';
  const g = pendingBirth; pendingBirth = null;
  const saved = !!(S.config && S.config.genesis && S.config.genesis.agent_memory_off);
  if (!g) return true;
  if ($('fb-notes') && saved !== !$('fb-notes').checked) {
    beginAgain(fill(W.notesNotSaved, { reason: 'the saved value reads back otherwise' }));
    return true;
  }
  askForBirth(g);
  return true;
}
export function refuseFirstRunConfig(requestID, text) {
  if (!requestID || requestID !== notesSave) return false;
  notesSave = ''; pendingBirth = null;
  beginAgain(fill(W.notesNotSaved, { reason: text }));
  return true;
}

export function firstbootConnectionLost() {
  const birth = $('fb-birth');
  if (!birth) return false;
  if (notesSave) {
    notesSave = ''; pendingBirth = null;
    beginAgain(W.beginOffline);
    return true;
  }
  if (S.identityExists || !birthSent) { beginReady(); return false; }
  beginAgain(W.beginLost);
  return true;
}
