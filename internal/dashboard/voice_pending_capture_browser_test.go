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
// .
// .
// .
// .
const voicePendingEngineOpenPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, abortDuplex, residentActive, converseForTest, render } from './voice.js';
import { S } from './state.js';

run(async () => {
  const frames = [], msgs = [];
  bindTransport(b => frames.push(b), m => { msgs.push(m); return 'r' + msgs.length; });
  const opens = () => msgs.filter(m => m.voice && m.voice.action === 'open').length;
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const dest = ac.createMediaStreamDestination();
  const track = dest.stream.getAudioTracks()[0];
  let release = null, asked = 0;
  navigator.mediaDevices.getUserMedia = () => { asked++; return new Promise(r => { release = () => r(dest.stream); }); };
  const show = (listen, speak, rev) => {
    S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };
  const converse = document.getElementById('converse');

  try {
    // THE GESTURE, and a permission prompt that stays up.
    show('interactive', 'on', 1);
    converseForTest('waiting');
    startDuplex();
    await until(() => release !== null, 'the microphone was never asked for');

    // THE HOST TURNS LISTENING OFF AND THEN BACK ON, and the operator is
    // still looking at the prompt: neither frame is a gesture.
    show('off', 'off', 2);
    show('interactive', 'on', 3);
    release();
    await until(() => track.readyState === 'ended',
      'an off accepted while the prompt was up was undone by the on that followed it: ' + track.readyState);
    assert(!residentActive() && opens() === 0, 'a session was opened for a cancelled microphone: ' + JSON.stringify(msgs));
    assert(frames.length === 0, 'audio was streamed from a cancelled microphone');
    assert(asked === 1, 'a status frame asked for a microphone: ' + asked + ' asks');
    assert(!converse.classList.contains('waiting'), 'the control still waits for a conversation that will not open: ' + converse.outerHTML);
  } finally { abortDuplex(); track.stop(); await ac.close(); }
});
</script>`

func TestAnOffAcceptedWhileTheMicrophoneWasPendingIsNotUndoneByTheOnAfterIt(t *testing.T) {
	runPageInEngines(t, voicePendingEngineOpenPage, micModules)
}

// .
// .
// .
// .
// .
const voicePendingCloudLatchPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, render, wireMic } from './voice.js';
import { S } from './state.js';

run(async () => {
  const frames = [], msgs = [];
  bindTransport(f => frames.push(new Uint8Array(f)), m => { msgs.push(m); return 'r' + msgs.length; });
  const realAudio = window.AudioContext || window.webkitAudioContext;
  const ac = new realAudio();
  const dest = ac.createMediaStreamDestination();
  const track = dest.stream.getAudioTracks()[0];
  let release = null, asked = 0;
  navigator.mediaDevices.getUserMedia = () => { asked++; return new Promise(r => { release = () => r(dest.stream); }); };
  // The capture graph is the page's usual double, and it is counted: a
  // graph built at all would mean the cancelled open went on to listen.
  let graphs = 0;
  window.AudioContext = class {
    constructor() { graphs++; this.sampleRate = 16000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { return { connect() {}, disconnect() {}, onaudioprocess: null }; }
    close() {}
  };
  const show = (listen, speak, rev) => {
    S.stats = { voice_state: 'cloud', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };
  const mic = document.getElementById('mic');

  try {
    show('interactive', 'on', 1);
    wireMic(); render();

    // THE LATCH, asked for by the keyboard and waiting on the prompt.
    mic.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await until(() => release !== null, 'the key never asked for a microphone');
    assert(mic.classList.contains('live'), 'the key did not latch: ' + mic.outerHTML);

    // OFF, THEN ON, while the prompt is still up.
    show('off', 'off', 2);
    show('interactive', 'on', 3);
    release();
    await until(() => track.readyState === 'ended',
      'the latched device outlived the off that was accepted while it was pending: ' + track.readyState);
    assert(!mic.classList.contains('live'), 'the latch survived an off it never heard: ' + mic.outerHTML);
    assert(frames.length === 0, 'a capture that never opened sent something: ' + frames.length + ' frames');
    assert(graphs === 0, 'the cancelled open went on to build a capture graph');
    assert(asked === 1, 'a status frame asked for a microphone: ' + asked + ' asks');
  } finally { track.stop(); window.AudioContext = realAudio; await ac.close(); }
});
</script>`

func TestAnOffAcceptedWhileTheCloudLatchWasPendingReleasesItAndSendsNothing(t *testing.T) {
	runPageInEngines(t, voicePendingCloudLatchPage, micModules)
}

// .
// .
// .
// .
// .
// .
const voiceOwnTapOutOfOffPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, abortDuplex, residentActive, render, wireMic } from './voice.js';
import { S } from './state.js';

run(async () => {
  const msgs = [];
  bindTransport(() => {}, m => { msgs.push(m); return 'r' + msgs.length; });
  const opens = () => msgs.filter(m => m.voice && m.voice.action === 'open');
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const dest = ac.createMediaStreamDestination();
  const track = dest.stream.getAudioTracks()[0];
  let release = null;
  navigator.mediaDevices.getUserMedia = () => new Promise(r => { release = () => r(dest.stream); });
  const show = (listen, speak, rev) => {
    S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };

  try {
    // VOICE OFF: nothing is heard and nothing is spoken.
    show('off', 'off', 1);
    wireMic();

    // THE TAP: talk to me. It asks the host for interactive and opens the
    // microphone under the same gesture.
    document.getElementById('converse').click();
    await until(() => release !== null, 'the tap never asked for a microphone');
    assert(msgs.some(m => m.type === 'config_set' && m.config['speech.mode'].listen === 'interactive'),
      'the tap did not ask for interactive: ' + JSON.stringify(msgs));

    // THE HOST'S ANSWER TO THAT TAP arrives before the prompt is answered.
    show('interactive', 'on', 2);
    release();
    await until(() => opens().length === 1, 'the tap\'s own conversation never opened: ' + JSON.stringify(msgs));
    assert(residentActive(), 'the conversation the tap opened is not resident');
    assert(track.readyState === 'live', 'the off the tap was made in cancelled the tap\'s own microphone: ' + track.readyState);
    assert(opens()[0].voice.mode === 'conversation', 'the tap opened something other than a conversation: ' + JSON.stringify(msgs));
  } finally { abortDuplex(); track.stop(); await ac.close(); }
});
</script>`

func TestTheTapOutOfOffOpensItsOwnMicrophoneUnderTheOffItIsLeaving(t *testing.T) {
	runPageInEngines(t, voiceOwnTapOutOfOffPage, micModules)
}

// .
// .
// .
// .
const voiceLateGraphUnderAcceptedOffPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, abortDuplex, residentActive, converseForTest, render } from './voice.js';
import { S } from './state.js';

run(async () => {
  const frames = [], msgs = [];
  bindTransport(b => frames.push(b), m => { msgs.push(m); return 'r' + msgs.length; });
  const opens = () => msgs.filter(m => m.voice && m.voice.action === 'open').length;
  const realAudio = window.AudioContext || window.webkitAudioContext;
  const ac = new realAudio();
  const dest = ac.createMediaStreamDestination();
  const track = dest.stream.getAudioTracks()[0];
  navigator.mediaDevices.getUserMedia = async () => dest.stream;
  // The capture graph's module load is held open, so the second await can
  // be answered under a mode that moved twice beneath it.
  let releaseGraph = null;
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

  try {
    show('interactive', 'on', 1);
    converseForTest('waiting');
    startDuplex();
    await until(() => releaseGraph !== null, 'the capture graph was never loaded');
    assert(track.readyState === 'live', 'the device never opened: ' + track.readyState);

    show('off', 'off', 2);
    show('interactive', 'on', 3);
    releaseGraph();
    await until(() => track.readyState === 'ended',
      'a capture graph that finished for a cancelled open kept the microphone: ' + track.readyState);
    assert(!residentActive() && opens() === 0, 'a session was opened for a cancelled capture: ' + JSON.stringify(msgs));
    assert(frames.length === 0, 'audio was streamed from a cancelled capture');
  } finally { abortDuplex(); track.stop(); window.AudioContext = realAudio; await ac.close(); }
});
</script>`

func TestACaptureGraphFinishingForACancelledOpenReleasesTheMicrophone(t *testing.T) {
	runPageInEngines(t, voiceLateGraphUnderAcceptedOffPage, micModules)
}

// .
// .
// .
// .
// .
// .
const voiceReplacementAfterCancelledOpenPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, abortDuplex, residentActive, converseForTest, render } from './voice.js';
import { S } from './state.js';

run(async () => {
  const msgs = [];
  bindTransport(() => {}, m => { msgs.push(m); return 'r' + msgs.length; });
  const opens = () => msgs.filter(m => m.voice && m.voice.action === 'open');
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const tracks = [], release = [];
  // Each ask gets a device of its own, and each one is answered when this
  // page says so — the second before the first.
  navigator.mediaDevices.getUserMedia = () => {
    const d = ac.createMediaStreamDestination();
    tracks.push(d.stream.getAudioTracks()[0]);
    return new Promise(r => release.push(() => r(d.stream)));
  };
  const show = (listen, speak, rev) => {
    S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };

  try {
    // THE FIRST OPEN, asked at revision 1 and cancelled by the off at 2.
    show('interactive', 'on', 1);
    converseForTest('waiting');
    startDuplex();
    await until(() => release.length === 1, 'the first open never asked for a microphone');
    show('off', 'off', 2);

    // THE OPERATOR ASKS AGAIN under a mode that listens.
    show('interactive', 'on', 3);
    converseForTest('waiting');
    startDuplex();
    await until(() => release.length === 2, 'the second open never asked for a microphone');

    // THE CANCELLED OPEN IS ANSWERED FIRST, with the replacement still
    // waiting on its own prompt.
    release[0]();
    await until(() => tracks[0].readyState === 'ended', 'the cancelled open kept its device: ' + tracks[0].readyState);

    // AND THE REPLACEMENT OPENS, untouched by it.
    release[1]();
    await until(() => opens().length === 1, 'the cancelled open took the replacement down with it: ' + JSON.stringify(msgs));
    assert(residentActive(), 'the replacement conversation is not resident');
    assert(tracks[1].readyState === 'live', 'the replacement lost its microphone: ' + tracks[1].readyState);
    assert(tracks.length === 2, 'a microphone was asked for that nobody gestured at: ' + tracks.length + ' devices');
    assert(opens()[0].voice.mode === 'conversation', 'the replacement is not a conversation: ' + JSON.stringify(msgs));
  } finally { abortDuplex(); tracks.forEach(t => t.stop()); await ac.close(); }
});
</script>`

func TestALateMicrophoneFromACancelledOpenLeavesItsReplacementAlone(t *testing.T) {
	runPageInEngines(t, voiceReplacementAfterCancelledOpenPage, micModules)
}

// .
// .
// .
// .
// .
// .
// .
const voiceReplacementAfterRejectedOpenPage = micMarkup + `<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, abortDuplex, residentActive, converseForTest, render } from './voice.js';
import { S } from './state.js';

run(async () => {
  const msgs = [];
  bindTransport(() => {}, m => { msgs.push(m); return 'r' + msgs.length; });
  const opens = () => msgs.filter(m => m.voice && m.voice.action === 'open');
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const tracks = [], answer = [];
  navigator.mediaDevices.getUserMedia = () => {
    const d = ac.createMediaStreamDestination();
    tracks.push(d.stream.getAudioTracks()[0]);
    return new Promise((resolve, reject) => answer.push({ grant: () => resolve(d.stream), refuse: () => reject(new Error('permission denied for an open nobody holds')) }));
  };
  const show = (listen, speak, rev) => {
    S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  const tick = () => new Promise(r => setTimeout(r, 10));
  const until = async (ok, what) => { for (let i = 0; i < 300 && !ok(); i++) await tick(); assert(ok(), what); };

  try {
    show('interactive', 'on', 1);
    converseForTest('waiting');
    startDuplex();
    await until(() => answer.length === 1, 'the first open never asked for a microphone');
    show('off', 'off', 2);
    show('interactive', 'on', 3);
    converseForTest('waiting');
    startDuplex();
    await until(() => answer.length === 2, 'the second open never asked for a microphone');

    // THE CANCELLED OPEN IS REFUSED FIRST, with the replacement still waiting.
    answer[0].refuse();
    for (let i = 0; i < 5; i++) await tick();
    assert(opens().length === 0, 'a refusal for a stale open sent something: ' + JSON.stringify(msgs));

    // AND THE REPLACEMENT OPENS, untouched by it.
    answer[1].grant();
    await until(() => opens().length === 1, 'the stale refusal cancelled the replacement: ' + JSON.stringify(msgs));
    assert(residentActive(), 'the replacement conversation is not resident');
    assert(tracks[1].readyState === 'live', 'the replacement lost its microphone: ' + tracks[1].readyState);
    assert(opens()[0].voice.mode === 'conversation', 'the replacement is not a conversation: ' + JSON.stringify(msgs));
  } finally { abortDuplex(); tracks.forEach(t => t.stop()); await ac.close(); }
});
</script>`

func TestARejectedObsoleteOpenCannotCancelTheNewerCapture(t *testing.T) {
	runPageInEngines(t, voiceReplacementAfterRejectedOpenPage, micModules)
}
