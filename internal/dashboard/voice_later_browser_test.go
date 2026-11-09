//go:build !windows

package dashboard

import "testing"

// THE VOICE IS FETCHED ONCE HOME IS DRAWN, AND HEARS WHAT WAITED (step
// 22). The shipped page, with a voice.js that records what reaches it
// and is held from arriving until the page under test lets it. Home is drawn
// without it. A reply, the host's session state, an event and a hush handed
// to the voice before it is in reach it when it arrives, in the order they
// came, after it is given the socket, wired to its controls and drawn. A
// frame of sound before then is let go; one after reaches it. A connection
// lost before it arrives lets go of everything that waited.
const voiceHeldJS = `window.__voiceCalls = [];
await new Promise(r => { window.__releaseVoice = r; });
const rec = name => (...args) => { window.__voiceCalls.push([name, ...args]); };
export function bindTransport(frame, json) {
  window.__voiceCalls.push(['bindTransport', typeof frame, typeof json]);
  return { receiveFrame: rec('receiveFrame'), sessionState: rec('sessionState'), voiceEvent: rec('voiceEvent'), hushFromHost: rec('hushFromHost') };
}
export const wireMic = rec('wireMic');
export const render = rec('render');
export const speak = rec('speak');
export const connectionLost = rec('connectionLost');
`

func voiceLaterJS(lose bool) string {
	lost := "false"
	if lose {
		lost = "true"
	}
	return `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const calls = () => (window.__voiceCalls || []).map(c => c[0]);
const lose = ` + lost + `;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  await until(() => S.view === 'home', 4000);
  await until(() => typeof window.__releaseVoice === 'function', 4000);
  check(document.getElementById('msg-input'), 'Home was not drawn while the voice was on its way');
  check(calls().length === 0, 'the voice was reached before it arrived: ' + calls().join());
  sockets[0].say({ type: 'response', role: 'identity', message: 'First words.', voice_reply: true });
  sockets[0].onmessage({ data: new ArrayBuffer(8) });
  sockets[0].say({ type: 'voice_session', voice_session: { state: 'listening' } });
  sockets[0].say({ type: 'voice_event', voice_event: { type: 'engine_state' } });
  sockets[0].say({ type: 'voice_hush', voice_hush: { stream: 's1' } });
  if (lose) sockets[0].close();
  window.__releaseVoice();
  // What waited is heard in the same step that draws the voice, so its
  // drawing is the sign that everything due has reached it.
  await until(() => calls().includes('render'), 4000);
  const first = calls().slice(0, 7);
  if (lose) {
    check(first.slice(0, 3).join() === 'bindTransport,wireMic,render', 'the voice was not given the socket, wired and drawn as it arrived: ' + first.join());
    check(!calls().some(n => ['speak', 'sessionState', 'voiceEvent', 'hushFromHost', 'receiveFrame'].includes(n)), 'what waited for a lost connection reached the voice: ' + calls().join());
  } else {
    check(first.join() === 'bindTransport,wireMic,render,speak,sessionState,voiceEvent,hushFromHost', 'what waited did not reach the voice, in order, after it was given the socket, wired and drawn: ' + calls().join());
    const said = window.__voiceCalls.find(c => c[0] === 'speak');
    check(said && said[1] === 'First words.' && said[2] === true, 'the reply that waited was not the one spoken: ' + JSON.stringify(said));
    check(window.__voiceCalls[0][1] === 'function' && window.__voiceCalls[0][2] === 'function', 'the voice was not given the socket\'s two senders');
    check(!calls().includes('receiveFrame'), 'a frame of sound from before the voice arrived reached it');
    sockets[0].onmessage({ data: new ArrayBuffer(8) });
    check(calls().includes('receiveFrame'), 'a frame of sound after the voice arrived did not reach it');
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`
}

func TestTheVoiceIsFetchedOnceHomeIsDrawnAndHearsWhatWaited(t *testing.T) {
	served := map[string][]byte{"/voice.js": []byte(voiceHeldJS)}
	t.Run("what waited, in order", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, voiceLaterJS(false), homeStyles, served)
	})
	t.Run("a lost connection lets it go", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, voiceLaterJS(true), homeStyles, served)
	})
}
