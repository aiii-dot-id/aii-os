
import { S } from './state.js';
import { $, esc } from './util.js';
import { send, query } from './ws.js';

let discoverRequestID = '';

function credentialNote(p) {
  if (p.status === 'credential_expired') return ' — signed in, but the saved token expired';
  if (p.status === 'no_credential') return ' — not signed in on this machine';
  return '';
}

export function renderProviderOptions() {
  const sel = $('fb-provider');
  if (!sel || S.identityExists) return;
  sel.innerHTML = '<option value="">choose a provider…</option>' +
    S.providers.map((p, i) =>
      '<option value="' + i + '"' + (p.preselect ? ' selected' : '') + '>' + esc(p.name) +
      credentialNote(p) +
      '</option>').join('');

  const pre = S.providers.find(p => p.preselect);
  const why = $('fb-cred-why');
  if (why) why.innerHTML = pre && pre.preselect_why ? esc(pre.preselect_why) : '';
  if (pre) onProviderChange();
  sel.onchange = () => {
    $('fb-apikey').value = '';
    onProviderChange();
  };
}
function onProviderChange() {
  const sel = $('fb-provider');
  const p = S.providers[parseInt(sel.value, 10)];
  const sub = $('fb-subscribe');
  if (!p) { setModelOptions([]); sub.style.display = 'none'; return; }

  const why = $('fb-cred-why');
  if (why && p.status !== 'ok' && p.status_reason) why.innerHTML = esc(p.status_reason);
  setModelOptions(p.models || []);

  if (p.default_model) $('fb-model').value = p.default_model;
  if (p.subscribe_url) {
    sub.style.display = '';
    sub.href = p.subscribe_url;
    sub.textContent = 'get a key at ' + p.subscribe_url.replace(/^https?:\/\//, '');
  } else sub.style.display = 'none';
  const key = $('fb-apikey').value.trim();
  discoverRequestID = query('discover', { provider: p.name, api_key: key });
  if (!discoverRequestID) fbHint('Not connected — reselect the provider after reconnect.');
}

export function acceptDiscoveryResponse(requestID, provider) {
  const sel = $('fb-provider');
  const current = S.providers[parseInt(sel && sel.value, 10)];
  if (!requestID || requestID !== discoverRequestID || !current || current.name !== provider) return false;
  discoverRequestID = '';
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
$('fb-birth').onclick = () => {
  const p = S.providers[parseInt($('fb-provider').value, 10)];

  const g = {
    provider: p ? p.name : '',
    model: $('fb-model').value,
    api_key: $('fb-apikey').value.trim(),
    endpoint: p ? p.endpoint : '',
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
