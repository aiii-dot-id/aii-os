import { pendingSlot } from './pending.js';
import { esc } from './util.js';
import { send } from './ws.js';

const pending = pendingSlot();
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
  // A blocked popup is recoverable through the current snapshot's link.
  return true;
}
function safeURL(value) {
  try { const u = new URL(value); return u.protocol === 'https:' || u.protocol === 'http:'; } catch { return false; }
}
export function signInProgress(view, target, name) {
  if (!view) return '';
  const device = view.device;
  if (view.status !== 'pending') return '<div class="store-hint">Sign-in ' + esc(view.status) + '.</div>';
  const url = device ? (device.verification_uri_complete || device.verification_uri) : view.url;
  const link = safeURL(url) ? '<a href="' + esc(url) + '" target="_blank" rel="noopener noreferrer">Continue sign-in</a>' : '';
  if (view.manual && (target === 'provider' || target === 'profile')) {
    return '<div class="device-code">' + link + ' — sign in, then copy the complete code shown by the provider.</div>' +
      '<form class="profile-paste" data-signin-complete="' + target + '" data-signin-name="' + esc(name) + '">' +
      '<input class="profile-paste-input" type="password" autocomplete="off" spellcheck="false" aria-label="Authorization code" placeholder="Paste the complete authorization code" required>' +
      '<button class="btn" type="submit">Complete sign-in</button><span class="savenote" role="status"></span></form>';
  }
  return '<div class="device-code">' + (device ? 'Enter <b>' + esc(device.user_code) + '</b> on the provider’s page. ' : '') + link + ' This page updates when sign-in completes.</div>';
}

// A CREDENTIAL IS EXPIRED when the runtime said so at the snapshot, or when
// the usable boundary it published has since passed: the providers are not
// re-sent to a page left open across that boundary, and the clock moves on.
export function credentialExpired(ci) {
  if (!ci) return false;
  if (ci.expired) return true;
  const usable = ci.expires_at ? Date.parse(ci.expires_at) : NaN;
  return !isNaN(usable) && usable <= Date.now();
}

// THE SIGN-IN CONTROLS ARE WANTED when no valid token is in hand: none
// adopted, one the runtime cannot read or has refused, or one past its
// expiry — or a sign-in already under way, which keeps its cancel. The
// runtime's own attempt to use the credential (the entry's status, in the
// two typed credential states the probe reports) counts before the
// credential's self-description does: a file that vanished after it was
// first read still described itself as usable, and the button the status
// line pointed to was hidden. Settings and the birth form share this rule.
// skipWithValidToken is the operator's dashboard.skip_signin_with_valid_token,
// on unless turned off: off, sign-in is offered beside a valid token too.
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
