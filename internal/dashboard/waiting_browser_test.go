//go:build !windows

package dashboard

import (
	"testing"
)

// notificationStub stands in for the browser's Notification: it records
// what is made, and requestPermission answers what the test sets.
const notificationStub = `
const made = [];
class StubNotification {
  constructor(title, options) { this.title = title; this.options = options || {}; made.push(this); }
  close() { this.closed = true; }
}
StubNotification.permission = 'default';
StubNotification.asked = 0;
StubNotification.answer = 'granted';
StubNotification.requestPermission = async () => { StubNotification.asked++; StubNotification.permission = StubNotification.answer; return StubNotification.answer; };
window.Notification = StubNotification;
try { localStorage.removeItem('aii.notify.asks'); } catch (e) {}
`

func TestTheWaitingSignalInBrowser(t *testing.T) {
	page := `<!doctype html>
<title>AII OS</title>
<script type="module">
import { run } from './__harness.js';
import { S } from './state.js';
` + notificationStub + `
const W = await import('./waiting.js');
run(async (assert) => {
  S.identityExists = true; S.stats = { name: 'Nova' }; S.view = 'chat';
  const take = asks => { S.asks = asks; W.acceptAsks(asks); };
  const q = (id, text) => ({ id, kind: 'clarify', text: text || 'Which flight, the early one or the late one?' });

  // The title counts the list; nothing is notified with the switch off.
  take([q('a0')]);
  assert(document.title === '(1) Nova — AII OS', 'one open at load: ' + document.title);
  take([q('a0'), q('a1')]);
  assert(document.title === '(2) Nova — AII OS', 'two open: ' + document.title);
  take([q('a1')]);
  assert(document.title === '(1) Nova — AII OS', 'one answered: ' + document.title);
  take([]);
  assert(document.title === 'Nova — AII OS', 'both answered: ' + document.title);
  take([q('a1b')]);
  assert(made.length === 0, 'a notification with the switch off: ' + made.length);
  assert(W.notifyState() === 'off', 'the switch reads ' + W.notifyState());
  take([]);

  // On, from the click: the browser is asked once, and then one
  // notification per new ask, tagged by it, with none of its words.
  assert(await W.turnNotify(true) === 'on' && StubNotification.asked === 1, 'turning on did not ask the browser once');
  take([q('a2', 'Shall I book the 7:05 to Lisbon?')]);
  assert(made.length === 1, 'one new ask made ' + made.length);
  const n = made[0];
  assert(n.title === 'Nova is waiting on you' && n.options.body === 'A question is open in Chat.', 'the words: ' + n.title + ' / ' + n.options.body);
  assert(n.options.tag === 'aii-ask-a2', 'the tag: ' + n.options.tag);
  assert(!JSON.stringify({ t: n.title, o: n.options }).includes('Lisbon'), 'the question’s words are in the notification');
  take([q('a2'), q('a0')]);
  assert(made.length === 1, 'an ask seen before, open at load, was notified when it came back');
  take([q('a2'), q('a3')]);
  assert(made.length === 2 && made[1].options.tag === 'aii-ask-a3', 'the next new ask: ' + made.length);
  let went = '';
  S.go = v => { went = v; };
  made[1].onclick();
  assert(went === 'chat' && made[1].closed, 'clicking it did not open Chat and close it');

  // Off again: nothing more.
  assert(await W.turnNotify(false) === 'off', 'turning off');
  take([q('a4')]);
  assert(made.length === 2, 'a notification with the switch turned off');

  // Blocked: the switch says so and asks nothing.
  StubNotification.permission = 'denied';
  const asked = StubNotification.asked;
  assert(await W.turnNotify(true) === 'blocked' && StubNotification.asked === asked, 'a blocked page asked the browser again');
  assert(W.notifySaid('blocked') === 'This browser has blocked notifications for this page. Allow them in the browser’s site settings, then turn this on again.', 'the blocked sentence');
  take([q('a5')]);
  assert(made.length === 2, 'a blocked page made a notification');
  // A refusal at the browser's question leaves it off and says blocked.
  StubNotification.permission = 'default'; StubNotification.answer = 'denied';
  assert(await W.turnNotify(true) === 'blocked', 'a refused question left the switch on');

  // Not available: no Notification, no secure context, a phone's browser.
  const mac = { userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)' };
  assert(W.notifyAvailability({ Notification: undefined, isSecureContext: true, navigator: mac }) === 'unavailable', 'no Notification');
  assert(W.notifyAvailability({ Notification: StubNotification, isSecureContext: false, navigator: mac }) === 'unavailable', 'plain HTTP on the LAN');
  assert(W.notifyAvailability({ Notification: undefined, isSecureContext: true, navigator: { userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X)' } }) === 'unavailable', 'an iPhone, whose Safari has no Notification');
  assert(W.notifyAvailability({ Notification: StubNotification, isSecureContext: true, navigator: { userAgent: 'x', userAgentData: { mobile: true } } }) === 'unavailable', 'a mobile client hint (Chrome on a phone)');
  assert(W.notifyAvailability({ Notification: StubNotification, isSecureContext: true, navigator: mac }) === 'yes', 'a desktop over a secure address');
  assert(W.notifySaid('unavailable') === 'This browser cannot show this page’s notifications here; the page’s title counts the open questions.', 'the not-available sentence: ' + W.notifySaid('unavailable'));
  assert(W.notifySaid('off') === '' && W.notifySaid('on') === '', 'the other sentences');

  // The project view's title goes through the same composer.
  take([q('a6')]);
  S.view = 'projects'; W.titlePlace('Proj X');
  assert(document.title === '(1) Proj X — AII OS', 'the project title: ' + document.title);
  S.view = 'chat'; W.renderTitle();
  assert(document.title === '(1) Nova — AII OS', 'leaving the project: ' + document.title);
  S.identityExists = false; take([]);
  assert(document.title === 'AII OS', 'no identity, nothing open: ' + document.title);
});
</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/state.js": []byte(`export const S = { asks: [], view: 'chat', stats: null, identityExists: false };`),
	})
}

// THE COUNT IS RIGHT AFTER A RECONNECT. The real ws.js: an ask is open at
// load; the socket drops; a second ask arrives while it is down; the page
// reconnects, asks for the list again as every connection does, and the
// title counts both. With the switch on, the ask that came while the socket
// was down is notified once, and the one open at load never.
func TestTheTitleCountIsRightAfterAReconnectInBrowser(t *testing.T) {
	page := `<!doctype html><title>AII OS</title><button id="send-btn" disabled></button><button id="mic" disabled></button>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
` + notificationStub + `
StubNotification.permission = 'granted';
localStorage.setItem('aii.notify.asks', 'on');
const timers = [];
let nextTimer = 1;
window.setTimeout = (fn, delay) => { const x = { id: nextTimer++, fn, delay }; timers.push(x); return x.id; };
window.clearTimeout = id => { const i = timers.findIndex(x => x.id === id); if (i >= 0) timers.splice(i, 1); };
const sockets = [];
class Socket {
  constructor(url) { this.url = url; this.readyState = 0; this.sent = []; sockets.push(this); }
  send(m) { this.sent.push(JSON.parse(m)); }
  open() { this.readyState = 1; this.onopen?.({}); }
  fail() { this.readyState = 3; this.onclose?.({}); }
  close() { this.readyState = 3; }
  recv(m) { this.onmessage?.({ data: JSON.stringify(m) }); }
}
window.WebSocket = Socket;
const ws = await import('/ws.js');
run((assert) => {
  S.identityExists = true; S.view = 'chat'; S.stats = { name: 'Nova' };
  const asksQueried = s => s.sent.some(x => x.type === 'query' && x.query === 'asks');
  ws.connect(); sockets[0].open();
  assert(asksQueried(sockets[0]), 'the first connection did not ask for the list');
  sockets[0].recv({ type: 'asks', asks: [{ id: 'a1', kind: 'clarify', text: 'one' }] });
  assert(document.title === '(1) Nova — AII OS', 'at load: ' + document.title);
  assert(made.length === 0, 'an ask open at load was notified');

  // Dropped. The host's push of the second ask goes nowhere.
  sockets[0].fail();
  assert(document.title === '(1) Nova — AII OS', 'the drop moved the count: ' + document.title);
  const retry = timers.shift();
  assert(retry, 'no reconnect was scheduled');
  retry.fn();
  assert(sockets.length === 2, 'the reconnect made no socket');
  sockets[1].open();
  assert(asksQueried(sockets[1]), 'the reconnect did not ask for the list again');
  sockets[1].recv({ type: 'asks', asks: [{ id: 'a1', kind: 'clarify', text: 'one' }, { id: 'a2', kind: 'clarify', text: 'two' }] });
  assert(document.title === '(2) Nova — AII OS', 'after the reconnect: ' + document.title);
  assert(made.length === 1 && made[0].options.tag === 'aii-ask-a2', 'the ask that came while down: ' + made.map(n => n.options.tag).join(','));

  // Both answered: the plain title.
  sockets[1].recv({ type: 'asks', asks: [] });
  assert(document.title === 'Nova — AII OS', 'both answered: ' + document.title);
});
</script>`
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false, asks: [] };`)
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, page, wsPageModules(wsJS, stateJS))
}
