//go:build !windows

// .
// .

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const voiceListenOffPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, abortDuplex, residentActive, converseForTest, render, decodeStreamFrame } from './voice.js';
import { S } from './state.js';

run(async () => {
  const frames = [], msgs = [];
  const host = bindTransport(b => frames.push(b), m => { msgs.push(m); return 'r' + msgs.length; });
  const ends = () => frames.filter(b => { const f = decodeStreamFrame(b); return f && f.kind === 3; }).length;
  const acted = a => msgs.filter(m => m.voice && m.voice.action === a).length;
  // A real microphone: a generated MediaStream with a real audio track, so
  // stopping it is observable the way stopping hardware is.
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const dest = ac.createMediaStreamDestination();
  const track = dest.stream.getAudioTracks()[0];
  let opened = 0;
  navigator.mediaDevices.getUserMedia = async () => { opened++; return dest.stream; };
  const show = (listen, speak, rev) => {
    S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };

  try {
    show('interactive', 'on', 1);
    converseForTest('waiting');
    await startDuplex();
    assert(residentActive(), 'the conversation did not start through the production entry point');
    host.sessionState({ state: 'open', session_id: 'vs-live' });
    assert(track.readyState === 'live', 'the microphone is not live while the identity listens: ' + track.readyState);
    const asked = opened;

    // THE IDENTITY, SETTINGS OR ANOTHER BROWSER TURNS LISTENING OFF.
    show('off', 'off', 2);
    await until(() => track.readyState === 'ended', 'listen=off left the microphone live: ' + track.readyState);
    assert(residentActive(), 'off ended the conversation — the reply for what was already heard is still owed');
    assert(acted('abort') === 0 && acted('close') === 0, 'off discarded the session instead of finishing its input: ' + JSON.stringify(msgs));
    await until(() => ends() === 1, 'the input was never finished: ' + ends() + ' END frames');

    // OWED ONCE. The next status frame carries the same off and says
    // nothing new.
    show('off', 'off', 3);
    await tick(); await tick();
    assert(ends() === 1 && acted('abort') === 0, 'a second off frame finished the input again: ' + ends() + ' END frames, ' + acted('abort') + ' aborts');

    // A STATUS FRAME NEVER OPENS A MICROPHONE, in either listening mode.
    abortDuplex();
    show('interactive', 'on', 4);
    await tick(); await tick();
    show('meeting', 'off', 5);
    await tick(); await tick();
    assert(opened === asked, 'a status frame asked for a microphone: ' + opened + ' opens, ' + asked + ' before');
    assert(!residentActive(), 'a status frame started a conversation');
  } finally { abortDuplex(); track.stop(); await ac.close(); }
});
</script>`

func TestListeningTurnedOffAnywhereFinishesTheConversationAndStopsThisPagesMicrophone(t *testing.T) {
	runPageInEngines(t, voiceListenOffPage, micModules)
}

// .
// .
// .
// .
// .
const voiceListenOffAfterOwnFinishPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, abortDuplex, residentActive, converseForTest, render, wireMic, decodeStreamFrame } from './voice.js';
import { S } from './state.js';

run(async () => {
  const frames = [], msgs = [];
  const host = bindTransport(b => frames.push(b), m => { msgs.push(m); return 'r' + msgs.length; });
  const ends = () => frames.filter(b => { const f = decodeStreamFrame(b); return f && f.kind === 3; }).length;
  const acted = a => msgs.filter(m => m.voice && m.voice.action === a).length;
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const dest = ac.createMediaStreamDestination();
  const track = dest.stream.getAudioTracks()[0];
  navigator.mediaDevices.getUserMedia = async () => dest.stream;
  const show = (listen, speak, rev) => {
    S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };

  try {
    show('interactive', 'on', 1);
    wireMic();
    converseForTest('waiting');
    await startDuplex();
    host.sessionState({ state: 'open', session_id: 'vs-live' });
    assert(track.readyState === 'live', 'the microphone is not live while the identity listens: ' + track.readyState);

    // THE TAP: finish listening, and ask the host for earbuds.
    document.getElementById('converse').click();
    await until(() => track.readyState === 'ended', 'the tap left the microphone open: ' + track.readyState);
    await until(() => ends() === 1, 'the tap did not finish the input: ' + ends() + ' END frames');
    assert(residentActive() && acted('abort') === 0, 'the tap ended the conversation instead of finishing its input: ' + JSON.stringify(msgs));
    assert(msgs.some(m => m.type === 'config_set' && m.config['speech.mode'].listen === 'off' && m.config['speech.mode'].speak === 'on'),
      'the tap did not ask for earbuds: ' + JSON.stringify(msgs));
    const said = msgs.length;

    // THE HOST'S ANSWER TO THAT TAP arrives: listening off, replies
    // spoken. Nothing further is owed on this page.
    show('off', 'on', 2);
    await tick(); await tick(); await tick();
    assert(ends() === 1, 'the off frame named a second cutoff: ' + ends() + ' END frames');
    assert(acted('abort') === 0, 'the off frame aborted a session that still owes a reply: ' + JSON.stringify(msgs));
    assert(msgs.length === said, 'the off frame sent something new: ' + JSON.stringify(msgs.slice(said)));
    assert(residentActive(), 'the off frame ended the conversation that was draining');
  } finally { abortDuplex(); track.stop(); await ac.close(); }
});
</script>`

func TestTheOperatorsOwnFinishIsNotPaidAgainByTheVoiceModeFrameThatFollows(t *testing.T) {
	runPageInEngines(t, voiceListenOffAfterOwnFinishPage, micModules)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const voiceListenOffCloudCapturePage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, render, wireMic } from './voice.js';
import { S } from './state.js';

run(async () => {
  const frames = [], msgs = [];
  bindTransport(f => frames.push(new Uint8Array(f)), m => { msgs.push(m); return 'r' + msgs.length; });
  const realAudio = window.AudioContext || window.webkitAudioContext;
  const ac = new realAudio();
  const tracks = [];
  navigator.mediaDevices.getUserMedia = async () => {
    const d = ac.createMediaStreamDestination();
    tracks.push(d.stream.getAudioTracks()[0]);
    return d.stream;
  };
  let node = null;
  window.AudioContext = class {
    constructor() { this.sampleRate = 16000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { node = { connect() {}, disconnect() {}, onaudioprocess: null }; return node; }
    close() {}
  };
  const show = (listen, speak, rev) => {
    S.stats = { voice_state: 'cloud', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };
  const mic = document.getElementById('mic');
  const press = type => mic.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true }));
  const talk = () => node.onaudioprocess({ inputBuffer: { getChannelData: () => new Float32Array(4800).fill(0.25) } });

  try {
    show('interactive', 'on', 1);
    wireMic(); render();

    // A LATCHED CAPTURE: a tap turned listening on and nothing is holding it.
    node = null;
    press('pointerdown'); press('pointerup');
    await until(() => node && node.onaudioprocess, 'the tap never opened the microphone');
    talk();
    assert(mic.classList.contains('live'), 'the tap did not latch: ' + mic.outerHTML);
    assert(tracks.length === 1 && tracks[0].readyState === 'live', 'the latched capture has no live device');

    // Listening turned off elsewhere: what was heard goes, the device is released.
    show('off', 'off', 2);
    await until(() => frames.length === 1, 'the latched capture was dropped instead of sent');
    assert(frames[0][2] === 1, 'what was heard was not sent as a conversation (mode byte ' + frames[0][2] + ')');
    await until(() => tracks[0].readyState === 'ended', 'the latched device stayed open under off: ' + tracks[0].readyState);
    assert(!mic.classList.contains('live'), 'the latch is still on after listening stopped: ' + mic.outerHTML);

    // AN EXPLICIT HOLD, and listening turned off in the middle of it.
    show('interactive', 'on', 3);
    node = null;
    press('pointerdown');
    await until(() => node && node.onaudioprocess, 'the hold never opened the microphone');
    talk();
    assert(tracks.length === 2, 'the hold did not open a device of its own');
    show('off', 'off', 4);
    await new Promise(r => setTimeout(r, 450)); // past the latch time: this is a hold
    assert(tracks[1].readyState === 'live', 'the off frame took the microphone out of the operator\'s hand: ' + tracks[1].readyState);
    assert(frames.length === 1, 'the off frame sent the hold before the operator released it');
    assert(mic.classList.contains('live'), 'the off frame stopped a hold in progress: ' + mic.outerHTML);
    press('pointerup');
    await until(() => frames.length === 2, 'the release did not send what was said');
    await until(() => tracks[1].readyState === 'ended', 'the release left the device open: ' + tracks[1].readyState);
  } finally { tracks.forEach(t => t.stop()); window.AudioContext = realAudio; await ac.close(); }
});
</script>`

func TestListeningTurnedOffSendsALatchedCaptureAndLeavesAHoldAlone(t *testing.T) {
	runPageInEngines(t, voiceListenOffCloudCapturePage, micModules)
}

// .
// .
// .
// .
// .
// .
// .
const voiceLateMicrophonePage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, abortDuplex, residentActive, converseForTest, render } from './voice.js';
import { S } from './state.js';

run(async () => {
  const frames = [], msgs = [];
  bindTransport(b => frames.push(b), m => { msgs.push(m); return 'r' + msgs.length; });
  const opens = () => msgs.filter(m => m.voice && m.voice.action === 'open').length;
  const realAudio = window.AudioContext || window.webkitAudioContext;
  const ac = new realAudio();
  const tracks = [];
  let releaseDevice = null, releaseGraph = null;
  navigator.mediaDevices.getUserMedia = () => new Promise(r => {
    releaseDevice = () => { const d = ac.createMediaStreamDestination(); tracks.push(d.stream.getAudioTracks()[0]); r(d.stream); };
  });
  // The capture graph's module load is held open the same way, so the
  // second await can be answered under a mode that moved beneath it.
  window.AudioContext = class {
    constructor() { this.sampleRate = 16000; this.destination = {}; this.currentTime = 0; this.state = 'running';
      this.audioWorklet = { addModule: () => new Promise(r => { releaseGraph = r; }) }; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { return { connect() {}, disconnect() {}, onaudioprocess: null }; }
    close() {}
  };
  const show = (listen, speak, rev) => {
    S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };
  const converse = document.getElementById('converse');

  try {
    // 1. THE PROMPT ANSWERED LATE: listening stopped while it was up.
    show('interactive', 'on', 1);
    converseForTest('waiting');
    startDuplex();
    await until(() => releaseDevice !== null, 'the microphone was never asked for');
    show('off', 'off', 2);
    releaseDevice();
    // The device itself is the witness, polled — never the promise: a page
    // that keeps the microphone goes on to wait for its capture graph, and
    // awaiting it there would hang instead of naming what went wrong.
    await until(() => tracks.length === 1 && tracks[0].readyState === 'ended',
      'a device that opened under listen=off stayed open: ' + tracks.map(t => t.readyState).join());
    assert(!residentActive() && opens() === 0, 'a session was opened under listen=off: ' + JSON.stringify(msgs));
    assert(frames.length === 0, 'audio was streamed under listen=off');
    assert(!converse.classList.contains('waiting'), 'the control still waits for a conversation that will not open: ' + converse.outerHTML);

    // 2. THE CAPTURE GRAPH LOADED LATE: the device was already open, and
    //    it is released too — nothing streams a sample.
    releaseDevice = null; releaseGraph = null;
    show('interactive', 'on', 3);
    converseForTest('waiting');
    startDuplex();
    await until(() => releaseDevice !== null, 'the second open never asked for a microphone');
    releaseDevice();
    await until(() => releaseGraph !== null, 'the capture graph was never loaded');
    show('off', 'off', 4);
    releaseGraph();
    await until(() => tracks.length === 2 && tracks[1].readyState === 'ended',
      'the device kept listening after the graph loaded under off: ' + tracks.map(t => t.readyState).join());
    assert(!residentActive() && opens() === 0, 'a session was opened under listen=off: ' + JSON.stringify(msgs));
    assert(frames.length === 0, 'audio was streamed under listen=off');
  } finally { abortDuplex(); tracks.forEach(t => t.stop()); window.AudioContext = realAudio; await ac.close(); }
});
</script>`

func TestAMicrophoneThatOpenedAfterListeningStoppedIsReleased(t *testing.T) {
	runPageInEngines(t, voiceLateMicrophonePage, micModules)
}

// .
// .
// .
// .
// .
// .
// .
const voiceMeetingTapPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, abortDuplex, residentActive, converseForTest, render, wireMic } from './voice.js';
import { S } from './state.js';

run(async () => {
  const frames = [], msgs = [];
  const host = bindTransport(b => frames.push(b), m => { msgs.push(m); return 'r' + msgs.length; });
  const opens = () => msgs.filter(m => m.voice && m.voice.action === 'open');
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const tracks = [];
  navigator.mediaDevices.getUserMedia = async () => {
    const d = ac.createMediaStreamDestination();
    tracks.push(d.stream.getAudioTracks()[0]);
    return d.stream;
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };

  try {
    S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: 'meeting', voice_speak: 'off', voice_mode_revision: 1 };
    S.connected = true; render();
    wireMic();
    converseForTest('waiting');
    await startDuplex();
    assert(residentActive(), 'the meeting did not start');
    assert(opens().length === 1 && opens()[0].voice.mode === 'meeting', 'the fixture did not open a meeting: ' + JSON.stringify(msgs));
    host.sessionState({ state: 'open', session_id: 'vs-meet' });
    assert(tracks.length === 1 && tracks[0].readyState === 'live', 'the meeting has no live microphone');

    // THE TAP OUT OF A MEETING: talk to me now.
    document.getElementById('converse').click();
    await until(() => msgs.some(m => m.type === 'config_set' && m.config['speech.mode'].listen === 'interactive'),
      'the tap did not ask for interactive: ' + JSON.stringify(msgs));
    await until(() => msgs.some(m => m.voice && m.voice.action === 'abort'),
      'the tap changed the mode and kept the meeting lane: ' + JSON.stringify(msgs));
    await until(() => tracks.length === 2, 'the replacement conversation never asked for a microphone');
    assert(tracks[0].readyState === 'ended', 'the meeting microphone outlived the meeting: ' + tracks[0].readyState);
    assert(opens().length === 1, 'the replacement opened before the host acknowledged the meeting closed: ' + JSON.stringify(msgs));

    // THE HOST CONFIRMS THE MEETING IS CLOSED, and the conversation opens.
    host.sessionState({ state: 'closed', session_id: 'vs-meet' });
    await until(() => opens().length === 2, 'no replacement conversation opened after the meeting was acknowledged closed: ' + JSON.stringify(msgs));
    assert(opens()[1].voice.mode === 'conversation', 'the replacement is not a conversation: ' + JSON.stringify(opens()[1]));
    assert(tracks[1].readyState === 'live', 'the replacement has no live microphone: ' + tracks[1].readyState);
    assert(residentActive(), 'no conversation is resident after the meeting');
  } finally { abortDuplex(); tracks.forEach(t => t.stop()); await ac.close(); }
});
</script>`

func TestLeavingAMeetingRetiresItsLaneBeforeTheVoiceConversationOpens(t *testing.T) {
	runPageInEngines(t, voiceMeetingTapPage, micModules)
}
