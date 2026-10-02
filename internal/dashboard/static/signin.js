import { pendingSlot } from './pending.js';
import { esc } from './util.js';
import { send } from './ws.js';
import { holdReloadWhile } from './overlay.js';

const pending = pendingSlot();
holdReloadWhile(() => [...document.querySelectorAll('.profile-paste-input')].some(el => el.value));
export function abandonSignIn(requestID) {
  const held = requestID ? pending.claim(requestID) : pending.drop();
  if (held && held.win && !held.win.closed) held.win.close();
  return !!held;
}
export function startSignIn(message) {
  abandonSignIn();
  const win = window.open('', '_blank');
  if (win) win.opener = null;
  const requestID = send(message);
  if (!pending.arm({ win }, requestID) && win) win.close();
}
export function acceptSignIn(message) {
  const held = pending.claim(message.request_id);
  if (!held) return false;
  if (held.win && !held.win.closed) {
    if (message.signin_url && safeURL(message.signin_url)) held.win.location = message.signin_url;
    else held.win.close();
  }
  return true;
}
function safeURL(value) {
  try { const u = new URL(value); return u.protocol === 'https:' || u.protocol === 'http:'; } catch { return false; }
}
export function signInProgress(view, target, name) {
  if (!view) return '';
  const device = view.device;
  if (view.status !== 'pending') return '<div class="store-hint" data-announce>Sign-in ' + esc(view.status) + '.</div>';
  const url = device ? (device.verification_uri_complete || device.verification_uri) : view.url;
  const link = safeURL(url) ? '<a href="' + esc(url) + '" target="_blank" rel="noopener noreferrer">Continue sign-in</a>' : '';
  if (view.manual && (target === 'provider' || target === 'profile')) {
    return '<div class="device-code" data-announce>' + link + ' — sign in, then copy the complete code shown by the provider.</div>' +
      '<form class="profile-paste" data-signin-complete="' + target + '" data-signin-name="' + esc(name) + '">' +
      '<input class="profile-paste-input" type="password" autocomplete="off" spellcheck="false" aria-label="Authorization code" placeholder="Paste the complete authorization code" required>' +
      '<button class="btn" type="submit">Complete sign-in</button><span class="savenote" role="status"></span></form>';
  }
  return '<div class="device-code" data-announce>' + (device ? 'Enter <b>' + esc(device.user_code) + '</b> on the provider’s page. ' : '') + link + ' This page updates when sign-in completes.</div>';
}

export function credentialExpired(ci) {
  if (!ci) return false;
  if (ci.expired) return true;
  const usable = ci.expires_at ? Date.parse(ci.expires_at) : NaN;
  return !isNaN(usable) && usable <= Date.now();
}

const CREDENTIAL_REFUSED = ['no_credential', 'credential_expired'];
export function signInWanted(p, skipWithValidToken = true) {
  if (p.signin && p.signin.status === 'pending') return true;
  if (!skipWithValidToken) return true;
  if (p.credential && CREDENTIAL_REFUSED.includes(p.status)) return true;
  const ci = p.credential_info;
  return !ci || !!ci.error || credentialExpired(ci);
}

export function wireSignInCompletion(root) {
  root.querySelectorAll('[data-signin-complete]').forEach(form => { form.onsubmit = event => {
    event.preventDefault();
    const target = form.dataset.signinComplete;
    if (target !== 'provider' && target !== 'profile') return;
    const input = form.querySelector('input');
    const value = input.value.trim();
    if (!value) return;
    const requestID = send({ type: target + '_signin_complete', [target]: form.dataset.signinName, input: value });
    const status = form.querySelector('[role="status"]');
    if (requestID) {
      input.value = '';
      status.textContent = 'Completing sign-in…';
    } else status.textContent = 'Not connected. Reconnect, then submit again.';
  }; });
}
