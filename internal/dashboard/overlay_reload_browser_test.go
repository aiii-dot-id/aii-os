//go:build !windows

// Runs through the unix browser-engine rig (browserengine_test.go, same tag).

package dashboard

import "testing"

func TestAPageSignedInAfterItLoadedReloadsOnceForTheOverlay(t *testing.T) {
	modules := wsProbeModules(t, `
import { report } from '/__harness.js';
try {
  const assert = (ok, why) => { if (!ok) throw new Error(why); };
  const realFetch = window.fetch.bind(window);
  let authorized = false, overlay = '';
  window.fetch = async (url, options = {}) => {
    if (url !== '/auth/token') return realFetch(url, options);
    if (options.method !== 'POST') return new Response(null, {status: authorized ? 204 : 401});
    authorized = true;
    const headers = {'Aii-Page-Key': 'pk1.the-key'};
    if (overlay) headers['Aii-Overlay'] = overlay;
    return new Response(null, {status: 204, headers});
  };
  class Socket { constructor() { this.readyState = 3; } send() {} }
  window.WebSocket = Socket;
  window.webkit = {messageHandlers: {aiiDashboardAuth: {postMessage() { return Promise.resolve('the-token'); }}}};
  const {recoverAuthentication} = await import('/ws.js');

  await recoverAuthentication();
  assert(authorized, 'the shell did not sign in');
  assert((window.__reloads || 0) === 0, 'a sign-in whose answer names no overlay reloaded the page');

  authorized = false; overlay = 'reload';
  await recoverAuthentication();
  assert(authorized, 'the shell did not sign in again');
  assert(window.__reloads === 1, 'THE PAGE DID NOT LOAD AGAIN FOR THE OVERLAY: it keeps the shipped frame it was given before it signed in (' + (window.__reloads || 0) + ' reloads)');

  authorized = false;
  Storage.prototype.setItem = function () { throw new DOMException('refused', 'SecurityError'); };
  await recoverAuthentication();
  assert(authorized, 'the shell did not sign in with storage refused');
  assert(window.__reloads === 1, 'a sign-in held in the tab\'s memory only was ended by a reload');
  report('OK');
} catch (e) { report('FAIL: ' + (e.message || String(e))); }
`)
	// overlay.js counts the reloads ws.js asks it for.
	modules["/overlay.js"] = append(stubModule("onOverlayChanged", "holdReloadWhile"),
		[]byte("export const reloadForTheOverlay = () => { window.__reloads = (window.__reloads || 0) + 1; };\n")...)
	runPageInEnginesWithHeaders(t, tokenPromptProbePage, modules, map[string]string{"Content-Security-Policy": uiCSP})
}
