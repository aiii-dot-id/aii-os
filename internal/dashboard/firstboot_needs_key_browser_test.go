//go:build !windows

package dashboard

import "testing"

const firstbootNeedsKeyPage = `<!DOCTYPE html><html><head><title>needs key</title></head><body>
<button id="send-btn">send</button>
<select id="fb-provider"><option value="0">Acme</option></select>
<button id="fb-birth" disabled>Birth</button>
<script type="module" src="/probe.js"></script>
</body></html>`

const firstbootNeedsKeyProbeJS = `
import { report } from '/__harness.js';
try {
  const assert = (ok, why) => { if (!ok) throw new Error(why); };
  const sockets = [];
  class Socket {
    constructor() { this.readyState = 0; sockets.push(this); }
    send() {}
    open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
    say(m) { this.onmessage({ data: JSON.stringify(m) }); }
  }
  Socket.CONNECTING = 0; Socket.OPEN = 1; Socket.CLOSING = 2; Socket.CLOSED = 3;
  window.WebSocket = Socket;
  const { connect } = await import('/ws.js');
  const { S } = await import('/state.js');
  connect();
  const socket = sockets[0];
  socket.open();
  socket.say({ type: 'status', stats: { name: '(not born)' } });
  S.providers = [{ name: 'Acme' }];
  const hints = window.__fb.hints, results = window.__fb.results;

  const needed = 'Acme lists its models only for a key';
  socket.say({ type: 'error', message: needed, request_id: 'discover-1', provider: 'Acme', needs_key: true });
  assert(hints.at(-1) === needed && results.length === 0, 'a refusal for want of a key was not the hint of the form: hints ' + JSON.stringify(hints) + ', results ' + JSON.stringify(results));

  const other = 'the provider refused the API key format';
  socket.say({ type: 'error', message: other, request_id: 'discover-2', provider: 'Acme' });
  assert(hints.at(-1) === needed && results.at(-1) === 'err:' + other, 'a refusal without needs_key was routed by its words: hints ' + JSON.stringify(hints) + ', results ' + JSON.stringify(results));
  assert(!document.getElementById('fb-birth').disabled, 'a shown refusal left Birth disabled');
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`

func TestTheBirthFormAsksForAKeyOnlyWhenTheServerSaysOneIsNeeded(t *testing.T) {
	modules := wsProbeModules(t, firstbootNeedsKeyProbeJS)
	modules["/firstboot.js"] = []byte(`window.__fb = { hints: [], results: [] };
export const fbHint = t => { window.__fb.hints.push(t); };
export const fbResult = (kind, text) => { window.__fb.results.push(kind + ':' + text); };
export const acceptDiscoveryResponse = () => true;
export const discoveryFailed = () => false;
export const renderProviderOptions = () => {};
export const setModelOptions = () => {};
export const discoveryAnswered = () => {};
export const firstbootConnectionLost = () => {};
`)
	runPageInEnginesWithHeaders(t, firstbootNeedsKeyPage, modules, map[string]string{"Content-Security-Policy": uiCSP})
}
