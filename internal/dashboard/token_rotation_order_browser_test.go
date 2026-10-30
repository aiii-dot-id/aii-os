//go:build !windows

package dashboard

import (
	"strconv"
	"testing"
)

func TestTokenChangeRetiresOnlyItsRevokedCarrier(t *testing.T) {
	for _, act := range []string{"rotate", "require"} {
		for _, order := range []string{"reply_before_close", "close_before_reply", "refused", "replacement_before_reply"} {
			t.Run(act+"/"+order, func(t *testing.T) {
				runTokenPromptProbe(t, `
import { report } from '/__harness.js';
import { S } from '/state.js';
try {
  const act = `+strconv.Quote(act)+`, order = `+strconv.Quote(order)+`;
  const assert = (ok, why) => { if (!ok) throw Error(why); };
  const nativeFetch = window.fetch.bind(window);
  let answer, statusChecks = 0;
  window.fetch = async (url, options = {}) => {
    if (url === '/auth/' + act) return new Promise(resolve => { answer = resolve; });
    if (url === '/auth/token') { statusChecks++; return { status: 204, ok: true, headers: new Headers() }; }
    return nativeFetch(url, options);
  };
  const sockets = [];
  window.WebSocket = class {
    constructor() { this.readyState = 0; this.sent = []; this.closed = 0; sockets.push(this); }
    send(data) { this.sent.push(JSON.parse(data)); }
    open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
    fail() { this.readyState = 3; if (this.onclose) this.onclose({}); }
    close() { this.closed++; this.fail(); }
  };
  const { connect, query } = await import('/ws.js');
  connect();
  const old = sockets[0]; old.open();
  const lateClose = old.onclose;
  const change = act === 'rotate' ? S.rotateAccessToken() : S.requireAccessToken();
  if (order === 'close_before_reply' || order === 'replacement_before_reply') old.fail();
  if (order === 'replacement_before_reply') { connect(); sockets[1].open(); }
  assert(statusChecks === 0, 'recovery checked the old cookie during the HTTP act');
  const ok = order !== 'refused';
  answer({ ok, status: ok ? 204 : 403, text: async () => 'refused', headers: new Headers() });
  const result = await change;
  assert(result.ok === ok, 'HTTP outcome changed');
  if (order === 'refused') {
    assert(result.text === 'refused' && old.closed === 0 && S.connected, 'a refusal retired the valid session');
    query('logs');
    assert(old.sent.some(m => m.query === 'logs'), 'a refusal prevented use of the valid carrier');
  } else {
    if (order !== 'replacement_before_reply') {
      assert(!S.connected && document.getElementById('send-btn').disabled, 'the revoked connection was presented as ready');
      assert(query('logs') === '', 'a query was sent before the new connection opened');
      assert(!old.sent.some(m => m.query === 'logs'), 'a query used the revoked carrier');
      assert(sockets.length === 2 && sockets[1].readyState === 0, 'success did not reconnect once');
      sockets[1].open();
    }
    const current = sockets[1];
    assert(current.closed === 0 && S.connected, 'a replacement carrier was retired');
    lateClose({});
    assert(S.connected && !document.getElementById('send-btn').disabled, 'a late old close retired the replacement');
    query('logs');
    assert(current.sent.filter(m => m.query === 'logs').length === 1, 'the new carrier did not receive the one query');
    assert(!old.sent.some(m => m.query === 'logs'), 'the query went to the old carrier');
    assert(!document.getElementById('dashboard-signin'), 'rotation asked the initiating page to sign in again');
  }
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`)
			})
		}
	}
}
