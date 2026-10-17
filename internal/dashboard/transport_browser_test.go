//go:build !windows

package dashboard

import "testing"

func TestThePageSettlesWhatCarriesItsSession(t *testing.T) {
	runTokenPromptProbe(t, `
import { report } from '/__harness.js';
try {
  const assert = (ok, why) => { if (!ok) throw Error(why); };
  const tick = ms => new Promise(resolve => setTimeout(resolve, ms || 0));
  const { channelWanted, cutIntoPieces, pieceJoiner, settle } = await import('/ws.js');

  assert(channelWanted('https:', 'abc.id.example.test', true), 'a page on a name over HTTPS does not ask for a channel');
  assert(!channelWanted('https:', '127.0.0.1', true) && !channelWanted('https:', 'localhost', true) && !channelWanted('http:', 'abc.id.example.test', true) && !channelWanted('https:', 'abc.id.example.test', false),
    'a page on the machine itself, in the clear, or with no peer connection asks for a channel');

  const round = (data, size) => { const join = pieceJoiner(); let whole = null; const pieces = cutIntoPieces(data, size); for (const p of pieces) { assert(p.length >= 1 && p.length <= size, 'a piece is larger than its channel takes'); assert(whole === null, 'a message was whole before its last piece'); whole = join(p.buffer); } return [pieces.length, whole]; };
  let [n, whole] = round('', 64); assert(n === 1 && whole.data === '', 'an empty message is not one piece that is its own last');
  [n, whole] = round('x'.repeat(63), 64); assert(n === 1 && whole.data.length === 63, 'a message that fits one piece was cut');
  [n, whole] = round('café '.repeat(500), 64); assert(n > 1 && whole.data === 'café '.repeat(500), 'a long text message did not arrive as it was sent');
  const bytes = new Uint8Array(100000).map((_, i) => i % 251);
  [n, whole] = round(bytes.buffer, 16384); assert(n === 7 && whole.data instanceof ArrayBuffer && new Uint8Array(whole.data).every((b, i) => b === i % 251), 'a binary message did not arrive as it was sent');
  const join = pieceJoiner(); assert(join(new Uint8Array(0).buffer) === false, 'a piece with no header was taken');
  assert(join(new Uint8Array([0, 65]).buffer) === null && join(new Uint8Array([3, 66]).buffer) === false, 'a message that changed kind part way was taken');

  const fakeSocket = () => ({ readyState: 1, sent: [], send(m) { this.sent.push(JSON.parse(m)); }, close() { this.readyState = 3; this.closedByPage = true; } });
  const fakePeer = () => {
    const dc = { readyState: 'connecting', bufferedAmount: 0, sent: [], send(p) { this.sent.push(p); }, close() { this.readyState = 'closed'; } };
    const pc = { connectionState: 'new', sctp: { maxMessageSize: 262144 }, dc, config: null, closed: false,
      createDataChannel(label, options) { pc.label = label; pc.options = options; return dc; },
      createOffer() { return Promise.resolve({ type: 'offer', sdp: 'v=0 fixture offer' }); },
      setLocalDescription(d) { pc.local = d; return Promise.resolve(); },
      setRemoteDescription(d) { pc.remote = d; return Promise.resolve(); },
      addIceCandidate(c) { (pc.candidates = pc.candidates || []).push(c); return Promise.resolve(); },
      close() { pc.closed = true; pc.connectionState = 'closed'; } };
    return pc;
  };
  const text = m => ({ data: JSON.stringify(m) });
  const rig = () => {
    const r = { socket: fakeSocket(), adopted: [], seen: [], ended: 0, lost: 0, pc: null };
    const adopt = c => { r.adopted.push(c); c.onmessage = e => r.seen.push(e.data); c.onclose = () => r.ended++; };
    r.pending = settle(r.socket, adopt, () => r.lost++, config => { r.pc = fakePeer(); r.pc.config = config; return r.pc; });
    return r;
  };

  let r = rig();
  assert(r.pending.readyState === 0 && r.socket.sent.length === 0, 'the page sent something, or held an open carrier, before the identity spoke');
  r.socket.onmessage(text({ type: 'status', stats: {} }));
  assert(r.adopted.length === 1 && r.adopted[0] === r.socket && r.seen.length === 1 && JSON.parse(r.seen[0]).type === 'status' && !r.pc, 'an identity that offers no channel: the socket was not made the carrier with its first message kept: ' + r.seen);

  r = rig();
  r.socket.onmessage(text({ type: 'transport_setup', ready_within_ms: 4000, ice_servers: [{ urls: ['stun:turn.example.test:3478'] }, { urls: ['turn:turn.example.test:3478'], username: 'u', credential: 'c' }] }));
  await tick(); await tick();
  assert(r.pc && r.pc.label === 'dashboard' && r.pc.options.ordered === true && r.pc.config.iceServers.length === 2 && r.pc.config.iceServers[1].credential === 'c', 'the peer connection was not made with the servers the identity named, or its channel is not the ordered one called dashboard');
  assert(r.socket.sent.length === 1 && r.socket.sent[0].type === 'transport_offer' && r.socket.sent[0].sdp === 'v=0 fixture offer', 'the page did not send its offer, or sent something else: ' + JSON.stringify(r.socket.sent));
  r.pc.onicecandidate({ candidate: { candidate: 'candidate:1 1 udp 1 192.0.2.7 9 typ host', sdpMid: '0', sdpMLineIndex: 0 } });
  r.pc.onicecandidate({ candidate: null });
  assert(r.socket.sent[1].type === 'transport_candidate' && r.socket.sent[1].sdp_mid === '0' && r.socket.sent[2].type === 'transport_candidates_done', 'the page did not pass on its candidates');
  r.socket.onmessage(text({ type: 'transport_answer', sdp: 'v=0 fixture answer' }));
  r.socket.onmessage(text({ type: 'transport_candidate', candidate: 'candidate:2 1 udp 1 192.0.2.8 9 typ host', sdp_mid: '0', sdp_mline_index: 0 }));
  await tick();
  assert(r.pc.remote.sdp === 'v=0 fixture answer' && r.pc.candidates.length === 1 && r.adopted.length === 0, 'the answer and the identity\'s candidate were not given to the peer connection, or a carrier was adopted before ready');
  r.socket.onmessage(text({ type: 'transport_ready', carrier: 'socket' }));
  assert(r.adopted.length === 1 && r.adopted[0] === r.socket && r.pc.closed && r.lost === 0, 'ready on the socket did not make the socket the carrier and end the attempt');

  r = rig();
  r.socket.onmessage(text({ type: 'transport_setup', ready_within_ms: 4000 }));
  await tick();
  let remoteReady;
  r.pc.setRemoteDescription = d => new Promise(resolve => { remoteReady = () => { r.pc.remote = d; resolve(); }; });
  r.pc.addIceCandidate = c => {
    assert(r.pc.remote, 'candidate applied before its answer');
    (r.pc.candidates = r.pc.candidates || []).push(c);
    return c.candidate === 'invalid' ? Promise.reject(Error('invalid candidate')) : Promise.resolve();
  };
  const candidate = value => text({ type: 'transport_candidate', candidate: value, sdp_mid: '0', sdp_mline_index: 0 });
  r.socket.onmessage(candidate('early'));
  r.socket.onmessage(text({ type: 'transport_answer', sdp: 'delayed answer' }));
  r.socket.onmessage(candidate('invalid'));
  r.socket.onmessage(candidate('while applying answer'));
  assert(!r.pc.candidates, 'early candidate was applied before the answer completed');
  remoteReady(); await tick();
  assert(r.pc.candidates.map(c => c.candidate).join('|') === 'early|invalid|while applying answer', 'early candidates were lost or a bad candidate blocked later paths');
  r.socket.onmessage(candidate('later'));
  assert(r.pc.candidates.length === 4, 'candidate after the answer was lost');
  r.pending.close();

  r = rig();
  r.socket.onmessage(text({ type: 'transport_setup', ready_within_ms: 4000 }));
  r.pc.setLocalDescription = d => {
    r.pc.local = d;
    r.pc.onicecandidate({ candidate: { candidate: 'early local', sdpMid: '0', sdpMLineIndex: 0 } });
    r.pc.onicecandidate({ candidate: null });
    return Promise.resolve();
  };
  await tick();
  assert(r.socket.sent.map(m => m.type).join('|') === 'transport_offer|transport_candidate|transport_candidates_done', 'local candidate preceded the offer or was lost');
  r.pending.close();

  r = rig();
  r.socket.onmessage(text({ type: 'transport_setup', ready_within_ms: 4000 }));
  await tick();
  r.pc.setRemoteDescription = () => new Promise(resolve => { remoteReady = resolve; });
  r.socket.onmessage(candidate('queued before fallback'));
  r.socket.onmessage(text({ type: 'transport_answer', sdp: 'late answer' }));
  r.socket.onmessage(text({ type: 'transport_ready', carrier: 'socket' }));
  remoteReady(); await tick();
  assert(!r.pc.candidates && r.adopted.length === 1 && r.adopted[0] === r.socket, 'late answer changed the settled carrier');

  r = rig();
  r.socket.onmessage(text({ type: 'transport_setup', ready_within_ms: 4000 }));
  await tick(); await tick();
  r.socket.onclose({});
  assert(r.lost === 0 && r.adopted.length === 0, 'the socket closing during an attempt was read as the session lost');
  r.pc.dc.readyState = 'open'; r.pc.dc.onopen({});
  for (const p of cutIntoPieces(JSON.stringify({ type: 'transport_ready', carrier: 'channel' }), 64)) r.pc.dc.onmessage({ data: p.buffer });
  assert(r.adopted.length === 1 && r.adopted[0] !== r.socket && r.adopted[0].readyState === 1, 'ready on the channel did not make the channel the carrier');
  const channel = r.adopted[0];
  channel.send(JSON.stringify({ type: 'probe' })); channel.send(new Uint8Array([1, 2, 3]).buffer);
  assert(r.pc.dc.sent.length === 2 && r.pc.dc.sent[0][0] === 2 && r.pc.dc.sent[1][0] === 3 && r.pc.dc.sent[1].length === 4, 'what the page sent on the channel was not cut into headed pieces');
  for (const p of cutIntoPieces('a reply', 64)) r.pc.dc.onmessage({ data: p.buffer });
  assert(r.seen.join() === 'a reply', 'a message on the channel did not reach the session');
  r.pc.connectionState = 'failed'; r.pc.onconnectionstatechange();
  r.pc.dc.onclose({});
  assert(r.ended === 1 && channel.readyState === 3, 'the path failing did not end the carrier exactly once: ' + r.ended);

  r = rig();
  r.socket.onmessage(text({ type: 'transport_setup', ready_within_ms: 60 }));
  await tick(200);
  assert(r.lost === 1 && r.adopted.length === 0 && r.pc.closed && r.socket.closedByPage, 'with no ready in the stated time the page did not give up on both, once: lost ' + r.lost);
  r.socket.onmessage(text({ type: 'transport_ready', carrier: 'socket' }));
  assert(r.adopted.length === 0, 'a ready that came after the page gave up was acted on');
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`)
}
