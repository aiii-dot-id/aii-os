//go:build !windows

package dashboard

import "testing"

func TestShellSignInRecoversWithoutPromptingUnderShippingCSP(t *testing.T) {
	runTokenPromptProbe(t, `
import { report } from '/__harness.js';
try {
  const assert = (ok, why) => { if (!ok) throw new Error(why); };
  const realFetch = window.fetch.bind(window);
  let expected = 'first-token', authorized = false, posts = 0, reads = 0, asks = 0, refuse = false;
  window.prompt = () => { asks++; throw new Error('phone was asked for a CLI token'); };
  window.fetch = async (url, options = {}) => {
    if (url !== '/auth/token') return realFetch(url, options);
    if (options.method !== 'POST') return {status: authorized ? 204 : 401};
    posts++;
    assert(options.credentials === 'same-origin', 'login dropped cookie credentials');
    assert(options.body === expected, 'stale or wrong token submitted');
    authorized = true; return {status: 204, ok: true};
  };
  class Socket { constructor() { this.readyState = 3; } send() {} }
  window.WebSocket = Socket;
  const {recoverAuthentication} = await import('/ws.js');
  const {shellAccessToken} = await import('/shell-auth.js');
  for (const platform of ['android', 'ios']) {
    const response = () => { reads++; return refuse ? '' : expected; };
    if (platform === 'android') window.aiiDashboardAuth = {
      postMessage(kind) { assert(kind === 'token', 'wrong bridge request'); queueMicrotask(() => this.onmessage({data: response()})); }
    };
    else window.webkit = {messageHandlers: {aiiDashboardAuth: {
      postMessage(kind) { assert(kind === 'token', 'wrong bridge request'); return Promise.resolve(response()); }
    }}};
    authorized = false;
    const before = posts;
    await recoverAuthentication();
    assert(authorized && posts === before + 1, platform + ' did not sign in');
    const beforeReads = reads;
    await recoverAuthentication();
    assert(reads === beforeReads, 'valid cookie caused another credential read');
    for (const cause of ['rotation', 'expired-cookie', 'restart']) {
      authorized = false; expected = platform + cause;
      await recoverAuthentication();
      assert(authorized, platform + ' did not recover ' + cause);
    }
    authorized = false; refuse = true;
    const beforeRefusal = posts;
    await recoverAuthentication();
    assert(!authorized && posts === beforeRefusal && asks === 0, 'native refusal fell open or prompted');
    refuse = false;
    await recoverAuthentication();
    assert(authorized, 'refusal left authentication latched');
    delete window.aiiDashboardAuth; delete window.webkit;
  }
  const oldTimeout = window.setTimeout, oldClear = window.clearTimeout;
  window.setTimeout = fn => { queueMicrotask(fn); return 1; };
  window.clearTimeout = () => {};
  window.aiiDashboardAuth = {postMessage() {}};
  let timedOut = false;
  try { await shellAccessToken(); } catch (_) { timedOut = true; }
  window.setTimeout = oldTimeout; window.clearTimeout = oldClear;
  assert(timedOut && window.aiiDashboardAuth.onmessage === null, 'missing reply did not release the bridge');
  assert(asks === 0, 'mobile used a manual token prompt');
  assert(!document.cookie.includes('aii_token'), 'page manufactured a cookie');
  report('OK');
} catch (e) { report('FAIL: ' + (e.message || String(e))); }
`)
}

func TestManualSignInRemainsVisibleOutsideChat(t *testing.T) {
	runTokenPromptProbe(t, `
import {report} from '/__harness.js';
try {
 const realFetch=window.fetch.bind(window);
 window.fetch=async(u,o)=>u==='/auth/token'?{status:401}:realFetch(u,o);
 window.WebSocket=class{constructor(){this.readyState=3;}send(){}};
 const composer=document.createElement('div');composer.id='composer-wrap';composer.style.display='none';document.body.appendChild(composer);
 const host=document.createElement('div');host.id='dashboard-auth';document.body.appendChild(host);
 const {recoverAuthentication}=await import('/ws.js');await recoverAuthentication();
 const form=document.getElementById('dashboard-signin');
 if(!form||form.parentElement!==host||form.getBoundingClientRect().height===0)throw Error('sign-in is hidden with Chat');
 report('OK');
} catch(e){report('FAIL: '+e.message);}
`)
}
