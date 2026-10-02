
import { startSignIn, signInProgress, signInWanted, wireSignInCompletion } from './signin.js';
import { S } from './state.js';
import { $, esc } from './util.js';
import { renderInto } from './announce.js';
import { send, query } from './ws.js';
import { renderRestoreDoor } from './firstboot-restore.js';

let discoverRequestID = '';
let selectedProvider = '';
let userSelected = false;
let connectedSignIn = '';
let askedURL = '';
let verifiedURL = '';

export function renderProviderOptions() {
  renderRestoreDoor();
  const sel = $('fb-provider');
  if (!sel || S.identityExists) return;
  sel.innerHTML = '<option value="">choose a provider…</option>' +
    S.providers.map((p, i) => p.chat === false ? '' :
      '<option value="' + i + '"' + ((selectedProvider ? p.name === selectedProvider : p.preselect) ? ' selected' : '') + '>' + esc(p.name) +
      '</option>').join('');

  const pre = S.providers.find(p => p.preselect);
  const current = S.providers[parseInt(sel.value, 10)];
  showWhy(current);
  renderSignIn(current);
  if (!selectedProvider && pre) { selectedProvider = pre.name; onProviderChange(); }
  if (current && userSelected && current.signin && current.signin.status === 'connected' && connectedSignIn !== current.name) {
    connectedSignIn = current.name; onProviderChange();
  }
  sel.onchange = () => {
    $('fb-apikey').value = '';
    $('fb-url').value = '';
    const p = S.providers[parseInt(sel.value, 10)];
    selectedProvider = p ? p.name : ''; userSelected = true; connectedSignIn = '';
    renderSignIn(p);
    onProviderChange();
  };
}
function showWhy(p) {
  const why = $('fb-cred-why');
  if (!why) return;
  why.textContent = !p ? ''
    : p.preselect && p.preselect_why ? p.preselect_why
    : p.status !== 'ok' && p.status_reason && !p.local ? p.status_reason : '';
}
function onProviderChange() {
  const sel = $('fb-provider');
  const p = S.providers[parseInt(sel.value, 10)];
  const sub = $('fb-subscribe');
  const url = $('fb-url');
  $('fb-url-row').hidden = !(p && p.local);
  askedURL = verifiedURL = '';
  showWhy(p);
  if (!p) { setModelOptions([]); sub.style.display = 'none'; return; }

  if (p.local && !url.value.trim()) url.value = p.endpoint || '';
  setModelOptions(p.local ? [] : p.models || []);

  if (p.default_model) $('fb-model').value = p.default_model;
  if (p.subscribe_url) {
    sub.style.display = '';
    sub.href = p.subscribe_url;
    sub.textContent = 'get a key at ' + p.subscribe_url.replace(/^https?:\/\//, '');
  } else sub.style.display = 'none';
  renderSignIn(p);
  const ask = { provider: p.name, api_key: $('fb-apikey').value.trim() };
  if (p.local) {
    askedURL = ask.base_url = url.value.trim();
    fbHint('Asking ' + askedURL + ' for its models…');
  }
  discoverRequestID = query('discover', ask);
  if (!discoverRequestID) fbHint('Not connected — reselect the provider after reconnect.');
}

function renderSignIn(p) {
  let row = $('fb-signin');
  if (!row) { row = document.createElement('div'); row.id = 'fb-signin'; $('fb-hint').after(row); }
  if (!p || !userSelected || !p.can_sign_in) { row.innerHTML = ''; return; }
  renderInto(row, signInProgress(p.signin, 'provider', p.name) +
    (signInWanted(p, S.skipSignInWithValidToken !== false) ? '<button class="btn" id="fb-signin-start">Sign in with ' + esc(p.name) + '</button>' : '') +
    (p.signin && p.signin.status === 'pending' ? '<button class="btn ghost" id="fb-signin-cancel">Cancel sign-in</button>' : ''));
  wireSignInCompletion(row);
  const cancel = $('fb-signin-cancel'); if (cancel) cancel.onclick = () => send({type:'provider_signin_cancel',provider:p.name});
  const start = $('fb-signin-start'); if (start) start.onclick = () => { connectedSignIn = ''; startSignIn({ type: 'provider_signin', provider: p.name }); };
}

export function acceptDiscoveryResponse(requestID, provider) {
  const sel = $('fb-provider');
  const current = S.providers[parseInt(sel && sel.value, 10)];
  if (!requestID || requestID !== discoverRequestID || !current || current.name !== provider) return false;
  discoverRequestID = '';
  return true;
}
export function discoveryAnswered(models) {
  const p = S.providers[parseInt($('fb-provider').value, 10)];
  setModelOptions(models);
  if (p && p.default_model) $('fb-model').value = p.default_model;
  verifiedURL = models.length ? askedURL : '';
  fbHint('');
}
export function discoveryFailed(text) {
  const p = S.providers[parseInt($('fb-provider').value, 10)];
  if (!p || !p.local) return false;
  fbHint('Nothing at ' + askedURL + ' answered with its models — start your local server there, or correct the address. (' + text + ')');
  return true;
}
export function setModelOptions(models) {
  $('fb-model').innerHTML = models.map(m => '<option>' + esc(m) + '</option>').join('') || '<option value="">—</option>';
}
export function fbHint(t) { $('fb-hint').textContent = t || ''; }
export function fbResult(kind, text) {
  $('fb-result').innerHTML = '<div class="' + (kind === 'ok' ? 'ok' : 'err') + '">' + esc(text) + '</div>';
}

$('fb-apikey').addEventListener('change', onProviderChange);
$('fb-url').addEventListener('change', onProviderChange);
$('fb-birth').onclick = () => {
  const p = S.providers[parseInt($('fb-provider').value, 10)];
  const url = $('fb-url').value.trim();
  if (p && p.local && (!url || url !== verifiedURL)) {
    fbResult('err', 'Birth waits until the server at ' + url + ' lists its models. Start it there, or correct the address.');
    return;
  }

  const g = {
    provider: p ? p.name : '',
    model: $('fb-model').value,
    api_key: $('fb-apikey').value.trim(),
    endpoint: p ? (p.local ? url : p.endpoint) : '',
  };
  $('fb-birth').disabled = true;
  fbResult('ok', 'Birthing…');
  if (!send({ type: 'genesis', genesis: g })) {
    $('fb-birth').disabled = false;
    fbResult('err', 'Not connected — birth did not start.');
  }
};

export function firstbootConnectionLost() {
  const birth = $('fb-birth');
  if (S.identityExists || !birth.disabled) return false;
  birth.disabled = false;
  fbResult('err', 'Connection lost before birth confirmation — check identity state after reconnect.');
  return true;
}
