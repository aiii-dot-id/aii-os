//go:build !windows

package dashboard

import "testing"

// A REFUSED READ LEAVES THE PAGE'S TURN RUNNING. The page takes a refusal that
// names no request as the turn's own, and ends the turn it shows (ws.js,
// onError). A read the host cannot answer is refused, and that refusal names
// the read it refuses, so a turn under way stays under way: Stop stays Stop.
// Here the host has no spend to give, so the read is refused. The host answers
// one socket's frames in the order they were sent (server.go, the read loop),
// so the status read sent after the spend read is answered after it: when that
// answer is heard, so is the spend read's refusal. Nothing here waits a fixed time;
// the reads go only once the page's socket is open and has answered.
const refusedReadKeepsTheTurnDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
const refusals = [];
let after = null;
const realSend = WebSocket.prototype.send;
let watched = null;
WebSocket.prototype.send = function (data) {
  if (watched !== this) {
    watched = this;
    this.addEventListener('message', ev => {
      try {
        const m = JSON.parse(ev.data);
        if (m.type === 'error') refusals.push({ request_id: m.request_id || '', message: m.message || '' });
        if (m.type === 'status' && m.request_id === 'after-the-spend-read') after = m;
      } catch (_) {}
    });
  }
  return realSend.call(this, data);
};
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  const { S } = await import('/state.js');
  const { setThinking } = await import('/presence.js');
  const { query } = await import('/ws.js');
  // The page's socket is open and has answered: a read asked before that is not
  // sent at all (ws.js, send, needs an open socket).
  await until(() => S.connected && S.pageFrames && S.pageFrames.status);
  if (!(S.connected && S.pageFrames && S.pageFrames.status)) return report('FAIL: rig: the page never connected');
  setThinking(true);
  const from = refusals.length;
  const sent = [query('spend', { days: 1, request_id: 'spend-during-a-turn' }), query('status', { request_id: 'after-the-spend-read' })];
  if (sent[0] !== 'spend-during-a-turn' || sent[1] !== 'after-the-spend-read') return report('FAIL: rig: the reads were not sent: ' + JSON.stringify(sent));
  await until(() => after);
  if (!after) return report('FAIL: rig: the status read sent after the spend read was not answered');
  const heard = refusals.slice(from);
  const named = heard.some(r => r.request_id === 'spend-during-a-turn');
  const running = !!S.thinking;
  if (!named || !running) {
    return report('FAIL: ' + [
      named ? '' : 'no refusal named the spend read (refusals heard since it was sent: ' + JSON.stringify(heard) + ')',
      running ? '' : 'the page\'s turn was ended: S.thinking is false',
    ].filter(Boolean).join('; '));
  }
  setThinking(false);
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestARefusedReadLeavesThePagesTurnRunning(t *testing.T) {
	pressed := 0
	runDashboardInEachEngine(t, func(t *testing.T) *Server {
		return New("127.0.0.1", 0, statusSectionHandler(&pressed))
	}, "/", refusedReadKeepsTheTurnDriverJS)
}
