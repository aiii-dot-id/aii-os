/* firstboot.js — the pre-birth flow: provider directory, live
   model discovery, and the genesis form. R66: frame — firstboot
   exists before any identity (and so before any section install)
   can exist; the lifeboat invariant's first face (UI_FRAME.md §2). */
import { S } from './state.js';
import { $, esc } from './util.js';
import { send, query } from './ws.js';

/* --- firstboot ---------------------------------------------- */
let discoverRequestID = '';

export function renderProviderOptions() {
  const sel = $('fb-provider');
  sel.innerHTML = '<option value="">choose a provider…</option>' +
    S.providers.map((p, i) =>
      '<option value="' + i + '"' + (p.preselect ? ' selected' : '') + '>' + esc(p.name) +
      (p.status === 'no_credential' ? ' — not signed in on this machine' : '') +
      '</option>').join('');
  /* Preselection is the runtime's answer to "what can this machine
     actually birth on right now": a subscription credential that is
     present and working wins, so an operator holding one births with
     nothing to paste. Derived per render — a lapsed subscription simply
     stops being the answer. */
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
  setModelOptions(p.models || []);
  /* providers.json remembers the default model. Stored keys never return
     to the browser; has_key says only that one is available server-side. */
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
// Live model discovery re-fires when the key arrives; the backend adds
// discovered models to the provider's configured set.
$('fb-apikey').addEventListener('change', onProviderChange);
$('fb-birth').onclick = () => {
  const p = S.providers[parseInt($('fb-provider').value, 10)];
  const g = {
    name: $('fb-name').value.trim(),
    operator_name: $('fb-operator').value.trim(),
    provider: p ? p.name : '', // the providers.json entry — birth writes the config POINTER at it
    model: $('fb-model').value,
    api_key: $('fb-apikey').value.trim(),
    endpoint: p ? p.endpoint : '',
  };
  if (!g.name || !g.operator_name) { fbResult('err', 'A name for each of you — that\'s where a relationship starts.'); return; }
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
